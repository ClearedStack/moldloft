// Package spool stages finished worker outcomes in /var/spool/cs before they
// are reported to Muster so transient Muster restarts or timeouts never lose a
// completed build.
package spool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ClearedStack/moldloft/internal/errs"
)

// Action names which Muster call a spooled entry represents.
type Action string

const (
	ActionComplete     Action = "complete"
	ActionFail         Action = "fail"
	ActionNeedsSubtool Action = "needs_subtool"
)

// Entry is one outcome staged on disk until Muster acknowledges it.
type Entry struct {
	File   string          `json:"-"`
	Action Action          `json:"action"`
	Lease  string          `json:"lease"`
	ItemID string          `json:"item_id"`
	State  string          `json:"state,omitempty"`
	Code   errs.Code       `json:"code,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Store manages the local spool directory.
type Store struct {
	mu  sync.Mutex
	dir string
	seq uint64
}

// Open prepares dir as a spool directory, removing any unfinished .tmp files
// left by an interrupted write and scanning existing entries to set the next
// sequence number.
func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("the spool directory path is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errors.New("the spool directory could not be prepared")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.New("the spool directory could not be read")
	}
	var maxSeq uint64
	for _, d := range entries {
		name := d.Name()
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		if strings.HasSuffix(name, ".json") {
			var n uint64
			if _, err := fmt.Sscanf(name, "%d-", &n); err == nil && n > maxSeq {
				maxSeq = n
			}
		}
	}
	return &Store{dir: dir, seq: maxSeq}, nil
}

// Enqueue writes e atomically into the spool directory and returns the stored
// Entry with File populated.
func (s *Store) Enqueue(e Entry) (Entry, error) {
	if e.Lease == "" || e.ItemID == "" {
		return Entry{}, errors.New("spool entry requires lease and item_id")
	}
	switch e.Action {
	case ActionComplete:
		if e.State == "" {
			return Entry{}, errors.New("complete spool entry requires state")
		}
	case ActionFail:
		if e.Code != errs.Parse && e.Code != errs.Timeout {
			return Entry{}, errors.New("fail spool entry requires E_PARSE or E_TIMEOUT")
		}
	case ActionNeedsSubtool:
	default:
		return Entry{}, errors.New("unknown spool action")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	name := fmt.Sprintf("%012d-%s.json", s.seq, sanitizeName(e.ItemID))
	finalPath := filepath.Join(s.dir, name)
	tmpPath := finalPath + ".tmp"

	data, err := json.Marshal(e)
	if err != nil {
		return Entry{}, errors.New("spool entry could not be encoded")
	}
	data = append(data, '\n')

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Entry{}, errors.New("spool temporary file could not be created")
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return Entry{}, errors.New("spool temporary file could not be written")
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return Entry{}, errors.New("spool temporary file could not be synced")
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return Entry{}, errors.New("spool temporary file could not be closed")
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return Entry{}, errors.New("spool entry could not be committed")
	}
	e.File = name
	return e, nil
}

// Pending returns every spooled entry in FIFO order. A corrupt file in the
// spool directory is removed so it cannot block subsequent items forever.
func (s *Store) Pending() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, errors.New("the spool directory could not be read")
	}
	var names []string
	for _, d := range dirEntries {
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			names = append(names, d.Name())
		}
	}
	sort.Strings(names)

	out := make([]Entry, 0, len(names))
	for _, name := range names {
		full := filepath.Join(s.dir, name)
		raw, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		var e Entry
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil || e.Lease == "" || e.Action == "" {
			_ = os.Remove(full)
			continue
		}
		e.File = name
		out = append(out, e)
	}
	return out, nil
}

// Remove deletes a delivered or permanently refused entry from the spool.
func (s *Store) Remove(file string) error {
	if file == "" || strings.ContainsAny(file, `/\`) {
		return errors.New("invalid spool file name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(filepath.Join(s.dir, file))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("spool entry could not be removed")
	}
	return nil
}

func sanitizeName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < 32; i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "item"
	}
	return b.String()
}
