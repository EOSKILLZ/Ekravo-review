package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
)

func testClient(t *testing.T, srv *httptest.Server, models ...string) *Client {
	t.Helper()
	return New(config.LLM{
		BaseURL: srv.URL, APIKey: "test", Models: models,
		Timeout: 5 * time.Second, MaxTokens: 100, Temperature: 0,
	})
}

func writeCompletion(w http.ResponseWriter, content string) {
	writeCompletionWithFinish(w, content, "stop")
}

func writeCompletionWithFinish(w http.ResponseWriter, content, finish string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model": "served",
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": content},
			"finish_reason": finish,
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	})
}

func TestChatFallsBackAfterServerError(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		if req.Model == "primary" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream down"))
			return
		}
		writeCompletion(w, "ok")
	}))
	defer srv.Close()

	c := testClient(t, srv, "primary", "backup")
	res, err := c.Chat(context.Background(), "sys", "user", Options{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Content != "ok" || res.Model != "backup" {
		t.Fatalf("result = %+v", res)
	}
	if len(seen) != 2 || seen[0] != "primary" || seen[1] != "backup" {
		t.Fatalf("models tried = %v", seen)
	}
	if res.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestJSONFallsBackOnInvalidJSON(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		if req.Model == "primary" {
			writeCompletion(w, "Sure! Here is a review of your code.")
			return
		}
		writeCompletion(w, "```json\n{\"findings\":[]}\n```")
	}))
	defer srv.Close()

	c := testClient(t, srv, "primary", "backup")
	res, err := c.JSON(context.Background(), "sys", "user", Options{})
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if res.Model != "backup" {
		t.Fatalf("model = %q, want backup", res.Model)
	}
	if string(res.Raw) != `{"findings":[]}` {
		t.Fatalf("raw = %s", res.Raw)
	}
	if len(seen) != 2 {
		t.Fatalf("calls = %v", seen)
	}
}

func TestChatRetriesWithoutResponseFormatWhenRejected(t *testing.T) {
	var formats []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		formats = append(formats, req.ResponseFormat != nil)
		if req.ResponseFormat != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"response_format is available only for OpenRouter-backed models"}}`))
			return
		}
		writeCompletion(w, `{"findings":[]}`)
	}))
	defer srv.Close()

	c := testClient(t, srv, "only")
	if _, err := c.JSON(context.Background(), "sys", "user", Options{}); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if len(formats) != 2 || !formats[0] || formats[1] {
		t.Fatalf("response_format flags = %v", formats)
	}
}

func TestJSONRetriesWithLargerBudgetWhenTruncated(t *testing.T) {
	var budgets []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		budgets = append(budgets, req.MaxTokens)
		if len(budgets) == 1 {
			writeCompletionWithFinish(w, `{"findings":[{"file":"a.go",`, "length")
			return
		}
		writeCompletion(w, `{"findings":[]}`)
	}))
	defer srv.Close()

	c := testClient(t, srv, "only")
	res, err := c.JSON(context.Background(), "sys", "user", Options{MaxTokens: 100})
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if string(res.Raw) != `{"findings":[]}` {
		t.Fatalf("raw = %s", res.Raw)
	}
	if len(budgets) != 2 || budgets[0] != 100 || budgets[1] != 200 {
		t.Fatalf("budgets = %v, want [100 200]", budgets)
	}
}

func TestJSONTruncationFallsBackToNextModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "truncating" {
			writeCompletionWithFinish(w, `{"findings":[`, "length")
			return
		}
		writeCompletion(w, `{"findings":[]}`)
	}))
	defer srv.Close()

	c := testClient(t, srv, "truncating", "backup")
	res, err := c.JSON(context.Background(), "sys", "user", Options{MaxTokens: 50})
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if res.Model != "backup" {
		t.Fatalf("model = %q, want backup", res.Model)
	}
}

func TestAuthFailureStopsChain(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	c := testClient(t, srv, "a", "b", "c")
	if _, err := c.Chat(context.Background(), "sys", "user", Options{}); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (auth failure is fatal)", calls)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		fail bool
	}{
		{"plain object", `{"findings":[]}`, `{"findings":[]}`, false},
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`, false},
		{"leading prose", `Here you go: {"findings":[{"x":"}{"}]} done`, `{"findings":[{"x":"}{"}]}`, false},
		{"bare array", `[{"a":1}]`, `[{"a":1}]`, false},
		{"empty", "", "", true},
		{"prose only", "no json here", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractJSON(tc.in)
			if tc.fail {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractJSON: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
