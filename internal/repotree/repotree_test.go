package repotree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTreePathsHashAndWrite(t *testing.T) {
	tree := Tree{}
	tree.Add("b/file.txt", []byte("beta\n"))
	tree.Add("a.txt", []byte("alpha\n"))

	paths := tree.Paths()
	if len(paths) != 2 || paths[0] != "a.txt" || paths[1] != "b/file.txt" {
		t.Fatalf("Paths() = %v", paths)
	}
	if len(tree.SHA256("a.txt")) != 64 {
		t.Fatalf("unexpected SHA256 length: %q", tree.SHA256("a.txt"))
	}

	dir := filepath.Join(t.TempDir(), "out")
	if err := tree.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tree.Write(dir); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("second Write error = %v, want ErrNotEmpty", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "b", "file.txt"))
	if err != nil || string(got) != "beta\n" {
		t.Fatalf("read back %q, err %v", got, err)
	}
}
