package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClearedStack/moldloft/internal/errs"
	"github.com/ClearedStack/moldloft/internal/muster"
	"github.com/ClearedStack/moldloft/internal/plainforge"
	"github.com/ClearedStack/moldloft/internal/repotree"
	"github.com/ClearedStack/moldloft/internal/spool"
	"github.com/ClearedStack/moldloft/internal/worker"
)

func loadFixtureWithItemID(t *testing.T, relPath, itemID string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", relPath))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["item_id"] = itemID
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type failingCommitter struct {
	err error
}

func (f failingCommitter) Commit(context.Context, string, string, repotree.Tree) (plainforge.Receipt, error) {
	return plainforge.Receipt{}, f.err
}

func TestPriorityLanesAllFourTemplatesAndRebuild(t *testing.T) {
	ms := muster.NewStandIn()
	msSrv := httptest.NewServer(ms)
	defer msSrv.Close()

	pf := plainforge.NewStandIn()
	pfSrv := httptest.NewServer(pf)
	defer pfSrv.Close()

	// Seed four items across backfill, normal and urgent lanes covering all
	// four stack templates.
	calcSpec := loadFixtureWithItemID(t, "stack-templates/calculator/fixtures/golden/sample-rate-buildup/spec.json", "PA-0001")
	checkSpec := loadFixtureWithItemID(t, "stack-templates/checklist/fixtures/golden/sample-solicitation-review/spec.json", "PA-0002")
	validSpec := loadFixtureWithItemID(t, "stack-templates/validator/fixtures/golden/sample-line-code-check/spec.json", "PA-0003")
	treeSpec := loadFixtureWithItemID(t, "stack-templates/decision-tree/fixtures/golden/sample-clause-routing/spec.json", "PA-0004")

	ms.Seed(muster.SeedItem{
		ID:      "PA-0001",
		State:   muster.StateApproved,
		Lane:    muster.LaneBackfill,
		Next:    "moldloft",
		Found:   &muster.FoundView{Source: "sam.gov", Title: "Untrusted Raw Title", Address: "https://sam.gov/opp/1"},
		Results: map[string]json.RawMessage{"sextant": json.RawMessage(`{"raw_html":"<p>never-touch</p>"}`), "waybill": calcSpec},
	})
	ms.Seed(muster.SeedItem{
		ID:      "PA-0002",
		State:   muster.StateApproved,
		Lane:    muster.LaneNormal,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": json.RawMessage(`{"slug":"sample-solicitation-review","spec":` + string(checkSpec) + `}`)},
	})
	ms.Seed(muster.SeedItem{
		ID:      "PA-0003",
		State:   muster.StateApproved,
		Lane:    muster.LaneUrgent,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": validSpec},
	})
	ms.Seed(muster.SeedItem{
		ID:      "PA-0004",
		State:   muster.StateBuilding,
		Lane:    muster.LaneUrgent,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": treeSpec},
	})

	mc, err := muster.NewHTTPClient(msSrv.URL, muster.StaticTokenSource("moldloft"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := plainforge.NewHTTPCommitter(pfSrv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := spool.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	w, err := worker.New(worker.Config{
		Muster:            mc,
		Committer:         pc,
		Spool:             sp,
		PollInterval:      10 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
		BackoffMin:        10 * time.Millisecond,
		BackoffMax:        50 * time.Millisecond,
		Log:               &logBuf,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	handled, err := w.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if handled != 4 {
		t.Fatalf("handled %d items, want 4", handled)
	}

	// Verify log lines ordered urgent (PA-0003, PA-0004) -> normal (PA-0002) -> backfill (PA-0001),
	// and never contain URLs, paths or untrusted titles.
	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	wantOrder := []string{"PA-0003", "PA-0004", "PA-0002", "PA-0001"}
	if len(lines) != len(wantOrder) {
		t.Fatalf("got %d log lines (%q), want %d", len(lines), logBuf.String(), len(wantOrder))
	}
	for i, wantID := range wantOrder {
		if !strings.Contains(lines[i], "item="+wantID) {
			t.Errorf("line %d = %q, expected item=%s", i, lines[i], wantID)
		}
	}
	if strings.Contains(logBuf.String(), "sam.gov") || strings.Contains(logBuf.String(), "Untrusted") {
		t.Errorf("worker logs leaked observation strings: %q", logBuf.String())
	}

	// Check every item moved to building (next: shakedown) with Chartroom-compatible results.moldloft
	// and live commits in Plainforge.
	wantSlugs := map[string]string{
		"PA-0001": "sample-rate-buildup",
		"PA-0002": "sample-solicitation-review",
		"PA-0003": "sample-line-code-check",
		"PA-0004": "sample-clause-routing",
	}
	for id, slug := range wantSlugs {
		state, held, next, results, ok := ms.ItemSnapshot(id)
		if !ok || state != muster.StateBuilding || held || next != "shakedown" {
			t.Fatalf("%s: state=%q held=%v next=%q ok=%v", id, state, held, next, ok)
		}
		rawRes := results["moldloft"]
		var topKeys map[string]json.RawMessage
		if err := json.Unmarshal(rawRes, &topKeys); err != nil {
			t.Fatalf("%s: unmarshal results.moldloft: %v", id, err)
		}
		if len(topKeys) != 2 || topKeys["publish"] == nil || topKeys["build"] == nil {
			t.Errorf("%s: unexpected top-level keys in results.moldloft: %s", id, rawRes)
		}
		var payload worker.ResultPayload
		if err := json.Unmarshal(rawRes, &payload); err != nil {
			t.Fatalf("%s: decode ResultPayload: %v", id, err)
		}
		if payload.Publish.Repository != "https://github.com/clearedstack/"+slug {
			t.Errorf("%s: publish.repository = %q", id, payload.Publish.Repository)
		}
		if !payload.Build.Committed || payload.Build.CommitSHA1 == "" || payload.Build.CommitSHA1 != pf.Head(slug) {
			t.Errorf("%s: build commit %q != plainforge head %q", id, payload.Build.CommitSHA1, pf.Head(slug))
		}
	}

	// Re-running with the identical spec is idempotent in Plainforge, while a
	// revised specification fast-forwards refs/heads/main to a new commit.
	firstHead := pf.Head("sample-rate-buildup")
	ms.Seed(muster.SeedItem{
		ID:      "PA-0001",
		State:   muster.StateBuilding,
		Lane:    muster.LaneUrgent,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": calcSpec},
	})
	if n, err := w.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("idempotent rebuild Drain: n=%d err=%v", n, err)
	}
	if got := pf.Head("sample-rate-buildup"); got != firstHead {
		t.Errorf("expected idempotent retry to preserve %s, got %s", firstHead, got)
	}

	revisedSpec := bytes.Replace(calcSpec, []byte(`"Sample hourly rate build-up"`), []byte(`"Sample hourly rate build-up revised"`), 1)
	ms.Seed(muster.SeedItem{
		ID:      "PA-0001",
		State:   muster.StateBuilding,
		Lane:    muster.LaneUrgent,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": revisedSpec},
	})
	if n, err := w.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("revised rebuild Drain: n=%d err=%v", n, err)
	}
	secondHead := pf.Head("sample-rate-buildup")
	if secondHead == "" || secondHead == firstHead {
		t.Errorf("expected revised rebuild commit parented on %s, got %s", firstHead, secondHead)
	}
}

func TestParseErrorNeedsSubtoolAndRebuildPauseRefusal(t *testing.T) {
	ms := muster.NewStandIn()
	msSrv := httptest.NewServer(ms)
	defer msSrv.Close()

	// PA-0010: forbidden raw observation field inside results.waybill -> E_PARSE (held + incident).
	ms.Seed(muster.SeedItem{
		ID:    "PA-0010",
		State: muster.StateApproved,
		Lane:  muster.LaneNormal,
		Next:  "moldloft",
		Results: map[string]json.RawMessage{
			"waybill": json.RawMessage(`{"contract_version":"v1","raw_html":"<p>contaminated</p>"}`),
		},
	})
	// PA-0011: unbuilt pattern on first build (state: approved) -> E_NEEDS_SUBTOOL (state: needs_subtool + incident).
	unbuiltSpec := loadFixtureWithItemID(t, "stack-templates/calculator/fixtures/golden/sample-rate-buildup/spec.json", "PA-0011")
	unbuiltSpec = bytes.Replace(unbuiltSpec, []byte(`"pattern":"calculator"`), []byte(`"pattern":"custom-wizard"`), 1)
	ms.Seed(muster.SeedItem{
		ID:      "PA-0011",
		State:   muster.StateApproved,
		Lane:    muster.LaneNormal,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": unbuiltSpec},
	})
	// PA-0012: unbuilt pattern on rebuild (state: building) -> Muster refuses needs_subtool with 409 move_not_allowed,
	// and worker drops the refused spool entry cleanly instead of looping.
	rebuildPauseSpec := bytes.Replace(unbuiltSpec, []byte(`"PA-0011"`), []byte(`"PA-0012"`), 1)
	ms.Seed(muster.SeedItem{
		ID:      "PA-0012",
		State:   muster.StateBuilding,
		Lane:    muster.LaneNormal,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": rebuildPauseSpec},
	})

	mc, _ := muster.NewHTTPClient(msSrv.URL, muster.StaticTokenSource("moldloft"), 5*time.Second)
	sp, _ := spool.Open(t.TempDir())
	var logBuf bytes.Buffer
	w, err := worker.New(worker.Config{
		Muster: mc,
		Spool:  sp,
		Log:    &logBuf,
	})
	if err != nil {
		t.Fatal(err)
	}

	if n, err := w.Drain(context.Background()); err != nil || n != 3 {
		t.Fatalf("Drain: n=%d err=%v", n, err)
	}
	if rem, _ := sp.Pending(); len(rem) != 0 {
		t.Fatalf("spool should be empty after Drain, got %d entries", len(rem))
	}
	if strings.Contains(logBuf.String(), "contaminated") {
		t.Errorf("log leaked forbidden field content: %q", logBuf.String())
	}

	state10, held10, _, _, _ := ms.ItemSnapshot("PA-0010")
	if state10 != muster.StateApproved || !held10 {
		t.Errorf("PA-0010: state=%q held=%v, want approved and held=true", state10, held10)
	}
	state11, _, _, _, _ := ms.ItemSnapshot("PA-0011")
	if state11 != muster.StateNeedsSubtool {
		t.Errorf("PA-0011: state=%q, want needs_subtool", state11)
	}

	// Verify Bellbuoy receives the two incidents (PA-0010 E_PARSE, PA-0011 E_NEEDS_SUBTOOL).
	bellbuoy, _ := muster.NewHTTPClient(msSrv.URL, muster.StaticTokenSource("bellbuoy"), 5*time.Second)
	inc1, err := bellbuoy.Lease(context.Background())
	if err != nil || inc1 == nil || inc1.Kind != muster.KindIncident || inc1.Incident.ID != "PA-0010" || inc1.Incident.Code != "E_PARSE" {
		t.Fatalf("incident 1: %+v err=%v", inc1, err)
	}
	inc2, err := bellbuoy.Lease(context.Background())
	if err != nil || inc2 == nil || inc2.Kind != muster.KindIncident || inc2.Incident.ID != "PA-0011" || inc2.Incident.Code != "E_NEEDS_SUBTOOL" {
		t.Fatalf("incident 2: %+v err=%v", inc2, err)
	}
}

func TestSpoolBuffersOutcomeAcrossMusterOutageAndWorkerRestart(t *testing.T) {
	ms := muster.NewStandIn()
	msSrv := httptest.NewServer(ms)
	defer msSrv.Close()

	spec := loadFixtureWithItemID(t, "stack-templates/calculator/fixtures/golden/sample-order-amount/spec.json", "PA-0020")
	ms.Seed(muster.SeedItem{
		ID:      "PA-0020",
		State:   muster.StateApproved,
		Lane:    muster.LaneUrgent,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": spec},
	})

	spoolDir := t.TempDir()
	sp1, err := spool.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}

	// Wrap Muster client so Lease succeeds, then Muster becomes store_slow (503) before Complete.
	mc, _ := muster.NewHTTPClient(msSrv.URL, muster.StaticTokenSource("moldloft"), 5*time.Second)
	slowClient := &slowOnCompleteClient{Client: mc, standIn: ms}

	w1, err := worker.New(worker.Config{
		Muster: slowClient,
		Spool:  sp1,
	})
	if err != nil {
		t.Fatal(err)
	}

	worked, err := w1.Step(context.Background())
	if !worked || !muster.IsRetryable(err) {
		t.Fatalf("Step during store_slow: worked=%v err=%v", worked, err)
	}
	pending, _ := sp1.Pending()
	if len(pending) != 1 || pending[0].Action != spool.ActionComplete {
		t.Fatalf("expected 1 pending complete in spool, got %+v", pending)
	}

	// Recover Muster and start a fresh Worker on the same spool directory.
	ms.SetCondition("")
	sp2, err := spool.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := worker.New(worker.Config{
		Muster: mc,
		Spool:  sp2,
	})
	if err != nil {
		t.Fatal(err)
	}
	worked, err = w2.Step(context.Background())
	if err != nil || !worked {
		t.Fatalf("Step after recovery: worked=%v err=%v", worked, err)
	}
	if rem, _ := sp2.Pending(); len(rem) != 0 {
		t.Fatalf("expected empty spool after recovery, got %d", len(rem))
	}
	state, held, next, _, ok := ms.ItemSnapshot("PA-0020")
	if !ok || state != muster.StateBuilding || held || next != "shakedown" {
		t.Fatalf("PA-0020 after spool flush: state=%q held=%v next=%q", state, held, next)
	}
}

func TestPlainforgeTimeoutReportsETimeoutWithoutHoldingItem(t *testing.T) {
	ms := muster.NewStandIn()
	msSrv := httptest.NewServer(ms)
	defer msSrv.Close()

	spec := loadFixtureWithItemID(t, "stack-templates/calculator/fixtures/golden/sample-order-amount/spec.json", "PA-0030")
	ms.Seed(muster.SeedItem{
		ID:      "PA-0030",
		State:   muster.StateApproved,
		Lane:    muster.LaneNormal,
		Next:    "moldloft",
		Results: map[string]json.RawMessage{"waybill": spec},
	})

	mc, _ := muster.NewHTTPClient(msSrv.URL, muster.StaticTokenSource("moldloft"), 5*time.Second)
	sp, _ := spool.Open(t.TempDir())
	w, err := worker.New(worker.Config{
		Muster:    mc,
		Committer: failingCommitter{err: errs.Timeoutf("plainforge", "connection timed out")},
		Spool:     sp,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	state, held, next, _, ok := ms.ItemSnapshot("PA-0030")
	if !ok || state != muster.StateApproved || held || next != "moldloft" {
		t.Fatalf("PA-0030 after E_TIMEOUT: state=%q held=%v next=%q", state, held, next)
	}
}

type slowOnCompleteClient struct {
	muster.Client
	standIn *muster.StandIn
	tripped bool
}

func (s *slowOnCompleteClient) Complete(ctx context.Context, lease, state string, result json.RawMessage) (muster.CompleteAck, error) {
	if !s.tripped {
		s.tripped = true
		s.standIn.SetCondition("logbook takes too long")
	}
	return s.Client.Complete(ctx, lease, state, result)
}
