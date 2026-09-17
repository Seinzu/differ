package server

import (
	"encoding/json"
	"net/http/httptest"
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
