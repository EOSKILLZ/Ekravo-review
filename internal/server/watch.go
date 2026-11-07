package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/ekravo/Ekravo-review/v2/internal/store"
)

const maxWatchRepos = 200

var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// handleWatch reads and replaces the repository watch list.
func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		watch, err := s.store.Watch(r.Context())
		if err != nil {
			s.log.Error("read watch list failed", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "watch failed"})
			return
		}
		writeJSON(w, http.StatusOK, watch)

	case http.MethodPut:
		var body struct {
			All   *bool    `json:"all"`
			Repos []string `json:"repos"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if body.All == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "all is required"})
			return
		}
		if len(body.Repos) > maxWatchRepos {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many repositories"})
			return
		}

		repos := make([]string, 0, len(body.Repos))
		for _, repo := range body.Repos {
			repo = strings.TrimSpace(repo)
			if repo == "" {
				continue
			}
			if !repoNamePattern.MatchString(repo) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repository: " + repo})
				return
			}
			repos = append(repos, repo)
		}

		if err := s.store.SetWatch(r.Context(), store.Watch{All: *body.All, Repos: repos}); err != nil {
			s.log.Error("save watch list failed", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "watch failed"})
			return
		}
		saved, err := s.store.Watch(r.Context())
		if err != nil {
			saved = store.Watch{All: *body.All, Repos: repos}
		}
		writeJSON(w, http.StatusOK, saved)

	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) registerWatch(mux *http.ServeMux) {
	mux.HandleFunc("/api/watch", s.protect(s.handleWatch))
}
