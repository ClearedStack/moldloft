// Package repotree holds a generated repository in memory and writes it out.
//
// A tree is built completely before anything touches the disk, so a failed
// build leaves nothing behind, and the bytes written depend only on the tree.
package repotree

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Tree maps a slash-separated relative path to the file's contents.
type Tree map[string][]byte

// Add puts a file in the tree. A path that is absolute, unclean or already
// present is a programming error in a stack template, so Add panics.
func (t Tree) Add(name string, data []byte) {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "..") || strings.Contains(name, "\\") {
		panic(fmt.Sprintf("repotree: bad path %q", name))
	}
	if _, dup := t[name]; dup {
		panic(fmt.Sprintf("repotree: path %q added twice", name))
	}
	t[name] = data
}

// Paths returns every path in byte order.
func (t Tree) Paths() []string {
	paths := make([]string, 0, len(t))
	for p := range t {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// SHA256 returns the hex hash of one file.
func (t Tree) SHA256(name string) string {
	sum := sha256.Sum256(t[name])
	return hex.EncodeToString(sum[:])
}

// ErrNotEmpty means the output folder already holds something.
var ErrNotEmpty = errors.New("the output folder already exists and is not empty")

// Write creates dir if needed and writes every file under it: folders with
// mode 0755, files with mode 0644. It refuses a folder that is not empty.
func (t Tree) Write(dir string) error {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return ErrNotEmpty
	}
	for _, name := range t.Paths() {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, t[name], 0o644); err != nil {
			return err
		}
	}
	return nil
}
