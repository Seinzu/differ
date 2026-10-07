package conversations

import (
	"context"
	"errors"
	"sort"
	"time"

	"differ/internal/git"
)

// Link connects a turn to a reviewed commit (or working-tree snapshot).
// Match says how, strongest first:
//   - "commit": the commit was made (or amended) during the turn, followed
//     through amends and rebases recorded by the post-rewrite hook;
//   - "content": the commit leaves a file the agent edited with exactly the
//     content the turn ended with, which survives rebases without the hook;
//   - "head": the commit was HEAD while the turn ran, so the conversation
//     happened at that point in the branch, even if it changed nothing.
//     Amended and rebased commits are found through the post-rewrite hook,
//     or by their author, author date, and subject when it did not run;
//   - "parent": the turn ran on the reviewed branch with HEAD at this
//     commit's parent, which is not under review itself, so the turn was
//     work toward this commit (such as a conversation before a branch's
//     first commit);
//   - "path": no stronger link exists, and this is the earliest commit
//     authored after the turn that changes a file the agent edited.
type Link struct {
	SHA   string `json:"sha"`
	Match string `json:"match"`
}

type LinkedTurn struct {
	Turn
	Links []Link `json:"links"`
	// OnBranch marks an unlinked turn that happened on the reviewed branch:
	// in a session with linked turns on it, or since the branch started.
	OnBranch bool `json:"onBranch"`
}

// follower resolves a commit SHA to its latest replacement after amends and
// rebases.
func follower(rewrites map[string]string) func(string) string {
	return func(sha string) string {
		// Bounded in case of a cycle (a rebase that was later undone).
		for i := 0; i < 100; i++ {
			next, ok := rewrites[sha]
			if !ok || next == sha {
				break
			}
			sha = next
		}
		return sha
	}
}

// turnHead is the commit a turn worked on: HEAD when it finished, or when it
// started if it never finished.
func turnHead(t Turn) string {
	if t.HeadAfter != "" {
		return t.HeadAfter
	}
	return t.HeadBefore
}

