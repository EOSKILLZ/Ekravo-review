package repocontext

import (
	"context"
	"log/slog"
	"math"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileInput is one source file to index, already ordered by priority.
type FileInput struct {
	Path   string
	Source []byte
}

// Builder turns source files into an Index. It is safe to reuse.
type Builder struct {
	Cache *Cache
	Opts  Options
	Log   *slog.Logger
}

// Build parses files in parallel, using the content-hash cache, then assembles
// the symbol maps and reference graph.
func (b *Builder) Build(ctx context.Context, repo, sha string, files []FileInput, strategy string) (*Index, Stats) {
	start := time.Now()
	workers := b.Opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}

	type parsed struct {
		fs     *FileSymbols
		cached bool
		err    error
	}
	results := make([]parsed, len(files))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				in := files[i]
				entry := detectLang(in.Path)
				hash := Hash(in.Source)
				if b.Cache != nil {
					if fs, ok := b.Cache.Get(ctx, repo, in.Path, hash); ok {
						results[i] = parsed{fs: fs, cached: true}
						continue
					}
				}
				fs, err := Parse(in.Path, in.Source, b.Opts.Languages)
				if err != nil {
					results[i] = parsed{err: err}
					continue
				}
				if fs.Lang == "" {
					fs.Lang = entry
				}
				if b.Cache != nil {
					if err := b.Cache.Put(ctx, repo, in.Path, hash, fs); err != nil && b.Log != nil {
						b.Log.Debug("context cache write failed", "path", in.Path, "err", err)
					}
				}
				results[i] = parsed{fs: fs}
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	ix := &Index{
		Repo: repo, SHA: sha, Partial: false, Source: strategy,
		BuiltAt:    time.Now().UTC(),
		byPath:     map[string]int{},
		bySymbol:   map[string][]SymRef{},
		graph:      map[int]map[int]float64{},
		methodSets: map[string]map[string]bool{},
		typeDefs:   map[string]typeDef{},
	}
	stats := Stats{IndexSource: strategy}
	for i, in := range files {
		r := results[i]
		if r.err != nil || r.fs == nil {
			continue
		}
		if r.cached {
			stats.Cached++
		}
		f := &IndexFile{
			Path: in.Path, Lang: r.fs.Lang, Source: in.Source,
			Symbols: r.fs, Test: IsTestPath(in.Path),
		}
		idx := len(ix.Files)
		ix.byPath[in.Path] = idx
		ix.Files = append(ix.Files, f)
		stats.Fetched++

		for j := range f.Symbols.Defs {
			s := &f.Symbols.Defs[j]
			if b.Opts.SimHash && s.StartByte >= 0 && s.EndByte > s.StartByte && s.EndByte <= len(in.Source) {
				s.Body = string(in.Source[s.StartByte:s.EndByte])
				s.SimHash = SimHash(s.Body)
			}
			ix.bySymbol[s.Name] = append(ix.bySymbol[s.Name], SymRef{File: idx, Sym: s})
			if s.Receiver != "" {
				set := ix.methodSets[s.Receiver]
				if set == nil {
					set = map[string]bool{}
					ix.methodSets[s.Receiver] = set
				}
				set[s.Name] = true
			} else if s.Kind != "method" && s.Kind != "function" {
				if _, ok := ix.typeDefs[s.Name]; !ok {
					ix.typeDefs[s.Name] = typeDef{Path: in.Path, Line: s.Line}
				}
			}
			stats.Symbols++
		}
		for j := range f.Symbols.Refs {
			ref := &f.Symbols.Refs[j]
			ix.bySymbol[ref.Name] = append(ix.bySymbol[ref.Name], SymRef{File: idx, Ref: ref})
		}
	}

	ix.buildGraph()
	stats.IndexMS = time.Since(start).Milliseconds()
	stats.Mode = ""
	return ix, stats
}

func (ix *Index) buildGraph() {
	refCounts := make([]map[string]int, len(ix.Files))
	for i, f := range ix.Files {
		counts := map[string]int{}
		for _, r := range f.Symbols.Refs {
			counts[r.Name]++
		}
		refCounts[i] = counts
	}

	for i := range ix.Files {
		for name, n := range refCounts[i] {
			defs := ix.bySymbol[name]
			weight := math.Sqrt(float64(n)) * nameMultiplier(name)
			added := 0
			for _, d := range defs {
				if d.Sym == nil || d.File == i {
					continue
				}
				if ix.graph[i] == nil {
					ix.graph[i] = map[int]float64{}
				}
				ix.graph[i][d.File] += weight
				added++
				if added >= 8 {
					break
				}
			}
		}
	}
}

