package conversations

import (
	"sort"
	"time"

	"differ/internal/git"
)

// Link connects a turn to a reviewed commit (or working-tree snapshot).
// Match says how, strongest first:
//   - "commit": the commit was made during the turn (followed through amends
//     and rebases recorded by the post-rewrite hook);
//   - "content": the commit leaves a file the agent edited with exactly the
//     content the turn ended with, which survives rebases without the hook;
//   - "path": no stronger link exists, and this is the earliest commit
//     authored after the turn that changes a file the agent edited.
type Link struct {
	SHA   string `json:"sha"`
	Match string `json:"match"`
}

type LinkedTurn struct {
	Turn
	Links []Link `json:"links"`
}

// LinkTurns links each turn to steps, the commits under review from oldest
// to newest (the last may be a working-tree snapshot). rewrites maps commit
// SHAs to their replacements after amends and rebases.
func LinkTurns(turns []Turn, rewrites map[string]string, steps []git.CommitInfo) []LinkedTurn {
	follow := func(sha string) string {
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
	index := map[string]int{}
	byContent := map[[2]string][]string{}
	for i, step := range steps {
		index[step.SHA] = i
		for _, c := range step.Changes {
			key := [2]string{c.Path, c.Blob}
			byContent[key] = append(byContent[key], step.SHA)
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
			before := follow(t.HeadBefore)
			for sha := follow(t.HeadAfter); sha != before; {
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
		for _, f := range t.Files {
			for _, sha := range byContent[[2]string{f.Path, f.Blob}] {
				add(sha, "content")
			}
		}
		if len(lt.Links) == 0 && len(t.Files) > 0 {
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
		sort.SliceStable(lt.Links, func(a, b int) bool { return index[lt.Links[a].SHA] < index[lt.Links[b].SHA] })
		linked = append(linked, lt)
	}
	return linked
}
