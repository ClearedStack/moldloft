package muster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ClearedStack/moldloft/internal/errs"
)

type contractExample struct {
	About   string         `json:"about"`
	Given   *exampleGiven  `json:"given,omitempty"`
	Request exampleRequest `json:"request"`
	Expect  exampleExpect  `json:"expect"`
}

type exampleGiven struct {
	Items     []SeedItem       `json:"items,omitempty"`
	Calls     []exampleRequest `json:"calls,omitempty"`
	Wait      string           `json:"wait,omitempty"`
	Condition string           `json:"condition,omitempty"`
}

type exampleRequest struct {
	As          string          `json:"as,omitempty"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	ContentType string          `json:"content_type,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
	Raw         string          `json:"raw,omitempty"`
}

type exampleExpect struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
	Stream string          `json:"stream,omitempty"`
}

func compactJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compactJSON(%s): %v", raw, err)
	}
	return buf.String()
}

// TestReplayContractExamples replays every saved example in
// testdata/contract/v1/examples/ (copied from ClearedStack/muster
// contract/v1/examples/). Every one of the 52 files is exercised:
//   - Worker call examples are replayed through HTTPClient, checking the exact
//     wire request body, headers, method, path and decoded result.
//   - All 28 refusal examples are replayed through HTTPClient across all five
//     worker calls, checking status, public code, cause, retryability and
//     verifying that the human reason string never leaks into Error().
//   - Non-worker calls (items, edits, health, feed, release, healthz) are
//     checked to ensure Moldloft's worker Client surface never exposes calls
//     outside the five worker endpoints.
//   - Every example whose setup uses items/worker calls/release/healthz is also
//     replayed end-to-end against StandIn, comparing status and JSON body byte
//     for byte.
func TestReplayContractExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "contract", "v1", "examples")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	sort.Strings(files)
	if len(files) != 52 {
		t.Fatalf("expected all 52 contract example files, found %d", len(files))
	}

	replayedClientWorker := 0
	replayedClientRefusal := 0
	replayedNonWorker := 0
	replayedStandIn := 0

	for _, file := range files {
		name := filepath.Base(file)
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var ex contractExample
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&ex); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			if ex.About == "" {
				t.Fatalf("%s has empty about", name)
			}

			switch {
			case strings.HasPrefix(name, "refused-"):
				replayRefusalThroughClient(t, name, ex)
				replayedClientRefusal++
			case isWorkerPath(ex.Request.Path):
				replayWorkerCallThroughClient(t, name, ex)
				replayedClientWorker++
			default:
				verifyNonWorkerBoundary(t, name, ex)
				replayedNonWorker++
			}

			if standInSupportsExample(ex) {
				replayAgainstStandIn(t, name, ex)
				replayedStandIn++
			}
		})
	}

	if replayedClientWorker+replayedClientRefusal+replayedNonWorker != len(files) {
		t.Fatalf("not every file was classified: worker=%d refusal=%d nonWorker=%d total=%d",
			replayedClientWorker, replayedClientRefusal, replayedNonWorker, len(files))
	}
	if replayedStandIn < 30 {
		t.Fatalf("expected StandIn to replay at least 30 examples, got %d", replayedStandIn)
	}
}

func isWorkerPath(path string) bool {
	switch path {
	case "/v1/lease", "/v1/heartbeat", "/v1/complete", "/v1/fail", "/v1/needs_subtool":
		return true
	default:
		return false
	}
}

