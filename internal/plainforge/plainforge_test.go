package plainforge

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ClearedStack/moldloft/internal/errs"
	"github.com/ClearedStack/moldloft/internal/repotree"
)

func sampleTree() repotree.Tree {
	t := repotree.Tree{}
	t.Add("README.md", []byte("# Sample Tool\n"))
	t.Add("a-b", []byte("sorts before directory a/\n"))
	t.Add("a/nested.txt", []byte("nested file\n"))
	t.Add("js/app.js", []byte("console.log('ok');\n"))
	return t
}

func TestBuildBundleIsDeterministicAndPassesGitFsck(t *testing.T) {
	tree := sampleTree()
	b1 := BuildBundle("sample-tool", "abcdef0123456789", "", tree)
	b2 := BuildBundle("sample-tool", "abcdef0123456789", "", tree)
	if b1.TreeSHA1 != b2.TreeSHA1 || b1.CommitSHA1 != b2.CommitSHA1 || !bytes.Equal(b1.Pack, b2.Pack) {
		t.Fatal("BuildBundle is not byte-for-byte deterministic")
	}

	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed; skipping git fsck verification")
	}
	bare := filepath.Join(t.TempDir(), "repo.git")
	if out, err := exec.Command(gitBin, "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	packPath := filepath.Join(t.TempDir(), "test.pack")
	if err := os.WriteFile(packPath, b1.Pack, 0o644); err != nil {
		t.Fatal(err)
	}
	// git index-pack --strict verifies every object, tree sort order, modes and pack SHA-1.
	idxCmd := exec.Command(gitBin, "-C", bare, "index-pack", "--strict", "--stdin")
	idxCmd.Stdin = bytes.NewReader(b1.Pack)
	if out, err := idxCmd.CombinedOutput(); err != nil {
		t.Fatalf("git index-pack --strict failed: %v\n%s", err, out)
	}
	_ = packPath
	if out, err := exec.Command(gitBin, "-C", bare, "update-ref", "refs/heads/main", b1.CommitSHA1).CombinedOutput(); err != nil {
		t.Fatalf("git update-ref: %v\n%s", err, out)
	}
	if out, err := exec.Command(gitBin, "-C", bare, "fsck", "--strict").CombinedOutput(); err != nil {
		t.Fatalf("git fsck --strict failed: %v\n%s", err, out)
	}
}

func TestHTTPCommitterInitialIdempotentAndFastForwardRebuild(t *testing.T) {
	standIn := NewStandIn()
	srv := httptest.NewServer(standIn)
	defer srv.Close()

	client, err := NewHTTPCommitter(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	tree1 := sampleTree()
	r1, err := client.Commit(ctx, "sample-tool", "1111111111111111", tree1)
	if err != nil {
		t.Fatalf("initial Commit: %v", err)
	}
	if !r1.Committed || standIn.Head("sample-tool") != r1.CommitSHA1 {
		t.Fatalf("unexpected receipt %+v, head %q", r1, standIn.Head("sample-tool"))
	}

	// Repeating the exact same initial build is idempotent.
	rDup, err := client.Commit(ctx, "sample-tool", "1111111111111111", tree1)
	if err != nil {
		t.Fatalf("idempotent Commit: %v", err)
	}
	if rDup.CommitSHA1 != r1.CommitSHA1 {
		t.Fatalf("commit hash changed on idempotent retry: %s vs %s", rDup.CommitSHA1, r1.CommitSHA1)
	}

	// A rebuild pushes a fast-forward child commit on refs/heads/main.
	tree2 := sampleTree()
	tree2.Add("extra.txt", []byte("rebuilt\n"))
	r2, err := client.Commit(ctx, "sample-tool", "2222222222222222", tree2)
	if err != nil {
		t.Fatalf("rebuild Commit: %v", err)
	}
	if r2.CommitSHA1 == r1.CommitSHA1 || standIn.Head("sample-tool") != r2.CommitSHA1 {
		t.Fatalf("expected fast-forward rebuild commit, got %+v", r2)
	}
}

func TestHTTPCommitterClassifiesTimeoutAndDryCommitter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client, err := NewHTTPCommitter(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Commit(context.Background(), "sample-tool", "1111", sampleTree()); errs.CodeOf(err) != errs.Timeout {
		t.Fatalf("got %v, want E_TIMEOUT", err)
	}

	dry, err := (DryCommitter{}).Commit(context.Background(), "sample-tool", "1111", sampleTree())
	if err != nil || dry.Committed || len(dry.CommitSHA1) != 40 || len(dry.TreeSHA1) != 40 {
		t.Fatalf("unexpected DryCommitter receipt: %+v, err %v", dry, err)
	}
}
