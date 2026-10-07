package git

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Worktree names the working tree, including uncommitted and untracked
// (but not ignored) files, wherever a comparison accepts a head ref.
const Worktree = ":worktree"

// SnapshotWorktree records the working tree as a tree object and returns its
// SHA. It stages into a temporary copy of the index, so the user's index,
// files, and refs are untouched; Git only gains unreferenced blob and tree
// objects, which it garbage-collects later.
func (r *Repository) SnapshotWorktree(ctx context.Context) (string, error) {
	tmp, err := os.CreateTemp("", "differ-index-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	// Starting from the real index lets Git reuse its cached file stats
	// instead of rehashing every file.
	copied := copyIndex(ctx, r, tmp)
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if !copied {
		// Git creates a missing index; it rejects an empty file.
		os.Remove(tmp.Name())
	}
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	if _, err := runEnv(ctx, r.Path, env, "add", "--all", "--ignore-errors"); err != nil {
		if !copied {
			return "", err
		}
		// An index Git cannot reuse elsewhere (such as a split index) falls
		// back to staging from scratch.
		os.Remove(tmp.Name())
		if _, err := runEnv(ctx, r.Path, env, "add", "--all", "--ignore-errors"); err != nil {
			return "", err
		}
	}
	out, err := runEnv(ctx, r.Path, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func copyIndex(ctx context.Context, r *Repository, dst *os.File) bool {
	out, err := run(ctx, r.Path, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return false
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.Path, path)
	}
	src, err := os.Open(path)
	if err != nil {
		return false
	}
	defer src.Close()
	_, err = io.Copy(dst, src)
	return err == nil
}