func replayWorkerCallThroughClient(t *testing.T, name string, ex contractExample) {
	t.Helper()
	var gotMethod, gotPath, gotAuth, gotCT string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		if len(ex.Expect.Body) > 0 {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(ex.Expect.Status)
		if len(ex.Expect.Body) > 0 {
			w.Write(ex.Expect.Body)
		}
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, StaticTokenSource("tok-"+ex.Request.As), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	switch ex.Request.Path {
	case "/v1/lease":
		grant, err := client.Lease(ctx)
		if err != nil {
			t.Fatalf("Lease error: %v", err)
		}
		if ex.Expect.Status == http.StatusNoContent {
			if grant != nil {
				t.Fatalf("expected nil grant on 204, got %+v", grant)
			}
		} else {
			gotJSON, _ := json.Marshal(grant)
			if compactJSON(t, gotJSON) != compactJSON(t, ex.Expect.Body) {
				t.Fatalf("grant mismatch:\n got: %s\nwant: %s", compactJSON(t, gotJSON), compactJSON(t, ex.Expect.Body))
			}
		}

	case "/v1/heartbeat":
		var req heartbeatReq
		if err := json.Unmarshal(ex.Request.Body, &req); err != nil {
			t.Fatal(err)
		}
		beat, err := client.Heartbeat(ctx, req.Lease)
		if err != nil {
			t.Fatalf("Heartbeat error: %v", err)
		}
		gotJSON, _ := json.Marshal(beat)
		if compactJSON(t, gotJSON) != compactJSON(t, ex.Expect.Body) {
			t.Fatalf("beat mismatch:\n got: %s\nwant: %s", compactJSON(t, gotJSON), compactJSON(t, ex.Expect.Body))
		}

	case "/v1/complete":
		var req completeReq
		if err := json.Unmarshal(ex.Request.Body, &req); err != nil {
			t.Fatal(err)
		}
		ack, err := client.Complete(ctx, req.Lease, req.State, req.Result)
		if err != nil {
			t.Fatalf("Complete error: %v", err)
		}
		gotJSON, _ := json.Marshal(ack)
		if compactJSON(t, gotJSON) != compactJSON(t, ex.Expect.Body) {
			t.Fatalf("complete ack mismatch:\n got: %s\nwant: %s", compactJSON(t, gotJSON), compactJSON(t, ex.Expect.Body))
		}

	case "/v1/fail":
		var req failReq
		if err := json.Unmarshal(ex.Request.Body, &req); err != nil {
			t.Fatal(err)
		}
		ack, err := client.Fail(ctx, req.Lease, req.Code)
		if err != nil {
			t.Fatalf("Fail error: %v", err)
		}
		gotJSON, _ := json.Marshal(ack)
		if compactJSON(t, gotJSON) != compactJSON(t, ex.Expect.Body) {
			t.Fatalf("fail ack mismatch:\n got: %s\nwant: %s", compactJSON(t, gotJSON), compactJSON(t, ex.Expect.Body))
		}

	case "/v1/needs_subtool":
		var req heartbeatReq
		if err := json.Unmarshal(ex.Request.Body, &req); err != nil {
			t.Fatal(err)
		}
		ack, err := client.NeedsSubtool(ctx, req.Lease)
		if err != nil {
			t.Fatalf("NeedsSubtool error: %v", err)
		}
		gotJSON, _ := json.Marshal(ack)
		if compactJSON(t, gotJSON) != compactJSON(t, ex.Expect.Body) {
			t.Fatalf("pause ack mismatch:\n got: %s\nwant: %s", compactJSON(t, gotJSON), compactJSON(t, ex.Expect.Body))
		}
	}

	if gotMethod != ex.Request.Method || gotPath != ex.Request.Path {
		t.Fatalf("request %s %s, want %s %s", gotMethod, gotPath, ex.Request.Method, ex.Request.Path)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotCT)
	}
	if gotAuth != "Bearer tok-"+ex.Request.As {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if compactJSON(t, gotBody) != compactJSON(t, ex.Request.Body) {
		t.Fatalf("%s request body mismatch:\n got: %s\nwant: %s", name, compactJSON(t, gotBody), compactJSON(t, ex.Request.Body))
	}
}

func replayRefusalThroughClient(t *testing.T, name string, ex contractExample) {
	t.Helper()
	var want wireRefusal
	if err := json.Unmarshal(ex.Expect.Body, &want); err != nil {
		t.Fatalf("unmarshal refusal %s: %v", name, err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ex.Expect.Status)
		w.Write(ex.Expect.Body)
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, StaticTokenSource("tok-moldloft"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	calls := []func() error{
		func() error { _, err := client.Lease(ctx); return err },
		func() error { _, err := client.Heartbeat(ctx, "PA-0001.1.1"); return err },
		func() error {
			_, err := client.Complete(ctx, "PA-0001.1.1", StateBuilding, json.RawMessage(`{"ok":true}`))
			return err
		},
		func() error { _, err := client.Fail(ctx, "PA-0001.1.1", errs.Parse); return err },
		func() error { _, err := client.NeedsSubtool(ctx, "PA-0001.1.1"); return err },
	}
	for i, fn := range calls {
		err := fn()
		if err == nil {
			t.Fatalf("call %d succeeded on refusal %s", i, name)
		}
		var ref *Refusal
		if !errors.As(err, &ref) {
			t.Fatalf("call %d returned %T (%v), want *Refusal", i, err, err)
		}
		if ref.Status != ex.Expect.Status || string(ref.Code) != want.Error || ref.Cause != want.Cause {
			t.Fatalf("call %d got (%d %s %s), want (%d %s %s)",
				i, ref.Status, ref.Code, ref.Cause, ex.Expect.Status, want.Error, want.Cause)
		}
		wantRetry := ex.Expect.Status == 429 || ex.Expect.Status == 503
		if ref.Retryable() != wantRetry || IsRetryable(err) != wantRetry {
			t.Fatalf("call %d Retryable() = %v, want %v", i, ref.Retryable(), wantRetry)
		}
		// Never repeat human reason text unless it happens to equal cause with spaces,
		// and Refusal.Error() uses fixed format "<Code>: muster: refused (<status> <cause>)".
		expectedText := string(ref.Code) + ": muster: refused ("
		if !strings.HasPrefix(err.Error(), expectedText) {
			t.Fatalf("unexpected error text: %q", err.Error())
		}
	}
}

func verifyNonWorkerBoundary(t *testing.T, name string, ex contractExample) {
	t.Helper()
	// Moldloft is a worker in cs-workers; non-worker calls belong strictly to
	// Conn, Trawl (/v1/items), Countersign (/v1/release), Shipkeeper (/v1/health),
	// or cluster probes (/healthz).
	allowedNonWorkerPaths := map[string]bool{
		"/v1/items":   true,
		"/v1/edits":   true,
		"/v1/health":  true,
		"/v1/feed":    true,
		"/v1/release": true,
		"/healthz":    true,
	}
	if !allowedNonWorkerPaths[ex.Request.Path] {
		t.Fatalf("%s has unrecognized path %q", name, ex.Request.Path)
	}
	if ex.Request.As == "moldloft" {
		t.Fatalf("%s unexpectedly names moldloft on non-worker path %s", name, ex.Request.Path)
	}
}

func standInSupportsExample(ex contractExample) bool {
	supportedPath := func(p string) bool {
		return isWorkerPath(p) || p == "/v1/release" || p == "/healthz" || p == "/v1/made-up"
	}
	if !supportedPath(ex.Request.Path) {
		return false
	}
	if ex.Given != nil {
		for _, c := range ex.Given.Calls {
			if !supportedPath(c.Path) {
				return false
			}
		}
	}
	return true
}

func replayAgainstStandIn(t *testing.T, name string, ex contractExample) {
	t.Helper()
	standIn := NewStandIn()
	srv := httptest.NewServer(standIn)
	defer srv.Close()

	send := func(req exampleRequest) (int, []byte) {
		var bodyReader io.Reader
		if req.Raw != "" {
			bodyReader = strings.NewReader(req.Raw)
		} else if len(req.Body) > 0 {
			bodyReader = bytes.NewReader(req.Body)
		}
		httpReq, err := http.NewRequest(req.Method, srv.URL+req.Path, bodyReader)
		if err != nil {
			t.Fatal(err)
		}
		ct := req.ContentType
		if ct == "" && req.Method == http.MethodPost {
			ct = "application/json"
		}
		if ct != "" {
			httpReq.Header.Set("Content-Type", ct)
		}
		if req.As != "" {
			httpReq.Header.Set("Authorization", "Bearer "+req.As)
		}
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, out
	}

	if ex.Given != nil {
		for _, it := range ex.Given.Items {
			standIn.Seed(it)
		}
		for i, c := range ex.Given.Calls {
			status, out := send(c)
			if status < 200 || status >= 300 {
				t.Fatalf("%s given.calls[%d] (%s %s) failed with %d: %s", name, i, c.Method, c.Path, status, out)
			}
		}
		if ex.Given.Wait != "" {
			d, err := time.ParseDuration(ex.Given.Wait)
			if err != nil {
				t.Fatalf("parse wait %q: %v", ex.Given.Wait, err)
			}
			standIn.Advance(d)
		}
		if ex.Given.Condition != "" {
			standIn.SetCondition(ex.Given.Condition)
		}
	}

	status, gotBody := send(ex.Request)
	if status != ex.Expect.Status {
		t.Fatalf("%s StandIn status = %d (%s), want %d", name, status, gotBody, ex.Expect.Status)
	}
	if len(ex.Expect.Body) == 0 {
		if len(gotBody) != 0 {
			t.Fatalf("%s StandIn body = %q, want empty", name, gotBody)
		}
		return
	}
	if compactJSON(t, gotBody) != compactJSON(t, ex.Expect.Body) {
		t.Fatalf("%s StandIn body mismatch:\n got: %s\nwant: %s", name, compactJSON(t, gotBody), compactJSON(t, ex.Expect.Body))
	}
}
