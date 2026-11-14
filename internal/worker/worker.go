// Package worker runs Moldloft's clean-room build loop against Muster's
// contract v1, staging completed outcomes in /var/spool/cs and optionally
// pushing generated Git repositories to Plainforge.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/errs"
	"github.com/ClearedStack/moldloft/internal/generate"
	"github.com/ClearedStack/moldloft/internal/muster"
	"github.com/ClearedStack/moldloft/internal/plainforge"
	"github.com/ClearedStack/moldloft/internal/spool"
)

// DefaultPublicRepoBase is the public repository prefix recorded in
// results.moldloft.publish.repository when MOLDLOFT_PUBLIC_REPO_BASE is unset.
const DefaultPublicRepoBase = "https://github.com/clearedstack"

// maxResultBytes leaves 512 bytes of headroom for the POST /v1/complete
// envelope beneath Muster's 64 KiB worker request limit.
const maxResultBytes = muster.MaxWorkerRequestBytes - 512

// PublishRecord is the Chartroom-compatible publish block written under
// results.moldloft.publish.
type PublishRecord struct {
	Repository string `json:"repository"`
}

// BuildRecord is Moldloft's deterministic build receipt written under
// results.moldloft.build.
type BuildRecord struct {
	ContractVersion string `json:"contract_version"`
	Template        string `json:"template"`
	ToolSlug        string `json:"tool_slug"`
	SpecSHA256      string `json:"spec_sha256"`
	ManifestSHA256  string `json:"manifest_sha256"`
	TreeSHA1        string `json:"tree_sha1"`
	CommitSHA1      string `json:"commit_sha1"`
	FileCount       int    `json:"file_count"`
	Committed       bool   `json:"committed"`
}

// ResultPayload is the JSON object Moldloft records in Muster's results map
// under results.moldloft. Its top-level fields match Shakedown's strict
// moldloftWire decoder (status, regulatory_ambiguity, publish, build, files)
// and Chartroom's publish block without colliding with other catalog keys.
type ResultPayload struct {
	Status              string            `json:"status,omitempty"`
	RegulatoryAmbiguity bool              `json:"regulatory_ambiguity,omitempty"`
	Publish             *PublishRecord    `json:"publish,omitempty"`
	Build               *BuildRecord      `json:"build,omitempty"`
	Files               map[string]string `json:"files,omitempty"`
}

// Config configures a Worker instance.
type Config struct {
	Muster            muster.Client
	Committer         plainforge.Committer
	Spool             *spool.Store
	PublicRepoBase    string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	BackoffMin        time.Duration
	BackoffMax        time.Duration
	Log               io.Writer
}

// Worker leases approved and building items from Muster, generates static
// sub-tool repositories, commits them when configured and reports outcomes.
type Worker struct {
	muster            muster.Client
	committer         plainforge.Committer
	spool             *spool.Store
	publicRepoBase    string
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	backoffMin        time.Duration
	backoffMax        time.Duration
	logMu             sync.Mutex
	logOut            io.Writer
}

// New validates cfg and returns a ready Worker.
func New(cfg Config) (*Worker, error) {
	if cfg.Muster == nil {
		return nil, errors.New("muster client is required")
	}
	if cfg.Spool == nil {
		return nil, errors.New("spool store is required")
	}
	committer := cfg.Committer
	if committer == nil {
		committer = plainforge.DryCommitter{}
	}
	base := strings.TrimSpace(cfg.PublicRepoBase)
	if base == "" {
		base = DefaultPublicRepoBase
	}
	normalizedBase, err := validatePublicRepoBase(base)
	if err != nil {
		return nil, err
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = 5 * time.Second
	}
	hb := cfg.HeartbeatInterval
	if hb <= 0 {
		hb = 20 * time.Second
	}
	bMin := cfg.BackoffMin
	if bMin <= 0 {
		bMin = 1 * time.Second
	}
	bMax := cfg.BackoffMax
	if bMax < bMin {
		bMax = 60 * time.Second
		if bMax < bMin {
			bMax = bMin
		}
	}
	logOut := cfg.Log
	if logOut == nil {
		logOut = io.Discard
	}
	return &Worker{
		muster:            cfg.Muster,
		committer:         committer,
		spool:             cfg.Spool,
		publicRepoBase:    normalizedBase,
		pollInterval:      poll,
		heartbeatInterval: hb,
		backoffMin:        bMin,
		backoffMax:        bMax,
		logOut:            logOut,
	}, nil
}

// Run executes the worker loop until ctx is canceled or Muster returns a
// non-retryable caller refusal on POST /v1/lease.
func (w *Worker) Run(ctx context.Context) error {
	backoff := w.backoffMin
	for {
		if ctx.Err() != nil {
			return nil
		}
		worked, err := w.Step(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if !muster.IsRetryable(err) {
				w.logEvent("fatal", "none", muster.CodeOfError(err))
				return err
			}
			if waitErr := w.waitWithSpoolHeartbeats(ctx, backoff); waitErr != nil {
				return nil
			}
			backoff *= 2
			if backoff > w.backoffMax {
				backoff = w.backoffMax
			}
			continue
		}
		backoff = w.backoffMin
		if !worked {
			if waitErr := sleepContext(ctx, w.pollInterval); waitErr != nil {
				return nil
			}
		}
	}
}

