// Package plainforge builds deterministic Git objects and packfiles from a
// generated repository tree and commits them to Plainforge over Git's smart
// HTTP protocol (or to an in-memory stand-in).
package plainforge

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/ClearedStack/moldloft/internal/repotree"
)

// ZeroHash is Git's all-zero object name for an unborn ref.
const ZeroHash = "0000000000000000000000000000000000000000"

const (
	objCommit = 1
	objTree   = 2
	objBlob   = 3
)

// Receipt records the deterministic Git hashes of a generated repository and
// whether it was pushed to a live Plainforge endpoint.
type Receipt struct {
	TreeSHA1   string
	CommitSHA1 string
	Committed  bool
}

// Committer routes a generated repository to Plainforge.
type Committer interface {
	Commit(ctx context.Context, slug, specSHA256 string, tree repotree.Tree) (Receipt, error)
}

// DryCommitter computes deterministic Git blob, tree and commit hashes in
// memory without opening a network connection. It is used when PLAINFORGE_URL
// is unset under SPEC.md's Muster-only NetworkPolicy.
type DryCommitter struct{}

// Commit computes the deterministic tree and initial commit hashes in memory.
func (DryCommitter) Commit(_ context.Context, slug, specSHA256 string, tree repotree.Tree) (Receipt, error) {
	bundle := BuildBundle(slug, specSHA256, "", tree)
	return Receipt{
		TreeSHA1:   bundle.TreeSHA1,
		CommitSHA1: bundle.CommitSHA1,
		Committed:  false,
	}, nil
}

// PackObject is one Git object ready to be written into a packfile.
type PackObject struct {
	Type int
	SHA1 [20]byte
	Data []byte
}

// Bundle holds all Git objects and the packv2 stream for one commit.
type Bundle struct {
	TreeSHA1   string
	CommitSHA1 string
	Objects    []PackObject
	Pack       []byte
}

type dirNode struct {
	files map[string][]byte
	dirs  map[string]*dirNode
}

func newDirNode() *dirNode {
	return &dirNode{
		files: map[string][]byte{},
		dirs:  map[string]*dirNode{},
	}
}

// BuildBundle constructs every Git blob, tree and commit object for tree and
// encodes them into a deterministic Git packfile v2. When parentHex is empty or
// ZeroHash, an initial root commit is produced; otherwise parentHex is recorded
// as the commit's parent so the update is a fast-forward.
func BuildBundle(slug, specSHA256, parentHex string, tree repotree.Tree) Bundle {
	root := newDirNode()
	for _, p := range tree.Paths() {
		parts := strings.Split(p, "/")
		cur := root
		for _, dir := range parts[:len(parts)-1] {
			next, ok := cur.dirs[dir]
			if !ok {
				next = newDirNode()
				cur.dirs[dir] = next
			}
			cur = next
		}
		cur.files[parts[len(parts)-1]] = tree[p]
	}

	seen := map[[20]byte]bool{}
	var objects []PackObject
	addObj := func(objType int, payload []byte) [20]byte {
		sum := hashGitObject(objType, payload)
		if !seen[sum] {
			seen[sum] = true
			cp := append([]byte(nil), payload...)
			objects = append(objects, PackObject{Type: objType, SHA1: sum, Data: cp})
		}
		return sum
	}

	rootSum := buildTreeObject(root, addObj)
	treeHex := hex.EncodeToString(rootSum[:])

	shortSpec := specSHA256
	if len(shortSpec) > 12 {
		shortSpec = shortSpec[:12]
	}
	var commitBuf bytes.Buffer
	fmt.Fprintf(&commitBuf, "tree %s\n", treeHex)
	if parentHex != "" && parentHex != ZeroHash {
		fmt.Fprintf(&commitBuf, "parent %s\n", parentHex)
	}
	const ident = "Moldloft <moldloft@clearedstack.invalid> 0 +0000"
	fmt.Fprintf(&commitBuf, "author %s\n", ident)
	fmt.Fprintf(&commitBuf, "committer %s\n\n", ident)
	fmt.Fprintf(&commitBuf, "Generate %s (%s)\n", slug, shortSpec)

	commitSum := addObj(objCommit, commitBuf.Bytes())
	commitHex := hex.EncodeToString(commitSum[:])

	return Bundle{
		TreeSHA1:   treeHex,
		CommitSHA1: commitHex,
		Objects:    objects,
		Pack:       encodePackV2(objects),
	}
}

type treeEntry struct {
	mode  string // "100644" or "40000"
	name  string
	isDir bool
	sha1  [20]byte
}

func buildTreeObject(dir *dirNode, addObj func(int, []byte) [20]byte) [20]byte {
	var entries []treeEntry
	fileNames := make([]string, 0, len(dir.files))
	for name := range dir.files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		sum := addObj(objBlob, dir.files[name])
		entries = append(entries, treeEntry{
			mode:  "100644",
			name:  name,
			isDir: false,
			sha1:  sum,
		})
	}
	dirNames := make([]string, 0, len(dir.dirs))
	for name := range dir.dirs {
		dirNames = append(dirNames, name)
	}
	sort.Strings(dirNames)
	for _, name := range dirNames {
		sum := buildTreeObject(dir.dirs[name], addObj)
		entries = append(entries, treeEntry{
			mode:  "40000",
			name:  name,
			isDir: true,
			sha1:  sum,
		})
	}
	// Git sorts tree entries as if directory names had a trailing '/'.
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i].name, entries[j].name
		if entries[i].isDir {
			a += "/"
		}
		if entries[j].isDir {
			b += "/"
		}
		return a < b
	})

	var buf bytes.Buffer
	for _, e := range entries {
		buf.WriteString(e.mode)
		buf.WriteByte(' ')
		buf.WriteString(e.name)
		buf.WriteByte(0)
		buf.Write(e.sha1[:])
	}
	return addObj(objTree, buf.Bytes())
}

func hashGitObject(objType int, payload []byte) [20]byte {
	var kind string
	switch objType {
	case objCommit:
		kind = "commit"
	case objTree:
		kind = "tree"
	case objBlob:
		kind = "blob"
	default:
		panic("plainforge: unknown git object type")
	}
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", kind, len(payload))
	h.Write(payload)
	var out [20]byte
	copy(out[:], h.Sum(nil))
	return out
}

func encodePackV2(objects []PackObject) []byte {
	var buf bytes.Buffer
	buf.WriteString("PACK")
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], 2)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(objects)))
	buf.Write(hdr[:])

	for _, obj := range objects {
		size := len(obj.Data)
		first := byte((obj.Type << 4) | (size & 0x0f))
		rem := size >> 4
		if rem > 0 {
			first |= 0x80
		}
		buf.WriteByte(first)
		for rem > 0 {
			b := byte(rem & 0x7f)
			rem >>= 7
			if rem > 0 {
				b |= 0x80
			}
			buf.WriteByte(b)
		}
		zw, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
		if err != nil {
			panic(err)
		}
		if _, err := zw.Write(obj.Data); err != nil {
			panic(err)
		}
		if err := zw.Close(); err != nil {
			panic(err)
		}
	}

	sum := sha1.Sum(buf.Bytes())
	buf.Write(sum[:])
	return buf.Bytes()
}
