package githubx

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// TreeEntry is one entry in a recursive git tree.
type TreeEntry struct {
	Path string
	Size int
	Dir  bool
}

// RepoSizeKB returns GitHub's repository size estimate in kilobytes.
func (c *Client) RepoSizeKB(ctx context.Context, repo string) (int, error) {
	var out struct {
		Size int `json:"size"`
	}
	if err := c.request(ctx, http.MethodGet, "/repos/"+repo, nil, &out); err != nil {
		return 0, err
	}
	return out.Size, nil
}

// DownloadTarball streams the repository archive for a commit.
func (c *Client) DownloadTarball(ctx context.Context, repo, ref string, w io.Writer) error {
	token, err := c.authTokenForRepo(ctx, repo)
	if err != nil {
		return err
	}
	u := c.baseURL + "/repos/" + repo + "/tarball/" + url.PathEscape(ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &Error{Status: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// ListTree returns the recursive tree for a commit.
func (c *Client) ListTree(ctx context.Context, repo, ref string) ([]TreeEntry, error) {
	path := "/repos/" + repo + "/git/trees/" + url.PathEscape(ref) + "?recursive=1"
	var out struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int    `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	entries := make([]TreeEntry, 0, len(out.Tree))
	for _, e := range out.Tree {
		entries = append(entries, TreeEntry{
			Path: e.Path, Size: e.Size, Dir: e.Type == "tree",
		})
	}
	return entries, nil
}

// FileAt reads a file pinned to a commit.
func (c *Client) FileAt(ctx context.Context, repo, filePath, ref string) ([]byte, error) {
	segments := strings.Split(filePath, "/")
	for i := range segments {
		segments[i] = url.PathEscape(segments[i])
	}
	path := "/repos/" + repo + "/contents/" + strings.Join(segments, "/") + "?ref=" + url.QueryEscape(ref)

	var out struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Encoding != "base64" {
		return []byte(out.Content), nil
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
}

// GitAuthHeader builds the git http.extraheader value for a repository token.
// It never puts the token in a URL or a process argument.
func (c *Client) GitAuthHeader(ctx context.Context, repo string) (string, error) {
	token, err := c.authTokenForRepo(ctx, repo)
	if err != nil {
		return "", err
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return "AUTHORIZATION: basic " + basic, nil
}
