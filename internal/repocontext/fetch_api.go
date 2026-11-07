package repocontext

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sync"
)

// fetchAPI is the last-resort source: list the tree and download only the
// changed paths, their directories, and root manifests. It is partial by
// design and never pretends otherwise.
func fetchAPI(ctx context.Context, src Source, repo, ref, dir string, changed []string, opts Options) error {
	if src == nil {
		return errors.New("context: no GitHub source")
	}
	entries, err := src.Tree(ctx, repo, ref)
	if err != nil {
		return err
	}

	want := map[string]bool{}
	for _, p := range changed {
		want[p] = true
		dir := path.Dir(p)
		if dir != "." {
			want[dir+"/"] = true
		}
	}
	for _, p := range []string{"go.mod", "go.sum", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml"} {
		want[p] = true
	}

	var paths []string
	for _, e := range entries {
		if e.Dir || e.Size <= 0 || e.Size > opts.MaxFileKB*1024 {
			continue
		}
		if skipFile(e.Path, path.Base(e.Path), opts.MaxFileKB) {
			continue
		}
		if want[e.Path] || want[path.Dir(e.Path)+"/"] {
			paths = append(paths, e.Path)
			if len(paths) >= 1500 {
				break
			}
		}
	}
	if len(paths) == 0 {
		return errors.New("context: no files selected from tree")
	}

	jobs := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	workers := 4
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				data, err := src.FileAt(ctx, repo, p, ref)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					continue
				}
				target := filepath.Join(dir, filepath.FromSlash(p))
				if !within(dir, target) {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					continue
				}
				_ = os.WriteFile(target, data, 0o600)
			}
		}()
	}
	for _, p := range paths {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- p:
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	return nil
}
