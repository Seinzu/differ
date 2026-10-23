package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPServer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "first")
	base := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-qb", "feature")
	writeFile("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\neleven and a half\n12\n")
	gitIn(t, dir, "commit", "-qam", "second")
	head := gitIn(t, dir, "rev-parse", "HEAD")

	var opened []string
	defer func(original func(string) error) { openBrowser = original }(openBrowser)
	openBrowser = func(link string) error { opened = append(opened, link); return nil }
	ts := httptest.NewServer(New(Config{Repository: dir, Database: filepath.Join(t.TempDir(), "differ.db")}, fstest.MapFS{}))
	defer ts.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) (map[string]any, bool) {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var out map[string]any
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			return map[string]any{"error": text}, true
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("%s: %s", name, text)
		}
		return out, false
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 6 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	if out, _ := call("get_current_view", nil); out["openTabs"].(float64) != 0 || out["note"] == nil {
		t.Fatalf("view without tabs: %v", out)
	}

	// No tab is open, so show_diff opens a browser.
	out, failed := call("show_diff", map[string]any{"branch": "feature", "file": "a.txt", "line": 12})
	if failed || out["openedBrowser"] != true || len(opened) != 1 || !strings.Contains(opened[0], "file=a.txt") || !strings.Contains(opened[0], "line=12") {
		t.Fatalf("show_diff without tabs: %v %v", out, opened)
	}
	if out, failed := call("show_diff", map[string]any{"branch": "feature", "file": "missing.txt"}); !failed {
		t.Fatalf("showed a file outside the diff: %v", out)
	}

	// A tab connects and reports its view; show_diff then navigates it.
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events?tab=t1", nil)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	lines := bufio.NewReader(stream.Body)
	lines.ReadString('\n') // ": connected"
	viewJSON := `{"tab":"t1","query":"?repo=x&branch=feature","repository":"` + dir + `","branch":"feature","base":"` + base + `","head":"` + head + `","mode":"total","viewBase":"` + base + `","viewHead":"` + head + `"}`
	res, err := http.Post(ts.URL+"/api/view", "application/json", strings.NewReader(viewJSON))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("report view: %v %v", res, err)
	}
	if out, _ := call("get_current_view", nil); out["openTabs"].(float64) != 1 || out["view"].(map[string]any)["branch"] != "feature" {
		t.Fatalf("current view: %v", out)
	}
	out, _ = call("show_diff", map[string]any{"commit": head})
	if out["shownInTab"] != true || len(opened) != 1 {
		t.Fatalf("show_diff with a tab: %v", out)
	}
	deadline := time.Now().Add(5 * time.Second)
	var event string
	for !strings.HasPrefix(event, "data:") && time.Now().Before(deadline) {
		event, _ = lines.ReadString('\n')
	}
	if !strings.Contains(event, `"navigate"`) || !strings.Contains(event, "mode=commits") || !strings.Contains(event, "commit="+head) {
		t.Fatalf("navigate event: %q", event)
	}

	// Comments default to the diff on screen.
	if out, failed := call("add_comment", map[string]any{"file": "a.txt", "line": 2, "body": "Too far from the change"}); !failed || !strings.Contains(out["error"].(string), "9-13") {
		t.Fatalf("commented outside the diff: %v", out)
	}
	out, failed = call("add_comment", map[string]any{"file": "a.txt", "line": 12, "body": "Is a half allowed?"})
	if failed {
		t.Fatalf("add_comment: %v", out)
	}
	thread := out["thread"].(map[string]any)
	if thread["code"] != "eleven and a half" || thread["comments"].([]any)[0].(map[string]any)["author"] != "Claude" {
		t.Fatalf("thread: %v", thread)
	}
	id := thread["id"]
	if out, failed := call("reply_to_comment", map[string]any{"threadId": id, "body": "Yes.", "author": "Reviewer bot"}); failed || len(out["thread"].(map[string]any)["comments"].([]any)) != 2 {
		t.Fatalf("reply: %v", out)
	}
	if out, failed := call("resolve_thread", map[string]any{"threadId": id}); failed || out["thread"].(map[string]any)["resolved"] != true {
		t.Fatalf("resolve: %v", out)
	}
	if out, _ := call("list_comments", nil); len(out["threads"].([]any)) != 0 || out["comparison"].(map[string]any)["onScreen"] != true {
		t.Fatalf("open threads after resolving: %v", out)
	}
	out, _ = call("list_comments", map[string]any{"branch": "feature", "status": "all"})
	threads := out["threads"].([]any)
	if len(threads) != 1 || threads[0].(map[string]any)["status"] != "exact" || threads[0].(map[string]any)["startLine"].(float64) != 12 {
		t.Fatalf("all threads: %v", out)
	}
	if out, failed := call("reply_to_comment", map[string]any{"threadId": 999, "body": "?"}); !failed {
		t.Fatalf("replied to a missing thread: %v", out)
	}
}
