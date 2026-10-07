package git

import (
	"context"
	"strings"
	"testing"
)

func TestDefaultRange(t *testing.T) {
	dir, repo, first := fixture(t)
	write(t, dir, "main.go", "package main\n\nfunc main() { println(1) }\n")
	second := commit(t, dir, "Second")
	gitCmd(t, dir, "checkout", "-qb", "feature")
	write(t, dir, "feature.txt", "feature\n")
	feature := commit(t, dir, "Feature")
	gitCmd(t, dir, "checkout", "-q", "main")
	write(t, dir, "later.txt", "main moved on\n")
	commit(t, dir, "Main moves on")
	gitCmd(t, dir, "checkout", "-q", "feature")
	ctx := context.Background()

	// The checked-out branch is compared with its merge base, so later main
	// commits do not appear as reversed changes.
	rg, err := repo.DefaultRange(ctx, "", 1)
	if err != nil || rg.Mode != "branch" || rg.Branch != "feature" || rg.DefaultBranch != "main" || rg.Base != second {
		t.Fatalf("feature range: %+v %v", rg, err)
	}
	c, err := repo.Compare(ctx, rg.Base, rg.Head)
	if err != nil || c.Head != feature || len(c.Files) != 1 || c.Files[0].Path != "feature.txt" {
		t.Fatalf("feature comparison: %+v %v", c, err)
	}

	rg, err = repo.DefaultRange(ctx, "main", 1)
	if err != nil || rg.Mode != "recent" || rg.Available != 3 || rg.Base != second {
		t.Fatalf("main range: %+v %v", rg, err)
	}
	if rg, err = repo.DefaultRange(ctx, "main", 2); err != nil || rg.Base != first {
		t.Fatalf("two commits: %+v %v", rg, err)
	}
	rg, err = repo.DefaultRange(ctx, "main", 3)
	if err != nil || rg.Base != ":empty" {
		t.Fatalf("whole history: %+v %v", rg, err)
	}
	if c, err = repo.Compare(ctx, rg.Base, rg.Head); err != nil || len(c.Commits) != 3 || c.Relationship != "forward" {
		t.Fatalf("whole history comparison: %+v %v", c, err)
	}

	// A branch without commits of its own shows its latest commits instead.
	gitCmd(t, dir, "branch", "stale", second)
	if rg, err = repo.DefaultRange(ctx, "stale", 1); err != nil || rg.Mode != "recent" || rg.Base != first {
		t.Fatalf("stale range: %+v %v", rg, err)
	}
	if _, err = repo.DefaultRange(ctx, "missing", 1); err == nil {
		t.Fatal("accepted a missing branch")
	}
	if _, err = repo.DefaultRange(ctx, "--help", 1); err == nil {
		t.Fatal("accepted an option as a branch")
	}

	info, err := repo.Info(ctx)
	if err != nil || info.CurrentBranch != "feature" || info.DefaultBranch != "main" || len(info.Branches) != 3 || info.Dirty {
		t.Fatalf("info: %+v %v", info, err)
	}
}

func TestWorktreeSnapshot(t *testing.T) {
	dir, repo, base := fixture(t)
	write(t, dir, ".gitignore", "ignored.txt\n")
	write(t, dir, "tracked.txt", "one\n")
	head := commit(t, dir, "Tracked")
	write(t, dir, "tracked.txt", "one\ntwo\n")
	write(t, dir, "staged.txt", "staged\n")
	gitCmd(t, dir, "add", "staged.txt")
	write(t, dir, "untracked.txt", "new\n")
	write(t, dir, "ignored.txt", "secret\n")
	statusBefore := gitCmd(t, dir, "status", "--porcelain")
	ctx := context.Background()

	c, err := repo.Compare(ctx, base, Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Worktree || c.Relationship != "forward" || len(c.Commits) != 2 || !c.Commits[1].Uncommitted || c.Commits[1].Parents[0] != head || c.Commits[1].SHA != c.Head {
		t.Fatalf("worktree history: %+v", c)
	}
	paths := []string{}
	for _, f := range c.Files {
		paths = append(paths, f.Path)
	}
	if got := strings.Join(paths, ","); got != ".gitignore,staged.txt,tracked.txt,untracked.txt" {
		t.Fatalf("worktree files: %s", got)
	}
	if after := gitCmd(t, dir, "status", "--porcelain"); after != statusBefore {
		t.Fatalf("snapshot changed the index:\n%s\nwant\n%s", after, statusBefore)
	}

	// File requests use the returned tree, as do commit-by-commit requests.
	p, err := repo.Patch(ctx, c.Base, c.Head, "tracked.txt", 3, false)
	if err != nil || !strings.Contains(p.Patch, "+two") {
		t.Fatalf("worktree patch: %+v %v", p, err)
	}
	step, err := repo.Compare(ctx, head, c.Head)
	if err != nil || step.Relationship != "snapshot" || len(step.Files) != 3 {
		t.Fatalf("uncommitted step: %+v %v", step, err)
	}
	if p, err = repo.Patch(ctx, head, Worktree, "untracked.txt", 3, false); err != nil || !strings.Contains(p.Patch, "+new") {
		t.Fatalf("untracked patch: %+v %v", p, err)
	}

	// A clean working tree adds no pseudo-commit.
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "Everything")
	clean, err := repo.Compare(ctx, base, Worktree)
	if err != nil || len(clean.Commits) != 2 || clean.Commits[1].Uncommitted {
		t.Fatalf("clean worktree: %+v %v", clean, err)
	}
	// Without a usable index, the snapshot stages from scratch.
	gitCmd(t, dir, "rm", "-q", "--cached", "-r", ".")
	write(t, dir, "tracked.txt", "changed again\n")
	fresh, err := repo.SnapshotWorktree(ctx)
	if err != nil || fresh == clean.Head {
		t.Fatalf("fresh snapshot: %s %v", fresh, err)
	}
}
