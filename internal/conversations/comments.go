package conversations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"differ/internal/git"
)

// Review comments are kept beside the conversations, in the same database,
// and shared by all worktrees of a repository.

const (
	maxCommentBytes = 64 << 10
	maxAnchorLines  = 1000
	contextLines    = 3
)

// Comment is one message in a thread, written by the person reviewing
// (kind "user") or by an agent such as Claude (kind "agent").
type Comment struct {
	ID         int64  `json:"id"`
	ThreadID   int64  `json:"threadId"`
	Author     string `json:"author"`
	AuthorKind string `json:"authorKind"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
	EditedAt   string `json:"editedAt,omitempty"`
}

// Thread is a conversation pinned to lines of a file, to a whole file (no
// lines), or to a whole comparison (no path). Blob and Snippet record the
// anchored content so the thread can be found again in other comparisons.
type Thread struct {
	ID        int64    `json:"id"`
	GitDir    string   `json:"-"`
	Path      string   `json:"path"`
	Side      string   `json:"side"`
	StartLine int      `json:"startLine"`
	EndLine   int      `json:"endLine"`
	Blob      string   `json:"-"`
	Snippet   string   `json:"snippet"`
	Before    []string `json:"-"`
	After     []string `json:"-"`
	// The comparison the thread was started in.
	Base       string     `json:"base"`
	Head       string     `json:"head"`
	Branch     string     `json:"branch"`
	CreatedAt  string     `json:"createdAt"`
	ResolvedAt string     `json:"resolvedAt,omitempty"`
	ResolvedBy string     `json:"resolvedBy,omitempty"`
	Comments   []Comment  `json:"comments"`
	Placement  *Placement `json:"placement,omitempty"`
}

// Placement is where a thread appears in a particular comparison. Exact means
// the file is unchanged on that side; moved means the anchored lines were
// found elsewhere in it; outdated means they are gone.
type Placement struct {
	Status string `json:"status"`
	// File is the file's path in the comparison, which differs from the
	// thread's path for the old side of a renamed file.
	File      string `json:"file"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

func checkBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	switch {
	case body == "":
		return "", errors.New("Write a comment first")
	case len(body) > maxCommentBytes || !utf8.ValidString(body):
		return "", errors.New("Comments are limited to 64 KB of text")
	}
	return body, nil
}

func checkAuthor(c *Comment) error {
	if c.AuthorKind == "" {
		c.AuthorKind = "user"
	}
	if c.AuthorKind != "user" && c.AuthorKind != "agent" {
		return fmt.Errorf("Unknown author kind %q", c.AuthorKind)
	}
	c.Author = strings.TrimSpace(c.Author)
	if c.Author == "" {
		c.Author = map[string]string{"user": "You", "agent": "Claude"}[c.AuthorKind]
	}
	if len(c.Author) > 200 {
		return errors.New("Author names are limited to 200 bytes")
	}
	return nil
}

