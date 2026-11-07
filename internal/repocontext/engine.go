package repocontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/diff"
)

const (
	maxIndexFiles = 20_000
	maxIndexBytes = 96 << 20
	manifestName  = ".ekravo-manifest.json"
)

// Engine owns fetched repositories, indexes, and the parse cache. It is safe
// for concurrent reviews; one index is built per repo@sha.
type Engine struct {
	source Source
	opts   Options
	cache  *Cache
	log    *slog.Logger

	mu      sync.Mutex
	entries map[string]*indexEntry
}

type indexEntry struct {
	ready chan struct{}
	ix    *Index
	stats Stats
	err   error
}

// New builds an engine. A nil logger is allowed.
func New(source Source, opts Options, cache *Cache, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{source: source, opts: opts, cache: cache, log: log, entries: map[string]*indexEntry{}}
}

// Close releases the cache.
func (e *Engine) Close() error {
	if e.cache != nil {
		return e.cache.Close()
	}
	return nil
}

// Options returns the engine's current options.
func (e *Engine) Options() Options { return e.opts }

// Context retrieves a bounded reference block for one diff section. A failure
// is reported as an empty block, never as a review failure.
func (e *Engine) Context(ctx context.Context, repo, sha string, section []*diff.File) (string, Stats) {
	if e == nil || sha == "" || len(section) == 0 {
		return "", Stats{Mode: "off"}
	}
	diffChars := sectionDiff(section)
	if e.opts.Budget(diffChars) == 0 {
		return "", Stats{Mode: "on"}
	}

	buildCtx, cancel := context.WithTimeout(ctx, e.opts.Timeout)
	defer cancel()

	ix, buildStats, err := e.ensure(buildCtx, repo, sha, section)
	if err != nil {
		e.log.Warn("repository context unavailable; reviewing diff only",
			"repo", repo, "sha", sha, "err", err)
		return "", Stats{Partial: true, IndexSource: buildStats.IndexSource}
	}

	text, stats := ix.Retrieve(section, e.opts)
	stats.Partial = ix.Partial
	stats.IndexMS = buildStats.IndexMS
	stats.Fetched = buildStats.Fetched
	stats.Cached = buildStats.Cached
	stats.Symbols = buildStats.Symbols
	stats.IndexSource = ix.Source
	stats.Mode = "on"
	return text, stats
}

func (e *Engine) ensure(ctx context.Context, repo, sha string, section []*diff.File) (*Index, Stats, error) {
	key := repo + "@" + sha
	e.mu.Lock()
	if entry, ok := e.entries[key]; ok {
		e.mu.Unlock()
		select {
		case <-entry.ready:
			return entry.ix, entry.stats, entry.err
		case <-ctx.Done():
			return nil, Stats{}, ctx.Err()
		}
	}
	entry := &indexEntry{ready: make(chan struct{})}
	e.entries[key] = entry
	e.mu.Unlock()

	ix, stats, err := e.build(ctx, repo, sha, changedDirs(section))
	entry.ix, entry.stats, entry.err = ix, stats, err
	close(entry.ready)

	if err != nil {
		e.mu.Lock()
		delete(e.entries, key)
		e.mu.Unlock()
	}
	return ix, stats, err
}

