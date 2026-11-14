package spool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ClearedStack/moldloft/internal/errs"
)

func TestSpoolLifecycleRecoveryAndCleanup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Plant a leftover .tmp file and a corrupt .json file to test recovery.
	if err := os.WriteFile(filepath.Join(dir, "000000000001-PA-0001.json.tmp"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000000000000-corrupt.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "000000000001-PA-0001.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("leftover .tmp file was not cleaned up")
	}

	e1, err := s.Enqueue(Entry{
		Action: ActionComplete,
		Lease:  "PA-0001.5.1",
		ItemID: "PA-0001",
		State:  "building",
		Result: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("Enqueue 1: %v", err)
	}
	e2, err := s.Enqueue(Entry{
		Action: ActionFail,
		Lease:  "PA-0002.6.1",
		ItemID: "PA-0002",
		Code:   errs.Parse,
	})
	if err != nil {
		t.Fatalf("Enqueue 2: %v", err)
	}

	// Re-open the directory as if the worker pod restarted.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	pending, err := s2.Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 2 || pending[0].Lease != e1.Lease || pending[1].Lease != e2.Lease {
		t.Fatalf("unexpected pending entries: %+v", pending)
	}
	if err := s2.Remove(e1.File); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	pending, err = s2.Pending()
	if err != nil || len(pending) != 1 || pending[0].Lease != e2.Lease {
		t.Fatalf("after Remove, pending = %+v, err %v", pending, err)
	}
}
