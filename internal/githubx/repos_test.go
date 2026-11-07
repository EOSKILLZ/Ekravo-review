package githubx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/config"
)

func TestListReposFromToken(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `[
			{"full_name":"o/active"},
			{"full_name":"o/archived","archived":true},
			{"full_name":"o/disabled","disabled":true},
			{"full_name":"o/second"}
		]`)
	}))
	defer srv.Close()

	c, err := New(config.GitHub{Token: "tok", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if path != "/user/repos" {
		t.Errorf("path = %q, want /user/repos", path)
	}
	if auth != "Bearer tok" {
		t.Errorf("authorization = %q, want bearer token", auth)
	}
	want := []string{"o/active", "o/second"}
	if len(repos) != len(want) || repos[0] != want[0] || repos[1] != want[1] {
		t.Fatalf("repos = %v, want %v", repos, want)
	}
}
