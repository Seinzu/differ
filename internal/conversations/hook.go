package conversations

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"differ/internal/git"
)

// HookInput is the JSON Claude Code passes to command hooks on stdin.
type HookInput struct {
	SessionID            string `json:"session_id"`
	TranscriptPath       string `json:"transcript_path"`
	Cwd                  string `json:"cwd"`
	HookEventName        string `json:"hook_event_name"`
	Prompt               string `json:"prompt"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// HandleClaudeHook records a UserPromptSubmit or Stop event. Events outside
// a Git working copy are ignored. It never writes to stdout, which Claude
// Code would add to the conversation.
func HandleClaudeHook(ctx context.Context, stdin io.Reader, dbPath string) error {
	var in HookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	if in.SessionID == "" {
		return fmt.Errorf("hook input has no session_id")
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	repo, err := git.Open(ctx, cwd)
	if err != nil {
		return nil
	}
	gitDir, err := repo.CommonDir(ctx)
	if err != nil {
		return err
	}
	turn := Turn{SessionID: in.SessionID, GitDir: gitDir, Worktree: repo.Path, Branch: repo.CurrentBranch(ctx)}
	head, _ := repo.Resolve(ctx, "HEAD")
	switch in.HookEventName {
	case "UserPromptSubmit":
		turn.HeadBefore, turn.Prompt = head, in.Prompt
	case "Stop":
		turn.HeadAfter = head
		response, files := readTranscript(in.TranscriptPath)
		if last := strings.TrimSpace(in.LastAssistantMessage); last != "" && !strings.Contains(response, last) {
			// The transcript may not be flushed when Stop runs.
			response = strings.TrimSpace(response + "\n\n" + last)
		}
		turn.Response = response
		if turn.Files, err = fileStates(ctx, repo, files); err != nil {
			return err
		}
	default:
		return nil
	}
	store, err := Open(dbPath, true)
	if err != nil {
		return err
	}
	defer store.Close()
	if in.HookEventName == "Stop" {
		return store.FinishTurn(ctx, turn, cwd, in.TranscriptPath)
	}
	_, err = store.StartTurn(ctx, turn, cwd, in.TranscriptPath)
	return err
}

// HandlePostRewrite records Git's post-rewrite hook input: one
// "old-sha new-sha" pair per line, after an amend or rebase.
func HandlePostRewrite(ctx context.Context, stdin io.Reader, kind, dir, dbPath string) error {
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return err
	}
	gitDir, err := repo.CommonDir(ctx)
	if err != nil {
		return err
	}
	var pairs [][2]string
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		if fields := strings.Fields(scanner.Text()); len(fields) >= 2 {
			pairs = append(pairs, [2]string{fields[0], fields[1]})
		}
	}
	if err := scanner.Err(); err != nil || len(pairs) == 0 {
		return err
	}
	store, err := Open(dbPath, true)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.RecordRewrites(ctx, gitDir, kind, pairs)
}

type transcriptEntry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	Input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"input"`
}

var editingTools = map[string]bool{"Edit": true, "MultiEdit": true, "Write": true, "NotebookEdit": true}

// readTranscript returns the assistant's text since the last real user prompt
// in a Claude Code transcript (JSON lines), and the files its editing tools
// touched. Subagent (sidechain) entries are skipped. A missing or malformed
// transcript yields what could be read.
func readTranscript(path string) (string, []string) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	var texts []string
	files := map[string]bool{}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		var entry transcriptEntry
		if len(line) > 0 && json.Unmarshal(line, &entry) == nil && !entry.IsSidechain && !entry.IsMeta {
			var text string
			var blocks []contentBlock
			if json.Unmarshal(entry.Message.Content, &text) != nil {
				_ = json.Unmarshal(entry.Message.Content, &blocks)
			}
			switch entry.Type {
			case "user":
				// Tool results are user entries too; only typed prompts start a turn.
				prompt := text != ""
				for _, b := range blocks {
					prompt = prompt || b.Type == "text"
				}
				if prompt {
					texts, files = nil, map[string]bool{}
				}
			case "assistant":
				for _, b := range blocks {
					switch {
					case b.Type == "text" && strings.TrimSpace(b.Text) != "":
						if len(texts) == 0 || texts[len(texts)-1] != b.Text {
							texts = append(texts, b.Text)
						}
					case b.Type == "tool_use" && editingTools[b.Name]:
						if p := b.Input.FilePath + b.Input.NotebookPath; p != "" {
							files[p] = true
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return strings.TrimSpace(strings.Join(texts, "\n\n")), paths
}

// fileStates hashes the edited files that are inside the repository.
func fileStates(ctx context.Context, repo *git.Repository, paths []string) ([]FileState, error) {
	root, err := filepath.EvalSymlinks(repo.Path)
	if err != nil {
		root = repo.Path
	}
	var relative []string
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(repo.Path, p)
		}
		dir, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil {
			dir = filepath.Dir(p)
		}
		rel, err := filepath.Rel(root, filepath.Join(dir, filepath.Base(p)))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		relative = append(relative, filepath.ToSlash(rel))
	}
	states := []FileState{}
	if len(relative) == 0 {
		return states, nil
	}
	hashes, err := repo.HashFiles(ctx, relative)
	if err != nil {
		return nil, err
	}
	for _, rel := range relative {
		states = append(states, FileState{Path: rel, Blob: hashes[rel]})
	}
	return states, nil
}
