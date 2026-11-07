package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/diff"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/repocontext"
)

type testSource struct{ tarball []byte }

func (s testSource) RepoSizeKB(context.Context, string) (int, error) { return 10, nil }
func (s testSource) Tarball(_ context.Context, _, _ string, w io.Writer) error {
	_, err := w.Write(s.tarball)
	return err
}
func (s testSource) Tree(context.Context, string, string) ([]repocontext.TreeEntry, error) {
	return nil, nil
}
func (s testSource) FileAt(context.Context, string, string, string) ([]byte, error) {
	return nil, nil
}
func (s testSource) GitAuth(context.Context) (string, error) { return "", nil }

func testTar(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"changed.go": "package demo\nfunc Target() {}\n",
		"caller.go":  "package demo\nfunc Caller() { Target() }\n",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: "o-r-sha/" + name, Mode: 0o600,
			Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSectionContextOnAndShadow(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := repocontext.OpenCache(cacheDir, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	opts := repocontext.DefaultOptions()
	opts.CacheDir = cacheDir
	opts.Fetch = "tarball"
	opts.MinDiffChars = 0
	opts.MaxChars = 5000
	opts.RatioMax = 1
	opts.Timeout = 5 * time.Second

	engine := repocontext.New(testSource{tarball: testTar(t)}, opts, cache, slog.Default())
	defer engine.Close()

	f, err := diff.ParseFilePatch("changed.go", "modified",
		"@@ -1,1 +1,2 @@\n package demo\n+func Use() { Target() }\n")
	if err != nil {
		t.Fatal(err)
	}
	chunks := []diff.Chunk{{File: f, Hunks: f.Hunks}}

	a := &App{
		cfg:     &config.Config{Context: config.Context{Mode: "on", MaxTotalChars: 30000}},
		log:     slog.Default(),
		context: engine,
	}
	text, stats := a.sectionContext(context.Background(), "o/r", "sha", chunks)
	if text == "" || stats.Files == 0 {
		t.Fatalf("on: text = %q, stats = %+v", text, stats)
	}
	if !bytes.Contains([]byte(text), []byte("caller.go")) {
		t.Fatalf("on: missing caller:\n%s", text)
	}

	a.cfg.Context.Mode = "shadow"
	shadow, shadowStats := a.sectionContext(context.Background(), "o/r", "sha", chunks)
	if shadow != "" {
		t.Fatalf("shadow sent context: %q", shadow)
	}
	if shadowStats.Files == 0 {
		t.Fatalf("shadow did not measure retrieval: %+v", shadowStats)
	}
}
