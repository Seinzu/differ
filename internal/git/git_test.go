package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Differ Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Differ Test", "GIT_COMMITTER_EMAIL=test@example.test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s (%v)", args, out, err)
	}
	return strings.TrimSpace(string(out))
}
func write(t *testing.T, dir, name, value string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
func commit(t *testing.T, dir, message string) string {
	t.Helper()
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", message)
	return gitCmd(t, dir, "rev-parse", "HEAD")
}
func fixture(t *testing.T) (string, *Repository, string) {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	base := commit(t, dir, "Initial commit")
	repo, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo, base
}

func TestComparisonAndPatches(t *testing.T) {
	dir, repo, base := fixture(t)
	gitCmd(t, dir, "mv", "main.go", "renamed file.go")
	write(t, dir, "odd\tname\n.txt", "<script>alert('safe')</script>\nlast line")
	write(t, dir, "binary.dat", "\x00\x01\x02")
	write(t, dir, "empty.txt", "")
	first := commit(t, dir, "Rename and add files")
	write(t, dir, "renamed file.go", "package main\n\nfunc main() { println(\"hello\") }\n")
	head := commit(t, dir, "Add greeting")
	ctx := context.Background()
	c, err := repo.Compare(ctx, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if c.Relationship != "forward" || len(c.Commits) != 2 || c.Commits[0].SHA != first || c.Commits[1].SHA != head {
		t.Fatalf("bad history: %+v", c)
	}
	renames, err := repo.Compare(ctx, base, first)
	if err != nil {
		t.Fatal(err)
	}
	var renameFound bool
	for _, f := range renames.Files {
		if f.Status == "R" && f.Path == "renamed file.go" && f.OldPath == "main.go" {
			renameFound = true
		}
	}
	if !renameFound {
		t.Fatalf("rename missing: %+v", renames.Files)
	}
	patch, err := repo.Patch(ctx, base, first, "odd\tname\n.txt", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch.Patch, "+last line\n\\ No newline at end of file") {
		t.Fatalf("bad patch: %+v", patch)
	}
	binary, err := repo.Patch(ctx, base, head, "binary.dat", 3, false)
	if err != nil || !binary.Binary {
		t.Fatalf("binary: %+v %v", binary, err)
	}
	modified, err := repo.Patch(ctx, first, head, "renamed file.go", 3, false)
	if err != nil || !strings.Contains(modified.Patch, "+func main() { println") {
		t.Fatalf("modified: %+v %v", modified, err)
	}
	reverse, err := repo.Compare(ctx, head, base)
	if err != nil || reverse.Relationship != "reverse" || len(reverse.Commits) != 2 {
		t.Fatalf("reverse: %+v %v", reverse, err)
	}
	equal, err := repo.Compare(ctx, head, head)
	if err != nil || equal.Relationship != "equal" || len(equal.Files) != 0 {
		t.Fatalf("equal: %+v %v", equal, err)
	}
	if _, err := repo.Resolve(ctx, "--help"); err == nil {
		t.Fatal("accepted option as a ref")
	}
	if _, err := repo.Patch(ctx, base, head, "../../etc/passwd", 3, false); err == nil {
		t.Fatal("accepted path outside comparison")
	}
}

func TestDivergentHistoryAndMerge(t *testing.T) {
	dir, repo, base := fixture(t)
	gitCmd(t, dir, "checkout", "-qb", "feature")
	write(t, dir, "feature.txt", "feature\n")
	feature := commit(t, dir, "Feature")
	gitCmd(t, dir, "checkout", "-q", "main")
	write(t, dir, "main.txt", "main\n")
	main := commit(t, dir, "Main work")
	ctx := context.Background()
	c, err := repo.Compare(ctx, feature, main)
	if err != nil {
		t.Fatal(err)
	}
	if c.Relationship != "diverged" || len(c.Commits) != 0 || len(c.Files) != 2 {
		t.Fatalf("diverged: %+v", c)
	}
	gitCmd(t, dir, "merge", "--no-ff", "-qm", "Merge feature", "feature")
	merged := gitCmd(t, dir, "rev-parse", "HEAD")
	c, err = repo.Compare(ctx, base, merged)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Commits) != 3 || len(c.Commits[2].Parents) != 2 || c.Commits[2].Parents[0] != main {
		t.Fatalf("merge: %+v", c.Commits)
	}
	c, err = repo.Compare(ctx, main, merged)
	if err != nil || len(c.Files) != 1 || c.Files[0].Path != "feature.txt" {
		t.Fatalf("merge diff: %+v %v", c, err)
	}
}

func TestWhitespaceDeletionAndLimits(t *testing.T) {
	dir, repo, base := fixture(t)
	write(t, dir, "main.go", "package main\n\nfunc main() { }\n")
	head := commit(t, dir, "Whitespace")
	ctx := context.Background()
	patch, err := repo.Patch(ctx, base, head, "main.go", 3, true)
	if err != nil || patch.Patch != "" {
		t.Fatalf("whitespace: %+v %v", patch, err)
	}
	gitCmd(t, dir, "rm", "main.go")
	deleted := commit(t, dir, "Delete")
	patch, err = repo.Patch(ctx, head, deleted, "main.go", 3, false)
	if err != nil || !strings.Contains(patch.Patch, "-package main") {
		t.Fatalf("deletion: %+v %v", patch, err)
	}
	write(t, dir, "large.txt", strings.Repeat("a", (2<<20)+1))
	large := commit(t, dir, "Large file")
	patch, err = repo.Patch(ctx, deleted, large, "large.txt", 3, false)
	if err != nil || !patch.TooLarge {
		t.Fatalf("large: %+v %v", patch, err)
	}
}

func TestRootCommitFromUnrelatedMerge(t *testing.T) {
	dir, repo, base := fixture(t)
	gitCmd(t, dir, "checkout", "--orphan", "other")
	gitCmd(t, dir, "rm", "-rf", ".")
	write(t, dir, "other.txt", "another history\n")
	root := commit(t, dir, "Other root")
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "merge", "--allow-unrelated-histories", "-qm", "Merge roots", "other")
	ctx := context.Background()
	c, err := repo.Compare(ctx, base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Commits) != 2 || len(c.Commits[0].Parents) != 0 {
		t.Fatalf("unexpected roots: %+v", c.Commits)
	}
	c, err = repo.Compare(ctx, ":empty", root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != ":empty" || len(c.Files) != 1 || c.Files[0].Status != "A" {
		t.Fatalf("root diff: %+v", c)
	}
	p, err := repo.Patch(ctx, c.Base, c.Head, "other.txt", 3, false)
	if err != nil || !strings.Contains(p.Patch, "+another history") {
		t.Fatalf("root patch: %+v %v", p, err)
	}
}
