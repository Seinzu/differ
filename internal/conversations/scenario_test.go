package conversations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"differ/internal/git"
)

// The reported workflow: on a branch, ask about the latest commit, then
// change a file and amend that commit.
func TestQuestionThenAmend(t *testing.T) {
	for _, tc := range []struct {
		name          string
		claudeAmends  bool
		recordRewrite bool
	}{
		{"user amends, hook recorded", false, true},
		{"user amends, no hook", false, false},
		{"claude amends, hook recorded", true, true},
		{"claude amends, no hook", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(t.TempDir(), "c.db")
			ctx := context.Background()
			gitCmd(t, dir, "init", "-q", "-b", "main")
			write(t, filepath.Join(dir, "base.txt"), "base\n")
			commitAll(t, dir, "Base")
			gitCmd(t, dir, "checkout", "-qb", "feature")
			write(t, filepath.Join(dir, "app.go"), "package app\n")
			original := commitAll(t, dir, "Add app")
			log := filepath.Join(t.TempDir(), "s.jsonl")

			hook(t, db, HookInput{SessionID: "s", HookEventName: "UserPromptSubmit", Cwd: dir, Prompt: "What does the latest commit do?", TranscriptPath: log})
			transcript(t, log, user("What does the latest commit do?"), assistant(text("It adds app.go.")))
			hook(t, db, HookInput{SessionID: "s", HookEventName: "Stop", Cwd: dir, TranscriptPath: log})

			if tc.claudeAmends {
				hook(t, db, HookInput{SessionID: "s", HookEventName: "UserPromptSubmit", Cwd: dir, Prompt: "Add a func and amend", TranscriptPath: log})
			}
			write(t, filepath.Join(dir, "app.go"), "package app\n\nfunc Run() {}\n")
			gitCmd(t, dir, "commit", "-qa", "--amend", "--no-edit")
			amended := gitCmd(t, dir, "rev-parse", "HEAD")
			if tc.claudeAmends {
				transcript(t, log, user("Add a func and amend"), assistant(text("Done."), edit("Edit", "app.go")))
				hook(t, db, HookInput{SessionID: "s", HookEventName: "Stop", Cwd: dir, TranscriptPath: log})
			}
			if tc.recordRewrite {
				if err := HandlePostRewrite(ctx, strings.NewReader(original+" "+amended+"\n"), "amend", dir, db); err != nil {
					t.Fatal(err)
				}
			}

			store, err := Open(db, false)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			repo, _ := git.Open(ctx, dir)
			gitDir, _ := repo.CommonDir(ctx)
			turns, _ := store.Turns(ctx, gitDir, 10)
			rewrites, _ := store.Rewrites(ctx, gitDir)
			linked, err := LinkCommits(ctx, repo, turns, rewrites, []string{amended}, "", "", "", "feature")
			if err != nil {
				t.Fatal(err)
			}
			for _, lt := range linked {
				// The question changed nothing: it links through HEAD.
				if lt.Prompt == "What does the latest commit do?" && len(lt.Links) > 0 && lt.Links[0].Match != "head" {
					t.Errorf("question linked by %q", lt.Links[0].Match)
				}
				if len(lt.Links) == 0 || lt.Links[0].SHA != amended {
					t.Errorf("turn %q not linked to the amended commit: %+v", lt.Prompt, lt.Links)
				}
			}
		})
	}
}

func TestBranchFallbackAndOtherBranches(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "c.db")
	ctx := context.Background()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	commitAll(t, dir, "Base")
	ask := func(prompt string) {
		hook(t, db, HookInput{SessionID: "s", HookEventName: "UserPromptSubmit", Cwd: dir, Prompt: prompt})
		hook(t, db, HookInput{SessionID: "s", HookEventName: "Stop", Cwd: dir, LastAssistantMessage: "ok"})
	}
	ask("On main")
	gitCmd(t, dir, "checkout", "-qb", "feature")
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	kept := commitAll(t, dir, "Kept")
	write(t, filepath.Join(dir, "b.txt"), "b\n")
	commitAll(t, dir, "Dropped later")
	ask("About the dropped commit")
	// Dropping a commit (here with reset) leaves nothing to link to, but the
	// turn still happened on this branch.
	gitCmd(t, dir, "reset", "-q", "--hard", kept)

	store, _ := Open(db, false)
	defer store.Close()
	repo, _ := git.Open(ctx, dir)
	gitDir, _ := repo.CommonDir(ctx)
	turns, _ := store.Turns(ctx, gitDir, 10)
	linked, err := LinkCommits(ctx, repo, turns, nil, []string{kept}, "", "", "", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 2 || len(linked[0].Links) != 0 || linked[0].OnBranch {
		t.Fatalf("main turn: %+v", linked[0])
	}
	if len(linked[1].Links) != 0 || !linked[1].OnBranch {
		t.Fatalf("feature turn: %+v", linked[1])
	}
}