func (e *Engine) build(ctx context.Context, repo, sha string, changed []string) (*Index, Stats, error) {
	dir := e.repoDir(repo, sha)
	stats := Stats{}

	if ok, _ := e.cached(dir); ok {
		files, err := collectFiles(dir, e.opts, changed)
		if err == nil && len(files) > 0 {
			ix, bs := (&Builder{Cache: e.cache, Opts: e.opts, Log: e.log}).
				Build(ctx, repo, sha, files, "cache")
			return ix, bs, nil
		}
		// A corrupt cache must not poison every later review.
		_ = os.RemoveAll(dir)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, stats, err
	}

	strategy := e.opts.Fetch
	sizeKB := 0
	if e.source != nil {
		if n, err := e.source.RepoSizeKB(ctx, repo); err == nil {
			sizeKB = n
		}
	}
	if strategy == "" || strategy == "auto" {
		if sizeKB > 150*1024 {
			strategy = "git"
		} else {
			strategy = "tarball"
		}
	}

	var fetchErr error
	switch strategy {
	case "tarball":
		fetchErr = fetchTarball(ctx, e.source, repo, sha, dir, e.opts)
	case "git":
		fetchErr = fetchGit(ctx, e.source, repo, sha, dir, changed)
	case "api":
		fetchErr = fetchAPI(ctx, e.source, repo, sha, dir, changed, e.opts)
	default:
		fetchErr = fmt.Errorf("unknown CONTEXT_FETCH %q", strategy)
	}
	if fetchErr != nil {
		e.log.Warn("context fetch failed; trying GitHub tree API",
			"repo", repo, "sha", sha, "strategy", strategy, "err", fetchErr)
		if err := fetchAPI(ctx, e.source, repo, sha, dir, changed, e.opts); err != nil {
			return nil, stats, fmt.Errorf("%s: %w; api: %v", strategy, fetchErr, err)
		}
		strategy = "api"
	}

	if err := writeManifest(dir, manifest{
		Repo: repo, SHA: sha, Strategy: strategy, BuiltAt: time.Now().UTC(),
	}); err != nil {
		e.log.Debug("context manifest write failed", "err", err)
	}

	files, err := collectFiles(dir, e.opts, changed)
	if err != nil {
		return nil, stats, err
	}
	if len(files) == 0 {
		return nil, stats, errors.New("context: repository produced no indexable files")
	}
	ix, bs := (&Builder{Cache: e.cache, Opts: e.opts, Log: e.log}).
		Build(ctx, repo, sha, files, strategy)
	ix.Partial = strategy == "api"
	return ix, bs, nil
}

func (e *Engine) repoDir(repo, sha string) string {
	repo = strings.ReplaceAll(repo, "/", "__")
	repo = strings.ReplaceAll(repo, "..", "_")
	return filepath.Join(e.opts.CacheDir, "repos", repo, sha)
}

func (e *Engine) cached(dir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(dir, manifestName)); err != nil {
		return false, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return false, err
	}
	return true, nil
}

type manifest struct {
	Repo     string    `json:"repo"`
	SHA      string    `json:"sha"`
	Strategy string    `json:"strategy"`
	BuiltAt  time.Time `json:"built_at"`
}

func writeManifest(dir string, m manifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, manifestName), data, 0o600)
}

func collectFiles(dir string, opts Options, changed []string) ([]FileInput, error) {
	changedRank := map[string]int{}
	for _, p := range changed {
		changedRank[p] = 0
		dir := filepath.ToSlash(filepath.Dir(p))
		if dir != "." {
			changedRank[dir] = 1
		}
	}

	type item struct {
		path string
		rank int
	}
	var items []item
	var total int64
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if skipFile(rel, d.Name(), opts.MaxFileKB) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() == 0 || info.Size() > int64(opts.MaxFileKB)*1024 {
			return nil
		}
		total += info.Size()
		if total > maxIndexBytes || len(items) >= maxIndexFiles {
			return filepath.SkipAll
		}
		rank := 2
		if r, ok := changedRank[rel]; ok {
			rank = r
		} else if r, ok := changedRank[filepath.ToSlash(filepath.Dir(rel))]; ok {
			rank = r + 1
		}
		items = append(items, item{path: rel, rank: rank})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		return items[i].path < items[j].path
	})

	out := make([]FileInput, 0, len(items))
	for _, it := range items {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(it.path)))
		if err != nil {
			continue
		}
		if isBinary(data) {
			continue
		}
		out = append(out, FileInput{Path: it.path, Source: data})
	}
	return out, nil
}

func changedDirs(section []*diff.File) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range section {
		if f == nil {
			continue
		}
		if !seen[f.Path] {
			seen[f.Path] = true
			out = append(out, f.Path)
		}
		dir := filepath.ToSlash(filepath.Dir(f.Path))
		if dir != "." && !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".venv": true, "venv": true,
	"__pycache__": true, ".idea": true, ".vscode": true, ".next": true,
	"coverage": true, "bin": true, "obj": true, "Pods": true,
}

func skipDir(name string) bool { return skippedDirs[name] }

var skippedExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".ico": true, ".pdf": true, ".zip": true, ".gz": true, ".tar": true,
	".jar": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".mp4": true,
	".mp3": true, ".lock": true, ".min.js": true, ".map": true,
}

func skipFile(path, name string, maxKB int) bool {
	lower := strings.ToLower(path)
	for ext := range skippedExt {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	if name == ".DS_Store" || strings.HasPrefix(name, ".env") || strings.HasPrefix(name, ".ekravo-") {
		return true
	}
	return false
}

func isBinary(src []byte) bool {
	n := len(src)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if src[i] == 0 {
			return true
		}
	}
	return false
}
