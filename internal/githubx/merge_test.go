package githubx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
)

func TestMergePullRequest(t *testing.T) {
	var got MergeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/o/r/pulls/1/merge" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "merged-sha", "merged": true})
	}))
	defer srv.Close()

	c, err := New(config.GitHub{Token: "t", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sha, err := c.MergePullRequest(context.Background(), "o/r", 1, MergeRequest{
		SHA: "abc", MergeMethod: "squash", CommitTitle: "Title",
	})
	if err != nil {
		t.Fatalf("MergePullRequest: %v", err)
	}
	if sha != "merged-sha" {
		t.Fatalf("sha = %q", sha)
	}
	if got.SHA != "abc" || got.MergeMethod != "squash" || got.CommitTitle != "Title" {
		t.Fatalf("payload = %+v", got)
	}
}

func TestMergePullRequestRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Pull Request is not mergeable"})
	}))
	defer srv.Close()

	c, err := New(config.GitHub{Token: "t", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.MergePullRequest(context.Background(), "o/r", 1, MergeRequest{SHA: "abc"}); err == nil {
		t.Fatal("expected an error for a rejected merge")
	}
}
