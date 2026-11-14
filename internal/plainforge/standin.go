package plainforge

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type memRepo struct {
	main    string
	objects map[string]PackObject
}

// StandIn is an in-memory Git smart HTTP server implementing the receive-pack
// surface of Plainforge for standalone runs and tests.
type StandIn struct {
	mu    sync.Mutex
	repos map[string]*memRepo
}

// NewStandIn returns an empty in-memory Plainforge stand-in.
func NewStandIn() *StandIn {
	return &StandIn{repos: map[string]*memRepo{}}
}

// Head returns the current tip hash of refs/heads/main for slug, or "" if
// slug has not been pushed.
func (s *StandIn) Head(slug string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.repos[slug]; ok {
		return r.main
	}
	return ""
}

// ObjectCount returns how many Git objects are stored for slug.
func (s *StandIn) ObjectCount(slug string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.repos[slug]; ok {
		return len(r.objects)
	}
	return 0
}

func (s *StandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	gitIdx := strings.Index(trimmed, ".git/")
	if gitIdx <= 0 {
		http.Error(w, "E_NEEDS_SUBTOOL: no such repository", http.StatusNotFound)
		return
	}
	slug := trimmed[:gitIdx]
	rest := trimmed[gitIdx+len(".git/"):]

	switch {
	case rest == "info/refs" && r.Method == http.MethodGet:
		if r.URL.Query().Get("service") != "git-receive-pack" {
			http.Error(w, "E_NEEDS_SUBTOOL: unsupported service", http.StatusForbidden)
			return
		}
		s.serveInfoRefs(w, slug)
	case rest == "git-receive-pack" && r.Method == http.MethodPost:
		s.serveReceivePack(w, r, slug)
	default:
		http.Error(w, "E_PARSE: unsupported method or path", http.StatusMethodNotAllowed)
	}
}

func (s *StandIn) serveInfoRefs(w http.ResponseWriter, slug string) {
	s.mu.Lock()
	repo := s.repos[slug]
	mainHash := ZeroHash
	if repo != nil && repo.main != "" {
		mainHash = repo.main
	}
	s.mu.Unlock()

	var buf bytes.Buffer
	writePktLine(&buf, []byte("# service=git-receive-pack\n"))
	buf.WriteString("0000")
	if mainHash == ZeroHash {
		writePktLine(&buf, []byte(ZeroHash+" capabilities^{}\x00report-status\n"))
	} else {
		writePktLine(&buf, []byte(mainHash+" refs/heads/main\x00report-status\n"))
	}
	buf.WriteString("0000")

	w.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func (s *StandIn) serveReceivePack(w http.ResponseWriter, r *http.Request, slug string) {
	const maxPush = 16 << 20
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxPush+1))
	if err != nil || len(raw) > maxPush {
		http.Error(w, "E_PARSE: invalid push body", http.StatusBadRequest)
		return
	}
	oldHash, newHash, refName, packBytes, err := splitReceivePackRequest(raw)
	if err != nil {
		http.Error(w, "E_PARSE: malformed receive-pack command", http.StatusBadRequest)
		return
	}
	objects, err := unpackV2(packBytes)
	if err != nil {
		writeReceiveStatus(w, "unpack E_PARSE", refName, "corrupt pack")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	repo := s.repos[slug]
	if repo == nil {
		repo = &memRepo{main: ZeroHash, objects: map[string]PackObject{}}
	}
	if oldHash != repo.main {
		writeReceiveStatus(w, "unpack ok", refName, "E_NEEDS_SUBTOOL: old ref value does not match")
		return
	}
	merged := map[string]PackObject{}
	for k, v := range repo.objects {
		merged[k] = v
	}
	for _, obj := range objects {
		h := hex.EncodeToString(obj.SHA1[:])
		merged[h] = obj
	}
	commitObj, ok := merged[newHash]
	if !ok || commitObj.Type != objCommit {
		writeReceiveStatus(w, "unpack ok", refName, "E_PARSE: new tip commit missing from pack")
		return
	}
	if oldHash != ZeroHash && !commitHasParent(commitObj.Data, oldHash) {
		writeReceiveStatus(w, "unpack ok", refName, "E_NEEDS_SUBTOOL: the update is not a fast-forward")
		return
	}
	repo.objects = merged
	repo.main = newHash
	s.repos[slug] = repo
	writeReceiveStatus(w, "unpack ok", refName, "ok")
}

