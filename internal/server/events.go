package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// view is what one open Differ tab shows, as the tab reports it.
type view struct {
	Tab string `json:"tab"`
	// Query is the tab's address query, which reopens the same comparison.
	Query      string `json:"query"`
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
	// Base and Head are the comparison under review.
	Base string `json:"base"`
	Head string `json:"head"`
	// Mode is total, commits, conversations, or comments.
	Mode string `json:"mode"`
	// Commit is the commit shown in commit-by-commit review, compared as
	// ViewBase to ViewHead; otherwise those match Base and Head.
	Commit     string    `json:"commit,omitempty"`
	ViewBase   string    `json:"viewBase"`
	ViewHead   string    `json:"viewHead"`
	File       string    `json:"file,omitempty"`
	ReportedAt time.Time `json:"reportedAt"`
}

// hub fans server events out to every open Differ tab and remembers what
// each tab shows, so agents can find and change the view in front of you.
type hub struct {
	mu    sync.Mutex
	subs  map[chan string]string
	views map[string]view
	// last is the tab that most recently reported its view: the one in use.
	last string
}

func newHub() *hub { return &hub{subs: map[chan string]string{}, views: map[string]view{}} }

func (h *hub) publish(event string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		send(ch, event)
	}
}

func send(ch chan string, event string) {
	select {
	case ch <- event:
	default: // A tab that is not reading misses the event and refetches later.
	}
}

// navigate sends a tab, preferably the one in use, to a view and returns
// how many tabs it reached.
func (h *hub) navigate(query string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	target := h.connectedTab()
	data, _ := json.Marshal(map[string]string{"type": "navigate", "tab": target, "query": query})
	reached := 0
	for ch, tab := range h.subs {
		if tab == target {
			send(ch, string(data))
			reached = 1
		}
	}
	return reached
}

// connectedTab is the tab in use if it is still open, otherwise the open tab
// that reported most recently, or "" when no tab is open. h.mu must be held.
func (h *hub) connectedTab() string {
	open := map[string]bool{}
	for _, tab := range h.subs {
		open[tab] = true
	}
	if open[h.last] {
		return h.last
	}
	best := ""
	for tab := range open {
		if best == "" || h.views[tab].ReportedAt.After(h.views[best].ReportedAt) {
			best = tab
		}
	}
	return best
}

// current returns the view of the tab in use, if any tab is open and has
// reported one, and the number of open tabs.
func (h *hub) current() (*view, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	open := map[string]bool{}
	for _, tab := range h.subs {
		open[tab] = true
	}
	v, ok := h.views[h.connectedTab()]
	if !ok {
		return nil, len(open)
	}
	return &v, len(open)
}

func (h *hub) report(v view) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v.ReportedAt = time.Now()
	h.views[v.Tab] = v
	h.last = v.Tab
}

// serve streams events to one tab until it disconnects or the server stops.
func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming is not supported", 500)
		return
	}
	tab := r.URL.Query().Get("tab")
	if len(tab) > 64 {
		http.Error(w, "Invalid tab", 400)
		return
	}
	ch := make(chan string, 16)
	h.mu.Lock()
	h.subs[ch] = tab
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case event := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
		flusher.Flush()
	}
}

func registerEvents(mux *http.ServeMux, events *hub) {
	mux.HandleFunc("GET /api/events", events.serve)
	mux.HandleFunc("POST /api/view", func(w http.ResponseWriter, r *http.Request) {
		var v view
		if err := decode(r, &v); err != nil {
			fail(w, err)
			return
		}
		if v.Tab == "" || len(v.Tab) > 64 {
			fail(w, errors.New("Invalid tab"))
			return
		}
		events.report(v)
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}
