package research

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchSendsContractAndCaches(t *testing.T) {
	var calls atomic.Int32
	var gotUA, gotAuth string
	var gotBody searchRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		gotUA = r.Header.Get("User-Agent")
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"web.search","query":"go 1.26 os.Root",
			"answer":"os.Root exists since Go 1.24.",
			"results":[
				{"title":"Go docs","url":"https://pkg.go.dev/os#Root","content":"Root is a directory handle."}
			],
			"usage":{"searches":1,"search_cost_credits":0.004}
		}`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, APIKey: "ekravo_test", Timeout: 5 * time.Second, MaxResults: 3})
	res, err := c.Search(context.Background(), "go 1.26 os.Root")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if gotUA == "" {
		t.Error("missing User-Agent; Cloudflare rejects requests without one")
	}
	if gotAuth != "Bearer ekravo_test" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if gotBody.Query != "go 1.26 os.Root" || gotBody.MaxResults != 3 || gotBody.SearchDepth != "fast" {
		t.Errorf("request body = %+v", gotBody)
	}
	if len(res.Sources) != 1 || res.Sources[0].URL != "https://pkg.go.dev/os#Root" {
		t.Fatalf("sources = %+v", res.Sources)
	}
	if res.Credits != 0.004 {
		t.Errorf("credits = %v", res.Credits)
	}

	if _, err := c.Search(context.Background(), "go 1.26 os.Root"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1 (cached)", calls.Load())
	}
}

func TestSearchErrorsAreNotSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"web search not enabled","code":"forbidden"}}`, http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, APIKey: "k"})
	_, err := c.Search(context.Background(), "some specific query")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want HTTP 403", err)
	}
}

func TestSearchDisabledWithoutConfiguration(t *testing.T) {
	c := New(Options{})
	if _, err := c.Search(context.Background(), "a long enough query"); err != ErrDisabled {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
}
