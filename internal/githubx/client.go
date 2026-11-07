// Package githubx is a small GitHub REST client: App JWT auth with cached
// installation tokens, pagination, rate-limit-aware retries, and review APIs.
package githubx

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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

const userAgent = "ekravo-review-v2 (+https://github.com/EOSKILLZ/ekravo-review)"

// ErrNotFound is returned for HTTP 404.
var ErrNotFound = errors.New("not found")

// Error is a non-2xx GitHub response.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("github HTTP %d: %s", e.Status, e.Message)
}

// Client is safe for concurrent use.
type Client struct {
	baseURL string
	token   string
	appID   int64
	key     *rsa.PrivateKey
	http    *http.Client

	mu             sync.Mutex
	installationID int64
	cachedToken    string
	tokenExpiry    time.Time
}

// New builds a client from either a personal token or App credentials.
func New(cfg config.GitHub) (*Client, error) {
	c := &Client{
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		token:          cfg.Token,
		appID:          cfg.AppID,
		installationID: cfg.InstallationID,
		http:           &http.Client{Timeout: 60 * time.Second},
	}
	if cfg.AppID != 0 && cfg.PrivateKeyPEM != "" {
		key, err := ParsePrivateKey(cfg.PrivateKeyPEM)
		if err != nil {
			return nil, err
		}
		c.key = key
	}
	if c.token == "" && c.key == nil {
		return nil, errors.New("github: no credentials")
	}
	return c, nil
}

// ParsePrivateKey accepts PKCS#1 and PKCS#8 PEM keys.
func ParsePrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, errors.New("github: private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github: private key is not RSA")
	}
	return key, nil
}

func (c *Client) authToken(ctx context.Context) (string, error) {
	if c.key == nil {
		return c.token, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	jwt, err := c.jwt()
	if err != nil {
		return "", err
	}

	if c.installationID == 0 {
		var installs []struct {
			ID int64 `json:"id"`
		}
		if err := c.do(ctx, http.MethodGet, "/app/installations", nil, &installs, jwt); err != nil {
			return "", fmt.Errorf("list app installations: %w", err)
		}
		if len(installs) == 0 {
			return "", errors.New("github: app has no installations")
		}
		c.installationID = installs[0].ID
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", c.installationID)
	if err := c.do(ctx, http.MethodPost, path, nil, &out, jwt); err != nil {
		return "", fmt.Errorf("create installation token: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("github: empty installation token")
	}

	expiry := out.ExpiresAt
	if expiry.IsZero() {
		expiry = time.Now().Add(50 * time.Minute)
	}
	c.cachedToken = out.Token
	c.tokenExpiry = expiry.Add(-2 * time.Minute)
	return c.cachedToken, nil
}

func (c *Client) jwt() (string, error) {
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": c.appID,
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	input := header + "." + payload
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	token, err := c.authToken(ctx)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path, body, out, token)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any, token string) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out != nil && len(data) > 0 {
				if err := json.Unmarshal(data, out); err != nil {
					return fmt.Errorf("decode github response: %w", err)
				}
			}
			return nil
		}

		if resp.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}

		apiErr := &Error{Status: resp.StatusCode, Message: truncate(string(data), 300)}
		lastErr = apiErr

		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if resp.StatusCode == http.StatusForbidden &&
			strings.Contains(strings.ToLower(string(data)), "secondary rate limit") {
			retry = true
		}
		if !retry || attempt == 2 {
			return apiErr
		}
		if err := sleep(ctx, backoff(attempt, resp.Header.Get("Retry-After"))); err != nil {
			return err
		}
	}
	return lastErr
}

