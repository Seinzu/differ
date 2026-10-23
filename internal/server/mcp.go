package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"differ/internal/conversations"
	"differ/internal/git"
)

// The MCP server lets agents such as Claude show you a diff in Differ and
// take part in its review comments. It is served over Streamable HTTP at /mcp
// by the running Differ, which is the process your browser tabs talk to.

const mcpInstructions = `Differ is the local code review tool the user reviews diffs in. Use it to show the user a diff, or a file or line in one, and to read, write, and reply to review comments on diffs.

Comparisons are named like the Differ UI: a branch (compared with its merge base on the default branch; default: the checked-out branch), optionally ending at its uncommitted changes, or explicit base and head refs. Give commit to use one commit of the comparison on its own, like commit-by-commit review. With no comparison arguments, list_comments and add_comment use the diff on the user's screen (see get_current_view).

Line numbers are 1-based. Side "new" is the file after the change (additions and unchanged lines); side "old" is before it (deletions).`

// comparisonArgs names a comparison; every field is optional.
type comparisonArgs struct {
	Repo        string `json:"repo,omitempty" jsonschema:"Absolute path of the repository or any of its worktrees. Defaults to the repository on screen, then the one Differ was started in."`
	Branch      string `json:"branch,omitempty" jsonschema:"Branch to review against the default branch. Defaults to the checked-out branch."`
	Count       int    `json:"count,omitempty" jsonschema:"For the default branch or a branch with no commits of its own: how many of its latest commits to include. Defaults to 1."`
	Uncommitted bool   `json:"uncommitted,omitempty" jsonschema:"End the comparison at the branch's working tree, including uncommitted changes."`
	Base        string `json:"base,omitempty" jsonschema:"Explicit base commit or ref (use with head instead of branch)."`
	Head        string `json:"head,omitempty" jsonschema:"Explicit head commit or ref (use with base). :worktree means the working tree."`
	Commit      string `json:"commit,omitempty" jsonschema:"A commit in the comparison to use on its own, compared with its first parent."`
}

func (a comparisonArgs) empty() bool {
	return a.Repo == "" && a.Branch == "" && a.Count == 0 && !a.Uncommitted && a.Base == "" && a.Head == "" && a.Commit == ""
}

type comparisonInfo struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
	Base       string `json:"base" jsonschema:"Base snapshot of the diff (a commit, tree, or :empty)."`
	Head       string `json:"head" jsonschema:"Head snapshot of the diff (a commit, or a tree for uncommitted changes)."`
	Commit     string `json:"commit,omitempty"`
	OnScreen   bool   `json:"onScreen" jsonschema:"Whether this is the diff on the user's screen."`
	// query reopens the comparison in the UI.
	query url.Values
}

type commentInfo struct {
	ID         int64  `json:"id"`
	Author     string `json:"author"`
	AuthorKind string `json:"authorKind" jsonschema:"user (the person reviewing) or agent."`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
}

type threadInfo struct {
	ID   int64  `json:"id"`
	File string `json:"file,omitempty" jsonschema:"Empty for a comment on the whole comparison."`
	Side string `json:"side,omitempty"`
	// The lines in this comparison, which differ from the original lines
	// when the code moved.
	StartLine  int           `json:"startLine,omitempty" jsonschema:"First line in this comparison; 0 for a comment on the whole file."`
	EndLine    int           `json:"endLine,omitempty"`
	Status     string        `json:"status" jsonschema:"exact, moved (lines found elsewhere in the file), or outdated (the lines changed)."`
	Code       string        `json:"code,omitempty" jsonschema:"The commented lines as they were when the thread started."`
	Resolved   bool          `json:"resolved"`
	ResolvedBy string        `json:"resolvedBy,omitempty"`
	Comments   []commentInfo `json:"comments"`
}

func describeThread(t conversations.Thread) threadInfo {
	info := threadInfo{ID: t.ID, File: t.Path, Side: t.Side, StartLine: t.StartLine, EndLine: t.EndLine, Status: "exact", Code: t.Snippet, Resolved: t.ResolvedAt != "", ResolvedBy: t.ResolvedBy, Comments: []commentInfo{}}
	if p := t.Placement; p != nil {
		info.Status, info.StartLine, info.EndLine = p.Status, p.StartLine, p.EndLine
		if p.File != "" {
			info.File = p.File
		}
	}
	for _, c := range t.Comments {
		info.Comments = append(info.Comments, commentInfo{ID: c.ID, Author: c.Author, AuthorKind: c.AuthorKind, Body: c.Body, CreatedAt: c.CreatedAt})
	}
	return info
}

