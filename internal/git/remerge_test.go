package git

import (
	"context"
	"strings"
	"testing"
)

func TestRemerge(t *testing.T) {
	dir, repo, base := fixture(t)
	ctx := context.Background()
	write(t, dir, "conflict.txt", "a\nb\nc\n")
	write(t, dir, "clean.txt", "one\n")
	commit(t, dir, "Add files")
	gitCmd(t, dir, "checkout", "-qb", "side")
	write(t, dir, "conflict.txt", "a\nside\nc\n")
	write(t, dir, "side.txt", "side only\n")
	commit(t, dir, "Side change")
	gitCmd(t, dir, "checkout", "-q", "main")
	write(t, dir, "conflict.txt", "a\nmain\nc\n")
	write(t, dir, "clean.txt", "one\ntwo\n")
	commit(t, dir, "Main change")

	// A conflicted merge, resolved by hand.
	cmd := []string{"-c", "user.name=Differ Test", "-c", "user.email=test@example.test", "merge", "-q", "side"}
	if out, _, err := runStatus(ctx, dir, nil, 1, cmd...); err != nil {
		t.Fatalf("merge: %s %v", out, err)
	}
	write(t, dir, "conflict.txt", "a\nboth\nc\n")
	merge := commit(t, dir, "Merge side")

	c, err := repo.Compare(ctx, Remerge, merge)
	if err != nil {
		t.Fatal(err)
	}
	if c.Relationship != "remerge" || c.Head != merge || len(c.Files) != 1 || c.Files[0].Path != "conflict.txt" {
		t.Fatalf("bad remerge comparison: %+v", c)
	}
	if len(c.Conflicts) != 1 || !strings.Contains(c.Conflicts[0], "conflict.txt") {
		t.Fatalf("bad conflicts: %+v", c.Conflicts)
	}
	p, err := repo.Patch(ctx, c.Base, c.Head, "conflict.txt", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Patch, "-<<<<<<< ") || !strings.Contains(p.Patch, "+both") || strings.Contains(p.Patch, "clean.txt") {
		t.Fatalf("bad remerge patch: %q", p.Patch)
	}
	if status := gitCmd(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("re-merging changed the working copy: %q", status)
	}

	// A clean merge shows nothing, unless something else changed during it.
	gitCmd(t, dir, "checkout", "-qb", "other", base)
	write(t, dir, "other.txt", "other\n")
	commit(t, dir, "Other change")
	gitCmd(t, dir, "checkout", "-q", "main")
	gitCmd(t, dir, "merge", "-q", "--no-edit", "other")
	clean, err := repo.Compare(ctx, Remerge, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(clean.Files) != 0 || len(clean.Conflicts) != 0 {
		t.Fatalf("clean merge should be empty: %+v", clean)
	}
	write(t, dir, "clean.txt", "one\ntwo\nextra\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "--amend", "--no-edit")
	amended, err := repo.Compare(ctx, Remerge, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(amended.Files) != 1 || amended.Files[0].Path != "clean.txt" || amended.Additions != 1 {
		t.Fatalf("manual change during merge missing: %+v", amended.Files)
	}

	if _, err := repo.Compare(ctx, Remerge, base); err == nil {
		t.Fatal("a commit that is not a merge should not be re-merged")
	}
}
