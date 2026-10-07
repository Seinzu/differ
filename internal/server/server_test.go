package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestLocalRequestBoundary(t *testing.T) {
	handler := New(Config{Repository: "/local/repo", Base: "HEAD~1", Head: "HEAD"}, fstest.MapFS{"index.html": {Data: []byte("Differ UI")}})
	for _, tc := range []struct {
		name, host, origin, site string
		status                   int
	}{
		{"local", "127.0.0.1:7331", "", "same-origin", 200},
		{"localhost", "localhost:7331", "http://localhost:7331", "same-origin", 200},
		{"dns rebinding", "evil.example:7331", "", "", 403},
		{"foreign origin", "localhost:7331", "https://evil.example", "", 403},
		{"cross site", "localhost:7331", "", "cross-site", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tc.host+"/api/config", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Sec-Fetch-Site", tc.site)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", res.Code, tc.status, res.Body)
			}
			if tc.status == 200 {
				var config Config
				if err := json.Unmarshal(res.Body.Bytes(), &config); err != nil || config.Repository != "/local/repo" {
					t.Fatalf("bad config: %s", res.Body)
				}
			}
		})
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", "http://localhost/", nil))
	if res.Code != 200 || res.Body.String() != "Differ UI" {
		t.Fatalf("missing UI: %d %s", res.Code, res.Body)
	}
}

func TestBrowse(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"repo/.git", "plain", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	listing, err := browse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 2 || listing.Entries[0].Name != "plain" || listing.Entries[0].Repository || listing.Entries[1].Name != "repo" || !listing.Entries[1].Repository {
		t.Fatalf("listing: %+v", listing.Entries)
	}
	if listing.Parent != filepath.Dir(dir) || listing.Repository {
		t.Fatalf("listing: %+v", listing)
	}
	if _, err := browse("relative/path"); err == nil {
		t.Fatal("accepted a relative path")
	}
}

func TestConversationsWithoutDatabase(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	database := filepath.Join(t.TempDir(), "missing.db")
	handler := New(Config{Repository: dir, Database: database}, fstest.MapFS{})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", "http://localhost/api/conversations", nil))
	var list conversationList
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil || res.Code != 200 || list.Enabled || list.Database != database || len(list.Turns) != 0 {
		t.Fatalf("conversations: %d %s", res.Code, res.Body)
	}
	if _, err := os.Stat(database); err == nil {
		t.Fatal("created the database while reading")
	}
}