type mcpServer struct {
	config Config
	events *hub
	// host is the address the server is reached at, for links.
	host atomic.Value
}

// openBrowser opens a link in the default browser; tests replace it.
var openBrowser = func(link string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", link).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", link).Start()
	default:
		return exec.Command("xdg-open", link).Start()
	}
}

// resolve finds the comparison a tool call names, or the one on screen when
// it names none.
func (s *mcpServer) resolve(ctx context.Context, args comparisonArgs) (*comparisonInfo, error) {
	current, _ := s.events.current()
	if args.empty() && current != nil {
		info := &comparisonInfo{Repository: current.Repository, Base: current.ViewBase, Head: current.ViewHead, Commit: current.Commit, OnScreen: true}
		if current.Mode != "commits" {
			info.Branch = current.Branch
		}
		info.query, _ = url.ParseQuery(strings.TrimPrefix(current.Query, "?"))
		return info, nil
	}
	sel := selection{Repo: args.Repo, Base: args.Base, Head: args.Head, Branch: args.Branch, Count: max(args.Count, 1), Uncommitted: args.Uncommitted}
	if sel.Repo == "" {
		sel.Repo = s.config.Repository
		if current != nil {
			sel.Repo = current.Repository
		}
	}
	if sel.Repo == "" {
		return nil, errors.New("No repository is open in Differ; pass repo")
	}
	if (sel.Base == "") != (sel.Head == "") {
		return nil, errors.New("Pass both base and head, or neither")
	}
	comparison, err := compare(ctx, sel)
	if err != nil {
		return nil, err
	}
	info := &comparisonInfo{Repository: comparison.Repository, Base: comparison.Base, Head: comparison.Head, query: url.Values{"repo": {comparison.Repository}}}
	if comparison.Range != nil {
		info.Branch = comparison.Range.Branch
		if info.Branch != "" {
			info.query.Set("branch", info.Branch)
		}
		if comparison.Range.Mode == "recent" {
			info.query.Set("count", strconv.Itoa(sel.Count))
		}
		if sel.Uncommitted {
			info.query.Set("uncommitted", "1")
		}
	} else {
		info.query.Set("base", sel.Base)
		info.query.Set("head", sel.Head)
	}
	if args.Commit == "" {
		return info, nil
	}
	repo, err := git.Open(ctx, comparison.Repository)
	if err != nil {
		return nil, err
	}
	sha := args.Commit
	if !strings.EqualFold(sha, git.Worktree) {
		if sha, err = repo.Resolve(ctx, args.Commit); err != nil {
			return nil, err
		}
	}
	for _, c := range comparison.Commits {
		if c.SHA == sha || (c.Uncommitted && args.Commit == git.Worktree) {
			parent := ":empty"
			if len(c.Parents) > 0 {
				parent = c.Parents[0]
			}
			info.Base, info.Head, info.Commit, info.Branch = parent, c.SHA, c.SHA, ""
			info.query.Set("mode", "commits")
			info.query.Set("commit", c.SHA)
			return info, nil
		}
	}
	if args.Branch != "" || args.Base != "" {
		return nil, fmt.Errorf("Commit %s is not part of this comparison", args.Commit)
	}
	// A commit outside the default comparison is shown on its own.
	parent, err := repo.Resolve(ctx, sha+"^")
	if err != nil {
		parent = ":empty"
	}
	return &comparisonInfo{Repository: comparison.Repository, Base: parent, Head: sha, query: url.Values{"repo": {comparison.Repository}, "base": {parent}, "head": {sha}}}, nil
}

