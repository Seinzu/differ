package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeLayouts(t *testing.T) {
	dir, _, _ := fixture(t)
	gitCmd(t, dir, "branch", "feature")
	ctx := context.Background()

	// A folder holding the bare repository in .bare, with worktree folders
	// beside it, is a common layout for worktree-based workflows.
	project := filepath.Join(t.TempDir(), "project")
	gitCmd(t, dir, "clone", "-q", "--bare", dir, filepath.Join(project, ".bare"))
	write(t, project, ".git", "gitdir: ./.bare\n")
	gitCmd(t, project, "worktree", "add", "-q", "main", "main")
	gitCmd(t, project, "worktree", "add", "-q", "feature", "feature")
	write(t, filepath.Join(project, "feature"), "wip.txt", "uncommitted\n")
	plain := filepath.Join(t.TempDir(), "plain.git")
	gitCmd(t, dir, "clone", "-q", "--bare", dir, plain)

	repo, err := Open(ctx, project)
	if err != nil || !repo.Bare || repo.Path != project {
		t.Fatalf("container: %+v %v", repo, err)
	}
	info, err := repo.Info(ctx)
	if err != nil || info.Name != "project" || info.Detached || len(info.Worktrees) != 2 {
		t.Fatalf("container info: %+v %v", info, err)
	}
	for _, b := range info.Branches {
		if b.Current || b.Worktree != filepath.Join(project, b.Name) {
			t.Fatalf("branch: %+v", b)
		}
	}
	for _, w := range info.Worktrees {
		if w.Current || w.Dirty != (w.Branch == "feature") {
			t.Fatalf("worktree: %+v", w)
		}
	}
	if rg, err := repo.DefaultRange(ctx, "", 1); err != nil || rg.Branch != "main" || rg.Mode != "recent" {
		t.Fatalf("container range: %+v %v", rg, err)
	}
	if _, err := repo.Compare(ctx, "main", Worktree); err == nil {
		t.Fatal("snapshotted a bare repository")
	}
	if path, err := repo.WorktreeFor(ctx, "feature"); err != nil || !samePath(path, filepath.Join(project, "feature")) {
		t.Fatalf("worktree for feature: %q %v", path, err)
	}

	// Inside a linked worktree, the repository keeps its name and sees the
	// other worktrees.
	linked, err := Open(ctx, filepath.Join(project, "main"))
	if err != nil || linked.Bare {
		t.Fatalf("linked: %+v %v", linked, err)
	}
	info, err = linked.Info(ctx)
	if err != nil || info.Name != "project" || info.CurrentBranch != "main" || info.Dirty {
		t.Fatalf("linked info: %+v %v", info, err)
	}
	feature, err := Open(ctx, filepath.Join(project, "feature"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := feature.Compare(ctx, "main", Worktree)
	if err != nil || len(c.Files) != 1 || c.Files[0].Path != "wip.txt" || c.Name != "project" {
		t.Fatalf("feature worktree: %+v %v", c, err)
	}

	bare, err := Open(ctx, filepath.Join(plain, "refs"))
	if err != nil || !bare.Bare || !samePath(bare.Path, plain) {
		t.Fatalf("plain bare: %+v %v", bare, err)
	}
	if info, err := bare.Info(ctx); err != nil || info.Name != "plain" || len(info.Worktrees) != 0 {
		t.Fatalf("plain info: %+v %v", info, err)
	}

	// A worktree whose folder was deleted is skipped.
	if err := os.RemoveAll(filepath.Join(project, "feature")); err != nil {
		t.Fatal(err)
	}
	if worktrees, err := repo.Worktrees(ctx); err != nil || len(worktrees) != 1 {
		t.Fatalf("prunable: %+v %v", worktrees, err)
	}
}