// Drain processes ready items until Muster returns 204 No Content, returning
// the number of leases handled.
func (w *Worker) Drain(ctx context.Context) (int, error) {
	count := 0
	for {
		if ctx.Err() != nil {
			return count, ctx.Err()
		}
		worked, err := w.Step(ctx)
		if err != nil {
			return count, err
		}
		if !worked {
			return count, nil
		}
		count++
	}
}

// Step flushes any pending spooled outcomes and, if the spool is clear, takes
// and processes at most one lease from Muster.
func (w *Worker) Step(ctx context.Context) (bool, error) {
	flushed, err := w.flushSpool(ctx)
	if err != nil {
		return flushed, err
	}
	grant, err := w.muster.Lease(ctx)
	if err != nil {
		w.logEvent("lease_error", "none", muster.CodeOfError(err))
		return flushed, err
	}
	if grant == nil {
		return flushed, nil
	}

	stopHB := w.startHeartbeat(ctx, grant.Lease)
	defer stopHB()

	entry := w.processGrant(ctx, grant)
	if _, err := w.spool.Enqueue(entry); err != nil {
		// Even if /var/spool/cs cannot be written, attempt to report the
		// outcome once so the leased item is not stranded unnecessarily.
		_ = w.deliverDirect(ctx, entry)
		return true, errs.Timeoutf("spool", "could not stage worker outcome")
	}
	if _, err := w.flushSpool(ctx); err != nil {
		return true, err
	}
	return true, nil
}

func (w *Worker) processGrant(ctx context.Context, grant *muster.Grant) spool.Entry {
	itemID := safeItemID(grant.ID)
	if grant.Kind != muster.KindItem || (grant.State != muster.StateApproved && grant.State != muster.StateBuilding) {
		return spool.Entry{
			Action: spool.ActionNeedsSubtool,
			Lease:  grant.Lease,
			ItemID: itemID,
		}
	}
	if !buildspec.IsMusterItemID(grant.ID) {
		return spool.Entry{
			Action: spool.ActionFail,
			Lease:  grant.Lease,
			ItemID: itemID,
			Code:   errs.Parse,
		}
	}

	// Clean-room boundary: ignore grant.Found and every key in grant.Results
	// other than "waybill".
	specBytes, regulatoryAmbiguity, err := buildspec.UnwrapWaybillEnvelope(grant.Results["waybill"])
	if err != nil {
		return failOrPause(grant.Lease, itemID, err)
	}
	if regulatoryAmbiguity {
		payload := ResultPayload{
			Status:              "regulatory_ambiguity",
			RegulatoryAmbiguity: true,
		}
		resultBytes, err := json.Marshal(payload)
		if err != nil || len(resultBytes) > maxResultBytes {
			return spool.Entry{
				Action: spool.ActionFail,
				Lease:  grant.Lease,
				ItemID: itemID,
				Code:   errs.Parse,
			}
		}
		return spool.Entry{
			Action: spool.ActionComplete,
			Lease:  grant.Lease,
			ItemID: itemID,
			State:  muster.StateBuilding,
			Result: resultBytes,
		}
	}

	tree, meta, err := generate.GenerateWithMeta(specBytes)
	if err != nil {
		return failOrPause(grant.Lease, itemID, err)
	}
	if meta.ItemID != grant.ID {
		return spool.Entry{
			Action: spool.ActionFail,
			Lease:  grant.Lease,
			ItemID: itemID,
			Code:   errs.Parse,
		}
	}

	files := make(map[string]string, len(tree))
	for _, p := range tree.Paths() {
		if !utf8.Valid(tree[p]) {
			return spool.Entry{
				Action: spool.ActionFail,
				Lease:  grant.Lease,
				ItemID: itemID,
				Code:   errs.Parse,
			}
		}
		files[p] = string(tree[p])
	}

	receipt, err := w.committer.Commit(ctx, meta.Slug, meta.SpecSHA256, tree)
	if err != nil {
		return failOrPause(grant.Lease, itemID, err)
	}

	payload := ResultPayload{
		Status: "built",
		Publish: &PublishRecord{
			Repository: w.publicRepoBase + "/" + meta.Slug,
		},
		Build: &BuildRecord{
			ContractVersion: muster.ContractVersion,
			Template:        meta.Template,
			ToolSlug:        meta.Slug,
			SpecSHA256:      meta.SpecSHA256,
			ManifestSHA256:  meta.ManifestSHA256,
			TreeSHA1:        receipt.TreeSHA1,
			CommitSHA1:      receipt.CommitSHA1,
			FileCount:       meta.FileCount,
			Committed:       receipt.Committed,
		},
		Files: files,
	}
	resultBytes, err := json.Marshal(payload)
	if err != nil || len(resultBytes) > maxResultBytes {
		return spool.Entry{
			Action: spool.ActionFail,
			Lease:  grant.Lease,
			ItemID: itemID,
			Code:   errs.Parse,
		}
	}

	return spool.Entry{
		Action: spool.ActionComplete,
		Lease:  grant.Lease,
		ItemID: itemID,
		State:  muster.StateBuilding,
		Result: resultBytes,
	}
}

