package git

import (
	"context"
	"path/filepath"
	"strings"
)

// A Checkout is one worktree of a repository: the main working tree or a
// linked one added with `git worktree add`.
type Checkout struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Head     string `json:"head"`
	Detached bool   `json:"detached"`
	Dirty    bool   `json:"dirty"`
	// Current marks the worktree Differ opened.
	Current bool `json:"current"`
}

// Worktree status is checked for this many worktrees at most, keeping
// repository info quick for people with many checkouts.
const maxDirtyChecks = 20

func samePath(a, b string) bool {
	if ea, err := filepath.EvalSymlinks(a); err == nil {
		a = ea
	}
	if eb, err := filepath.EvalSymlinks(b); err == nil {
		b = eb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// Worktrees lists the repository's checkouts, skipping the bare repository
// itself and worktrees whose folders are missing.
func (r *Repository) Worktrees(ctx context.Context) ([]Checkout, error) {
	out, err := run(ctx, r.Path, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	worktrees := []Checkout{}
	var w Checkout
	skip := false
	flush := func() {
		if w.Path != "" && !skip {
			w.Current = !r.Bare && samePath(w.Path, r.Path)
			worktrees = append(worktrees, w)
		}
		w, skip = Checkout{}, false
	}
	for _, field := range strings.Split(string(out), "\x00") {
		key, value, _ := strings.Cut(field, " ")
		switch key {
		case "":
			flush()
		case "worktree":
			w.Path = value
		case "HEAD":
			w.Head = value
		case "branch":
			w.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			w.Detached = true
		case "bare", "prunable":
			skip = true
		}
	}
	flush()
	for i := range worktrees {
		if i == maxDirtyChecks {
			break
		}
		status, err := run(ctx, worktrees[i].Path, "status", "--porcelain", "--untracked-files=normal")
		worktrees[i].Dirty = err == nil && len(status) > 0
	}
	return worktrees, nil
}

// WorktreeFor returns the path of the worktree that has branch checked out,
// or, for an empty branch, the opened worktree when its HEAD is detached.
func (r *Repository) WorktreeFor(ctx context.Context, branch string) (string, error) {
	worktrees, err := r.Worktrees(ctx)
	if err != nil {
		return "", err
	}
	for _, w := range worktrees {
		if (branch != "" && w.Branch == branch) || (branch == "" && w.Current && w.Detached) {
			return w.Path, nil
		}
	}
	return "", nil
}

// Name is the repository's name, shared by all its worktrees: the folder
// holding .git or .bare, or a bare repository's folder without ".git".
func (r *Repository) Name(ctx context.Context) string {
	dir, err := r.CommonDir(ctx)
	if err != nil {
		return filepath.Base(r.Path)
	}
	name := filepath.Base(dir)
	if name == ".git" || name == ".bare" {
		name = filepath.Base(filepath.Dir(dir))
	}
	return strings.TrimSuffix(name, ".git")
}