func writeReceiveStatus(w http.ResponseWriter, unpackLine, refName, refOutcome string) {
	var buf bytes.Buffer
	writePktLine(&buf, []byte(unpackLine+"\n"))
	if refOutcome == "ok" {
		writePktLine(&buf, []byte("ok "+refName+"\n"))
	} else {
		writePktLine(&buf, []byte(fmt.Sprintf("ng %s %s\n", refName, refOutcome)))
	}
	buf.WriteString("0000")
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func splitReceivePackRequest(raw []byte) (oldHash, newHash, refName string, pack []byte, err error) {
	if len(raw) < 8 {
		return "", "", "", nil, errors.New("short request")
	}
	var ln uint64
	_, err = fmt.Sscanf(string(raw[:4]), "%04x", &ln)
	if err != nil || ln < 5 || int(ln)+4 > len(raw) {
		return "", "", "", nil, errors.New("bad command pkt-line")
	}
	cmdLine := string(raw[4:ln])
	if string(raw[ln:ln+4]) != "0000" {
		return "", "", "", nil, errors.New("expected flush-pkt after single ref update")
	}
	if nul := strings.IndexByte(cmdLine, 0); nul >= 0 {
		cmdLine = cmdLine[:nul]
	}
	cmdLine = strings.TrimSuffix(cmdLine, "\n")
	parts := strings.Split(cmdLine, " ")
	if len(parts) != 3 || !isHex40(parts[0]) || !isHex40(parts[1]) || parts[2] != "refs/heads/main" {
		return "", "", "", nil, errors.New("unsupported ref update")
	}
	return parts[0], parts[1], parts[2], raw[ln+4:], nil
}

func unpackV2(pack []byte) ([]PackObject, error) {
	if len(pack) < 32 || string(pack[:4]) != "PACK" {
		return nil, errors.New("bad pack magic")
	}
	if binary.BigEndian.Uint32(pack[4:8]) != 2 {
		return nil, errors.New("unsupported pack version")
	}
	count := int(binary.BigEndian.Uint32(pack[8:12]))
	wantSum := sha1.Sum(pack[:len(pack)-20])
	if !bytes.Equal(wantSum[:], pack[len(pack)-20:]) {
		return nil, errors.New("pack checksum mismatch")
	}

	r := bytes.NewReader(pack[12 : len(pack)-20])
	out := make([]PackObject, 0, count)
	for i := 0; i < count; i++ {
		first, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		objType := int((first >> 4) & 0x07)
		if objType != objCommit && objType != objTree && objType != objBlob {
			return nil, errors.New("unsupported pack object type")
		}
		size := int(first & 0x0f)
		shift := uint(4)
		cont := (first & 0x80) != 0
		for cont {
			b, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			size |= int(b&0x7f) << shift
			shift += 7
			cont = (b & 0x80) != 0
		}
		zr, err := zlib.NewReader(r)
		if err != nil {
			return nil, err
		}
		payload, err := io.ReadAll(io.LimitReader(zr, int64(size)+1))
		zr.Close()
		if err != nil || len(payload) != size {
			return nil, errors.New("corrupt zlib object stream")
		}
		sum := hashGitObject(objType, payload)
		out = append(out, PackObject{Type: objType, SHA1: sum, Data: payload})
	}
	if r.Len() != 0 {
		return nil, errors.New("trailing bytes inside pack")
	}
	return out, nil
}

func commitHasParent(commitData []byte, wantParent string) bool {
	for _, line := range strings.Split(string(commitData), "\n") {
		if line == "" {
			break
		}
		if line == "parent "+wantParent {
			return true
		}
	}
	return false
}
