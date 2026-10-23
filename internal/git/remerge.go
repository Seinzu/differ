package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Remerge names, as a comparison's base, the tree Git produces by merging the
// head commit's two parents again. Comparing it with the merge commit shows
// only what was done during the merge, such as conflict resolutions, like
// git show --remerge-diff.
const Remerge = ":remerge"

func (r *Repository) compareRemerge(ctx context.Context, headRef string) (*Comparison, error) {
	head, err := r.Resolve(ctx, headRef)
	if err != nil {
		return nil, err
	}
	out, err := run(ctx, r.Path, "rev-list", "--parents", "--max-count=1", head, "--")
	if err != nil {
		return nil, err
	}
	parents := strings.Fields(string(out))[1:]
	if len(parents) != 2 {
		return nil, errors.New("Only a merge commit with two parents can be re-merged")
	}
	base, conflicts, err := r.remergeTree(ctx, parents[0], parents[1])
	if err != nil {
		return nil, err
	}
	c := &Comparison{Repository: r.Path, Name: r.Name(ctx), Base: base, Head: head, Relationship: "remerge", Commits: []Commit{}, Conflicts: conflicts}
	if c.Files, err = r.Files(ctx, base, head); err != nil {
		return nil, err
	}
	for _, f := range c.Files {
		c.Additions += f.Additions
		c.Deletions += f.Deletions
	}
	return c, nil
}

// remergeTree merges two commits without touching the index, working tree,
// or refs, and returns the resulting tree (with conflict markers left in
// conflicted files) and Git's conflict messages. Like SnapshotWorktree, it
// only adds unreferenced objects, which Git garbage-collects later.
func (r *Repository) remergeTree(ctx context.Context, first, second string) (string, []string, error) {
	// Abbreviated SHAs label the conflict markers, as git show --remerge-diff does.
	names := []string{first, second}
	for i, sha := range names {
		label, err := run(ctx, r.Path, "rev-parse", "--short", sha)
		if err != nil {
			return "", nil, err
		}
		names[i] = strings.TrimSpace(string(label))
	}
	// merge-tree exits with status 1 when the merge has conflicts.
	out, _, err := runStatus(ctx, r.Path, nil, 1, "merge-tree", "--write-tree", "--name-only", "-z", names[0], names[1])
	if err != nil {
		if strings.Contains(err.Error(), "usage:") || strings.Contains(err.Error(), "unknown option") {
			return "", nil, errors.New("Showing merge resolutions needs Git 2.38 or newer")
		}
		return "", nil, fmt.Errorf("Cannot re-merge the parents: %w", err)
	}
	// Output: the tree, conflicted paths, an empty token, then messages, each
	// as a path count, the paths, a type, and the message text.
	tokens := strings.Split(string(out), "\x00")
	tree := strings.TrimSpace(tokens[0])
	if tree == "" {
		return "", nil, errors.New("Unexpected Git merge-tree output")
	}
	i := 1
	for i < len(tokens) && tokens[i] != "" {
		i++
	}
	conflicts := []string{}
	for i++; i < len(tokens) && tokens[i] != ""; {
		count, err := strconv.Atoi(tokens[i])
		if err != nil || i+count+2 >= len(tokens) {
			break
		}
		kind, message := tokens[i+count+1], strings.TrimSpace(tokens[i+count+2])
		if strings.HasPrefix(kind, "CONFLICT") {
			conflicts = append(conflicts, message)
		}
		i += count + 3
	}
	return tree, conflicts, nil
}
