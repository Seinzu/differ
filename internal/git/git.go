// Package git reads commit snapshots using the installed Git executable.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxOutput = 32 << 20

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxOutput {
		return 0, errors.New("Git output exceeds 32 MB; choose a smaller comparison")
	}
	return b.Buffer.Write(p)
}

func run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args = append([]string{"--no-pager", "--literal-pathspecs", "-c", "core.quotePath=false", "-c", "diff.submodule=short", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	var out limitedBuffer
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Git operation timed out or was cancelled: %w", ctx.Err())
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s", message)
	}
	return out.Bytes(), nil
}

type Repository struct{ Path string }

func Open(ctx context.Context, path string) (*Repository, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	root, err := run(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("Open a local Git working copy: %w", err)
	}
	return &Repository{Path: strings.TrimSuffix(string(root), "\n")}, nil
}

func (r *Repository) Resolve(ctx context.Context, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" || len(ref) > 1024 {
		return "", errors.New("Enter a commit SHA, branch, or tag")
	}
	data, err := run(ctx, r.Path, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("Cannot resolve commit %q", ref)
	}
	return strings.TrimSpace(string(data)), nil
}

// A root commit can enter a range through a merge of unrelated histories.
// Hashing the empty tree (without -w) supports it without changing the repository.
func (r *Repository) resolveBase(ctx context.Context, ref string) (string, error) {
	if ref != ":empty" {
		return r.Resolve(ctx, ref)
	}
	out, err := run(ctx, r.Path, "hash-object", "-t", "tree", "--stdin")
	return strings.TrimSpace(string(out)), err
}

type Commit struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
	Subject string   `json:"subject"`
	Author  string   `json:"author"`
	Date    string   `json:"date"`
}

