// Package conversations records prompts and responses from Claude Code
// sessions in a SQLite database and links them to commits.
package conversations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultPath is the database shared by every repository: $DIFFER_DB, or
// differ/conversations.db in the user's configuration directory.
func DefaultPath() string {
	if path := os.Getenv("DIFFER_DB"); path != "" {
		return path
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "differ", "conversations.db")
}

// FileState is a file the agent edited during a turn and the blob SHA of its
// content when the turn ended (empty when the file was deleted). Matching
// blobs survive rebases that change commit SHAs and dates.
type FileState struct {
	Path string `json:"path"`
	Blob string `json:"blob"`
}

// Turn is one prompt and the agent's response to it.
type Turn struct {
	ID          int64       `json:"id"`
	SessionID   string      `json:"sessionId"`
	GitDir      string      `json:"-"`
	Worktree    string      `json:"worktree"`
	Branch      string      `json:"branch"`
	HeadBefore  string      `json:"headBefore"`
	HeadAfter   string      `json:"headAfter"`
	Prompt      string      `json:"prompt"`
	Response    string      `json:"response"`
	Files       []FileState `json:"files"`
	PromptedAt  string      `json:"promptedAt"`
	RespondedAt string      `json:"respondedAt,omitempty"`
}

type Store struct{ db *sql.DB }

var migrations = []string{
	`CREATE TABLE turns (
		id INTEGER PRIMARY KEY,
		session_id TEXT NOT NULL,
		git_dir TEXT NOT NULL,
		worktree TEXT NOT NULL,
		cwd TEXT NOT NULL,
		branch TEXT NOT NULL DEFAULT '',
		head_before TEXT NOT NULL DEFAULT '',
		head_after TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL,
		response TEXT NOT NULL DEFAULT '',
		files TEXT NOT NULL DEFAULT '[]',
		transcript_path TEXT NOT NULL DEFAULT '',
		prompted_at TEXT NOT NULL,
		responded_at TEXT
	);
	CREATE INDEX turns_by_repository ON turns (git_dir, prompted_at);
	CREATE INDEX turns_by_session ON turns (session_id, id);
	CREATE TABLE rewrites (
		git_dir TEXT NOT NULL,
		old_sha TEXT NOT NULL,
		new_sha TEXT NOT NULL,
		kind TEXT NOT NULL,
		rewritten_at TEXT NOT NULL,
		PRIMARY KEY (git_dir, old_sha)
	);`,
}

// Open opens the database, creating it when create is set. Otherwise a
// missing database is reported as os.ErrNotExist.
func Open(path string, create bool) (*Store, error) {
	if !create {
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Concurrent sessions each run hooks; WAL and a busy timeout let them
	// write without failing on each other's locks.
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("conversation database version %d is newer than this Differ supports", version)
	}
	for ; version < len(migrations); version++ {
		if _, err := tx.Exec(migrations[version]); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// StartTurn records a submitted prompt.
func (s *Store) StartTurn(ctx context.Context, t Turn, cwd, transcript string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO turns
		(session_id, git_dir, worktree, cwd, branch, head_before, prompt, transcript_path, prompted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.SessionID, t.GitDir, t.Worktree, cwd, t.Branch, t.HeadBefore, t.Prompt, transcript, now())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// FinishTurn records the response to the session's latest open prompt. When
// no prompt was recorded (the hook was installed mid-session), the response
// is stored as a turn of its own.
func (s *Store) FinishTurn(ctx context.Context, t Turn, cwd, transcript string) error {
	files, err := json.Marshal(t.Files)
	if err != nil {
		return err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM turns WHERE session_id = ? AND responded_at IS NULL ORDER BY id DESC LIMIT 1`, t.SessionID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if id, err = s.StartTurn(ctx, t, cwd, transcript); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE turns SET response = ?, head_after = ?, files = ?, responded_at = ?,
		branch = CASE WHEN branch = '' THEN ? ELSE branch END WHERE id = ?`,
		t.Response, t.HeadAfter, string(files), now(), t.Branch, id)
	return err
}

// RecordRewrites stores the old-to-new commit mapping Git reports after an
// amend or rebase.
func (s *Store) RecordRewrites(ctx context.Context, gitDir, kind string, pairs [][2]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := now()
	for _, pair := range pairs {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO rewrites (git_dir, old_sha, new_sha, kind, rewritten_at) VALUES (?, ?, ?, ?, ?)`, gitDir, pair[0], pair[1], kind, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Rewrites returns the repository's old-to-new commit mapping.
func (s *Store) Rewrites(ctx context.Context, gitDir string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT old_sha, new_sha FROM rewrites WHERE git_dir = ?`, gitDir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	mapping := map[string]string{}
	for rows.Next() {
		var old, new string
		if err := rows.Scan(&old, &new); err != nil {
			return nil, err
		}
		mapping[old] = new
	}
	return mapping, rows.Err()
}

// Turns returns the repository's most recent turns, oldest first.
func (s *Store) Turns(ctx context.Context, gitDir string, limit int) ([]Turn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (
		SELECT id, session_id, git_dir, worktree, branch, head_before, head_after, prompt, response, files, prompted_at, COALESCE(responded_at, '')
		FROM turns WHERE git_dir = ? ORDER BY prompted_at DESC, id DESC LIMIT ?
	) ORDER BY prompted_at, id`, gitDir, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	turns := []Turn{}
	for rows.Next() {
		var t Turn
		var files string
		if err := rows.Scan(&t.ID, &t.SessionID, &t.GitDir, &t.Worktree, &t.Branch, &t.HeadBefore, &t.HeadAfter, &t.Prompt, &t.Response, &files, &t.PromptedAt, &t.RespondedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(files), &t.Files); err != nil || t.Files == nil {
			t.Files = []FileState{}
		}
		turns = append(turns, t)
	}
	return turns, rows.Err()
}
