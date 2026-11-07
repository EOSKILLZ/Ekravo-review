package repocontext

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
)

// fetchGit performs a blobless partial clone, scoped to the changed directories
// when there are few enough of them. Sparse checkout keeps a monorepo within a
// fraction of its full size.
func fetchGit(ctx context.Context, src Source, repo, ref, dir string, changed []string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return errors.New("git is not installed")
	}
	if src == nil {
		return errors.New("context: no GitHub source")
	}

	if err := runGit(ctx, git, dir, nil, "init", "--quiet"); err != nil {
		return err
	}
	clean := strings.TrimSuffix(repo, ".git")
	cloneURL := "https://github.com/" + clean + ".git"
	if err := runGit(ctx, git, dir, nil, "remote", "add", "origin", cloneURL); err != nil {
		return err
	}

	auth, _ := src.GitAuth(ctx, repo)
	env := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_LFS_SKIP_SMUDGE=1",
	}
	if auth != "" {
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
			"GIT_CONFIG_VALUE_0="+auth,
		)
	}

	_ = runGit(ctx, git, dir, env, "config", "core.sparseCheckout", "true")
	_ = runGit(ctx, git, dir, env, "sparse-checkout", "init", "--cone")
	if dirs := sparseDirs(changed); len(dirs) > 0 {
		args := append([]string{"sparse-checkout", "set"}, dirs...)
		if err := runGit(ctx, git, dir, env, args...); err != nil {
			return err
		}
	}

	if err := runGit(ctx, git, dir, env, "fetch", "--depth=1", "--filter=blob:none", "origin", ref); err != nil {
		// Never substitute the default branch: context must be pinned to the
		// PR's base commit. Let the caller fall back to the ref-pinned API.
		return fmt.Errorf("fetch %s: %w", ref, err)
	}
	if err := runGit(ctx, git, dir, env, "checkout", "--quiet", "--force", "FETCH_HEAD"); err != nil {
		return err
	}
	return nil
}

func runGit(ctx context.Context, git, dir string, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}

// sparseDirs converts changed file paths into a small set of cone directories.
func sparseDirs(changed []string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, p := range changed {
		p = path.Clean(p)
		dir := path.Dir(p)
		if dir == "." || dir == "/" || strings.HasPrefix(dir, "..") {
			continue
		}
		top := strings.Split(dir, "/")[0]
		if top == "" || seen[top] {
			continue
		}
		seen[top] = true
		dirs = append(dirs, top)
	}
	if len(dirs) > 20 {
		return nil
	}
	return dirs
}