var hunkHeader = regexp.MustCompile(`(?m)^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// checkLines refuses a range that is not within one hunk of the file's diff,
// shown with Differ's default three lines of context, so that agents comment
// on lines the person reviewing can see.
func checkLines(ctx context.Context, repo *git.Repository, base, head, path, side string, start, end int) error {
	patch, err := repo.Patch(ctx, base, head, path, 3, false)
	if err != nil {
		return err
	}
	if patch.Message != "" && patch.Patch == "" {
		return fmt.Errorf("%s has no text diff to comment on (%s); comment on the whole file instead", path, patch.Message)
	}
	var ranges []string
	for _, m := range hunkHeader.FindAllStringSubmatch(patch.Patch, -1) {
		first, count := m[3], m[4]
		if side == "old" {
			first, count = m[1], m[2]
		}
		from, _ := strconv.Atoi(first)
		n := 1
		if count != "" {
			n, _ = strconv.Atoi(count)
		}
		if n == 0 {
			continue
		}
		if start >= from && end <= from+n-1 {
			return nil
		}
		ranges = append(ranges, fmt.Sprintf("%d-%d", from, from+n-1))
	}
	if len(ranges) == 0 {
		return fmt.Errorf("%s has no lines on the %s side of this diff", path, side)
	}
	return fmt.Errorf("Lines %d-%d of %s are not in one hunk of the diff on the %s side. Lines shown: %s", start, end, path, side, strings.Join(ranges, ", "))
}

type getViewArgs struct{}

type viewResult struct {
	OpenTabs   int    `json:"openTabs" jsonschema:"How many Differ tabs are open."`
	View       *view  `json:"view,omitempty" jsonschema:"The view in the tab in use. Missing when no tab has reported one."`
	Repository string `json:"repository,omitempty" jsonschema:"The repository Differ was started in, when no tab is open."`
	URL        string `json:"url,omitempty"`
	Note       string `json:"note,omitempty"`
}

type showDiffArgs struct {
	comparisonArgs
	File string `json:"file,omitempty" jsonschema:"A file in the diff to scroll to."`
	Line int    `json:"line,omitempty" jsonschema:"A line of that file to scroll to and highlight."`
	Side string `json:"side,omitempty" jsonschema:"The side line is on: new (default) or old."`
}

type showDiffResult struct {
	URL           string         `json:"url" jsonschema:"A link that opens the same view."`
	ShownInTab    bool           `json:"shownInTab" jsonschema:"Whether an open Differ tab switched to the diff."`
	OpenedBrowser bool           `json:"openedBrowser" jsonschema:"Whether a browser window was opened because no tab was open."`
	Comparison    comparisonInfo `json:"comparison"`
}

type listCommentsArgs struct {
	comparisonArgs
	Status string `json:"status,omitempty" jsonschema:"open (default), resolved, or all."`
	File   string `json:"file,omitempty" jsonschema:"Only threads on this file."`
}

type listCommentsResult struct {
	Comparison comparisonInfo `json:"comparison"`
	Threads    []threadInfo   `json:"threads"`
}

type addCommentArgs struct {
	comparisonArgs
	File    string `json:"file,omitempty" jsonschema:"The file to comment on. Leave empty to comment on the whole comparison."`
	Side    string `json:"side,omitempty" jsonschema:"new (default): the file after the change; old: before it, for deleted lines."`
	Line    int    `json:"line,omitempty" jsonschema:"First line to comment on. Leave empty to comment on the whole file."`
	EndLine int    `json:"endLine,omitempty" jsonschema:"Last line of a range. Defaults to line."`
	Body    string `json:"body" jsonschema:"The comment, in plain text."`
	Author  string `json:"author,omitempty" jsonschema:"Name to sign the comment with. Defaults to Claude."`
}

type threadResult struct {
	Thread threadInfo `json:"thread"`
}

type replyArgs struct {
	ThreadID int64  `json:"threadId" jsonschema:"The thread's id, from list_comments."`
	Body     string `json:"body" jsonschema:"The reply, in plain text."`
	Author   string `json:"author,omitempty" jsonschema:"Name to sign the reply with. Defaults to Claude."`
}

type resolveArgs struct {
	ThreadID int64  `json:"threadId" jsonschema:"The thread's id, from list_comments."`
	Reopen   bool   `json:"reopen,omitempty" jsonschema:"Reopen a resolved thread instead of resolving it."`
	Author   string `json:"author,omitempty" jsonschema:"Who is resolving it. Defaults to Claude."`
}

func agentName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "Claude"
	}
	return name
}

func (s *mcpServer) link(query url.Values) string {
	host, _ := s.host.Load().(string)
	if host == "" {
		host = "127.0.0.1:7331"
	}
	return "http://" + host + "/?" + query.Encode()
}

func (s *mcpServer) changed(gitDir string) {
	data := fmt.Sprintf(`{"type":"comments","gitDir":%q}`, gitDir)
	s.events.publish(data)
}

func (s *mcpServer) build() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "differ", Title: "Differ", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: mcpInstructions})

	mcp.AddTool(server, &mcp.Tool{Name: "get_current_view", Description: "What the user is looking at in Differ: the repository, the comparison, the commit in commit-by-commit review, and the file in view."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ getViewArgs) (*mcp.CallToolResult, viewResult, error) {
			current, tabs := s.events.current()
			result := viewResult{OpenTabs: tabs, View: current}
			if current != nil {
				result.URL = s.link(mustQuery(current.Query))
			} else {
				result.Note = "No Differ tab has reported a view. Use show_diff to open one."
				result.Repository = s.config.Repository
			}
			return nil, result, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "show_diff", Description: "Show the user a diff in Differ, optionally scrolled to a file and line. The tab in use switches to it; when no tab is open, a browser window opens."},
		func(ctx context.Context, req *mcp.CallToolRequest, args showDiffArgs) (*mcp.CallToolResult, showDiffResult, error) {
			if args.empty() {
				return nil, showDiffResult{}, errors.New("Name the diff to show: a branch, base and head, or a commit")
			}
			info, err := s.resolve(ctx, args.comparisonArgs)
			if err != nil {
				return nil, showDiffResult{}, err
			}
			if args.File != "" {
				repo, err := git.Open(ctx, info.Repository)
				if err != nil {
					return nil, showDiffResult{}, err
				}
				files, err := repo.SnapshotFiles(ctx, info.Base, info.Head)
				if err != nil {
					return nil, showDiffResult{}, err
				}
				found := false
				for _, f := range files {
					found = found || f.Path == args.File || f.OldPath == args.File
				}
				if !found {
					return nil, showDiffResult{}, fmt.Errorf("%s is not changed in this diff", args.File)
				}
				info.query.Set("file", args.File)
				if args.Line > 0 {
					info.query.Set("line", strconv.Itoa(args.Line))
					if args.Side == "old" {
						info.query.Set("side", "old")
					}
				}
			}
			result := showDiffResult{URL: s.link(info.query), Comparison: *info}
			if s.events.navigate(info.query.Encode()) > 0 {
				result.ShownInTab = true
			} else if err := openBrowser(result.URL); err == nil {
				result.OpenedBrowser = true
			}
			return nil, result, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "list_comments", Description: "List review comment threads on a diff (by default, the one on the user's screen), with their replies, where each now appears, and the code it was written on."},
		func(ctx context.Context, req *mcp.CallToolRequest, args listCommentsArgs) (*mcp.CallToolResult, listCommentsResult, error) {
			info, err := s.resolve(ctx, args.comparisonArgs)
			if err != nil {
				return nil, listCommentsResult{}, err
			}
			result := listCommentsResult{Comparison: *info, Threads: []threadInfo{}}
			store, err := conversations.Open(s.config.Database, false)
			if err != nil {
				return nil, result, nil // No database yet means no comments.
			}
			defer store.Close()
			repo, err := git.Open(ctx, info.Repository)
			if err != nil {
				return nil, result, err
			}
			gitDir, err := repo.CommonDir(ctx)
			if err != nil {
				return nil, result, err
			}
			threads, err := store.Threads(ctx, gitDir)
			if err != nil {
				return nil, result, err
			}
			if threads, err = conversations.Place(ctx, repo, threads, info.Base, info.Head, info.Branch); err != nil {
				return nil, result, err
			}
			for _, t := range threads {
				resolved := t.ResolvedAt != ""
				if (args.Status == "" || args.Status == "open") && resolved || args.Status == "resolved" && !resolved {
					continue
				}
				thread := describeThread(t)
				if args.File != "" && thread.File != args.File && t.Path != args.File {
					continue
				}
				result.Threads = append(result.Threads, thread)
			}
			return nil, result, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "add_comment", Description: "Start a review comment thread on lines of a file in a diff (by default, the one on the user's screen), on a whole file, or on the whole diff. Lines must be within the diff as Differ shows it."},
		func(ctx context.Context, req *mcp.CallToolRequest, args addCommentArgs) (*mcp.CallToolResult, threadResult, error) {
			info, err := s.resolve(ctx, args.comparisonArgs)
			if err != nil {
				return nil, threadResult{}, err
			}
			repo, err := git.Open(ctx, info.Repository)
			if err != nil {
				return nil, threadResult{}, err
			}
			gitDir, err := repo.CommonDir(ctx)
			if err != nil {
				return nil, threadResult{}, err
			}
			side := args.Side
			if side == "" {
				side = "new"
			}
			end := args.EndLine
			if end == 0 {
				end = args.Line
			}
			if args.File == "" && args.Line > 0 {
				return nil, threadResult{}, errors.New("Pass file with line")
			}
			thread := conversations.Thread{GitDir: gitDir, Path: args.File, Side: side, StartLine: args.Line, EndLine: end, Base: info.Base, Head: info.Head, Branch: info.Branch}
			if args.File != "" && args.Line > 0 {
				if err := checkLines(ctx, repo, info.Base, info.Head, args.File, side, args.Line, end); err != nil {
					return nil, threadResult{}, err
				}
			}
			if err := conversations.Anchor(ctx, repo, &thread); err != nil {
				return nil, threadResult{}, err
			}
			store, err := conversations.Open(s.config.Database, true)
			if err != nil {
				return nil, threadResult{}, err
			}
			defer store.Close()
			created, err := store.CreateThread(ctx, thread, conversations.Comment{Body: args.Body, Author: agentName(args.Author), AuthorKind: "agent"})
			if err != nil {
				return nil, threadResult{}, err
			}
			s.changed(gitDir)
			return nil, threadResult{Thread: describeThread(*created)}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "reply_to_comment", Description: "Reply to a review comment thread."},
		func(ctx context.Context, req *mcp.CallToolRequest, args replyArgs) (*mcp.CallToolResult, threadResult, error) {
			return s.update(func(store *conversations.Store) (*conversations.Thread, error) {
				return store.Reply(ctx, args.ThreadID, conversations.Comment{Body: args.Body, Author: agentName(args.Author), AuthorKind: "agent"})
			})
		})

	mcp.AddTool(server, &mcp.Tool{Name: "resolve_thread", Description: "Resolve a review comment thread, or reopen it. Resolve only threads you were asked to, or whose request you have fully addressed."},
		func(ctx context.Context, req *mcp.CallToolRequest, args resolveArgs) (*mcp.CallToolResult, threadResult, error) {
			return s.update(func(store *conversations.Store) (*conversations.Thread, error) {
				return store.Resolve(ctx, args.ThreadID, !args.Reopen, agentName(args.Author))
			})
		})
	return server
}

// update changes a stored thread and announces the change to open tabs.
func (s *mcpServer) update(f func(*conversations.Store) (*conversations.Thread, error)) (*mcp.CallToolResult, threadResult, error) {
	store, err := conversations.Open(s.config.Database, false)
	if err != nil {
		return nil, threadResult{}, errors.New("No such thread")
	}
	defer store.Close()
	thread, err := f(store)
	if err != nil {
		return nil, threadResult{}, err
	}
	s.changed(thread.GitDir)
	return nil, threadResult{Thread: describeThread(*thread)}, nil
}

func mustQuery(query string) url.Values {
	values, _ := url.ParseQuery(strings.TrimPrefix(query, "?"))
	return values
}

func registerMCP(mux *http.ServeMux, config Config, events *hub) {
	s := &mcpServer{config: config, events: events}
	server := s.build()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	serve := func(w http.ResponseWriter, r *http.Request) {
		s.host.Store(r.Host)
		handler.ServeHTTP(w, r)
	}
	// Methods are named so the patterns do not overlap the UI's "GET /".
	for _, method := range []string{"GET", "POST", "DELETE"} {
		mux.HandleFunc(method+" /mcp", serve)
	}
}
