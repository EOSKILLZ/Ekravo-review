package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/store"
)

func watchRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDashboardWatchSettings(t *testing.T) {
	s, _ := dashboardServer(t, "")
	h := s.Handler()

	rec := watchRequest(t, h, http.MethodGet, "/api/watch", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rec.Code, rec.Body.String())
	}
	var initial store.Watch
	if err := json.Unmarshal(rec.Body.Bytes(), &initial); err != nil || !initial.All {
		t.Fatalf("initial watch = %s (%v)", rec.Body.String(), err)
	}

	if bad := watchRequest(t, h, http.MethodPut, "/api/watch", `{"all":false,"repos":["not-a-repo"]}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid repo status = %d", bad.Code)
	}
	if missing := watchRequest(t, h, http.MethodPut, "/api/watch", `{"repos":[]}`); missing.Code != http.StatusBadRequest {
		t.Fatalf("missing all status = %d", missing.Code)
	}

	ok := watchRequest(t, h, http.MethodPut, "/api/watch", `{"all":false,"repos":["o/r","o/other","o/r"]}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("put status = %d body=%s", ok.Code, ok.Body.String())
	}
	var saved store.Watch
	if err := json.Unmarshal(ok.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode saved: %v", err)
	}
	if saved.All || len(saved.Repos) != 2 {
		t.Fatalf("saved watch = %+v", saved)
	}

	rec = watchRequest(t, h, http.MethodGet, "/api/watch", "")
	if !strings.Contains(rec.Body.String(), `"all":false`) || !strings.Contains(rec.Body.String(), "o/other") {
		t.Fatalf("get after put = %s", rec.Body.String())
	}

	repos := watchRequest(t, h, http.MethodGet, "/api/repos", "")
	if !strings.Contains(repos.Body.String(), `"watch"`) || !strings.Contains(repos.Body.String(), `"all":false`) {
		t.Fatalf("repos response missing watch state: %s", repos.Body.String())
	}

	post := watchRequest(t, h, http.MethodPost, "/api/watch", "")
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, PUT" {
		t.Fatalf("post status = %d allow=%q", post.Code, post.Header().Get("Allow"))
	}
}
