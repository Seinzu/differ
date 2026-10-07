package server

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxListing = 2000

type DirEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Repository bool   `json:"repository"`
}

// Listing is one directory in the repository chooser. Only directory names
// are listed, never file contents.
type Listing struct {
	Path       string     `json:"path"`
	Parent     string     `json:"parent"`
	Home       string     `json:"home"`
	Repository bool       `json:"repository"`
	Entries    []DirEntry `json:"entries"`
	Truncated  bool       `json:"truncated"`
}

// isRepository recognizes working copies and worktrees (a .git folder or
// file) and bare repositories (HEAD beside objects and refs folders).
func isRepository(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	for _, name := range []string{"objects", "refs"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || !info.IsDir() {
			return false
		}
	}
	info, err := os.Stat(filepath.Join(dir, "HEAD"))
	return err == nil && info.Mode().IsRegular()
}

func browse(path string) (*Listing, error) {
	home, _ := os.UserHomeDir()
	switch {
	case path == "":
		path = home
	case path == "~" || strings.HasPrefix(path, "~/"):
		path = filepath.Join(home, path[1:])
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("Enter an absolute folder path")
	}
	path = filepath.Clean(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, errors.New("Cannot open folder " + path)
	}
	listing := &Listing{Path: path, Home: home, Repository: isRepository(path), Entries: []DirEntry{}}
	if parent := filepath.Dir(path); parent != path {
		listing.Parent = parent
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(path, name)
		if !entry.IsDir() {
			// Follow symlinks to directories; skip everything else.
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			if info, err := os.Stat(full); err != nil || !info.IsDir() {
				continue
			}
		}
		if len(listing.Entries) == maxListing {
			listing.Truncated = true
			break
		}
		listing.Entries = append(listing.Entries, DirEntry{Name: name, Path: full, Repository: isRepository(full)})
	}
	sort.Slice(listing.Entries, func(i, j int) bool {
		return strings.ToLower(listing.Entries[i].Name) < strings.ToLower(listing.Entries[j].Name)
	})
	return listing, nil
}
