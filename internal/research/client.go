// Package research calls Ekravo's hosted web-research endpoint. It is used only
// to verify externally checkable review claims, never to explore a codebase.
package research

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	userAgent        = "ekravo-review-v2 (+https://github.com/EOSKILLZ/ekravo-review)"
	maxResponseBytes = 4 << 20
	cacheTTL         = time.Hour
	cacheMax         = 256
)

// Source is one result returned by the research backend.
type Source struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Content       string  `json:"content"`
	Score         float64 `json:"score"`
	PublishedDate string  `json:"published_date"`
}

// Result is one research answer with its sources.
type Result struct {
	Query   string   `json:"query"`
	Answer  string   `json:"answer"`
	Sources []Source `json:"results"`
	Credits float64  `json:"-"`
}

// Options configures the client.
type Options struct {
	BaseURL    string
	APIKey     string
	Timeout    time.Duration
	MaxResults int
}

// Client is safe for concurrent use and caches identical queries.
type Client struct {
	baseURL    string
	apiKey     string
	http       *http.Client
	maxResults int

	mu    sync.Mutex
	cache map[string]cacheEntry
	now   func() time.Time
}

type cacheEntry struct {
	result  *Result
	expires time.Time
}

// New builds a research client. A nil-safe zero client returns ErrDisabled.
func New(opt Options) *Client {
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	maxResults := opt.MaxResults
	if maxResults <= 0 {
		maxResults = 4
	}
	return &Client{
		baseURL:    strings.TrimRight(opt.BaseURL, "/"),
		apiKey:     opt.APIKey,
		http:       &http.Client{Timeout: timeout},
		maxResults: maxResults,
		cache:      map[string]cacheEntry{},
		now:        time.Now,
	}
}

// ErrDisabled is returned when no endpoint or key is configured.
var ErrDisabled = fmt.Errorf("web research is not configured")

type searchRequest struct {
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results"`
	SearchDepth   string `json:"search_depth"`
	IncludeAnswer string `json:"include_answer"`
}

type searchResponse struct {
	Query   string   `json:"query"`
	Answer  string   `json:"answer"`
	Results []Source `json:"results"`
	Usage   struct {
		Searches      int     `json:"searches"`
		SearchCredits float64 `json:"search_cost_credits"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// Search runs one query. Results are cached for an hour so repeated reviews of
// the same version claim do not spend credits twice.
func (c *Client) Search(ctx context.Context, query string) (*Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty research query")
	}
	if c == nil || c.baseURL == "" || c.apiKey == "" {
		return nil, ErrDisabled
	}

	key := strings.ToLower(query)
	c.mu.Lock()
	if entry, ok := c.cache[key]; ok && c.now().Before(entry.expires) {
		c.mu.Unlock()
		return entry.result, nil
	}
	c.mu.Unlock()

	body := searchRequest{
		Query:         query,
		MaxResults:    c.maxResults,
		SearchDepth:   "fast",
		IncludeAnswer: "basic",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/web", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	// Ekravo sits behind Cloudflare, which rejects requests without a UA.
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web research: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("web research: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("web research: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var parsed searchResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("web research: malformed response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("web research: %s", parsed.Error.Message)
	}

	result := &Result{
		Query:   parsed.Query,
		Answer:  strings.TrimSpace(parsed.Answer),
		Sources: parsed.Results,
		Credits: parsed.Usage.SearchCredits,
	}
	if result.Query == "" {
		result.Query = query
	}
	for i := range result.Sources {
		if len(result.Sources[i].Content) > 2000 {
			result.Sources[i].Content = result.Sources[i].Content[:2000] + "…"
		}
	}

	c.mu.Lock()
	if len(c.cache) >= cacheMax {
		c.cache = map[string]cacheEntry{}
	}
	c.cache[key] = cacheEntry{result: result, expires: c.now().Add(cacheTTL)}
	c.mu.Unlock()

	return result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
