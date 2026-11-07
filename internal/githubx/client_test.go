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
		case r.URL.Path == "/app/installations":
			auth := r.Header.Get("Authorization")
			jwtSeen = strings.HasPrefix(auth, "Bearer eyJ")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7}})

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
