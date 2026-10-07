package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"differ/internal/conversations"
)

// runHook handles `differ hook claude` (Claude Code UserPromptSubmit and Stop
// hooks) and `differ hook post-rewrite KIND` (Git's post-rewrite hook). It
// always exits successfully so a capture problem never blocks Claude or Git.
func runHook(args []string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var err error
	switch {
	case len(args) == 1 && args[0] == "claude":
		err = conversations.HandleClaudeHook(ctx, os.Stdin, conversations.DefaultPath())
	case len(args) == 2 && args[0] == "post-rewrite":
		err = conversations.HandlePostRewrite(ctx, os.Stdin, args[1], ".", conversations.DefaultPath())
	default:
		err = fmt.Errorf("usage: differ hook claude | differ hook post-rewrite (amend|rebase)")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "differ:", err)
	}
	return 0
}

func installHooks(args []string) int {
	flags := flag.NewFlagSet("install-hooks", flag.ExitOnError)
	repo := flags.String("repo", ".", "Path to the Git working copy")
	local := flags.Bool("local", false, "Write .claude/settings.local.json (personal, usually ignored) instead of .claude/settings.json (shared)")
	command := flags.String("command", "", "How hooks run Differ (default: differ when it is on PATH, otherwise this executable's path)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: differ install-hooks [flags]\n\nCapture Claude Code prompts and responses in this repository.")
		flags.PrintDefaults()
	}
	_ = flags.Parse(args)
	if *command == "" {
		*command = "differ"
		if _, err := exec.LookPath("differ"); err != nil {
			if self, err := os.Executable(); err == nil {
				*command, _ = filepath.Abs(self)
			}
		}
	}
	report, err := conversations.Install(context.Background(), *repo, *command, *local)
	if err != nil {
		fmt.Fprintln(os.Stderr, "differ:", err)
		return 1
	}
	for _, line := range report {
		fmt.Println(line)
	}
	fmt.Println("Conversations will be saved to " + conversations.DefaultPath())
	return 0
}
