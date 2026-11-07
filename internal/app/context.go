package app

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"strings"

	"github.com/ekravo/Ekravo-review/v2/internal/config"
	"github.com/ekravo/Ekravo-review/v2/internal/githubx"
	"github.com/ekravo/Ekravo-review/v2/internal/repocontext"
)

type ghSource struct {
	c       *githubx.Client
	webBase string
}

func (g ghSource) RepoSizeKB(ctx context.Context, repo string) (int, error) {
	return g.c.RepoSizeKB(ctx, repo)
}

func (g ghSource) Tarball(ctx context.Context, repo, ref string, w io.Writer) error {
	return g.c.DownloadTarball(ctx, repo, ref, w)
}

func (g ghSource) Tree(ctx context.Context, repo, ref string) ([]repocontext.TreeEntry, error) {
	entries, err := g.c.ListTree(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	out := make([]repocontext.TreeEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, repocontext.TreeEntry{Path: e.Path, Size: e.Size, Dir: e.Dir})
	}
	return out, nil
}

func (g ghSource) FileAt(ctx context.Context, repo, path, ref string) ([]byte, error) {
	return g.c.FileAt(ctx, repo, path, ref)
}

func (g ghSource) GitAuth(ctx context.Context, repo string) (string, error) {
	return g.c.GitAuthHeader(ctx, repo)
}

func (g ghSource) WebBaseURL() string { return g.webBase }

// webBaseURL maps an API base to the web host used for git clones. GitHub.com
// serves the API from api.github.com; Enterprise serves /api/v3 on the same
// host as the web UI.
func webBaseURL(apiBase string) string {
	u, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || u.Host == "" {
		return "https://github.com"
	}
	if strings.EqualFold(u.Host, "api.github.com") {
		return "https://github.com"
	}
	return u.Scheme + "://" + u.Host
}

func newContextEngine(cfg *config.Config, gh *githubx.Client, log *slog.Logger) (*repocontext.Engine, error) {
	if cfg.Context.Mode == "off" {
		return nil, nil
	}
	opts := repocontext.DefaultOptions()
	opts.Fetch = cfg.Context.Fetch
	opts.CacheDir = cfg.Context.CacheDir
	opts.MaxChars = cfg.Context.MaxChars
	opts.MaxTotalChars = cfg.Context.MaxTotalChars
	opts.MaxFiles = cfg.Context.MaxFiles
	opts.MaxFileKB = cfg.Context.MaxFileKB
	opts.MaxRepoMB = cfg.Context.MaxRepoMB
	opts.CacheRows = cfg.Context.CacheRows
	opts.RatioMax = cfg.Context.RatioMax
	opts.MinDiffChars = cfg.Context.MinDiffChars
	opts.Timeout = cfg.Context.Timeout
	opts.Workers = cfg.Context.Workers
	opts.Languages = cfg.Context.Languages
	opts.Learnings = cfg.Context.Learnings
	opts.SimHash = cfg.Context.SimHash

	cache, err := repocontext.OpenCache(opts.CacheDir, opts.CacheRows)
	if err != nil {
		return nil, err
	}
	return repocontext.New(ghSource{c: gh, webBase: webBaseURL(cfg.GitHub.BaseURL)}, opts, cache, log), nil
}
