package conversations

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"differ/internal/git"
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

func write(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, message string) string {
	t.Helper()
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", message)
	return gitCmd(t, dir, "rev-parse", "HEAD")
}

// transcript writes a Claude Code style JSON-lines transcript.
func transcript(t *testing.T, path string, entries ...any) {
	t.Helper()
	var lines []string
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	write(t, path, strings.Join(lines, "\n")+"\n")
}

func user(content any) map[string]any {
	return map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}}
}
func assistant(blocks ...map[string]any) map[string]any {
	return map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": blocks}}
}
func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
func edit(tool, path string) map[string]any {
	return map[string]any{"type": "tool_use", "name": tool, "input": map[string]any{"file_path": path}}
}

func hook(t *testing.T, db string, input HookInput) {
	t.Helper()
	data, _ := json.Marshal(input)
	if err := HandleClaudeHook(context.Background(), strings.NewReader(string(data)), db); err != nil {
		t.Fatal(err)
	}
}

func TestReadTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	transcript(t, path,
		user("Earlier prompt"),
		assistant(text("Earlier answer"), edit("Write", "/repo/old.go")),
		user([]map[string]any{{"type": "text", "text": "Fix the bug"}}),
		assistant(text("Looking."), edit("Edit", "/repo/a.go")),
		user([]map[string]any{{"type": "tool_result", "content": "ok"}}),
		map[string]any{"type": "assistant", "isSidechain": true, "message": map[string]any{"content": []map[string]any{text("subagent chatter"), edit("Write", "/repo/side.go")}}},
		assistant(edit("MultiEdit", "/repo/b.go"), text("Fixed it.")),
	)
	response, files := readTranscript(path)
	if response != "Looking.\n\nFixed it." {
		t.Fatalf("response: %q", response)
	}
	if strings.Join(files, ",") != "/repo/a.go,/repo/b.go" {
		t.Fatalf("files: %v", files)
	}
	if response, files := readTranscript(filepath.Join(t.TempDir(), "missing")); response != "" || files != nil {
		t.Fatal("missing transcript")
	}
}

