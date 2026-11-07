package repocontext

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var errTooLarge = errors.New("context: archive exceeds size limit")

type limitWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n+int64(len(p)) > l.max {
		return 0, errTooLarge
	}
	n, err := l.w.Write(p)
	l.n += int64(n)
	return n, err
}

func fetchTarball(ctx context.Context, src Source, repo, ref, dir string, opts Options) error {
	if src == nil {
		return errors.New("context: no GitHub source")
	}
	tmp, err := os.CreateTemp(dir, ".download-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	maxBytes := int64(opts.MaxRepoMB) * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = 200 << 20
	}
	lw := &limitWriter{w: tmp, max: maxBytes}
	if err := src.Tarball(ctx, repo, ref, lw); err != nil {
		return fmt.Errorf("download tarball: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	gz, err := gzip.NewReader(tmp)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	maxFile := int64(opts.MaxFileKB) * 1024
	if maxFile <= 0 {
		maxFile = 512 << 10
	}
	files := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tarball: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if files >= maxIndexFiles {
			break
		}
		name := path.Clean(strings.ReplaceAll(hdr.Name, "\\", "/"))
		// GitHub tarballs put everything under <owner>-<repo>-<sha>/.
		if i := strings.IndexByte(name, '/'); i > 0 {
			name = name[i+1:]
		}
		if name == "" || name == "." || path.IsAbs(name) || strings.HasPrefix(name, "../") ||
			strings.Contains(name, "/../") {
			continue
		}
		if skipDir(path.Base(path.Dir(name))) || skipFile(name, path.Base(name), opts.MaxFileKB) {
			continue
		}
		if hdr.Size <= 0 || hdr.Size > maxFile {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if !within(dir, target) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, cerr := io.CopyN(f, tr, hdr.Size)
		f.Close()
		if cerr != nil {
			return fmt.Errorf("extract %s: %w", name, cerr)
		}
		files++
	}
	if files == 0 {
		return errors.New("context: tarball contained no source files")
	}
	return nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