func nameMultiplier(name string) float64 {
	mul := 1.0
	snake := strings.Contains(name, "_") && hasLetter(name)
	kebab := strings.Contains(name, "-") && hasLetter(name)
	camel := hasUpper(name) && hasLower(name)
	if (snake || kebab || camel) && len(name) >= 8 {
		mul *= 10
	}
	if strings.HasPrefix(name, "_") {
		mul *= 0.1
	}
	if len(name) <= 2 {
		mul *= 0.1
	}
	return mul
}

func hasLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

func hasUpper(s string) bool { return strings.ToLower(s) != s }
func hasLower(s string) bool { return strings.ToUpper(s) != s }

// IsTestPath recognises the common per-language test conventions.
func IsTestPath(p string) bool {
	lower := strings.ToLower(p)
	base := strings.ToLower(path.Base(p))
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") ||
		strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.tsx") ||
		strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".spec.ts") ||
		strings.HasSuffix(base, ".spec.js") || strings.HasSuffix(base, "_spec.rb") ||
		strings.HasSuffix(base, "_test.rb") || strings.HasSuffix(base, "test.java") ||
		strings.HasSuffix(base, "tests.cs") || strings.HasSuffix(base, "_test.rs") {
		return true
	}
	return strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/spec/") ||
		strings.Contains(lower, "/tests/") || strings.Contains(lower, "/test/")
}

// File returns the indexed file for a path.
func (ix *Index) File(p string) *IndexFile {
	if ix == nil {
		return nil
	}
	if i, ok := ix.byPath[p]; ok {
		return ix.Files[i]
	}
	return nil
}

// Definitions returns definitions for a name.
func (ix *Index) Definitions(name string) []SymRef {
	if ix == nil {
		return nil
	}
	var out []SymRef
	for _, r := range ix.bySymbol[name] {
		if r.Sym != nil {
			out = append(out, r)
		}
	}
	return out
}

// References returns reference uses for a name.
func (ix *Index) References(name string) []SymRef {
	if ix == nil {
		return nil
	}
	var out []SymRef
	for _, r := range ix.bySymbol[name] {
		if r.Ref != nil {
			out = append(out, r)
		}
	}
	return out
}

// TestsFor returns test files that mention the given path's base name or
// symbols. The mapping is intentionally conservative.
func (ix *Index) TestsFor(p string) []int {
	if ix == nil {
		return nil
	}
	base := strings.TrimSuffix(path.Base(p), path.Ext(p))
	stem := strings.TrimSuffix(base, "_test")
	var out []int
	for i, f := range ix.Files {
		if !f.Test || f.Path == p {
			continue
		}
		tb := strings.ToLower(path.Base(f.Path))
		if strings.Contains(tb, strings.ToLower(stem)) {
			out = append(out, i)
			continue
		}
		for _, d := range f.Symbols.Defs {
			if strings.Contains(strings.ToLower(d.Name), strings.ToLower(stem)) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// ImportedFiles resolves local import strings to indexed paths.
func (ix *Index) ImportedFiles(imports []string) []int {
	var out []int
	seen := map[int]bool{}
	for _, imp := range imports {
		imp = strings.Trim(imp, `"'`)
		if imp == "" || strings.HasPrefix(imp, ".") {
			continue
		}
		suffix := strings.TrimPrefix(strings.ReplaceAll(imp, ".", "/"), "/")
		for i, f := range ix.Files {
			if seen[i] {
				continue
			}
			dir := path.Dir(f.Path)
			if strings.HasSuffix(dir, suffix) || strings.HasSuffix(f.Path, suffix+path.Ext(f.Path)) {
				seen[i] = true
				out = append(out, i)
			}
		}
	}
	sort.Ints(out)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// EnclosingSymbol finds the smallest symbol containing a line.
func (f *IndexFile) EnclosingSymbol(line int) *Symbol {
	if f == nil {
		return nil
	}
	var best *Symbol
	for i := range f.Symbols.Defs {
		s := &f.Symbols.Defs[i]
		if s.Line <= line && (s.EndLine == 0 || s.EndLine >= line) {
			if best == nil || (s.EndLine-s.Line) < (best.EndLine-best.Line) {
				best = s
			}
		}
	}
	return best
}

func detectLang(p string) string {
	lower := strings.ToLower(p)
	switch path.Ext(lower) {
	case ".go":
		return "go"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "jsx"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".cs":
		return "c_sharp"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".hpp":
		return "cpp"
	case ".kt":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".scala":
		return "scala"
	case ".lua":
		return "lua"
	case ".ex", ".exs":
		return "elixir"
	case ".sh", ".bash":
		return "bash"
	case ".sql":
		return "sql"
	}
	return strings.TrimPrefix(path.Ext(lower), ".")
}
