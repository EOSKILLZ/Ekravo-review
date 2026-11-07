package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/config"
)

const secret = "test-secret-value-123456"

func sign(body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func testServer(t *testing.T, h Handlers) *Server {
	t.Helper()
	cfg := &config.Config{
		Bot:    config.Bot{Handle: "owlbot", WebhookSecret: secret, Address: ":0"},
		GitHub: config.GitHub{IgnoreRepos: map[string]struct{}{}},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, log, &Metrics{}, h)
}

func do(handler http.Handler, body, event, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestPullRequestWebhookDispatches(t *testing.T) {
	var gotRepo string
	var gotNumber int
	s := testServer(t, Handlers{
		OnPullRequest: func(_ context.Context, repo string, number int, _ string, _ bool) error {
			gotRepo, gotNumber = repo, number
			return nil
		},
	})

	body := `{"action":"opened","repository":{"full_name":"o/r"},` +
		`"pull_request":{"number":7,"draft":false,"head":{"sha":"abc"}}}`
	rec := do(s.Handler(), body, "pull_request", sign(body))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if gotRepo != "o/r" || gotNumber != 7 {
		t.Fatalf("dispatched %q #%d", gotRepo, gotNumber)
	}
}

func TestBadSignatureRejected(t *testing.T) {
	s := testServer(t, Handlers{OnPullRequest: func(context.Context, string, int, string, bool) error {
		t.Error("callback must not run")
		return nil
	}})
	body := `{"action":"opened","repository":{"full_name":"o/r"},"pull_request":{"number":7}}`
	rec := do(s.Handler(), body, "pull_request", "sha256=deadbeef")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestIssueCommentCommandDispatched(t *testing.T) {
	var got string
	var gotNumber int
	s := testServer(t, Handlers{
		OnCommand: func(_ context.Context, _ string, number int, command string) error {
			got, gotNumber = command, number
			return nil
		},
	})

	body := `{"action":"created","repository":{"full_name":"o/r"},` +
		`"issue":{"number":9,"pull_request":{}},"comment":{"body":"@owlbot SUMMARIZE please"}}`
	rec := do(s.Handler(), body, "issue_comment", sign(body))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if got != "summarize" || gotNumber != 9 {
		t.Fatalf("command = %q pr = %d", got, gotNumber)
	}
}

func TestPlainIssueCommentIgnored(t *testing.T) {
	s := testServer(t, Handlers{OnCommand: func(context.Context, string, int, string) error {
		t.Error("callback must not run for a non-PR issue")
		return nil
	}})
	body := `{"action":"created","repository":{"full_name":"o/r"},"issue":{"number":9},"comment":{"body":"@owlbot review"}}`
	rec := do(s.Handler(), body, "issue_comment", sign(body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPingResponds(t *testing.T) {
	s := testServer(t, Handlers{})
	rec := do(s.Handler(), `{"zen":"hi"}`, "ping", sign(`{"zen":"hi"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestQueueFullReturnsServiceUnavailable(t *testing.T) {
	s := testServer(t, Handlers{
		OnPullRequest: func(context.Context, string, int, string, bool) error { return ErrQueueFull },
	})
	body := `{"action":"opened","repository":{"full_name":"o/r"},` +
		`"pull_request":{"number":7,"head":{"sha":"abc"}}}`
	rec := do(s.Handler(), body, "pull_request", sign(body))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("503 must include Retry-After so GitHub retries")
	}
}

func TestBotCommentsDoNotTriggerCommands(t *testing.T) {
	s := testServer(t, Handlers{OnCommand: func(context.Context, string, int, string) error {
		t.Error("a bot comment must not dispatch a command")
		return nil
	}})
	body := `{"action":"created","repository":{"full_name":"o/r"},` +
		`"issue":{"number":9,"pull_request":{}},"comment":{"body":"@owlbot review"},` +
		`"sender":{"login":"owlbot[bot]","type":"Bot"}}`
	if rec := do(s.Handler(), body, "issue_comment", sign(body)); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
}

func TestIgnoredRepoIsNotDispatched(t *testing.T) {
	cfg := &config.Config{
		Bot:    config.Bot{Handle: "owlbot", WebhookSecret: secret, Address: ":0"},
		GitHub: config.GitHub{IgnoreRepos: map[string]struct{}{"o/r": {}}},
	}
	s := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), &Metrics{}, Handlers{
		OnPullRequest: func(context.Context, string, int, string, bool) error {
			t.Error("must be ignored")
			return nil
		},
	})
	body := `{"action":"opened","repository":{"full_name":"o/r"},"pull_request":{"number":1,"head":{"sha":"a"}}}`
	if rec := do(s.Handler(), body, "pull_request", sign(body)); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
}
