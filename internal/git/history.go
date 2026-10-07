package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CommonDir identifies a repository across its linked worktrees.
func (r *Repository) CommonDir(ctx context.Context) (string, error) {
	out, err := run(ctx, r.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

// HashFiles returns the blob SHA Git would store for each working-tree path
// (relative to the repository root), without writing objects. A missing
// path maps to an empty string.
func (r *Repository) HashFiles(ctx context.Context, paths []string) (map[string]string, error) {
	hashes := map[string]string{}
	var present []string
	for _, path := range paths {
		hashes[path] = ""
		if info, err := os.Stat(filepath.Join(r.Path, path)); err == nil && info.Mode().IsRegular() {
			present = append(present, path)
		}
	}
	if len(present) == 0 {
		return hashes, nil
	}
	out, err := run(ctx, r.Path, append([]string{"hash-object", "--"}, present...)...)
	if err != nil {
		return nil, err
	}
	for i, oid := range strings.Fields(string(out)) {
		if i < len(present) {
			hashes[present[i]] = oid
		}
	}
	return hashes, nil
}

// Change is a commit's effect on one path: the blob it leaves there, or an
// empty blob for a deletion.
type Change struct {
	Path string
	Blob string
}

// CommitInfo is what linking needs to know about a commit: its first-parent
// changes, parents, and author date (which rebases preserve by default).
type CommitInfo struct {
	SHA     string
	Parents []string
	Date    time.Time
	Changes []Change
}

var objectName = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// IsObjectName reports whether s is a full SHA-1 or SHA-256 object name.
func IsObjectName(s string) bool { return objectName.MatchString(s) }

// Commits describes the given commits, which must be full SHAs.
func (r *Repository) Commits(ctx context.Context, shas []string) (map[string]CommitInfo, error) {
	commits := map[string]CommitInfo{}
	if len(shas) == 0 {
		return commits, nil
	}
	for _, sha := range shas {
		if !objectName.MatchString(sha) {
			return nil, fmt.Errorf("Invalid commit SHA %q", sha)
		}
	}
	args := append([]string{"log", "--no-walk=unsorted", "--first-parent", "-m", "--no-renames", "--raw", "--no-abbrev", "-z", "--format=%x01%H%x00%P%x00%aI"}, shas...)
	out, err := run(ctx, r.Path, append(args, "--")...)
	if err != nil {
		return nil, err
	}
	for _, record := range strings.Split(string(out), "\x01")[1:] {
		tokens := strings.Split(record, "\x00")
		if len(tokens) < 3 {
			continue
		}
		info := CommitInfo{SHA: strings.TrimSpace(tokens[0]), Parents: strings.Fields(tokens[1]), Changes: parseRaw(tokens[3:])}
		info.Date, _ = time.Parse(time.RFC3339, strings.TrimSpace(tokens[2]))
		commits[info.SHA] = info
	}
	return commits, nil
}

// TreeChanges lists paths that differ between two trees, like Commits does.
func (r *Repository) TreeChanges(ctx context.Context, base, head string) ([]Change, error) {
	out, err := run(ctx, r.Path, "diff", "--no-renames", "--raw", "--no-abbrev", "-z", base, head, "--")
	if err != nil {
		return nil, err
	}
	return parseRaw(strings.Split(string(out), "\x00")), nil
}

func parseRaw(tokens []string) []Change {
	var changes []Change
	for i := 0; i+1 < len(tokens); i++ {
		header := strings.TrimSpace(tokens[i])
		if !strings.HasPrefix(header, ":") {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 5 {
			continue
		}
		blob := fields[3]
		if strings.Trim(blob, "0") == "" {
			blob = ""
		}
		changes = append(changes, Change{Path: tokens[i+1], Blob: blob})
		i++
	}
	return changes
}

// GitPath resolves a path inside the Git directory, honoring settings such
// as core.hooksPath.
func (r *Repository) GitPath(ctx context.Context, name string) (string, error) {
	out, err := run(ctx, r.Path, "rev-parse", "--path-format=absolute", "--git-path", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