// LinkTurns links each turn to steps, the commits under review from oldest
// to newest (the last may be a working-tree snapshot). rewrites maps commit
// SHAs to their replacements after amends and rebases. outside describes
// commits turns worked on that are not under review, so rewritten ones can
// be recognized by identity. Unlinked turns on branch are marked OnBranch
// when their session has linked turns on it or they came after since.
func LinkTurns(turns []Turn, rewrites map[string]string, steps []git.CommitInfo, outside map[string]git.CommitInfo, branch string, since time.Time) []LinkedTurn {
	follow := follower(rewrites)
	index := map[string]int{}
	byContent := map[[2]string][]string{}
	byIdentity := map[string]string{}
	for i, step := range steps {
		index[step.SHA] = i
		if step.Identity != "" {
			byIdentity[step.Identity] = step.SHA
		}
		for _, c := range step.Changes {
			key := [2]string{c.Path, c.Blob}
			byContent[key] = append(byContent[key], step.SHA)
		}
	}
	// reviewed finds the commit under review that sha has become, if any.
	reviewed := func(sha string) (string, bool) {
		if sha == "" {
			return "", false
		}
		sha = follow(sha)
		if _, ok := index[sha]; ok {
			return sha, true
		}
		if info, ok := outside[sha]; ok && info.Identity != "" {
			current, ok := byIdentity[info.Identity]
			return current, ok
		}
		return "", false
	}
	// children maps a commit to the first reviewed commit built on it.
	children := map[string]string{}
	for _, step := range steps {
		if len(step.Parents) > 0 {
			if _, ok := children[step.Parents[0]]; !ok {
				children[step.Parents[0]] = step.SHA
			}
		}
		if since.IsZero() || step.Date.Before(since) {
			since = step.Date
		}
	}
	linked := make([]LinkedTurn, 0, len(turns))
	for _, t := range turns {
		lt := LinkedTurn{Turn: t, Links: []Link{}}
		seen := map[string]bool{}
		add := func(sha, match string) {
			if !seen[sha] {
				seen[sha] = true
				lt.Links = append(lt.Links, Link{sha, match})
			}
		}
		if t.HeadAfter != "" && t.HeadAfter != t.HeadBefore {
			if after, ok := reviewed(t.HeadAfter); ok {
				before, _ := reviewed(t.HeadBefore)
				if before == after {
					// The turn's starting commit was amended into its last one.
					add(after, "commit")
				}
				for sha := after; sha != before; {
					i, ok := index[sha]
					if !ok {
						break
					}
					add(sha, "commit")
					if len(steps[i].Parents) == 0 {
						break
					}
					sha = steps[i].Parents[0]
				}
			}
		}
		for _, f := range t.Files {
			for _, sha := range byContent[[2]string{f.Path, f.Blob}] {
				add(sha, "content")
			}
		}
		// Where the turn's edits landed matters more than where it started,
		// so the path fallback runs whenever nothing stronger matched.
		edited := len(lt.Links) > 0
		if sha, ok := reviewed(turnHead(t)); ok {
			add(sha, "head")
		} else if child, ok := children[follow(turnHead(t))]; ok && turnHead(t) != "" && branch != "" && t.Branch == branch {
			// Only on the reviewed branch: the commit a branch starts from is
			// also HEAD for unrelated work on the branch it came from.
			add(child, "parent")
		}
		if !edited && len(t.Files) > 0 {
			if done, err := time.Parse(time.RFC3339, t.RespondedAt); err == nil {
				paths := map[string]bool{}
				for _, f := range t.Files {
					paths[f.Path] = true
				}
				best := -1
				for i, step := range steps {
					if step.Date.Before(done.Truncate(time.Second)) || (best >= 0 && !step.Date.Before(steps[best].Date)) {
						continue
					}
					for _, c := range step.Changes {
						if paths[c.Path] {
							best = i
							break
						}
					}
				}
				if best >= 0 {
					add(steps[best].SHA, "path")
				}
			}
		}
		if len(lt.Links) == 0 && branch != "" && t.Branch == branch && !since.IsZero() {
			if at, err := time.Parse(time.RFC3339, t.PromptedAt); err == nil && !at.Before(since.Truncate(time.Second)) {
				lt.OnBranch = true
			}
		}
		sort.SliceStable(lt.Links, func(a, b int) bool { return index[lt.Links[a].SHA] < index[lt.Links[b].SHA] })
		linked = append(linked, lt)
	}
	// A session that produced linked turns on this branch was working on it
	// throughout, including turns from before a rebase moved its start.
	sessions := map[string]bool{}
	for _, lt := range linked {
		if len(lt.Links) > 0 && lt.Branch == branch {
			sessions[lt.SessionID] = true
		}
	}
	for i := range linked {
		if lt := &linked[i]; len(lt.Links) == 0 && branch != "" && lt.Branch == branch && sessions[lt.SessionID] {
			lt.OnBranch = true
		}
	}
	return linked
}

// LinkCommits describes the commits under review (full SHAs, oldest first) and an
// optional working-tree snapshot (tree, with its parent commit), then links
// turns to them.
func LinkCommits(ctx context.Context, repo *git.Repository, turns []Turn, rewrites map[string]string, shas []string, base, tree, treeParent, branch string) ([]LinkedTurn, error) {
	infos, err := repo.Commits(ctx, shas)
	if err != nil {
		return nil, err
	}
	steps := make([]git.CommitInfo, 0, len(shas)+1)
	for _, sha := range shas {
		if info, ok := infos[sha]; ok {
			steps = append(steps, info)
		}
	}
	if tree != "" {
		if !git.IsObjectName(tree) || !git.IsObjectName(treeParent) {
			return nil, errors.New("Invalid working-tree snapshot")
		}
		changes, err := repo.TreeChanges(ctx, treeParent, tree)
		if err != nil {
			return nil, err
		}
		steps = append(steps, git.CommitInfo{SHA: tree, Parents: []string{treeParent}, Date: time.Now(), Changes: changes})
	}
	// Describe the commits turns worked on that are not under review: after
	// an amend or rebase without a recorded rewrite, their identity still
	// matches the reviewed commit that replaced them.
	follow := follower(rewrites)
	var missing []string
	for _, t := range turns {
		if sha := follow(turnHead(t)); git.IsObjectName(sha) && infos[sha].SHA == "" {
			missing = append(missing, sha)
		}
	}
	// The branch cannot have existed before the commit it started from.
	if git.IsObjectName(base) {
		missing = append(missing, base)
	}
	outside, err := repo.Commits(ctx, missing)
	if err != nil {
		return nil, err
	}
	return LinkTurns(turns, rewrites, steps, outside, branch, outside[base].Date), nil
}
