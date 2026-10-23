package conversations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"differ/internal/git"
)

func TestCommentAnchoring(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "app.go"), "package app\n\nfunc a() {}\n\nfunc b() {}\n")
	first := commitAll(t, dir, "first")
	write(t, filepath.Join(dir, "app.go"), "package app\n\nfunc a() {}\n\nfunc b() { return }\n\nfunc c() {}\n")
	write(t, filepath.Join(dir, "other.go"), "package app\n")
	second := commitAll(t, dir, "second")
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "differ.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Comment on func c() as added by the second commit.
	thread := Thread{GitDir: "repo", Path: "app.go", Side: "new", StartLine: 7, Base: first, Head: second, Branch: "main"}
	if err := Anchor(ctx, repo, &thread); err != nil {
		t.Fatal(err)
	}
	if thread.Snippet != "func c() {}" || thread.EndLine != 7 || strings.Join(thread.Before, "|") != "|func b() { return }|" {
		t.Fatalf("bad anchor: %+v", thread)
	}
	created, err := store.CreateThread(ctx, thread, Comment{Body: "  Why c?  "})
	if err != nil {
		t.Fatal(err)
	}
	if created.Comments[0].Body != "Why c?" || created.Comments[0].Author != "You" {
		t.Fatalf("bad comment: %+v", created.Comments)
	}
	if _, err := store.Reply(ctx, created.ID, Comment{Body: "For symmetry.", AuthorKind: "agent"}); err != nil {
		t.Fatal(err)
	}
	if err := Anchor(ctx, repo, &Thread{Path: "app.go", Side: "new", StartLine: 9, Base: first, Head: second}); err == nil {
		t.Fatal("anchored past the end of the file")
	}
	if err := Anchor(ctx, repo, &Thread{Path: "other.go", Side: "old", StartLine: 1, Base: first, Head: second}); err == nil {
		t.Fatal("anchored to the old side of an added file")
	}
	general := Thread{GitDir: "repo", Base: first, Head: second, Branch: "main"}
	if err := Anchor(ctx, repo, &general); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateThread(ctx, general, Comment{Body: "Looks good overall"}); err != nil {
		t.Fatal(err)
	}

	// Lines inserted above move the comment; changing the line outdates it.
	write(t, filepath.Join(dir, "app.go"), "package app\n\nimport \"fmt\"\n\nfunc a() {}\n\nfunc b() { return }\n\nfunc c() {}\n")
	third := commitAll(t, dir, "third")
	threads, err := store.Threads(ctx, "repo")
	if err != nil || len(threads) != 2 || len(threads[0].Comments) != 2 || threads[0].Comments[1].Author != "Claude" {
		t.Fatalf("threads %+v, %v", threads, err)
	}
	place := func(base, head, branch string) []Thread {
		t.Helper()
		placed, err := Place(ctx, repo, threads, base, head, branch)
		if err != nil {
			t.Fatal(err)
		}
		return placed
	}
	if p := place(first, second, ""); len(p) != 2 || *p[0].Placement != (Placement{"exact", "app.go", 7, 7}) {
		t.Fatalf("original comparison: %+v", p)
	}
	if p := place(second, third, ""); len(p) != 1 || *p[0].Placement != (Placement{"moved", "app.go", 9, 9}) {
		t.Fatalf("after insert: %+v", p)
	}
	if p := place(first, third, "main"); len(p) != 2 || p[1].Path != "" {
		t.Fatalf("branch comparison should include the general comment: %+v", p)
	}
	write(t, filepath.Join(dir, "app.go"), "package app\n\nfunc c() { panic(1) }\n")
	fourth := commitAll(t, dir, "fourth")
	if p := place(third, fourth, ""); p[0].Placement.Status != "outdated" {
		t.Fatalf("after change: %+v", p[0].Placement)
	}
	// On the old side of the next commit the original lines are still there.
	threads[0].Side = "old"
	if p := place(third, fourth, ""); p[0].Placement.Status != "moved" || p[0].Placement.StartLine != 9 {
		t.Fatalf("old side: %+v", p[0].Placement)
	}
	if p := place(third, ":empty", ""); len(p) != 1 || p[0].Path != "app.go" {
		t.Fatalf("deleted side: %+v", p)
	}

	resolved, err := store.Resolve(ctx, created.ID, true, "Claude")
	if err != nil || resolved.ResolvedBy != "Claude" || resolved.ResolvedAt == "" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	edited, err := store.EditComment(ctx, resolved.Comments[0].ID, "Why add c?")
	if err != nil || edited.Comments[0].Body != "Why add c?" || edited.Comments[0].EditedAt == "" {
		t.Fatalf("edit: %+v %v", edited, err)
	}
	for i, c := range edited.Comments {
		gitDir, remaining, err := store.DeleteComment(ctx, c.ID)
		if err != nil || gitDir != "repo" || (remaining == nil) != (i == len(edited.Comments)-1) {
			t.Fatalf("delete %d: %v %v", i, remaining, err)
		}
	}
	if _, err := store.Thread(ctx, created.ID); !IsNotFound(err) {
		t.Fatalf("thread without comments should be gone: %v", err)
	}
	if _, err := store.CreateThread(ctx, general, Comment{Body: strings.Repeat("x", maxCommentBytes+1)}); err == nil {
		t.Fatal("accepted an oversized comment")
	}
}

func TestLocatePrefersMatchingContext(t *testing.T) {
	lines := strings.Split("a\nx\nb\nc\nx\nd", "\n")
	thread := Thread{Snippet: "x", Before: []string{"c"}, After: []string{"d"}, StartLine: 2}
	if got := locate(lines, thread); got != 5 {
		t.Fatalf("got %d, want the x between c and d", got)
	}
	thread.Before, thread.After = nil, nil
	if got := locate(lines, thread); got != 2 {
		t.Fatalf("got %d, want the nearest x", got)
	}
}
