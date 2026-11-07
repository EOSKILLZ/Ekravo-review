package repocontext

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/diff"
)

type fakeSource struct {
	tarball []byte
	calls   atomic.Int64
}

func (f *fakeSource) RepoSizeKB(context.Context, string) (int, error) { return 10, nil }
func (f *fakeSource) Tarball(_ context.Context, _, _ string, w io.Writer) error {
	f.calls.Add(1)
	_, err := w.Write(f.tarball)
	return err
}
func (f *fakeSource) Tree(context.Context, string, string) ([]TreeEntry, error) { return nil, nil }
func (f *fakeSource) FileAt(context.Context, string, string, string) ([]byte, error) {
	return nil, nil
}
func (f *fakeSource) GitAuth(context.Context) (string, error) { return "", nil }

func makeTar(t *testing.T, prefix string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{Name: prefix + name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
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

func TestFetchTarballRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSource{tarball: makeTar(t, "repo-sha/", map[string]string{
		"pkg/a.go":   "package pkg\nfunc A() {}\n",
		"../evil.go": "package evil\n",
	})}
	opts := DefaultOptions()
	if err := fetchTarball(context.Background(), src, "o/r", "sha", dir, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "a.go")); err != nil {
		t.Fatalf("valid file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.go")); !os.IsNotExist(err) {
		t.Fatalf("traversal escaped: %v", err)
	}
}

func TestEngineBuildsAndReusesCache(t *testing.T) {
	cacheDir := t.TempDir()
	tarball := makeTar(t, "o-r-sha/", map[string]string{
		"changed.go": "package demo\nfunc Target() {}\n",
		"caller.go":  "package demo\nfunc Caller() { Target() }\n",
	})
	src := &fakeSource{tarball: tarball}
	opts := DefaultOptions()
	opts.CacheDir = cacheDir
	opts.Fetch = "tarball"
	opts.Timeout = 5 * time.Second
	opts.MinDiffChars = 0
	opts.MaxChars = 5000
	opts.RatioMax = 1

	cache, err := OpenCache(cacheDir, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	eng := New(src, opts, cache, nil)
	defer eng.Close()

	patch := "@@ -1,1 +1,2 @@\n package demo\n+func Use() { Target() }\n"
	f, err := diff.ParseFilePatch("changed.go", "modified", patch)
	if err != nil {
		t.Fatal(err)
	}

	text, stats := eng.Context(context.Background(), "o/r", "sha", []*diff.File{f})
	if text == "" || stats.Fetched < 2 {
		t.Fatalf("context = %q, stats = %+v", text, stats)
	}
	if !contains(text, "caller.go") {
		t.Fatalf("missing caller:\n%s", text)
	}
	if src.calls.Load() != 1 {
		t.Fatalf("tarball calls = %d, want 1", src.calls.Load())
	}

	// A second engine over the same cache must not re-download.
	cache2, err := OpenCache(cacheDir, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer cache2.Close()
	eng2 := New(src, opts, cache2, nil)
	defer eng2.Close()
	_, _ = eng2.Context(context.Background(), "o/r", "sha", []*diff.File{f})
	if src.calls.Load() != 1 {
		t.Fatalf("tarball re-downloaded: %d", src.calls.Load())
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
