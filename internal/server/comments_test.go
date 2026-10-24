package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.test", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s (%v)", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestCommentEndpoints(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.name", "Ada Lovelace")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "first")
	base := gitIn(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-qam", "second")
	head := gitIn(t, dir, "rev-parse", "HEAD")
	database := filepath.Join(t.TempDir(), "differ.db")
	handler := New(Config{Repository: dir, Database: database}, fstest.MapFS{})
	send := func(method, path, contentType, origin, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost:7331"+path, strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Origin", origin)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		var v map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &v)
		return res.Code, v
	}

	code, list := send("GET", "/api/threads?base="+base+"&head="+head, "", "", "")
	if code != 200 || list["author"] != "Ada Lovelace" || len(list["threads"].([]any)) != 0 {
		t.Fatalf("empty list: %d %v", code, list)
	}
	if _, err := os.Stat(database); err == nil {
		t.Fatal("created the database while reading")
	}
	create := `{"base":"` + base + `","head":"` + head + `","path":"a.txt","side":"new","startLine":2,"body":"Why two?","author":"Ada Lovelace"}`
	if code, _ := send("POST", "/api/threads", "text/plain", "", create); code != 400 {
		t.Fatalf("accepted a non-JSON body: %d", code)
	}
	if code, _ := send("POST", "/api/threads", "application/json", "https://evil.example", create); code != 403 {
		t.Fatalf("accepted a cross-origin write: %d", code)
	}
	code, thread := send("POST", "/api/threads", "application/json", "http://localhost:7331", create)
	if code != 201 || thread["snippet"] != "two" {
		t.Fatalf("create: %d %v", code, thread)
	}
	id := fmt.Sprint(thread["id"])
	if code, _ := send("POST", "/api/threads/"+id+"/comments", "application/json", "", `{"body":"To count.","authorKind":"agent"}`); code != 200 {
		t.Fatalf("reply: %d", code)
	}
	if code, _ := send("PATCH", "/api/threads/"+id, "application/json", "", `{"resolved":true,"by":"Ada Lovelace"}`); code != 200 {
		t.Fatalf("resolve: %d", code)
	}
	if code, _ := send("PATCH", "/api/threads/999", "application/json", "", `{"resolved":true}`); code != 404 {
		t.Fatalf("resolve missing: %d", code)
	}
	code, list = send("GET", "/api/threads?base="+base+"&head="+head, "", "", "")
	threads := list["threads"].([]any)
	if code != 200 || len(threads) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
	placed := threads[0].(map[string]any)
	comments := placed["comments"].([]any)
	if placed["resolvedBy"] != "Ada Lovelace" || placed["placement"].(map[string]any)["status"] != "exact" || len(comments) != 2 || comments[1].(map[string]any)["author"] != "Claude" {
		t.Fatalf("placed thread: %v", placed)
	}
	// In the first commit, a.txt has no line "two", so the thread is outdated there.
	code, list = send("GET", "/api/threads?base=:empty&head="+base, "", "", "")
	if code != 200 || len(list["threads"].([]any)) != 1 || list["threads"].([]any)[0].(map[string]any)["placement"].(map[string]any)["status"] != "outdated" {
		t.Fatalf("other comparison: %d %v", code, list)
	}
}
