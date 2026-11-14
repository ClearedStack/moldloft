// Package muster speaks Muster's contract v1 from a worker in cs-workers and
// provides an in-memory HTTP stand-in for testing and standalone execution.
package muster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ClearedStack/moldloft/internal/errs"
)

// ContractVersion is the only contract version Moldloft speaks with Muster.
const ContractVersion = "v1"

// MaxWorkerRequestBytes is Muster's 64 KiB body limit on worker calls.
const MaxWorkerRequestBytes = 64 << 10

// MaxResponseBytes bounds how much of a response body Moldloft will read so a
// broken peer cannot exhaust the 30 MiB memory budget.
const MaxResponseBytes = 2 << 20

// Kinds of queue steps defined by contract v1.
const (
	KindItem     = "item"
	KindProposal = "proposal"
	KindIncident = "incident"
)

// Priority lanes defined by contract v1.
const (
	LaneUrgent   = "urgent"
	LaneNormal   = "normal"
	LaneBackfill = "backfill"
)

// Item and proposal states used in worker calls.
const (
	StateDiscovered      = "discovered"
	StateRecorded        = "recorded"
	StateObserved        = "observed"
	StateScreenedOut     = "screened_out"
	StateScored          = "scored"
	StateApproved        = "approved"
	StateBuilding        = "building"
	StateLive            = "live"
	StateParked          = "parked"
	StateLinked          = "linked"
	StateMerged          = "merged"
	StateNeedsSubtool    = "needs_subtool"
	StateRejected        = "rejected"
	StateRetired         = "retired"
	StatePendingApproval = "pending_approval"
	StateDeclined        = "declined"
)

// Client is the worker side of Muster's contract v1: the five calls a worker
// in cs-workers may make.
type Client interface {
	Lease(ctx context.Context) (*Grant, error)
	Heartbeat(ctx context.Context, lease string) (Beat, error)
	Complete(ctx context.Context, lease, state string, result json.RawMessage) (CompleteAck, error)
	Fail(ctx context.Context, lease string, code errs.Code) (FailAck, error)
	NeedsSubtool(ctx context.Context, lease string) (PauseAck, error)
}

// Grant is Muster's 200 answer to POST /v1/lease.
type Grant struct {
	Lease    string                     `json:"lease"`
	Kind     string                     `json:"kind"`
	ID       string                     `json:"id"`
	State    string                     `json:"state"`
	Lane     string                     `json:"lane"`
	Attempt  int                        `json:"attempt"`
	Expires  string                     `json:"expires"`
	Found    *FoundView                 `json:"found,omitempty"`
	Results  map[string]json.RawMessage `json:"results,omitempty"`
	Proposal *ProposalView              `json:"proposal,omitempty"`
	Incident *IncidentView              `json:"incident,omitempty"`
}

// FoundView is where an item was found, as recorded through POST /v1/items.
type FoundView struct {
	Source  string `json:"source"`
	Title   string `json:"title"`
	Address string `json:"address"`
}

// ProposalView is what a lease carries for an edit proposal.
type ProposalView struct {
	Target     string `json:"target"`
	Repository string `json:"repository"`
	File       string `json:"file"`
	EditKind   string `json:"edit_kind"`
	Change     string `json:"change"`
	Reason     string `json:"reason"`
}

// IncidentView is what Bellbuoy is shown for an incident step.
type IncidentView struct {
	ID     string `json:"id"`
	Worker string `json:"worker"`
	Code   string `json:"code"`
	State  string `json:"state"`
}

// Beat is Muster's 200 answer to POST /v1/heartbeat.
type Beat struct {
	Lease   string `json:"lease"`
	Expires string `json:"expires"`
}

// CompleteAck is Muster's 200 answer to POST /v1/complete.
type CompleteAck struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Next  string `json:"next,omitempty"`
}

// FailAck is Muster's 200 answer to POST /v1/fail.
type FailAck struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Next  string `json:"next,omitempty"`
	Held  bool   `json:"held,omitempty"`
}

// PauseAck is Muster's 200 answer to POST /v1/needs_subtool.
type PauseAck struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// knownCauses is the closed set of refusal causes defined in CONTRACT.md.
// Checking against this set guarantees no untrusted string from a peer is ever
// repeated in an error or log line.
var knownCauses = map[string]bool{
	"not_json":         true,
	"unknown_field":    true,
	"missing_field":    true,
	"bad_field":        true,
	"no_credential":    true,
	"not_your_call":    true,
	"no_such_call":     true,
	"wrong_method":     true,
	"too_large":        true,
	"wrong_type":       true,
	"lease_not_held":   true,
	"move_not_allowed": true,
	"duplicate":        true,
	"unknown_contract": true,
	"unknown_value":    true,
	"rate_limited":     true,
	"store_slow":       true,
	"busy":             true,
	"bad_record":       true,
	"unexpected":       true,
}

// Refusal is a structured contract v1 refusal from Muster. The human-facing
// reason field is validated on the wire and intentionally discarded so external
// text can never enter Moldloft's logs or errors.
type Refusal struct {
	Status int
	Code   errs.Code
	Cause  string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("%s: muster: refused (%d %s)", r.Code, r.Status, r.Cause)
}

// Retryable reports whether the refusal represents transient load or slowness
// where the caller should keep its spooled outcome and try again.
func (r *Refusal) Retryable() bool {
	return r.Status == 429 || r.Status == 503
}

// CodeOfError extracts the public error code from either *Refusal or
// *errs.Error.
func CodeOfError(err error) errs.Code {
	var ref *Refusal
	if errors.As(err, &ref) {
		return ref.Code
	}
	return errs.CodeOf(err)
}

// IsRetryable reports whether err is a transient transport failure or a
// retryable Muster refusal (429 or 503).
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var ref *Refusal
	if errors.As(err, &ref) {
		return ref.Retryable()
	}
	return errs.CodeOf(err) == errs.Timeout
}
