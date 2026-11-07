package githubx

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
)

func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func TestAppAuthCreatesInstallationTokenAndFetchesPR(t *testing.T) {
	var jwtSeen, tokenSeen bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r/installation":
			auth := r.Header.Get("Authorization")
			jwtSeen = strings.HasPrefix(auth, "Bearer eyJ")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7})

		case r.URL.Path == "/app/installations/7/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "inst-token",
				"expires_at": time.Now().Add(time.Hour),
			})

		case r.URL.Path == "/repos/o/r/pulls/1":
			tokenSeen = r.Header.Get("Authorization") == "Bearer inst-token"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number": 1,
				"title":  "Fix it",
				"state":  "open",
				"head":   map[string]any{"sha": "abc", "ref": "feat"},
				"base":   map[string]any{"ref": "main", "repo": map[string]any{"full_name": "o/r"}},
			})

		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(config.GitHub{
		AppID: 42, PrivateKeyPEM: testKeyPEM(t), BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pr, err := c.GetPullRequest(context.Background(), "o/r", 1)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if pr.Title != "Fix it" || pr.Head.SHA != "abc" || pr.Base.Repo.FullName != "o/r" {
		t.Fatalf("pr = %+v", pr)
	}
	if !jwtSeen {
		t.Error("app installations call did not use a JWT")
	}
	if !tokenSeen {
		t.Error("PR call did not use the installation token")
	}
}

func TestAppUsesTheInstallationThatOwnsEachRepo(t *testing.T) {
	minted := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/one/installation":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/repos/o/two/installation":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9})
		case "/app/installations/7/access_tokens":
			minted["one"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "token-a", "expires_at": time.Now().Add(time.Hour)})
		case "/app/installations/9/access_tokens":
			minted["two"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "token-b", "expires_at": time.Now().Add(time.Hour)})
		case "/repos/o/one/pulls/1":
			if got := r.Header.Get("Authorization"); got != "Bearer token-a" {
				t.Errorf("o/one used %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 1, "title": "One", "head": map[string]any{"sha": "a"}})
		case "/repos/o/two/pulls/2":
			if got := r.Header.Get("Authorization"); got != "Bearer token-b" {
				t.Errorf("o/two used %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 2, "title": "Two", "head": map[string]any{"sha": "b"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(config.GitHub{AppID: 42, PrivateKeyPEM: testKeyPEM(t), BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		repo string
		n    int
	}{{"o/one", 1}, {"o/two", 2}, {"o/one", 1}} {
		if _, err := c.GetPullRequest(context.Background(), tc.repo, tc.n); err != nil {
			t.Fatalf("%s#%d: %v", tc.repo, tc.n, err)
		}
	}
	if minted["one"] != 1 || minted["two"] != 1 {
		t.Fatalf("minted = %v, want one token per installation", minted)
	}
}

func TestListReposUnionsInstallations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7}, {"id": 9}})
		case "/app/installations/7/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "token-a", "expires_at": time.Now().Add(time.Hour)})
		case "/app/installations/9/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "token-b", "expires_at": time.Now().Add(time.Hour)})
		case "/installation/repositories":
			switch r.Header.Get("Authorization") {
			case "Bearer token-a":
				_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 2, "repositories": []map[string]any{
					{"full_name": "o/one"}, {"full_name": "o/archived", "archived": true},
				}})
			case "Bearer token-b":
				_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "repositories": []map[string]any{
					{"full_name": "ekravo/Ekravo-review"},
				}})
			default:
				t.Errorf("unexpected token %q", r.Header.Get("Authorization"))
				_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 0, "repositories": []any{}})
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(config.GitHub{AppID: 42, PrivateKeyPEM: testKeyPEM(t), BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	want := []string{"ekravo/Ekravo-review", "o/one"}
	if strings.Join(repos, ",") != strings.Join(want, ",") {
		t.Fatalf("repos = %v, want %v", repos, want)
	}
}

func TestRepoWithoutInstallationReportsIt(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	c, err := New(config.GitHub{AppID: 42, PrivateKeyPEM: testKeyPEM(t), BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetPullRequest(context.Background(), "o/r", 1); err == nil ||
		!strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v, want a not-installed error", err)
	}
}

func TestGetFileDecodesBase64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path.Base(r.URL.Path) != ".ekravo.yml" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"encoding": "base64",
			"content":  "dG9uZTogc3RyaWN0Cg==\n",
		})
	}))
	defer srv.Close()

	c, err := New(config.GitHub{Token: "t", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.GetFile(context.Background(), "o/r", ".ekravo.yml")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if strings.TrimSpace(string(data)) != "tone: strict" {
		t.Fatalf("content = %q", data)
	}
}

func TestMissingFileIsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	c, err := New(config.GitHub{Token: "t", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetFile(context.Background(), "o/r", ".ekravo.yml"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
