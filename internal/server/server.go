package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"differ/internal/conversations"
	"differ/internal/git"
)

type Config struct {
	Repository string `json:"repository"`
	Base       string `json:"base"`
	Head       string `json:"head"`
	// Database holds captured Claude Code conversations.
	Database string `json:"-"`
}

const maxLinkedCommits = 2000

func New(config Config, assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, config) })
	mux.HandleFunc("GET /api/compare", func(w http.ResponseWriter, r *http.Request) {
		repo, err := git.Open(r.Context(), repository(r, config))
		if err != nil {
			fail(w, err)
			return
		}
		q := r.URL.Query()
		base, head := q.Get("base"), q.Get("head")
		checkout := repo
		var rg *git.Range
		if base == "" && head == "" {
			// Without explicit refs, review a branch against the default branch.
			count := 1
			if q.Get("count") != "" {
				if count, err = strconv.Atoi(q.Get("count")); err != nil {
					http.Error(w, "Invalid count", 400)
					return
				}
			}
			if rg, err = repo.DefaultRange(r.Context(), q.Get("branch"), count); err != nil {
				fail(w, err)
				return
			}
			base, head = rg.Base, rg.Head
			if q.Get("uncommitted") == "1" {
				// Uncommitted changes live in whichever worktree has the branch
				// checked out, which need not be the one that was opened.
				path, err := repo.WorktreeFor(r.Context(), rg.Branch)
				if err == nil && path == "" {
					err = fmt.Errorf("%s is not checked out in any worktree, so it has no uncommitted changes", rg.Branch)
				}
				if err != nil {
					fail(w, err)
					return
				}
				if checkout, err = git.Open(r.Context(), path); err != nil {
					fail(w, err)
					return
				}
				head = git.Worktree
			}
		}
		comparison, err := checkout.Compare(r.Context(), base, head)
		if err != nil {
			fail(w, err)
			return
		}
		if checkout.Path != repo.Path {
			comparison.WorktreePath = checkout.Path
			comparison.Repository = repo.Path
		}
		comparison.Range = rg
		writeJSON(w, 200, comparison)
	})
	mux.HandleFunc("GET /api/repo", func(w http.ResponseWriter, r *http.Request) {
		repo, err := git.Open(r.Context(), repository(r, config))
		if err != nil {
			fail(w, err)
			return
		}
		info, err := repo.Info(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, info)
	})
	mux.HandleFunc("GET /api/conversations", func(w http.ResponseWriter, r *http.Request) {
		repo, err := git.Open(r.Context(), repository(r, config))
		if err != nil {
			fail(w, err)
			return
		}
		result, err := linkedConversations(r, repo, config.Database)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("GET /api/browse", func(w http.ResponseWriter, r *http.Request) {
		listing, err := browse(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, listing)
	})
	mux.HandleFunc("GET /api/file", func(w http.ResponseWriter, r *http.Request) {
		repo, err := git.Open(r.Context(), repository(r, config))
		if err != nil {
			fail(w, err)
			return
		}
		q := r.URL.Query()
		contextLines := 3
		if q.Get("context") == "all" {
			contextLines = 1000000
		} else if q.Get("context") != "" {
			contextLines, err = strconv.Atoi(q.Get("context"))
			if err != nil || contextLines < 0 || contextLines > 100 {
				http.Error(w, "Invalid context", 400)
				return
			}
		}
		patch, err := repo.Patch(r.Context(), q.Get("base"), q.Get("head"), q.Get("path"), contextLines, q.Get("whitespace") == "ignore")
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, patch)
	})
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "Unknown API endpoint", 404) })
	if _, err := fs.Stat(assets, "index.html"); err == nil {
		mux.Handle("GET /", http.FileServerFS(assets))
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Frontend not built. Run npm install && npm run build, then restart Differ.", http.StatusServiceUnavailable)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only local hostnames are accepted, preventing DNS rebinding to this
		// read-only but locally privileged repository browser.
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "Local requests only", 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "Cross-site requests are not allowed", 403)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, "Cross-origin requests are not allowed", 403)
					return
				}
			}
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		mux.ServeHTTP(w, r)
	})
}

func repository(r *http.Request, config Config) string {
	if path := r.URL.Query().Get("repo"); path != "" {
		return path
	}
	return config.Repository
}
func fail(w http.ResponseWriter, err error) {
	writeJSON(w, 400, map[string]string{"error": err.Error()})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type conversationList struct {
	Enabled  bool                       `json:"enabled"`
	Database string                     `json:"database"`
	Turns    []conversations.LinkedTurn `json:"turns"`
}

// linkedConversations returns the repository's captured turns linked to the
// commits listed in the commits parameter (oldest first) and, optionally, a
// working-tree snapshot given as tree and its parent commit as treeParent.
func linkedConversations(r *http.Request, repo *git.Repository, database string) (*conversationList, error) {
	result := &conversationList{Database: database, Turns: []conversations.LinkedTurn{}}
	store, err := conversations.Open(database, false)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return nil, err
	}
	defer store.Close()
	result.Enabled = true
	q := r.URL.Query()
	var shas []string
	if q.Get("commits") != "" {
		shas = strings.Split(q.Get("commits"), ",")
	}
	if len(shas) > maxLinkedCommits {
		shas = shas[len(shas)-maxLinkedCommits:]
	}
	gitDir, err := repo.CommonDir(r.Context())
	if err != nil {
		return nil, err
	}
	turns, err := store.Turns(r.Context(), gitDir, 500)
	if err != nil {
		return nil, err
	}
	rewrites, err := store.Rewrites(r.Context(), gitDir)
	if err != nil {
		return nil, err
	}
	result.Turns, err = conversations.LinkCommits(r.Context(), repo, turns, rewrites, shas, q.Get("tree"), q.Get("treeParent"), q.Get("branch"))
	return result, err
}
