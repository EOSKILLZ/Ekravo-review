// Package llm talks to the Ekravo gateway (https://ekravo.com/v1) by default,
// and to any OpenAI-compatible chat-completions endpoint by configuration.
// It is built for free-model reality: a model chain, per-model circuit
// breaking, and a JSON extraction ladder instead of trusting the first reply.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
)

const (
	userAgent        = "ekravo-review-v2 (+https://github.com/ekravo/Ekravo-review)"
	downCooldown     = 10 * time.Minute
	badJSONCooldown  = 5 * time.Minute
	maxResponseBytes = 8 << 20
)

// Usage is token accounting reported by the provider.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Result is one successful completion.
type Result struct {
	Content      string
	Model        string
	Usage        Usage
	Latency      time.Duration
	FinishReason string
}

// Options controls a single call.
type Options struct {
	MaxTokens   int
	Temperature float64
	JSON        bool
}

// APIError is a non-2xx provider response.
type APIError struct {
	Status int
	Body   string
	Model  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("model %s returned HTTP %d: %s", e.Model, e.Status, truncate(e.Body, 300))
}

// Client is safe for concurrent use.
type Client struct {
	baseURL         string
	apiKey          string
	models          []string
	http            *http.Client
	maxTokens       int
	temperature     float64
	reasoningEffort string

	mu   sync.Mutex
	down map[string]time.Time

	now func() time.Time
}

// New builds a client from configuration.
func New(cfg config.LLM) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return &Client{
		baseURL:         strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:          cfg.APIKey,
		models:          append([]string(nil), cfg.Models...),
		http:            &http.Client{Timeout: timeout},
		maxTokens:       cfg.MaxTokens,
		temperature:     cfg.Temperature,
		reasoningEffort: cfg.ReasoningEffort,
		down:            map[string]time.Time{},
		now:             time.Now,
	}
}