type File struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath"`
	Status    string `json:"status"`
	OldMode   string `json:"oldMode"`
	NewMode   string `json:"newMode"`
	OldOID    string `json:"-"`
	NewOID    string `json:"-"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
}

type Comparison struct {
	Repository   string   `json:"repository"`
	Name         string   `json:"name"`
	Base         string   `json:"base"`
	Head         string   `json:"head"`
	Relationship string   `json:"relationship"`
	Commits      []Commit `json:"commits"`
	Files        []File   `json:"files"`
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
}

func (r *Repository) ancestor(ctx context.Context, a, b string) (bool, error) {
	// rev-list produces an empty result exactly when every commit reachable
	// from a is also reachable from b, without treating Git failures as divergence.
	out, err := run(ctx, r.Path, "rev-list", "--max-count=1", a, "--not", b, "--")
	return len(out) == 0, err
}

func (r *Repository) Compare(ctx context.Context, baseRef, headRef string) (*Comparison, error) {
	base, err := r.resolveBase(ctx, baseRef)
	if err != nil {
		return nil, err
	}
	head, err := r.Resolve(ctx, headRef)
	if err != nil {
		return nil, err
	}
	c := &Comparison{Repository: r.Path, Name: filepath.Base(r.Path), Base: base, Head: head, Relationship: "diverged", Commits: []Commit{}}
	older, newer := base, head
	if base == head {
		c.Relationship = "equal"
	} else if baseRef != ":empty" {
		forward, err := r.ancestor(ctx, base, head)
		if err != nil {
			return nil, err
		}
		if forward {
			c.Relationship = "forward"
		} else {
			reverse, err := r.ancestor(ctx, head, base)
			if err != nil {
				return nil, err
			}
			if reverse {
				c.Relationship = "reverse"
				older, newer = head, base
			}
		}
	}
	if c.Relationship == "forward" || c.Relationship == "reverse" {
		// Include side-branch commits brought in by merges, in parent-before-child order.
		out, err := run(ctx, r.Path, "log", "--reverse", "--topo-order", "--format=%H%x00%P%x00%s%x00%an%x00%aI%x00", older+".."+newer, "--")
		if err != nil {
			return nil, err
		}
		parts := strings.Split(string(out), "\x00")
		for i := 0; i+4 < len(parts); i += 5 {
			c.Commits = append(c.Commits, Commit{strings.TrimSpace(parts[i]), strings.Fields(parts[i+1]), parts[i+2], parts[i+3], parts[i+4]})
		}
	}
	c.Files, err = r.Files(ctx, base, head)
	if err != nil {
		return nil, err
	}
	for _, f := range c.Files {
		c.Additions += f.Additions
		c.Deletions += f.Deletions
	}
	if baseRef == ":empty" {
		c.Base = ":empty"
	}
	return c, nil
}

func (r *Repository) Files(ctx context.Context, base, head string) ([]File, error) {
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--ignore-submodules=none", "--find-renames", "--raw", "--no-abbrev", "-z", base, head, "--"}
	out, err := run(ctx, r.Path, args...)
	if err != nil {
		return nil, err
	}
	files := []File{}
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens) && tokens[i] != ""; {
		fields := strings.Fields(strings.TrimPrefix(tokens[i], ":"))
		if len(fields) != 5 || i+1 >= len(tokens) {
			return nil, errors.New("Unexpected Git raw diff format")
		}
		f := File{OldMode: fields[0], NewMode: fields[1], OldOID: fields[2], NewOID: fields[3], Status: fields[4][:1], Path: tokens[i+1], OldPath: tokens[i+1]}
		i += 2
		if f.Status == "R" || f.Status == "C" {
			if i >= len(tokens) {
				return nil, errors.New("Incomplete rename record")
			}
			f.Path = tokens[i]
			i++
		}
		files = append(files, f)
	}
	args = []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--ignore-submodules=none", "--find-renames", "--numstat", "-z", base, head, "--"}
	out, err = run(ctx, r.Path, args...)
	if err != nil {
		return nil, err
	}
	byPath := map[string]int{}
	for i, f := range files {
		byPath[f.Path] = i
	}
	tokens = strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens) && tokens[i] != ""; {
		fields := strings.SplitN(tokens[i], "\t", 3)
		i++
		if len(fields) != 3 {
			return nil, errors.New("Unexpected Git numstat format")
		}
		path := fields[2]
		if path == "" {
			if i+1 >= len(tokens) {
				return nil, errors.New("Incomplete rename stats")
			}
			path = tokens[i+1]
			i += 2
		}
		if index, ok := byPath[path]; ok {
			files[index].Binary = fields[0] == "-"
			files[index].Additions, _ = strconv.Atoi(fields[0])
			files[index].Deletions, _ = strconv.Atoi(fields[1])
		}
	}
	return files, nil
}

type Patch struct {
	Patch    string `json:"patch"`
	Binary   bool   `json:"binary"`
	TooLarge bool   `json:"tooLarge"`
	Message  string `json:"message,omitempty"`
}

func (r *Repository) Patch(ctx context.Context, baseRef, headRef, path string, contextLines int, ignoreWhitespace bool) (*Patch, error) {
	base, err := r.resolveBase(ctx, baseRef)
	if err != nil {
		return nil, err
	}
	head, err := r.Resolve(ctx, headRef)
	if err != nil {
		return nil, err
	}
	files, err := r.Files(ctx, base, head)
	if err != nil {
		return nil, err
	}
	var file *File
	for i := range files {
		if files[i].Path == path {
			file = &files[i]
			break
		}
	}
	if file == nil {
		return nil, errors.New("File is not part of this comparison")
	}
	if file.Binary {
		return &Patch{Binary: true, Message: "Binary file changed. No text preview available."}, nil
	}
	if file.OldMode == "160000" || file.NewMode == "160000" {
		return &Patch{Message: fmt.Sprintf("Submodule changed from %s to %s", file.OldOID, file.NewOID)}, nil
	}
	for _, oid := range []string{file.OldOID, file.NewOID} {
		if strings.Trim(oid, "0") == "" {
			continue
		}
		size, err := run(ctx, r.Path, "cat-file", "-s", oid)
		if err != nil {
			return nil, err
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(size)))
		if n > 2<<20 {
			return &Patch{TooLarge: true, Message: "This file exceeds the 2 MB text preview limit."}, nil
		}
	}
	if file.Status == "A" || file.Status == "D" {
		oid, sign := file.NewOID, "+"
		if file.Status == "D" {
			oid, sign = file.OldOID, "-"
		}
		content, err := run(ctx, r.Path, "cat-file", "blob", oid)
		if err != nil {
			return nil, err
		}
		if len(content) == 0 {
			return &Patch{Message: "Empty file."}, nil
		}
		lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
		if len(lines) > 10000 {
			return &Patch{TooLarge: true, Message: "This patch exceeds the 10,000 line preview limit."}, nil
		}
		var patch strings.Builder
		if sign == "+" {
			fmt.Fprintf(&patch, "@@ -0,0 +1,%d @@\n", len(lines))
		} else {
			fmt.Fprintf(&patch, "@@ -1,%d +0,0 @@\n", len(lines))
		}
		for _, line := range lines {
			patch.WriteString(sign + line + "\n")
		}
		if content[len(content)-1] != '\n' {
			patch.WriteString("\\ No newline at end of file\n")
		}
		return &Patch{Patch: patch.String()}, nil
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--diff-algorithm=myers", "--unified=" + strconv.Itoa(contextLines)}
	if ignoreWhitespace {
		args = append(args, "--ignore-all-space", "--ignore-blank-lines")
	}
	args = append(args, file.OldOID, file.NewOID, "--")
	out, err := run(ctx, r.Path, args...)
	if err != nil {
		return nil, err
	}
	if len(out) > 4<<20 || bytes.Count(out, []byte("\n")) > 10000 {
		return &Patch{TooLarge: true, Message: "This patch exceeds the 4 MB or 10,000 line preview limit."}, nil
	}
	if bytes.Contains(out, []byte("\nBinary files ")) {
		return &Patch{Binary: true, Message: "Binary file changed. No text preview available."}, nil
	}
	return &Patch{Patch: string(out)}, nil
}