// sideFile finds path on one side of a comparison.
func sideFile(files []git.File, path, side string) (git.File, string, bool) {
	for _, f := range files {
		if side == "old" && f.OldPath == path && f.Status != "A" {
			return f, f.OldOID, true
		}
		if side == "new" && f.Path == path && f.Status != "D" {
			return f, f.NewOID, true
		}
	}
	return git.File{}, "", false
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

// Anchor completes a new thread's anchor from the comparison it is written
// in: the blob on its side, and the text of and around its lines. A thread
// without a path is about the whole comparison.
func Anchor(ctx context.Context, repo *git.Repository, t *Thread) error {
	if t.Path == "" {
		t.Side, t.StartLine, t.EndLine = "", 0, 0
		return nil
	}
	if t.Side != "old" && t.Side != "new" {
		return errors.New(`Side must be "old" or "new"`)
	}
	files, err := repo.SnapshotFiles(ctx, t.Base, t.Head)
	if err != nil {
		return err
	}
	_, blob, ok := sideFile(files, t.Path, t.Side)
	if !ok {
		return fmt.Errorf("%s is not on the %s side of this comparison", t.Path, t.Side)
	}
	t.Blob = blob
	if t.StartLine == 0 && t.EndLine == 0 {
		return nil
	}
	if t.EndLine == 0 {
		t.EndLine = t.StartLine
	}
	if t.StartLine < 1 || t.EndLine < t.StartLine || t.EndLine-t.StartLine >= maxAnchorLines {
		return fmt.Errorf("Invalid line range %d-%d", t.StartLine, t.EndLine)
	}
	content, err := repo.Blob(ctx, blob)
	if err != nil {
		return err
	}
	lines := splitLines(content)
	if t.EndLine > len(lines) {
		return fmt.Errorf("%s has %d lines on the %s side", t.Path, len(lines), t.Side)
	}
	t.Snippet = strings.Join(lines[t.StartLine-1:t.EndLine], "\n")
	t.Before = lines[max(0, t.StartLine-1-contextLines) : t.StartLine-1]
	t.After = lines[t.EndLine:min(len(lines), t.EndLine+contextLines)]
	return nil
}

// locate finds the anchored snippet in lines, preferring the match whose
// surrounding lines agree most with the recorded context, then the one
// nearest the original line. It returns a 1-based line, or 0.
func locate(lines []string, t Thread) int {
	snippet, before, after := strings.Split(t.Snippet, "\n"), t.Before, t.After
	best, bestScore, bestDistance := 0, -1, 0
	for i := 0; i+len(snippet) <= len(lines); i++ {
		if !equalLines(lines[i:i+len(snippet)], snippet) {
			continue
		}
		score := 0
		for k := 1; k <= len(before) && i-k >= 0 && lines[i-k] == before[len(before)-k]; k++ {
			score++
		}
		end := i + len(snippet)
		for k := 0; k < len(after) && end+k < len(lines) && lines[end+k] == after[k]; k++ {
			score++
		}
		distance := max(i+1-t.StartLine, t.StartLine-i-1)
		if score > bestScore || (score == bestScore && distance < bestDistance) {
			best, bestScore, bestDistance = i+1, score, distance
		}
	}
	return best
}

func equalLines(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Place keeps the threads that belong in a comparison and says where each
// appears. Threads on files outside it are dropped; threads on the whole
// comparison are kept when they were started in the same comparison or on
// the same branch.
func Place(ctx context.Context, repo *git.Repository, threads []Thread, base, head, branch string) ([]Thread, error) {
	files, err := repo.SnapshotFiles(ctx, base, head)
	if err != nil {
		return nil, err
	}
	contents := map[string][]string{}
	placed := []Thread{}
	for _, t := range threads {
		if t.Path == "" {
			if (t.Base == base && t.Head == head) || (branch != "" && t.Branch == branch) {
				t.Placement = &Placement{Status: "exact"}
				placed = append(placed, t)
			}
			continue
		}
		file, blob, ok := sideFile(files, t.Path, t.Side)
		if !ok {
			continue
		}
		switch {
		case blob == t.Blob || t.StartLine == 0:
			t.Placement = &Placement{Status: "exact", File: file.Path, StartLine: t.StartLine, EndLine: t.EndLine}
		default:
			lines, ok := contents[blob]
			if !ok {
				content, err := repo.Blob(ctx, blob)
				if err != nil && !errors.Is(err, git.ErrTooLarge) {
					return nil, err
				}
				lines = splitLines(content)
				contents[blob] = lines
			}
			if start := locate(lines, t); start > 0 {
				t.Placement = &Placement{Status: "moved", File: file.Path, StartLine: start, EndLine: start + t.EndLine - t.StartLine}
			} else {
				t.Placement = &Placement{Status: "outdated", File: file.Path}
			}
		}
		placed = append(placed, t)
	}
	return placed, nil
}

// CreateThread stores a new, already anchored thread and its first comment.
func (s *Store) CreateThread(ctx context.Context, t Thread, first Comment) (*Thread, error) {
	body, err := checkBody(first.Body)
	if err != nil {
		return nil, err
	}
	if err := checkAuthor(&first); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	before, _ := json.Marshal(t.Before)
	after, _ := json.Marshal(t.After)
	at := now()
	result, err := tx.ExecContext(ctx, `INSERT INTO threads
		(git_dir, path, side, start_line, end_line, blob, snippet, context_before, context_after, base, head, branch, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.GitDir, t.Path, t.Side, t.StartLine, t.EndLine, t.Blob, t.Snippet, string(before), string(after), t.Base, t.Head, t.Branch, at)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments (thread_id, author, author_kind, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, first.Author, first.AuthorKind, body, at); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Thread(ctx, id)
}

// Reply adds a comment to a thread.
func (s *Store) Reply(ctx context.Context, threadID int64, c Comment) (*Thread, error) {
	body, err := checkBody(c.Body)
	if err != nil {
		return nil, err
	}
	if err := checkAuthor(&c); err != nil {
		return nil, err
	}
	if _, err := s.Thread(ctx, threadID); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO comments (thread_id, author, author_kind, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		threadID, c.Author, c.AuthorKind, body, now()); err != nil {
		return nil, err
	}
	return s.Thread(ctx, threadID)
}

// Resolve marks a thread resolved by someone, or reopens it.
func (s *Store) Resolve(ctx context.Context, threadID int64, resolved bool, by string) (*Thread, error) {
	var err error
	if resolved {
		_, err = s.db.ExecContext(ctx, `UPDATE threads SET resolved_at = ?, resolved_by = ? WHERE id = ?`, now(), by, threadID)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE threads SET resolved_at = NULL, resolved_by = '' WHERE id = ?`, threadID)
	}
	if err != nil {
		return nil, err
	}
	return s.Thread(ctx, threadID)
}

// EditComment replaces a comment's text and returns its thread.
func (s *Store) EditComment(ctx context.Context, id int64, body string) (*Thread, error) {
	body, err := checkBody(body)
	if err != nil {
		return nil, err
	}
	var threadID int64
	if err := s.db.QueryRowContext(ctx, `UPDATE comments SET body = ?, edited_at = ? WHERE id = ? RETURNING thread_id`, body, now(), id).Scan(&threadID); err != nil {
		return nil, notFound(err, "comment")
	}
	return s.Thread(ctx, threadID)
}

// DeleteComment removes a comment, and its thread once no comments are left.
// It returns the thread's repository and the remaining thread, if any.
func (s *Store) DeleteComment(ctx context.Context, id int64) (string, *Thread, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	var threadID int64
	if err := tx.QueryRowContext(ctx, `DELETE FROM comments WHERE id = ? RETURNING thread_id`, id).Scan(&threadID); err != nil {
		return "", nil, notFound(err, "comment")
	}
	var gitDir string
	if err := tx.QueryRowContext(ctx, `SELECT git_dir FROM threads WHERE id = ?`, threadID).Scan(&gitDir); err != nil {
		return "", nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE id = ? AND NOT EXISTS (SELECT 1 FROM comments WHERE thread_id = ?)`, threadID, threadID); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	t, err := s.Thread(ctx, threadID)
	if errors.Is(err, ErrNotFound) {
		return gitDir, nil, nil
	}
	return gitDir, t, err
}

// ErrNotFound is wrapped by errors for a missing thread or comment.
var ErrNotFound = errors.New("not found")

func notFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("No such %s: %w", what, ErrNotFound)
	}
	return err
}

// IsNotFound reports whether err names a missing thread or comment.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

const threadColumns = `id, git_dir, path, side, start_line, end_line, blob, snippet, context_before, context_after, base, head, branch, created_at, COALESCE(resolved_at, ''), resolved_by`

func (s *Store) Thread(ctx context.Context, id int64) (*Thread, error) {
	threads, err := s.threads(ctx, `WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(threads) == 0 {
		return nil, notFound(sql.ErrNoRows, "thread")
	}
	return &threads[0], nil
}

// Threads returns a repository's threads with their comments, oldest first.
func (s *Store) Threads(ctx context.Context, gitDir string) ([]Thread, error) {
	return s.threads(ctx, `WHERE git_dir = ?`, gitDir)
}

func (s *Store) threads(ctx context.Context, where string, arg any) ([]Thread, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+threadColumns+` FROM threads `+where+` ORDER BY id`, arg)
	if err != nil {
		return nil, err
	}
	threads := []Thread{}
	byID := map[int64]int{}
	for rows.Next() {
		var t Thread
		var before, after string
		if err := rows.Scan(&t.ID, &t.GitDir, &t.Path, &t.Side, &t.StartLine, &t.EndLine, &t.Blob, &t.Snippet, &before, &after, &t.Base, &t.Head, &t.Branch, &t.CreatedAt, &t.ResolvedAt, &t.ResolvedBy); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(before), &t.Before)
		_ = json.Unmarshal([]byte(after), &t.After)
		t.Comments = []Comment{}
		byID[t.ID] = len(threads)
		threads = append(threads, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(threads) == 0 {
		return threads, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id, thread_id, author, author_kind, body, created_at, COALESCE(edited_at, '')
		FROM comments WHERE thread_id IN (SELECT id FROM threads `+where+`) ORDER BY id`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.ThreadID, &c.Author, &c.AuthorKind, &c.Body, &c.CreatedAt, &c.EditedAt); err != nil {
			return nil, err
		}
		if i, ok := byID[c.ThreadID]; ok {
			threads[i].Comments = append(threads[i].Comments, c)
		}
	}
	return threads, rows.Err()
}
