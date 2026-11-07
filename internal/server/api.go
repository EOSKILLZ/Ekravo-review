package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/review"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/store"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/version"
)

const sessionCookie = "ekravo_dash"

type runtimeInfo struct {
	Mode                  string   `json:"mode"`
	Provider              string   `json:"provider"`
	ProviderURL           string   `json:"provider_url"`
	Models                []string `json:"models"`
	Handle                string   `json:"handle"`
	RepoURL               string   `json:"repo_url"`
	AutoApprove           bool     `json:"auto_approve"`
	AutoMerge             bool     `json:"auto_merge"`
	AutoMergeMethod       string   `json:"auto_merge_method"`
	WebResearch           bool     `json:"web_research"`
	WebResearchMaxQueries int      `json:"web_research_max_queries"`
	Workers               int      `json:"workers"`
	MaxFiles              int      `json:"max_files"`
	ChunkChars            int      `json:"chunk_chars"`
	ContextMode           string   `json:"context_mode"`
	ContextFetch          string   `json:"context_fetch"`
	ContextMaxChars       int      `json:"context_max_chars"`
	ContextMaxTotalChars  int      `json:"context_max_total_chars"`
	DashboardAuth         bool     `json:"dashboard_auth"`
}

func (s *Server) runtimeInfo() runtimeInfo {
	mode := "polling"
	if s.cfg.WebhookMode() {
		mode = "webhook"
	}
	provider, providerURL := providerFrom(s.cfg.LLM.BaseURL)
	return runtimeInfo{
		Mode:                  mode,
		Provider:              provider,
		ProviderURL:           providerURL,
		Models:                append([]string(nil), s.cfg.LLM.Models...),
		Handle:                s.cfg.Bot.Handle,
		RepoURL:               s.cfg.Bot.RepoURL,
		AutoApprove:           s.cfg.Bot.AutoApprove,
		AutoMerge:             s.cfg.Bot.AutoMerge,
		AutoMergeMethod:       s.cfg.Bot.AutoMergeMethod,
		WebResearch:           s.cfg.Research.Enabled,
		WebResearchMaxQueries: s.cfg.Research.MaxQueries,
		Workers:               s.cfg.Bot.Workers,
		MaxFiles:              s.cfg.Bot.MaxFiles,
		ChunkChars:            s.cfg.LLM.ChunkChars,
		ContextMode:           s.cfg.Context.Mode,
		ContextFetch:          s.cfg.Context.Fetch,
		ContextMaxChars:       s.cfg.Context.MaxChars,
		ContextMaxTotalChars:  s.cfg.Context.MaxTotalChars,
		DashboardAuth:         s.cfg.Dashboard.Token != "",
	}
}

// providerFrom labels the configured endpoint. Ekravo is the default and the
// recommended provider; anything else is named by its host.
func providerFrom(baseURL string) (name, link string) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return "Custom endpoint", ""
	}
	host := strings.TrimPrefix(u.Host, "www.")
	if host == "ekravo.com" || strings.HasSuffix(host, ".ekravo.com") {
		return "Ekravo", "https://ekravo.com"
	}
	return host, u.Scheme + "://" + u.Host
}

func (s *Server) authorized(r *http.Request) bool {
	token := s.cfg.Dashboard.Token
	if token == "" {
		return true
	}
	supplied := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		supplied = c.Value
	}
	if supplied == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			supplied = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) == 1
}

func (s *Server) protect(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.cfg.Dashboard.Enabled {
			http.NotFound(w, r)
			return
		}
		if !s.authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.cfg.Dashboard.Enabled {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodPost:
		var body struct {
			Token string `json:"token"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if s.cfg.Dashboard.Token != "" &&
			subtle.ConstantTimeCompare([]byte(body.Token), []byte(s.cfg.Dashboard.Token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    s.cfg.Dashboard.Token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil,
			MaxAge:   7 * 24 * 60 * 60,
		})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case http.MethodDelete:
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
		})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		w.Header().Set("Allow", "POST, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleAPIHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	dbOK := true
	if s.store != nil {
		if err := s.store.Ping(r.Context()); err != nil {
			dbOK = false
		}
	}
	status := http.StatusOK
	if !dbOK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ok":      dbOK,
		"db":      dbOK,
		"version": version.Version,
	})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	days := 14
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 90 {
			days = n
		}
	}
	ov, err := s.store.Overview(r.Context(), days)
	if err != nil {
		s.log.Error("dashboard overview failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "overview failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"totals":  ov.Totals,
		"series":  ov.Series,
		"recent":  ov.Recent,
		"runtime": s.runtimeInfo(),
	})
}

func (s *Server) handleReviews(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	q := r.URL.Query()
	limit := 25
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	offset := 0
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n > 0 {
		offset = n
	}
	search := strings.TrimSpace(q.Get("q"))
	if len(search) > 200 {
		search = search[:200]
	}
	reviews, total, err := s.store.ListReviews(r.Context(), store.ReviewFilter{
		Verdict: q.Get("verdict"),
		Query:   search,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		s.log.Error("dashboard reviews failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reviews failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reviews": reviews,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/api/reviews/")
	id, err := strconv.ParseInt(strings.Trim(raw, "/"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}

	rv, found, err := s.store.ReviewByID(r.Context(), id)
	if err != nil {
		s.log.Error("dashboard review failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "review failed"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	findings := []review.Finding{}
	if strings.TrimSpace(rv.FindingsJSON) != "" {
		if err := json.Unmarshal([]byte(rv.FindingsJSON), &findings); err != nil {
			s.log.Warn("stored findings are not readable", "review", id, "err", err)
			findings = []review.Finding{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"review": rv, "findings": findings})
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	repos, err := s.store.RepoStats(r.Context())
	if err != nil {
		s.log.Error("dashboard repos failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "repos failed"})
		return
	}
	watch, err := s.store.Watch(r.Context())
	if err != nil {
		s.log.Warn("dashboard watch list failed", "err", err)
		watch = store.Watch{All: true, Repos: []string{}}
	}
	available := []string{}
	if s.handlers.AvailableRepos != nil {
		if list, err := s.handlers.AvailableRepos(r.Context()); err != nil {
			s.log.Warn("dashboard could not list github repositories", "err", err)
		} else {
			available = list
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos, "watch": watch, "available": available})
}

func (s *Server) handleRuntime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.runtimeInfo())
}

func (s *Server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", s.handleAPIHealth)
	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/api/overview", s.protect(s.handleOverview))
	mux.HandleFunc("/api/reviews", s.protect(s.handleReviews))
	mux.HandleFunc("/api/reviews/", s.protect(s.handleReview))
	mux.HandleFunc("/api/repos", s.protect(s.handleRepos))
	mux.HandleFunc("/api/runtime", s.protect(s.handleRuntime))
	s.registerWatch(mux)
	s.registerLearnings(mux)
}