func failOrPause(lease, itemID string, err error) spool.Entry {
	switch errs.CodeOf(err) {
	case errs.NeedsSubtool:
		return spool.Entry{
			Action: spool.ActionNeedsSubtool,
			Lease:  lease,
			ItemID: itemID,
		}
	case errs.Timeout:
		return spool.Entry{
			Action: spool.ActionFail,
			Lease:  lease,
			ItemID: itemID,
			Code:   errs.Timeout,
		}
	default:
		return spool.Entry{
			Action: spool.ActionFail,
			Lease:  lease,
			ItemID: itemID,
			Code:   errs.Parse,
		}
	}
}

func (w *Worker) flushSpool(ctx context.Context) (bool, error) {
	pending, err := w.spool.Pending()
	if err != nil {
		return false, errs.Timeoutf("spool", "could not read spool directory")
	}
	delivered := false
	for _, e := range pending {
		err := w.deliverDirect(ctx, e)
		if err == nil {
			_ = w.spool.Remove(e.File)
			delivered = true
			w.logOutcome(e)
			continue
		}
		if muster.IsRetryable(err) {
			_, _ = w.muster.Heartbeat(ctx, e.Lease)
			w.logEvent("spool_retry", safeItemID(e.ItemID), muster.CodeOfError(err))
			return delivered, err
		}
		// Non-retryable refusal (for example 409 lease_not_held or 409
		// move_not_allowed when needs_subtool is attempted on a rebuild):
		// CONTRACT.md requires dropping the refused call rather than looping.
		_ = w.spool.Remove(e.File)
		w.logEvent("spool_dropped", safeItemID(e.ItemID), muster.CodeOfError(err))
	}
	return delivered, nil
}

func (w *Worker) deliverDirect(ctx context.Context, e spool.Entry) error {
	switch e.Action {
	case spool.ActionComplete:
		_, err := w.muster.Complete(ctx, e.Lease, e.State, e.Result)
		return err
	case spool.ActionFail:
		_, err := w.muster.Fail(ctx, e.Lease, e.Code)
		return err
	case spool.ActionNeedsSubtool:
		_, err := w.muster.NeedsSubtool(ctx, e.Lease)
		return err
	default:
		return errs.Parsef("spool", "unknown action")
	}
}

func (w *Worker) startHeartbeat(parent context.Context, lease string) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(w.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = w.muster.Heartbeat(ctx, lease)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (w *Worker) waitWithSpoolHeartbeats(ctx context.Context, d time.Duration) error {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	ticker := time.NewTicker(w.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return nil
		case <-ticker.C:
			pending, err := w.spool.Pending()
			if err != nil {
				continue
			}
			for _, e := range pending {
				_, _ = w.muster.Heartbeat(ctx, e.Lease)
			}
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (w *Worker) logOutcome(e spool.Entry) {
	id := safeItemID(e.ItemID)
	switch e.Action {
	case spool.ActionComplete:
		w.writeLog(fmt.Sprintf("moldloft: event=completed item=%s state=%s\n", id, muster.StateBuilding))
	case spool.ActionFail:
		w.writeLog(fmt.Sprintf("moldloft: event=failed item=%s code=%s\n", id, safeCode(e.Code)))
	case spool.ActionNeedsSubtool:
		w.writeLog(fmt.Sprintf("moldloft: event=needs_subtool item=%s code=%s\n", id, errs.NeedsSubtool))
	}
}

func (w *Worker) logEvent(event, itemID string, code errs.Code) {
	w.writeLog(fmt.Sprintf("moldloft: event=%s item=%s code=%s\n", event, itemID, safeCode(code)))
}

func (w *Worker) writeLog(line string) {
	w.logMu.Lock()
	defer w.logMu.Unlock()
	_, _ = io.WriteString(w.logOut, line)
}

func safeItemID(id string) string {
	if buildspec.IsMusterItemID(id) {
		return id
	}
	return "unknown"
}

func safeCode(c errs.Code) errs.Code {
	switch c {
	case errs.Parse, errs.NeedsSubtool, errs.Timeout:
		return c
	default:
		return errs.Parse
	}
}

func validatePublicRepoBase(raw string) (string, error) {
	trimmed := strings.TrimRight(raw, "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("public repository base must be an https URL without credentials or query")
	}
	host := u.Hostname()
	if host == "" || host != strings.ToLower(host) || !strings.Contains(host, ".") || u.Port() != "" {
		return "", errors.New("public repository base must use a lower-case dotted host without port")
	}
	path := strings.TrimPrefix(u.Path, "/")
	if path == "" || strings.Contains(path, "//") {
		return "", errors.New("public repository base must include an owner or prefix path")
	}
	return trimmed, nil
}
