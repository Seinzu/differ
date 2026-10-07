package conversations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"differ/internal/git"
)

const (
	claudeMarker  = "hook claude"
	rewriteMarker = "hook post-rewrite"
)

// Install adds the Claude Code hooks to the repository's .claude/settings.json
// (settings.local.json when local is set, which Git usually ignores) and a
// post-rewrite Git hook. executable is how the hooks invoke Differ. Existing
// settings and hooks are kept; it reports what it did.
func Install(ctx context.Context, dir, executable string, local bool) ([]string, error) {
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return nil, err
	}
	name := "settings.json"
	if local {
		name = "settings.local.json"
	}
	quoted := shellQuote(executable)
	// Hooks must never interrupt Claude: skip silently when Differ is missing.
	command := fmt.Sprintf("command -v %s >/dev/null 2>&1 && %s %s || true", quoted, quoted, claudeMarker)
	// Claude reads settings from the folder it runs in, so a bare repository
	// gets them in each of its worktrees instead.
	roots := []string{repo.Path}
	var report []string
	if repo.Bare {
		worktrees, err := repo.Worktrees(ctx)
		if err != nil {
			return nil, err
		}
		roots = nil
		for _, w := range worktrees {
			roots = append(roots, w.Path)
		}
		report = append(report, "Bare repository: worktrees added later need `differ install-hooks` too, unless .claude/settings.json is committed.")
	}
	for _, root := range roots {
		settings := filepath.Join(root, ".claude", name)
		added, err := addClaudeHooks(settings, command)
		if err != nil {
			return nil, err
		}
		if added {
			report = append(report, "Added Claude Code hooks to "+settings)
		} else {
			report = append(report, "Claude Code hooks already present in "+settings)
		}
	}

	out, err := repo.GitPath(ctx, "hooks")
	if err != nil {
		return nil, err
	}
	hook := filepath.Join(out, "post-rewrite")
	line := fmt.Sprintf(`command -v %s >/dev/null 2>&1 && %s %s "$1" || true`, quoted, quoted, rewriteMarker)
	existing, err := os.ReadFile(hook)
	switch {
	case err == nil && strings.Contains(string(existing), rewriteMarker):
		report = append(report, "Git post-rewrite hook already present in "+hook)
	case err == nil:
		report = append(report, fmt.Sprintf("Left the existing %s unchanged. To follow rebases, add:\n  %s", hook, line))
	case errors.Is(err, os.ErrNotExist):
		script := "#!/bin/sh\n# Differ: record rewritten commits so captured conversations follow amends and rebases.\n" + line + "\n"
		if err := os.MkdirAll(out, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
			return nil, err
		}
		report = append(report, "Added Git post-rewrite hook "+hook)
	default:
		return nil, err
	}
	return report, nil
}

func addClaudeHooks(path, command string) (bool, error) {
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return false, fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	added := false
	for _, event := range []string{"UserPromptSubmit", "Stop"} {
		groups, _ := hooks[event].([]any)
		if containsCommand(groups) {
			continue
		}
		hooks[event] = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
		added = true
	}
	if !added {
		return false, nil
	}
	settings["hooks"] = hooks
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false) // Keep shell operators readable.
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(settings); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, out.Bytes(), 0o644)
}

func containsCommand(groups []any) bool {
	for _, group := range groups {
		g, _ := group.(map[string]any)
		entries, _ := g["hooks"].([]any)
		for _, entry := range entries {
			e, _ := entry.(map[string]any)
			if command, _ := e["command"].(string); strings.Contains(command, claudeMarker) {
				return true
			}
		}
	}
	return false
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