func backoff(attempt int, retryAfter string) time.Duration {
	if n, err := strconv.Atoi(retryAfter); err == nil && n > 0 && n <= 30 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(1<<attempt) * time.Second
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

// PullRequest is the subset of GitHub's PR payload the bot uses.
type PullRequest struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Mergeable      *bool  `json:"mergeable"`
	MergeableState string `json:"mergeable_state"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	ChangedFiles   int    `json:"changed_files"`
	Commits        int    `json:"commits"`
	HTMLURL        string `json:"html_url"`
	User           struct {
		Login string `json:"login"`
	} `json:"user"`
	Base struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ChangedFile is one file in a PR or comparison.
type ChangedFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
	Patch            string `json:"patch"`
}

// ReviewComment is one inline comment anchored to a line in the new file.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// ReviewRequest is the body for POST /pulls/{n}/reviews.
type ReviewRequest struct {
	CommitID string          `json:"commit_id"`
	Body     string          `json:"body"`
	Event    string          `json:"event"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// IssueComment is a PR conversation comment.
type IssueComment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	IssueURL  string    `json:"issue_url"`
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// GetPullRequest fetches one PR.
func (c *Client) GetPullRequest(ctx context.Context, repo string, number int) (*PullRequest, error) {
	var pr PullRequest
	if err := c.request(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), nil, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// ListOpenPullRequests returns every open PR in a repo.
// ListRepos returns every repository the bot can reach: the App installation's
// repositories, or the token owner's repositories when running on a PAT. It is
// best-effort discovery for the dashboard, not an authorization check.
func (c *Client) ListRepos(ctx context.Context) ([]string, error) {
	if c.key != nil {
		var out []string
		for page := 1; page <= 30; page++ {
			var batch struct {
				TotalCount   int `json:"total_count"`
				Repositories []struct {
					FullName string `json:"full_name"`
					Archived bool   `json:"archived"`
					Disabled bool   `json:"disabled"`
				} `json:"repositories"`
			}
			path := fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page)
			if err := c.request(ctx, http.MethodGet, path, nil, &batch); err != nil {
				return nil, err
			}
			for _, r := range batch.Repositories {
				if r.Disabled || r.Archived {
					continue
				}
				out = append(out, r.FullName)
			}
			if len(batch.Repositories) < 100 || len(out) >= batch.TotalCount {
				break
			}
		}
		return out, nil
	}

	var out []string
	for page := 1; page <= 30; page++ {
		var batch []struct {
			FullName string `json:"full_name"`
			Archived bool   `json:"archived"`
			Disabled bool   `json:"disabled"`
		}
		path := fmt.Sprintf("/user/repos?per_page=100&affiliation=owner,organization_member&page=%d", page)
		if err := c.request(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			if r.Disabled || r.Archived {
				continue
			}
			out = append(out, r.FullName)
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

func (c *Client) ListOpenPullRequests(ctx context.Context, repo string) ([]PullRequest, error) {
	var all []PullRequest
	for page := 1; page <= 30; page++ {
		var batch []PullRequest
		path := fmt.Sprintf("/repos/%s/pulls?state=open&per_page=100&page=%d", repo, page)
		if err := c.request(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// ListChangedFiles returns the full PR diff, paginated.
func (c *Client) ListChangedFiles(ctx context.Context, repo string, number int) ([]ChangedFile, error) {
	var all []ChangedFile
	for page := 1; page <= 30; page++ {
		var batch []ChangedFile
		path := fmt.Sprintf("/repos/%s/pulls/%d/files?per_page=100&page=%d", repo, number, page)
		if err := c.request(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// CompareFiles returns files changed between two commits.
func (c *Client) CompareFiles(ctx context.Context, repo, base, head string) ([]ChangedFile, error) {
	var all []ChangedFile
	for page := 1; page <= 30; page++ {
		var out struct {
			Files []ChangedFile `json:"files"`
		}
		path := fmt.Sprintf("/repos/%s/compare/%s...%s?per_page=100&page=%d", repo, base, head, page)
		if err := c.request(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Files...)
		if len(out.Files) < 100 {
			break
		}
	}
	return all, nil
}

// MergeRequest is the body for PUT /pulls/{n}/merge.
type MergeRequest struct {
	CommitTitle   string `json:"commit_title,omitempty"`
	CommitMessage string `json:"commit_message,omitempty"`
	SHA           string `json:"sha,omitempty"`
	MergeMethod   string `json:"merge_method,omitempty"`
}

// MergePullRequest merges a pull request. Pinning SHA prevents merging a commit
// that was never reviewed.
func (c *Client) MergePullRequest(ctx context.Context, repo string, number int, req MergeRequest) (string, error) {
	var out struct {
		SHA     string `json:"sha"`
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
	}
	if err := c.request(ctx, http.MethodPut,
		fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number), req, &out); err != nil {
		return "", err
	}
	if !out.Merged {
		return "", fmt.Errorf("merge not completed: %s", out.Message)
	}
	return out.SHA, nil
}

// CreateReview posts a review, optionally with inline comments.
func (c *Client) CreateReview(ctx context.Context, repo string, number int, req ReviewRequest) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number), req, &out)
	return out.ID, err
}

// UpdateReview replaces a review body.
func (c *Client) UpdateReview(ctx context.Context, repo string, number int, reviewID int64, body string) error {
	return c.request(ctx, http.MethodPut,
		fmt.Sprintf("/repos/%s/pulls/%d/reviews/%d", repo, number, reviewID),
		map[string]string{"body": body}, nil)
}

// DeleteReview removes a previous bot review and its inline comments.
func (c *Client) DeleteReview(ctx context.Context, repo string, number int, reviewID int64) error {
	return c.request(ctx, http.MethodDelete,
		fmt.Sprintf("/repos/%s/pulls/%d/reviews/%d", repo, number, reviewID), nil, nil)
}

// CreateIssueComment posts a plain PR comment.
func (c *Client) CreateIssueComment(ctx context.Context, repo string, number int, body string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number),
		map[string]string{"body": body}, &out)
	return out.ID, err
}

// ListIssueComments returns repository comments created since a timestamp.
func (c *Client) ListIssueComments(ctx context.Context, repo string, since time.Time) ([]IssueComment, error) {
	var all []IssueComment
	for page := 1; page <= 10; page++ {
		var batch []IssueComment
		path := fmt.Sprintf("/repos/%s/issues/comments?per_page=100&page=%d&since=%s",
			repo, page, since.UTC().Format(time.RFC3339))
		if err := c.request(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// GetFile reads a repository file at the default branch.
func (c *Client) GetFile(ctx context.Context, repo, path string) ([]byte, error) {
	var out struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.request(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/contents/%s", repo, path), nil, &out); err != nil {
		return nil, err
	}
	if out.Encoding != "base64" {
		return []byte(out.Content), nil
	}
	cleaned := strings.ReplaceAll(out.Content, "\n", "")
	return base64.StdEncoding.DecodeString(cleaned)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
