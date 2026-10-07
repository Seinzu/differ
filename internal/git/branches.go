package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Branch struct {
	Name    string `json:"name"`
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	// Current is set for the branch checked out in the opened worktree.
	Current bool `json:"current"`
	// Worktree is where the branch is checked out, if anywhere.
	Worktree string `json:"worktree,omitempty"`
}

// Info describes a working copy for choosing what to review.
type Info struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
	Bare       bool   `json:"bare"`
	// CurrentBranch is checked out in the opened worktree. In a bare
	// repository it is the branch HEAD names, which no worktree may have.
	CurrentBranch string     `json:"currentBranch"`
	DefaultBranch string     `json:"defaultBranch"`
	Detached      bool       `json:"detached"`
	Dirty         bool       `json:"dirty"`
	Branches      []Branch   `json:"branches"`
	Worktrees     []Checkout `json:"worktrees"`
}

// CurrentBranch is the checked-out branch, or empty for a detached HEAD.
func (r *Repository) CurrentBranch(ctx context.Context) string {
	out, err := run(ctx, r.Path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (r *Repository) hasBranch(ctx context.Context, name string) bool {
	_, err := run(ctx, r.Path, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+name+"^{commit}")
	return err == nil
}

// DefaultBranch prefers main, then master, then the branch origin/HEAD names.
func (r *Repository) DefaultBranch(ctx context.Context) string {
	for _, name := range []string{"main", "master"} {
		if r.hasBranch(ctx, name) {
			return name
		}
	}
	out, err := run(ctx, r.Path, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		name := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
		if r.hasBranch(ctx, name) {
			return name
		}
	}
	return ""
}

func (r *Repository) Info(ctx context.Context) (*Info, error) {
	info := &Info{Repository: r.Path, Name: r.Name(ctx), Bare: r.Bare, CurrentBranch: r.CurrentBranch(ctx), DefaultBranch: r.DefaultBranch(ctx), Branches: []Branch{}}
	info.Detached = info.CurrentBranch == "" && !r.Bare
	var err error
	if info.Worktrees, err = r.Worktrees(ctx); err != nil {
		return nil, err
	}
	checkedOut := map[string]string{}
	for _, w := range info.Worktrees {
		if w.Branch != "" {
			checkedOut[w.Branch] = w.Path
		}
		if w.Current {
			info.Dirty = w.Dirty
		}
	}
	out, err := run(ctx, r.Path, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)%00%(objectname)%00%(contents:subject)%00%(committerdate:iso-strict)%00", "refs/heads")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(out), "\x00")
	for i := 0; i+3 < len(parts); i += 4 {
		name := strings.TrimSpace(parts[i])
		info.Branches = append(info.Branches, Branch{Name: name, SHA: parts[i+1], Subject: parts[i+2], Date: parts[i+3], Current: !r.Bare && name == info.CurrentBranch, Worktree: checkedOut[name]})
	}
	return info, nil
}

// Range is the comparison Differ chooses for a branch: the branch's commits
// since it left the default branch or, on the default branch, its latest commits.
type Range struct {
	Mode          string `json:"mode"`
	Branch        string `json:"branch"`
	DefaultBranch string `json:"defaultBranch"`
	Count         int    `json:"count"`
	Available     int    `json:"available"`
	Base          string `json:"base"`
	Head          string `json:"head"`
}

const MaxRecentCommits = 10000

// DefaultRange compares branch (the checked-out branch when empty) with its
// merge base on the default branch. On the default branch itself, on a branch
// with no commits of its own, or without a default branch, it shows the latest
// count first-parent commits instead.
func (r *Repository) DefaultRange(ctx context.Context, branch string, count int) (*Range, error) {
	if count < 0 || count > MaxRecentCommits {
		return nil, fmt.Errorf("Choose between 0 and %d recent commits", MaxRecentCommits)
	}
	rg := &Range{Branch: branch, DefaultBranch: r.DefaultBranch(ctx), Count: count, Head: "HEAD"}
	if rg.Branch == "" {
		rg.Branch = r.CurrentBranch(ctx)
		// A bare repository's HEAD can name a branch that does not exist.
		if r.Bare && rg.Branch != "" && !r.hasBranch(ctx, rg.Branch) {
			rg.Branch = rg.DefaultBranch
		}
	}
	if rg.Branch != "" {
		if !r.hasBranch(ctx, rg.Branch) {
			return nil, fmt.Errorf("No local branch named %q", rg.Branch)
		}
		rg.Head = "refs/heads/" + rg.Branch
	}
	head, err := r.Resolve(ctx, rg.Head)
	if err != nil {
		return nil, errors.New("This repository has no commits yet")
	}
	if rg.DefaultBranch != "" && rg.Branch != rg.DefaultBranch {
		// Merge-base exits with status 1 for unrelated histories; that falls
		// through to showing recent commits.
		if out, err := run(ctx, r.Path, "merge-base", "refs/heads/"+rg.DefaultBranch, head); err == nil {
			if base := strings.TrimSpace(string(out)); base != head {
				rg.Mode, rg.Base = "branch", base
				return rg, nil
			}
		}
	}
	rg.Mode = "recent"
	out, err := run(ctx, r.Path, "rev-list", "--count", "--first-parent", head, "--")
	if err != nil {
		return nil, err
	}
	rg.Available, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	if count >= rg.Available {
		rg.Base = ":empty"
		return rg, nil
	}
	rg.Base, err = r.Resolve(ctx, head+"~"+strconv.Itoa(count))
	return rg, err
}
