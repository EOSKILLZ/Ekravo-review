package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/store"
)

func TestReposIncludesAvailableWithoutReviews(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "repos.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := &config.Config{
		Bot:       config.Bot{Handle: "ekravo", Address: ":0"},
		Dashboard: config.Dashboard{Enabled: true},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(cfg, log, &Metrics{}, Handlers{
		Store: st,
		AvailableRepos: func(context.Context) ([]string, error) {
			return []string{"o/reviewed", "o/fresh"}, nil
		},
	})

	rec := get(s.Handler(), "/api/repos", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Repos []struct {
			Repo string `json:"repo"`
		} `json:"repos"`
		Available []string    `json:"available"`
		Watch     store.Watch `json:"watch"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Available) != 2 || body.Available[1] != "o/fresh" {
		t.Fatalf("available = %v", body.Available)
	}
	if len(body.Repos) != 0 {
		t.Fatalf("repos = %v, want none reviewed", body.Repos)
	}
	if !body.Watch.All {
		t.Fatalf("watch = %+v, want all by default", body.Watch)
	}
}

func TestReposSurvivesGithubFailure(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "repos-fail.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := &config.Config{
		Bot:       config.Bot{Handle: "ekravo", Address: ":0"},
		Dashboard: config.Dashboard{Enabled: true},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(cfg, log, &Metrics{}, Handlers{
		Store: st,
		AvailableRepos: func(context.Context) ([]string, error) {
			return nil, io.ErrUnexpectedEOF
		},
	})

	rec := get(s.Handler(), "/api/repos", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when github is down", rec.Code)
	}
	var body struct {
		Available []string `json:"available"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Available) != 0 {
		t.Fatalf("available = %v, want empty on error", body.Available)
	}
}
