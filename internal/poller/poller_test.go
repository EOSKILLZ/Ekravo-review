package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/config"
)

func TestWatchedReposFollowsWatchList(t *testing.T) {
	cfg := &config.Config{GitHub: config.GitHub{
		Repos:       []string{"o/configured"},
		IgnoreRepos: map[string]struct{}{},
	}}
	p := New(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	ctx := context.Background()

	got := p.watchedRepos(ctx)
	if len(got) != 1 || got[0] != "o/configured" {
		t.Fatalf("without a watch list = %v", got)
	}

	p.SetWatch(func(context.Context) (bool, []string, error) {
		return false, []string{"o/one", "o/two"}, nil
	})
	if got = p.watchedRepos(ctx); len(got) != 2 || got[1] != "o/two" {
		t.Fatalf("selected watch list = %v", got)
	}

	p.SetWatch(func(context.Context) (bool, []string, error) { return true, nil, nil })
	if got = p.watchedRepos(ctx); len(got) != 1 || got[0] != "o/configured" {
		t.Fatalf("watch-all should fall back to configured repos = %v", got)
	}

	p.SetWatch(func(context.Context) (bool, []string, error) { return false, nil, errors.New("db down") })
	if got = p.watchedRepos(ctx); len(got) != 1 || got[0] != "o/configured" {
		t.Fatalf("read errors must fail open = %v", got)
	}

	p.SetWatch(func(context.Context) (bool, []string, error) { return false, nil, nil })
	if got = p.watchedRepos(ctx); len(got) != 0 {
		t.Fatalf("empty selection should poll nothing = %v", got)
	}
}
