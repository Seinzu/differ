package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"strconv"

	"differ/internal/conversations"
	"differ/internal/git"
)

type threadList struct {
	// Author is the name the person reviewing comments under.
	Author  string                 `json:"author"`
	Threads []conversations.Thread `json:"threads"`
}

type threadRequest struct {
	Base       string `json:"base"`
	Head       string `json:"head"`
	Branch     string `json:"branch"`
	Path       string `json:"path"`
	Side       string `json:"side"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	Body       string `json:"body"`
	Author     string `json:"author"`
	AuthorKind string `json:"authorKind"`
}

type commentRequest struct {
	Body       string `json:"body"`
	Author     string `json:"author"`
	AuthorKind string `json:"authorKind"`
}

type resolveRequest struct {
	Resolved bool   `json:"resolved"`
	By       string `json:"by"`
}

// decode reads a JSON request body. Requiring the JSON content type means a
// cross-origin page cannot send one without a preflight Differ never allows.
func decode(r *http.Request, v any) error {
	if media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); media != "application/json" {
		return errors.New("Send a JSON request body")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("Invalid request: %v", err)
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("Invalid id")
	}
	return id, nil
}

func failStore(w http.ResponseWriter, err error) {
	if conversations.IsNotFound(err) {
		writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	fail(w, err)
}

func registerComments(mux *http.ServeMux, config Config, events *hub) {
	changed := func(gitDir string) {
		data, _ := json.Marshal(map[string]string{"type": "comments", "gitDir": gitDir})
		events.publish(string(data))
	}
	// withStore opens the database for one request; reads of a database that
	// does not exist yet see no comments.
	withStore := func(w http.ResponseWriter, create bool, f func(*conversations.Store)) {
		store, err := conversations.Open(config.Database, create)
		if !create && errors.Is(err, os.ErrNotExist) {
			f(nil)
			return
		} else if err != nil {
			fail(w, err)
			return
		}
		defer store.Close()
		f(store)
	}
	mux.HandleFunc("GET /api/threads", func(w http.ResponseWriter, r *http.Request) {
		repo, err := git.Open(r.Context(), repository(r, config))
		if err != nil {
			fail(w, err)
			return
		}
		result := threadList{Author: repo.UserName(r.Context()), Threads: []conversations.Thread{}}
		withStore(w, false, func(store *conversations.Store) {
			if store == nil {
				writeJSON(w, 200, result)
				return
			}
			gitDir, err := repo.CommonDir(r.Context())
			if err != nil {
				fail(w, err)
				return
			}
			threads, err := store.Threads(r.Context(), gitDir)
			if err != nil {
				fail(w, err)
				return
			}
			q := r.URL.Query()
			if q.Get("base") != "" || q.Get("head") != "" {
				if threads, err = conversations.Place(r.Context(), repo, threads, q.Get("base"), q.Get("head"), q.Get("branch")); err != nil {
					fail(w, err)
					return
				}
			}
			result.Threads = threads
			writeJSON(w, 200, result)
		})
	})
	mux.HandleFunc("POST /api/threads", func(w http.ResponseWriter, r *http.Request) {
		var req threadRequest
		if err := decode(r, &req); err != nil {
			fail(w, err)
			return
		}
		repo, err := git.Open(r.Context(), repository(r, config))
		if err != nil {
			fail(w, err)
			return
		}
		if req.Base == "" || req.Head == "" {
			fail(w, errors.New("Name the comparison with base and head"))
			return
		}
		gitDir, err := repo.CommonDir(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		thread := conversations.Thread{GitDir: gitDir, Path: req.Path, Side: req.Side, StartLine: req.StartLine, EndLine: req.EndLine, Base: req.Base, Head: req.Head, Branch: req.Branch}
		if err := conversations.Anchor(r.Context(), repo, &thread); err != nil {
			fail(w, err)
			return
		}
		withStore(w, true, func(store *conversations.Store) {
			created, err := store.CreateThread(r.Context(), thread, conversations.Comment{Body: req.Body, Author: req.Author, AuthorKind: req.AuthorKind})
			if err != nil {
				fail(w, err)
				return
			}
			changed(gitDir)
			writeJSON(w, 201, created)
		})
	})
	// The remaining writes act on stored threads and comments, which know
	// their repository.
	write := func(pattern string, f func(*http.Request, *conversations.Store, int64) (string, *conversations.Thread, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r)
			if err != nil {
				fail(w, err)
				return
			}
			withStore(w, false, func(store *conversations.Store) {
				if store == nil {
					failStore(w, conversations.ErrNotFound)
					return
				}
				gitDir, thread, err := f(r, store, id)
				if err != nil {
					failStore(w, err)
					return
				}
				changed(gitDir)
				writeJSON(w, 200, map[string]any{"thread": thread})
			})
		})
	}
	write("POST /api/threads/{id}/comments", func(r *http.Request, store *conversations.Store, id int64) (string, *conversations.Thread, error) {
		var req commentRequest
		if err := decode(r, &req); err != nil {
			return "", nil, err
		}
		t, err := store.Reply(r.Context(), id, conversations.Comment{Body: req.Body, Author: req.Author, AuthorKind: req.AuthorKind})
		return gitDirOf(t), t, err
	})
	write("PATCH /api/threads/{id}", func(r *http.Request, store *conversations.Store, id int64) (string, *conversations.Thread, error) {
		var req resolveRequest
		if err := decode(r, &req); err != nil {
			return "", nil, err
		}
		t, err := store.Resolve(r.Context(), id, req.Resolved, req.By)
		return gitDirOf(t), t, err
	})
	write("PATCH /api/comments/{id}", func(r *http.Request, store *conversations.Store, id int64) (string, *conversations.Thread, error) {
		var req commentRequest
		if err := decode(r, &req); err != nil {
			return "", nil, err
		}
		t, err := store.EditComment(r.Context(), id, req.Body)
		return gitDirOf(t), t, err
	})
	write("DELETE /api/comments/{id}", func(r *http.Request, store *conversations.Store, id int64) (string, *conversations.Thread, error) {
		return store.DeleteComment(r.Context(), id)
	})
}

func gitDirOf(t *conversations.Thread) string {
	if t == nil {
		return ""
	}
	return t.GitDir
}