// Models returns the configured chain in priority order.
func (c *Client) Models() []string { return append([]string(nil), c.models...) }

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Temperature     float64         `json:"temperature"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// Chat returns the first successful completion from the model chain.
func (c *Client) Chat(ctx context.Context, system, user string, opt Options) (*Result, error) {
	var failures []string
	for _, model := range c.available() {
		res, err := c.chatModel(ctx, model, system, user, opt, opt.JSON)
		if err == nil {
			return res, nil
		}
		failures = append(failures, err.Error())
		if fatal(err) {
			break
		}
	}
	return nil, fmt.Errorf("all models failed: %s", strings.Join(failures, "; "))
}

// JSONResult is validated JSON plus the model that actually produced it.
type JSONResult struct {
	Raw     []byte
	Model   string
	Usage   Usage
	Latency time.Duration
}

// JSON asks for a JSON object and validates it before returning. A model that
// returns prose is penalised so the next call goes straight to the fallback.
func (c *Client) JSON(ctx context.Context, system, user string, opt Options) (*JSONResult, error) {
	opt.JSON = true
	var failures []string
	for _, model := range c.available() {
		res, err := c.chatModel(ctx, model, system, user, opt, true)
		if err != nil {
			failures = append(failures, err.Error())
			if fatal(err) {
				break
			}
			continue
		}
		// A reasoning model can burn the whole budget before closing the JSON
		// object. Retry once with double the budget before moving on.
		if res.FinishReason == "length" {
			retryOpt := opt
			budget := retryOpt.MaxTokens
			if budget <= 0 {
				budget = c.maxTokens
			}
			retryOpt.MaxTokens = min(budget*2, 8192)
			if res2, err2 := c.chatModel(ctx, model, system, user, retryOpt, true); err2 == nil && res2.FinishReason != "length" {
				res = res2
			} else {
				failures = append(failures, fmt.Sprintf("%s: output truncated at max_tokens", model))
				continue
			}
		}

		raw, err := ExtractJSON(res.Content)
		if err == nil {
			return &JSONResult{Raw: raw, Model: res.Model, Usage: res.Usage, Latency: res.Latency}, nil
		}
		c.penalize(model, badJSONCooldown)
		failures = append(failures, fmt.Sprintf("%s: %v", model, err))
	}
	return nil, fmt.Errorf("no model produced valid JSON: %s", strings.Join(failures, "; "))
}

func (c *Client) chatModel(ctx context.Context, model, system, user string, opt Options, wantJSON bool) (*Result, error) {
	maxTokens := opt.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.maxTokens
	}
	temperature := opt.Temperature
	if temperature == 0 {
		temperature = c.temperature
	}

	body := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens:       maxTokens,
		Temperature:     temperature,
		ReasoningEffort: c.reasoningEffort,
	}
	if wantJSON {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	for attempt := 0; attempt < 2; attempt++ {
		start := c.now()
		res, apiErr := c.post(ctx, body)
		latency := c.now().Sub(start)

		if apiErr == nil {
			if res.Content == "" {
				return nil, fmt.Errorf("model %s returned empty content", model)
			}
			res.Model = model
			res.Latency = latency
			return res, nil
		}

		// Not every model on the gateway supports response_format. Retry once
		// without it and let ExtractJSON recover the object from prose.
		if apiErr.Status == http.StatusBadRequest && body.ResponseFormat != nil &&
			strings.Contains(strings.ToLower(apiErr.Body), "response_format") {
			body.ResponseFormat = nil
			continue
		}

		// Older/direct models may not accept reasoning_effort at all.
		if apiErr.Status == http.StatusBadRequest && body.ReasoningEffort != "" &&
			strings.Contains(strings.ToLower(apiErr.Body), "reasoning_effort") {
			body.ReasoningEffort = ""
			continue
		}

		if apiErr.Status == http.StatusTooManyRequests {
			c.penalize(model, downCooldown)
			if wait := retryAfter(apiErr.Body, 2*time.Second); wait <= 5*time.Second && attempt == 0 {
				if err := sleep(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
			return nil, apiErr
		}

		if apiErr.Status >= 500 || apiErr.Status == http.StatusPaymentRequired {
			c.penalize(model, downCooldown)
		}
		return nil, apiErr
	}
	return nil, fmt.Errorf("model %s rejected the request twice", model)
}

func (c *Client) post(ctx context.Context, body chatRequest) (*Result, *APIError) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &APIError{Status: 0, Body: err.Error(), Model: body.Model}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &APIError{Status: 0, Body: err.Error(), Model: body.Model}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &APIError{Status: 0, Body: err.Error(), Model: body.Model}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, &APIError{Status: resp.StatusCode, Body: err.Error(), Model: body.Model}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Body: string(raw), Model: body.Model}
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, &APIError{Status: resp.StatusCode, Body: "malformed JSON: " + err.Error(), Model: body.Model}
	}
	if parsed.Error != nil {
		return nil, &APIError{Status: resp.StatusCode, Body: parsed.Error.Message, Model: body.Model}
	}
	if len(parsed.Choices) == 0 {
		return nil, &APIError{Status: resp.StatusCode, Body: "no choices in response", Model: body.Model}
	}
	return &Result{
		Content:      strings.TrimSpace(parsed.Choices[0].Message.Content),
		Model:        body.Model,
		Usage:        parsed.Usage,
		FinishReason: parsed.Choices[0].FinishReason,
	}, nil
}

// Ping checks that the endpoint is reachable and the key is accepted.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("provider rejected the API key (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("provider unavailable (HTTP %d)", resp.StatusCode)
	}
	return nil
}

func (c *Client) available() []string {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, m := range c.models {
		if until, ok := c.down[m]; ok && now.Before(until) {
			continue
		}
		out = append(out, m)
	}
	// If every model is cooling down, try the chain anyway rather than fail fast.
	if len(out) == 0 {
		return append([]string(nil), c.models...)
	}
	return out
}

func (c *Client) penalize(model string, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.down[model] = c.now().Add(d)
}

// DownUntil exposes circuit-breaker state for ops endpoints.
func (c *Client) DownUntil(model string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.down[model]
	return t, ok
}

func fatal(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden
}

func retryAfter(body string, def time.Duration) time.Duration {
	var payload struct {
		RetryAfter any `json:"retry_after"`
	}
	if json.Unmarshal([]byte(body), &payload) == nil && payload.RetryAfter != nil {
		switch v := payload.RetryAfter.(type) {
		case float64:
			return time.Duration(v * float64(time.Second))
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return time.Duration(n) * time.Second
			}
		}
	}
	return def
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ExtractJSON pulls the first valid JSON object or array out of model output,
// tolerating code fences and leading or trailing prose.
func ExtractJSON(s string) ([]byte, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
	if s == "" {
		return nil, fmt.Errorf("empty content")
	}

	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}

	if json.Valid([]byte(s)) {
		return []byte(s), nil
	}

	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return nil, fmt.Errorf("no JSON value in output")
	}
	open := s[start]
	closeCh := byte('}')
	if open == '[' {
		closeCh = ']'
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case open:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				candidate := []byte(s[start : i+1])
				if json.Valid(candidate) {
					return candidate, nil
				}
				return nil, fmt.Errorf("unbalanced or invalid JSON")
			}
		}
	}
	return salvageTruncated(s[start:])
}

// salvageTruncated recovers complete findings from a response that was cut off
// mid-object. It keeps the last complete element and closes the open arrays and
// objects around it, rather than throwing away the whole review section.
func salvageTruncated(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("unterminated JSON value")
	}
	closeCh := byte('}')
	if s[0] == '[' {
		closeCh = ']'
	}
	for cut := len(s) - 1; cut > 0; cut-- {
		if s[cut] != closeCh {
			continue
		}
		if repaired, ok := closeContainers(s[:cut+1]); ok && json.Valid(repaired) {
			return repaired, nil
		}
	}
	return nil, fmt.Errorf("unterminated JSON value")
}

func closeContainers(prefix string) ([]byte, bool) {
	stack := make([]byte, 0, 8)
	inString, escaped := false, false
	for i := 0; i < len(prefix); i++ {
		ch := prefix[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) == 0 {
				return nil, false
			}
			stack = stack[:len(stack)-1]
		}
	}
	if inString {
		return nil, false
	}
	var b strings.Builder
	b.Grow(len(prefix) + len(stack))
	b.WriteString(prefix)
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	return []byte(b.String()), true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
