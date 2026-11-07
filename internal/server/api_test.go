package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/store"
)

func dashboardServer(t *testing.T, token string) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "dash.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.SaveReview(context.Background(), &store.Review{
		Repo: "o/r", PR: 3, Title: "Add dashboard", HeadSHA: "abc",
		Verdict: "approve", Findings: 0, Model: "m", Files: 2,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Bot:    config.Bot{Handle: "owlbot", Address: ":0"},
		GitHub: config.GitHub{IgnoreRepos: map[string]struct{}{}},
		Dashboard: config.Dashboard{
			Enabled: true,
			Token:   token,
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, log, &Metrics{}, Handlers{Store: st}), st
}

func get(h http.Handler, path, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDashboardAPIRefusesAnonymousRequests(t *testing.T) {
	s, _ := dashboardServer(t, "super-secret-token")
	if code := get(s.Handler(), "/api/overview", "").Code; code != http.StatusUnauthorized {
		t.Fatalf("anonymous overview status = %d, want 401", code)
	}
	if code := get(s.Handler(), "/api/overview", "wrong").Code; code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", code)
	}
}

func TestDashboardOverviewAndReviews(t *testing.T) {
	s, _ := dashboardServer(t, "super-secret-token")
	h := s.Handler()

	rec := get(h, "/api/overview?days=7", "super-secret-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("overview status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Totals  store.Totals `json:"totals"`
		Series  []store.Day  `json:"series"`
		Runtime runtimeInfo  `json:"runtime"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if body.Totals.Reviews != 1 || len(body.Series) != 7 {
		t.Fatalf("overview = %+v", body)
	}
	if body.Runtime.DashboardAuth != true {
		t.Error("runtime should report that auth is enabled")
	}

	rec = get(h, "/api/reviews?limit=10", "super-secret-token")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Add dashboard") {
		t.Fatalf("reviews status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = get(h, "/api/reviews/1", "super-secret-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("review detail status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"findings":[]`) {
		t.Errorf("detail body = %s", rec.Body.String())
	}

	if rec := get(h, "/api/repos", "super-secret-token"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "o/r") {
		t.Fatalf("repos status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDashboardSessionCookie(t *testing.T) {
	s, _ := dashboardServer(t, "super-secret-token")
	h := s.Handler()

	bad := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"token":"nope"}`))
	bad.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d", badRec.Code)
	}

	good := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"token":"super-secret-token"}`))
	good.Header.Set("Content-Type", "application/json")
	goodRec := httptest.NewRecorder()
	h.ServeHTTP(goodRec, good)
	if goodRec.Code != http.StatusOK {
		t.Fatalf("good token status = %d body=%s", goodRec.Code, goodRec.Body.String())
	}
	cookies := goodRec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != sessionCookie || !cookies[0].HttpOnly {
		t.Fatalf("cookies = %+v", cookies)
	}
}

func TestDashboardLearningsLifecycle(t *testing.T) {
	s, _ := dashboardServer(t, "super-secret-token")
	h := s.Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer super-secret-token")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	created := do(http.MethodPost, "/api/learnings",
		`{"repo":"o/r","text":"Use errors.Is.","path_glob":"internal/**"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil || createdBody.ID == 0 {
		t.Fatalf("create body = %s (%v)", created.Body.String(), err)
	}

	listed := do(http.MethodGet, "/api/learnings?repo=o/r", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "Use errors.Is.") {
		t.Fatalf("list status = %d body=%s", listed.Code, listed.Body.String())
	}

	patch := `{"repo":"o/r","id":` + strconv.FormatInt(createdBody.ID, 10) +
		`,"text":"Use errors.Is everywhere.","enabled":false}`
	if rec := do(http.MethodPatch, "/api/learnings", patch); rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", rec.Code, rec.Body.String())
	}
	var after struct {
		Learnings []store.Learning `json:"learnings"`
	}
	rec := do(http.MethodGet, "/api/learnings?repo=o/r", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode learnings: %v", err)
	}
	if len(after.Learnings) != 1 || after.Learnings[0].Text != "Use errors.Is everywhere." || after.Learnings[0].Enabled {
		t.Fatalf("after patch = %+v", after.Learnings)
	}

	if rec := do(http.MethodPatch, "/api/learnings", `{"repo":"o/r","id":999,"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing learning patch status = %d", rec.Code)
	}

	del := "/api/learnings?repo=o/r&id=" + strconv.FormatInt(createdBody.ID, 10)
	if rec := do(http.MethodDelete, del, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/learnings?repo=o/r", ""); strings.Contains(rec.Body.String(), "errors.Is") {
		t.Fatalf("delete left the learning behind: %s", rec.Body.String())
	}
}

func TestLearningsRejectsBadRequests(t *testing.T) {
	s, _ := dashboardServer(t, "")
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/learnings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing repo status = %d", rec.Code)
	}

	post := httptest.NewRequest(http.MethodPost, "/api/learnings", strings.NewReader(`{"repo":"o/r","text":"  "}`))
	post.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	h.ServeHTTP(postRec, post)
	if postRec.Code != http.StatusBadRequest {
		t.Fatalf("empty text status = %d", postRec.Code)
	}
}

func TestEmbeddedDashboardIsServed(t *testing.T) {
	s, _ := dashboardServer(t, "")
	rec := get(s.Handler(), "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Ekravo Review") {
		t.Fatalf("dashboard body did not contain the app shell")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("missing CSP: %q", csp)
	}
	if code := get(s.Handler(), "/some/unknown/route", "").Code; code != http.StatusOK {
		t.Fatalf("SPA fallback status = %d", code)
	}
}
