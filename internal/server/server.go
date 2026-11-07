// Package server exposes the GitHub webhook endpoint plus health and metrics.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/store"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/webui"
)

const maxWebhookBytes = 5 << 20

// Metrics holds counters exported at /metrics.
type Metrics struct {
	Webhooks atomic.Int64
	Accepted atomic.Int64
	Rejected atomic.Int64
	Reviews  atomic.Int64
	Merges   atomic.Int64
	Failures atomic.Int64
	LLMCalls atomic.Int64
}

// Handlers are the callbacks invoked for accepted deliveries.
type Handlers struct {
	OnPullRequest func(ctx context.Context, repo string, number int, sha string, draft bool)
	OnCommand     func(ctx context.Context, repo string, number int, command string)
	Ready         func(ctx context.Context) error
	Store         *store.Store
}

// Server is the HTTP surface.
type Server struct {
	cfg      *config.Config
	log      *slog.Logger
	metrics  *Metrics
	handlers Handlers
	store    *store.Store
	srv      *http.Server
	command  *regexp.Regexp
}

// New builds the server. Call Start to listen.
func New(cfg *config.Config, log *slog.Logger, metrics *Metrics, handlers Handlers) *Server {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	s := &Server{
		cfg:      cfg,
		log:      log,
		metrics:  metrics,
		handlers: handlers,
		store:    handlers.Store,
		command: regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(cfg.Bot.Handle) +
			`\s+(review|explain|summarize|recheck|learn|forget|learnings)\b([^\n]*)`),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/metrics", s.handleMetrics)
	s.registerAPI(mux)
	if cfg.Dashboard.Enabled {
		mux.Handle("/", webui.Handler())
	}

	s.srv = &http.Server{
		Addr:              cfg.Bot.Address,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

// Handler exposes the mux for tests.
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// Start blocks until the server stops.
func (s *Server) Start() error {
	s.log.Info("webhook server listening", "addr", s.cfg.Bot.Address)
	err := s.srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown drains in-flight requests.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.handlers.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.handlers.Ready(ctx); err != nil {
			http.Error(w, "dependency unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	write := func(name, help string, v int64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
	}
	write("ekravo_review_webhooks_total", "Webhook deliveries received.", s.metrics.Webhooks.Load())
	write("ekravo_review_webhooks_accepted_total", "Deliveries accepted for processing.", s.metrics.Accepted.Load())
	write("ekravo_review_webhooks_rejected_total", "Deliveries rejected by signature or size.", s.metrics.Rejected.Load())
	write("ekravo_review_reviews_total", "Reviews posted to GitHub.", s.metrics.Reviews.Load())
	write("ekravo_review_auto_merges_total", "Pull requests auto-merged after a clean review.", s.metrics.Merges.Load())
	write("ekravo_review_failures_total", "Failed review attempts.", s.metrics.Failures.Load())
	write("ekravo_review_llm_calls_total", "LLM requests issued.", s.metrics.LLMCalls.Load())
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.metrics.Webhooks.Add(1)

	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.metrics.Rejected.Add(1)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}

	if !validSignature(s.cfg.Bot.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		s.metrics.Rejected.Add(1)
		s.log.Warn("rejected webhook: bad signature", "delivery", r.Header.Get("X-GitHub-Delivery"))
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	if event == "ping" {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "pong")
		return
	}

	if err := s.dispatch(event, body); err != nil {
		s.metrics.Rejected.Add(1)
		s.log.Warn("rejected webhook payload", "event", event, "err", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	s.metrics.Accepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, "accepted\n")
}

func (s *Server) dispatch(event string, body []byte) error {
	switch event {
	case "pull_request":
		var p struct {
			Action     string `json:"action"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			PullRequest struct {
				Number int  `json:"number"`
				Draft  bool `json:"draft"`
				Head   struct {
					SHA string `json:"sha"`
				} `json:"head"`
			} `json:"pull_request"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return err
		}
		switch p.Action {
		case "opened", "synchronize", "ready_for_review", "reopened":
		default:
			return nil
		}
		repo := p.Repository.FullName
		if repo == "" || p.PullRequest.Number == 0 {
			return fmt.Errorf("missing repository or pull request")
		}
		if _, ignored := s.cfg.GitHub.IgnoreRepos[strings.ToLower(repo)]; ignored {
			s.log.Debug("skipping ignored repo", "repo", repo)
			return nil
		}
		if s.handlers.OnPullRequest != nil {
			s.handlers.OnPullRequest(context.Background(), repo, p.PullRequest.Number,
				p.PullRequest.Head.SHA, p.PullRequest.Draft)
		}

	case "issue_comment":
		var p struct {
			Action     string `json:"action"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			Issue struct {
				Number      int             `json:"number"`
				PullRequest json.RawMessage `json:"pull_request"`
			} `json:"issue"`
			Comment struct {
				Body string `json:"body"`
			} `json:"comment"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return err
		}
		if p.Action != "created" || len(p.Issue.PullRequest) == 0 {
			return nil
		}
		m := s.command.FindStringSubmatch(p.Comment.Body)
		if len(m) < 2 {
			return nil
		}
		repo := p.Repository.FullName
		if _, ignored := s.cfg.GitHub.IgnoreRepos[strings.ToLower(repo)]; ignored {
			return nil
		}
		if s.handlers.OnCommand != nil {
			cmd := strings.ToLower(m[1])
			if args := strings.TrimSpace(m[2]); args != "" && (cmd == "learn" || cmd == "forget") {
				cmd += " " + args
			}
			s.handlers.OnCommand(context.Background(), repo, p.Issue.Number, cmd)
		}

	default:
		s.log.Debug("ignoring event", "event", event)
	}
	return nil
}

func validSignature(secret string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(header), []byte(want))
}