// Work on a branch from its start: questions and edits before the first
// commit (HEAD still on the commit the branch started from), then more
// turns between commits.
func TestWholeBranchHistory(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "c.db")
	ctx := context.Background()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	commitAll(t, dir, "Base")
	gitCmd(t, dir, "checkout", "-qb", "feature")
	log := filepath.Join(t.TempDir(), "s.jsonl")
	turn := func(prompt string, edits ...string) {
		hook(t, db, HookInput{SessionID: "s", HookEventName: "UserPromptSubmit", Cwd: dir, Prompt: prompt, TranscriptPath: log})
		var blocks []map[string]any
		for _, f := range edits {
			write(t, filepath.Join(dir, f), prompt+"\n")
			blocks = append(blocks, edit("Write", f))
		}
		transcript(t, log, user(prompt), assistant(append(blocks, text("ok"))...))
		hook(t, db, HookInput{SessionID: "s", HookEventName: "Stop", Cwd: dir, TranscriptPath: log})
	}
	turn("How should I structure this?")
	turn("Draft the parser", "parser.go")
	// The user tweaks the draft before committing, so content does not match.
	write(t, filepath.Join(dir, "parser.go"), "package parser\n")
	first := commitAll(t, dir, "Add parser")
	turn("Review the parser")
	write(t, filepath.Join(dir, "lexer.go"), "package parser\n")
	second := commitAll(t, dir, "Add lexer")

	store, _ := Open(db, false)
	defer store.Close()
	repo, _ := git.Open(ctx, dir)
	gitDir, _ := repo.CommonDir(ctx)
	turns, _ := store.Turns(ctx, gitDir, 10)
	base := gitCmd(t, dir, "rev-parse", "main")
	linked, err := LinkCommits(ctx, repo, turns, nil, []string{first, second}, base, "", "", "feature")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"How should I structure this?": first,
		"Draft the parser":             first,
		"Review the parser":            first,
	}
	for _, lt := range linked {
		if len(lt.Links) == 0 || lt.Links[0].SHA != want[lt.Prompt] {
			t.Errorf("%q: links %+v, on branch %v", lt.Prompt, lt.Links, lt.OnBranch)
		}
	}
	if linked[0].Links[0].Match != "parent" {
		t.Errorf("question before the first commit linked by %q", linked[0].Links[0].Match)
	}
}

func TestSessionsKeepEarlierTurnsOnBranch(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	at := func(minutes int) string { return start.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339) }
	commit := git.CommitInfo{SHA: strings.Repeat("c", 40), Parents: []string{strings.Repeat("b", 40)}, Date: start.Add(3 * time.Hour)}
	turns := []Turn{
		// Before the branch's starting point moved in a rebase: HEAD is a
		// commit nothing under review builds on.
		{ID: 1, SessionID: "work", Branch: "feature", HeadAfter: strings.Repeat("a", 40), PromptedAt: at(0)},
		{ID: 2, SessionID: "work", Branch: "feature", HeadAfter: commit.SHA, PromptedAt: at(200)},
		// An older session that reused the branch name.
		{ID: 3, SessionID: "old", Branch: "feature", HeadAfter: strings.Repeat("d", 40), PromptedAt: at(-600)},
	}
	linked := LinkTurns(turns, nil, []git.CommitInfo{commit}, nil, "feature", start.Add(time.Hour))
	if !linked[0].OnBranch || len(linked[1].Links) != 1 || linked[2].OnBranch {
		t.Fatalf("on branch: %v %v %v", linked[0].OnBranch, linked[1].Links, linked[2].OnBranch)
	}
}