func TestCaptureAndLinkThroughRebase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "nested dir", "conversations.db")
	ctx := context.Background()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	commitAll(t, dir, "Base")
	gitCmd(t, dir, "checkout", "-qb", "feature")
	log := filepath.Join(t.TempDir(), "session.jsonl")

	// Turn 1: the agent edits and commits.
	hook(t, db, HookInput{SessionID: "s1", HookEventName: "UserPromptSubmit", Cwd: dir, Prompt: "Add a greeting", TranscriptPath: log})
	write(t, filepath.Join(dir, "greet.go"), "package greet\n")
	committed := commitAll(t, dir, "Add greeting")
	transcript(t, log, user("Add a greeting"), assistant(text("Added greet.go and committed."), edit("Write", filepath.Join(dir, "greet.go"))))
	hook(t, db, HookInput{SessionID: "s1", HookEventName: "Stop", Cwd: dir, TranscriptPath: log})

	// Turn 2: edits without committing; the user commits later.
	hook(t, db, HookInput{SessionID: "s1", HookEventName: "UserPromptSubmit", Cwd: filepath.Join(dir), Prompt: "Document it", TranscriptPath: log})
	write(t, filepath.Join(dir, "README.md"), "# Greet\n")
	transcript(t, log, user("Document it"), assistant(edit("Write", "README.md")))
	hook(t, db, HookInput{SessionID: "s1", HookEventName: "Stop", Cwd: dir, TranscriptPath: log, LastAssistantMessage: "Wrote the README."})
	time.Sleep(1100 * time.Millisecond) // Author dates have one-second resolution.
	write(t, filepath.Join(dir, "README.md"), "# Greet\n\nEdited by hand.\n")
	documented := commitAll(t, dir, "Docs")

	// Outside a repository, hooks record nothing.
	hook(t, db, HookInput{SessionID: "s2", HookEventName: "UserPromptSubmit", Cwd: t.TempDir(), Prompt: "Elsewhere"})

	store, err := Open(db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	gitDir, _ := repo.CommonDir(ctx)
	turns, err := store.Turns(ctx, gitDir, 10)
	if err != nil || len(turns) != 2 {
		t.Fatalf("turns: %+v %v", turns, err)
	}
	first, second := turns[0], turns[1]
	if first.Prompt != "Add a greeting" || first.Response != "Added greet.go and committed." || first.Branch != "feature" || first.HeadAfter != committed || len(first.Files) != 1 || first.Files[0].Path != "greet.go" || first.Files[0].Blob == "" {
		t.Fatalf("first turn: %+v", first)
	}
	if second.Response != "Wrote the README." || len(second.Files) != 1 || second.Files[0].Path != "README.md" {
		t.Fatalf("second turn: %+v", second)
	}

	link := func(rewrites map[string]string, shas ...string) []LinkedTurn {
		t.Helper()
		linked, err := LinkCommits(ctx, repo, turns, rewrites, shas, "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		return linked
	}
	linked := link(nil, committed, documented)
	if len(linked[0].Links) != 1 || linked[0].Links[0] != (Link{committed, "commit"}) {
		t.Fatalf("first links: %+v", linked[0].Links)
	}
	// The second turn ran at the first commit; its README edit was changed
	// before committing, so only path and time link it to the Docs commit.
	if len(linked[1].Links) != 2 || linked[1].Links[0] != (Link{committed, "head"}) || linked[1].Links[1] != (Link{documented, "path"}) {
		t.Fatalf("second links: %+v", linked[1].Links)
	}

	// Rebase onto a moved main; record the rewrite as the Git hook would.
	gitCmd(t, dir, "checkout", "-q", "main")
	write(t, filepath.Join(dir, "main.txt"), "main moved\n")
	commitAll(t, dir, "Main moves")
	gitCmd(t, dir, "checkout", "-q", "feature")
	gitCmd(t, dir, "rebase", "-q", "main")
	rebasedDocs := gitCmd(t, dir, "rev-parse", "HEAD")
	rebased := gitCmd(t, dir, "rev-parse", "HEAD~1")

	if _, err := repo.Commits(ctx, []string{"--all"}); err == nil {
		t.Fatal("accepted an option as a commit")
	}
	// Without rewrite records, the rebased commit keeps its author, author
	// date, and subject, so the first turn is still known to have made it.
	linked = link(nil, rebased, rebasedDocs)
	if len(linked[0].Links) != 1 || linked[0].Links[0] != (Link{rebased, "commit"}) || linked[1].Links[0] != (Link{rebased, "head"}) || linked[1].Links[1] != (Link{rebasedDocs, "path"}) {
		t.Fatalf("identity links: %+v", linked)
	}
	input := committed + " " + rebased + "\n" + documented + " " + rebasedDocs + "\n"
	if err := HandlePostRewrite(ctx, strings.NewReader(input), "rebase", dir, db); err != nil {
		t.Fatal(err)
	}
	rewrites, err := store.Rewrites(ctx, gitDir)
	if err != nil || rewrites[committed] != rebased {
		t.Fatalf("rewrites: %v %v", rewrites, err)
	}
	linked = link(rewrites, rebased, rebasedDocs)
	if linked[0].Links[0] != (Link{rebased, "commit"}) {
		t.Fatalf("rewritten links: %+v", linked[0].Links)
	}
}

func TestInstall(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	settings := filepath.Join(dir, ".claude", "settings.json")
	write(t, settings, `{"permissions": {"allow": ["Bash(ls)"]}, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "echo done"}]}]}}`)
	ctx := context.Background()
	if _, err := Install(ctx, dir, "/opt/my tools/differ", false); err != nil {
		t.Fatal(err)
	}
	report, err := Install(ctx, dir, "/opt/my tools/differ", false)
	if err != nil || !strings.Contains(strings.Join(report, "\n"), "already present") {
		t.Fatalf("second install: %v %v", report, err)
	}
	data, _ := os.ReadFile(settings)
	var parsed struct {
		Permissions map[string]any `json:"permissions"`
		Hooks       map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Permissions == nil || len(parsed.Hooks["Stop"]) != 2 || len(parsed.Hooks["UserPromptSubmit"]) != 1 {
		t.Fatalf("settings: %s", data)
	}
	if command := parsed.Hooks["Stop"][1].Hooks[0].Command; command != `command -v '/opt/my tools/differ' >/dev/null 2>&1 && '/opt/my tools/differ' hook claude || true` {
		t.Fatalf("command: %s", command)
	}
	hook, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", "post-rewrite"))
	if err != nil || !strings.Contains(string(hook), `hook post-rewrite "$1"`) {
		t.Fatalf("post-rewrite hook: %s %v", hook, err)
	}

	// An existing post-rewrite hook is left alone.
	other := t.TempDir()
	gitCmd(t, other, "init", "-q")
	write(t, filepath.Join(other, ".git", "hooks", "post-rewrite"), "#!/bin/sh\necho mine\n")
	report, err = Install(ctx, other, "differ", true)
	if err != nil || !strings.Contains(strings.Join(report, "\n"), "Left the existing") {
		t.Fatalf("existing hook: %v %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(other, ".claude", "settings.local.json")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallInBareRepository(t *testing.T) {
	source := t.TempDir()
	gitCmd(t, source, "init", "-q", "-b", "main")
	write(t, filepath.Join(source, "a.txt"), "a\n")
	commitAll(t, source, "Init")
	gitCmd(t, source, "branch", "feature")
	project := filepath.Join(t.TempDir(), "project")
	gitCmd(t, source, "clone", "-q", "--bare", source, filepath.Join(project, ".bare"))
	write(t, filepath.Join(project, ".git"), "gitdir: ./.bare\n")
	gitCmd(t, project, "worktree", "add", "-q", "main", "main")
	gitCmd(t, project, "worktree", "add", "-q", "feature", "feature")
	if _, err := Install(context.Background(), project, "differ", true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"main/.claude/settings.local.json", "feature/.claude/settings.local.json", ".bare/hooks/post-rewrite"} {
		if _, err := os.Stat(filepath.Join(project, path)); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".claude")); err == nil {
		t.Fatal("wrote settings into the bare repository folder")
	}
}
