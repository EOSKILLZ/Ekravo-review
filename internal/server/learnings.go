package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) handleLearnings(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		repo := strings.TrimSpace(r.URL.Query().Get("repo"))
		if repo == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo is required"})
			return
		}
		list, err := s.store.ListLearnings(r.Context(), repo, true)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "learnings failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"learnings": list})

	case http.MethodPost:
		var body struct {
			Repo     string `json:"repo"`
			Text     string `json:"text"`
			PathGlob string `json:"path_glob"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		body.Repo = strings.TrimSpace(body.Repo)
		if body.Repo == "" || strings.TrimSpace(body.Text) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo and text are required"})
			return
		}
		id, err := s.store.AddLearning(r.Context(), body.Repo, body.PathGlob, body.Text, "dashboard")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not add learning"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})

	case http.MethodPatch:
		var body struct {
			Repo     string  `json:"repo"`
			ID       int64   `json:"id"`
			Text     *string `json:"text"`
			PathGlob *string `json:"path_glob"`
			Enabled  *bool   `json:"enabled"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		body.Repo = strings.TrimSpace(body.Repo)
		if body.Repo == "" || body.ID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo and id are required"})
			return
		}
		ok, err := s.store.UpdateLearning(r.Context(), body.Repo, body.ID, body.Text, body.PathGlob, body.Enabled)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	case http.MethodDelete:
		repo := strings.TrimSpace(r.URL.Query().Get("repo"))
		id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
		if repo == "" || err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo and id are required"})
			return
		}
		ok, err := s.store.DeleteLearning(r.Context(), repo, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		w.Header().Set("Allow", "GET, POST, PATCH, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) registerLearnings(mux *http.ServeMux) {
	mux.HandleFunc("/api/learnings", s.protect(s.handleLearnings))
}
