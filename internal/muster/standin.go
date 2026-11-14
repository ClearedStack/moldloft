package muster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ClearedStack/moldloft/internal/errs"
)

var workerCallers = map[string]bool{
	"trawl":       true,
	"sextant":     true,
	"assay":       true,
	"countersign": true,
	"waybill":     true,
	"moldloft":    true,
	"shakedown":   true,
	"chartroom":   true,
	"tidegauge":   true,
	"bellbuoy":    true,
}

var allCallers = map[string]bool{
	"trawl":       true,
	"sextant":     true,
	"assay":       true,
	"countersign": true,
	"waybill":     true,
	"moldloft":    true,
	"shakedown":   true,
	"chartroom":   true,
	"tidegauge":   true,
	"bellbuoy":    true,
	"conn":        true,
	"shipkeeper":  true,
}

type itemTransition struct {
	worker     string
	from       string
	to         string
	nextWorker string
	nextLane   string
	delay      time.Duration
}

// itemSteps encodes the Queue steps table in CONTRACT.md.
var itemSteps = []itemTransition{
	{"trawl", StateDiscovered, StateRecorded, "sextant", "", 0},
	{"trawl", StateDiscovered, StateRejected, "", "", 0},
	{"sextant", StateRecorded, StateObserved, "assay", "", 0},
	{"assay", StateObserved, StateScored, "countersign", "", 0},
	{"assay", StateObserved, StateScreenedOut, "countersign", "", 0},
	{"countersign", StateScored, StateApproved, "waybill", "", 0},
	{"countersign", StateScored, StateParked, "", "", 0},
	{"countersign", StateScored, StateLinked, "chartroom", "", 0},
	{"countersign", StateScored, StateMerged, "chartroom", "", 0},
	{"countersign", StateScored, StateRejected, "", "", 0},
	{"countersign", StateScreenedOut, StateObserved, "assay", "", 0},
	{"waybill", StateApproved, StateApproved, "moldloft", "", 0},
	{"moldloft", StateApproved, StateBuilding, "shakedown", "", 0},
	{"shakedown", StateBuilding, StateBuilding, "chartroom", "", 0},
	{"shakedown", StateBuilding, StateParked, "", "", 0},
	{"chartroom", StateBuilding, StateLive, "tidegauge", LaneBackfill, 7 * 24 * time.Hour},
	{"chartroom", StateLinked, StateLinked, "", "", 0},
	{"chartroom", StateMerged, StateMerged, "", "", 0},
	{"chartroom", StateRetired, StateRetired, "", "", 0},
	{"tidegauge", StateLive, StateLive, "tidegauge", LaneBackfill, 7 * 24 * time.Hour},
	{"tidegauge", StateLive, StateRetired, "chartroom", LaneUrgent, 0},
	{"tidegauge", StateLive, StateBuilding, "waybill", LaneUrgent, 0},
	{"waybill", StateBuilding, StateBuilding, "moldloft", "", 0},
	{"moldloft", StateBuilding, StateBuilding, "shakedown", "", 0},
}

// mayPauseStates encodes which states allow a move to needs_subtool in CONTRACT.md.
var mayPauseStates = map[string]bool{
	StateRecorded: true,
	StateObserved: true,
	StateApproved: true,
}

func defaultWorkerForState(state string) string {
	switch state {
	case StateDiscovered:
		return "trawl"
	case StateRecorded:
		return "sextant"
	case StateObserved:
		return "assay"
	case StateScored, StateScreenedOut:
		return "countersign"
	case StateApproved:
		return "waybill"
	case StateBuilding:
		return "moldloft"
	case StateLive:
		return "tidegauge"
	case StateLinked, StateMerged, StateRetired:
		return "chartroom"
	default:
		return ""
	}
}

// SeedItem describes a made-up item loaded into StandIn before calls run.
type SeedItem struct {
	ID      string                     `json:"id"`
	State   string                     `json:"state"`
	Lane    string                     `json:"lane"`
	Next    string                     `json:"next"`
	Found   *FoundView                 `json:"found,omitempty"`
	Results map[string]json.RawMessage `json:"results,omitempty"`
}

type storedItem struct {
	ID         string
	State      string
	PausedFrom string
	PausedBy   string
	Found      *FoundView
	Results    map[string]json.RawMessage
}

type queueEntry struct {
	Key      string // e.g. "PA-0001" or "incident:PA-0001"
	Kind     string
	Subject  string
	Worker   string
	Lane     string
	Seq      int
	Attempt  int
	Lease    string
	Holder   string
	Expires  time.Time
	ReadyAt  time.Time
	Held     bool
	Incident *IncidentView
}

type finishedLease struct {
	caller string
	call   string
	status int
	body   []byte
}

// StandIn is an in-memory HTTP stand-in for Muster implementing the worker
// calls, release, healthz and refusal rules of contract v1.
type StandIn struct {
	mu        sync.Mutex
	now       time.Time
	leaseFor  time.Duration
	nextSeq   int
	condition string
	items     map[string]*storedItem
	entries   map[string]*queueEntry
	finished  map[string]finishedLease
}

// NewStandIn creates a StandIn starting at 2026-01-05T09:00:00Z with a 60s
// lease duration, matching contract/v1/examples.
func NewStandIn() *StandIn {
	return &StandIn{
		now:      time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC),
		leaseFor: 60 * time.Second,
		items:    map[string]*storedItem{},
		entries:  map[string]*queueEntry{},
		finished: map[string]finishedLease{},
	}
}

// Advance moves StandIn's clock forward by d.
func (s *StandIn) Advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
}

// SetCondition sets a simulated failure condition (or "" to clear it).
func (s *StandIn) SetCondition(cond string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.condition = cond
}

// Seed adds an item and, when appropriate, its waiting queue step.
func (s *StandIn) Seed(seed SeedItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := map[string]json.RawMessage{}
	for k, v := range seed.Results {
		res[k] = append(json.RawMessage(nil), v...)
	}
	s.items[seed.ID] = &storedItem{
		ID:      seed.ID,
		State:   seed.State,
		Found:   seed.Found,
		Results: res,
	}
	lane := seed.Lane
	if lane == "" {
		lane = LaneNormal
	}
	worker := seed.Next
	if worker == "" && seed.State != StateRejected && seed.State != StateParked && seed.State != StateNeedsSubtool {
		worker = defaultWorkerForState(seed.State)
	}
	if worker != "" {
		s.nextSeq++
		s.entries[seed.ID] = &queueEntry{
			Key:     seed.ID,
			Kind:    KindItem,
			Subject: seed.ID,
			Worker:  worker,
			Lane:    lane,
			Seq:     s.nextSeq,
			ReadyAt: s.now,
		}
	}
}

// ItemSnapshot returns an item's current state, whether its step is held, next
// worker, and stored results map for assertions in tests.
func (s *StandIn) ItemSnapshot(id string) (state string, held bool, nextWorker string, results map[string]json.RawMessage, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, exists := s.items[id]
	if !exists {
		return "", false, "", nil, false
	}
	out := map[string]json.RawMessage{}
	for k, v := range it.Results {
		out[k] = append(json.RawMessage(nil), v...)
	}
	if e, hasEntry := s.entries[id]; hasEntry {
		return it.State, e.Held, e.Worker, out, true
	}
	return it.State, false, "", out, true
}

func (s *StandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			writeRefusal(w, http.StatusMethodNotAllowed, errs.Parse, "wrong_method", "this call does not accept that method")
			return
		}
		writeJSONBytes(w, http.StatusOK, []byte(`{"status":"ok"}`))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.condition {
	case "every place for a call in flight is taken":
		writeRefusal(w, http.StatusServiceUnavailable, errs.Timeout, "busy", "too busy; try again")
		return
	case "the cluster does not answer the identity check in time":
		writeRefusal(w, http.StatusServiceUnavailable, errs.Timeout, "busy", "the identity check took too long; try again")
		return
	}

	knownPath := map[string]bool{
		"/v1/lease":         true,
		"/v1/heartbeat":     true,
		"/v1/complete":      true,
		"/v1/fail":          true,
		"/v1/needs_subtool": true,
		"/v1/release":       true,
	}
	caller, authOK := extractBearer(r)
	if !authOK {
		writeRefusal(w, http.StatusUnauthorized, errs.Parse, "no_credential", "no valid credential was presented")
		return
	}
	if !allCallers[caller] {
		writeRefusal(w, http.StatusUnauthorized, errs.Parse, "no_credential", "no valid credential was presented")
		return
	}
	if !knownPath[r.URL.Path] {
		writeRefusal(w, http.StatusNotFound, errs.Parse, "no_such_call", "no such call")
		return
	}
	if r.Method != http.MethodPost {
		writeRefusal(w, http.StatusMethodNotAllowed, errs.Parse, "wrong_method", "this call does not accept that method")
		return
	}

	switch r.URL.Path {
	case "/v1/release":
		if caller != "countersign" {
			writeRefusal(w, http.StatusForbidden, errs.Parse, "not_your_call", "this caller may not make this call")
			return
		}
	default:
		if !workerCallers[caller] {
			writeRefusal(w, http.StatusForbidden, errs.Parse, "not_your_call", "this caller may not make this call")
			return
		}
	}

	ct := r.Header.Get("Content-Type")
	mediaType := strings.TrimSpace(strings.Split(ct, ";")[0])
	if mediaType != "application/json" {
		writeRefusal(w, http.StatusUnsupportedMediaType, errs.Parse, "wrong_type", "the content type must be application/json")
		return
	}

	limit := MaxWorkerRequestBytes
	if r.URL.Path == "/v1/complete" && caller == "waybill" {
		limit = 256 << 10
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, int64(limit)+1))
	if err != nil {
		writeRefusal(w, http.StatusBadRequest, errs.Parse, "not_json", "the request is not one json value")
		return
	}
	if len(raw) > limit {
		writeRefusal(w, http.StatusRequestEntityTooLarge, errs.Parse, "too_large", "the request is too large")
		return
	}

	fields, refStatus, refCode, refCause, refReason := parseRequestObject(raw)
	if refStatus != 0 {
		writeRefusal(w, refStatus, refCode, refCause, refReason)
		return
	}

	switch s.condition {
	case "logbook takes too long":
		writeRefusal(w, http.StatusServiceUnavailable, errs.Timeout, "store_slow", "the record store took too long")
		return
	case "logbook refuses a field":
		writeRefusal(w, http.StatusBadRequest, errs.Parse, "bad_field", "the record store refused a field")
		return
	case "logbook has no rule for a value":
		writeRefusal(w, http.StatusUnprocessableEntity, errs.NeedsSubtool, "unknown_value", "the record store has no rule for a value")
		return
	case "logbook fails in a way nothing provides for":
		writeRefusal(w, http.StatusInternalServerError, errs.Parse, "unexpected", "an unexpected condition; the request was refused")
		return
	case "the queue says sextant is next for an item that is live":
		writeRefusal(w, http.StatusInternalServerError, errs.Parse, "bad_record", "a stored record was not consistent and has been set aside")
		return
	}

	switch r.URL.Path {
	case "/v1/lease":
		s.handleLease(w, caller, fields)
	case "/v1/heartbeat":
		s.handleHeartbeat(w, caller, fields)
	case "/v1/complete":
		s.handleComplete(w, caller, fields)
	case "/v1/fail":
		s.handleFail(w, caller, fields)
	case "/v1/needs_subtool":
		s.handleNeedsSubtool(w, caller, fields)
	case "/v1/release":
		s.handleRelease(w, caller, fields)
	}
}

func (s *StandIn) handleLease(w http.ResponseWriter, worker string, fields map[string]json.RawMessage) {
	if !onlyFields(w, fields, "contract") {
		return
	}
	if !requireContract(w, fields) {
		return
	}
	e := s.pickNext(worker)
	if e == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	state := s.entryState(e)
	if !s.workerMayHold(worker, e, state) {
		e.Held, e.Lease, e.Holder, e.Expires = true, "", "", time.Time{}
		if e.Kind == KindItem {
			s.openIncident(e.Subject, worker, string(errs.Parse), state)
		}
		writeRefusal(w, http.StatusInternalServerError, errs.Parse, "bad_record", "a stored record was not consistent and has been set aside")
		return
	}
	e.Attempt++
	e.Lease = fmt.Sprintf("%s.%d.%d", e.Key, e.Seq, e.Attempt)
	e.Holder = worker
	e.Expires = s.now.Add(s.leaseFor)

	g := Grant{
		Lease:   e.Lease,
		Kind:    e.Kind,
		ID:      e.Subject,
		State:   state,
		Lane:    e.Lane,
		Attempt: e.Attempt,
		Expires: s.now.Add(s.leaseFor).UTC().Format(time.RFC3339),
	}
	if e.Kind == KindItem {
		it := s.items[e.Subject]
		if it.Found != nil {
			g.Found = it.Found
		}
		if len(it.Results) > 0 {
			g.Results = it.Results
		}
	} else if e.Kind == KindIncident {
		g.Incident = e.Incident
	}
	out, _ := json.Marshal(g)
	writeJSONBytes(w, http.StatusOK, out)
}

func (s *StandIn) handleHeartbeat(w http.ResponseWriter, worker string, fields map[string]json.RawMessage) {
	if !onlyFields(w, fields, "contract", "lease") {
		return
	}
	if !requireContract(w, fields) {
		return
	}
	lease, ok := reqString(w, fields, "lease")
	if !ok {
		return
	}
	e := s.findHeldLease(lease, worker, false)
	if e == nil {
		writeRefusal(w, http.StatusConflict, errs.Parse, "lease_not_held", "the lease is not held")
		return
	}
	e.Expires = s.now.Add(s.leaseFor)
	out, _ := json.Marshal(Beat{Lease: e.Lease, Expires: e.Expires.UTC().Format(time.RFC3339)})
	writeJSONBytes(w, http.StatusOK, out)
}

func (s *StandIn) handleComplete(w http.ResponseWriter, worker string, fields map[string]json.RawMessage) {
	if !onlyFields(w, fields, "contract", "lease", "state", "result") {
		return
	}
	if !requireContract(w, fields) {
		return
	}
	lease, ok := reqString(w, fields, "lease")
	if !ok {
		return
	}
	toState, ok := reqString(w, fields, "state")
	if !ok {
		return
	}
	var resultRaw json.RawMessage
	if raw, hasRes := fields["result"]; hasRes {
		if !isJSONObject(raw) {
			writeRefusal(w, http.StatusBadRequest, errs.Parse, "bad_field", "a field does not have the form this call requires")
			return
		}
		resultRaw = bytes.TrimSpace(raw)
	}
	if prev, done := s.finished[lease]; done && prev.caller == worker && prev.call == "complete" {
		writeJSONBytes(w, prev.status, prev.body)
		return
	}
	e := s.findHeldLease(lease, worker, true)
	if e == nil {
		writeRefusal(w, http.StatusConflict, errs.Parse, "lease_not_held", "the lease is not held")
		return
	}

	if e.Kind == KindIncident {
		if len(resultRaw) > 0 || toState != e.Incident.State {
			writeRefusal(w, http.StatusConflict, errs.Parse, "move_not_allowed", "that move is not allowed")
			return
		}
		delete(s.entries, e.Key)
		ack, _ := json.Marshal(CompleteAck{ID: e.Subject, State: toState})
		s.finished[lease] = finishedLease{caller: worker, call: "complete", status: http.StatusOK, body: ack}
		writeJSONBytes(w, http.StatusOK, ack)
		return
	}

	it := s.items[e.Subject]
	step, ok := findStep(worker, it.State, toState)
	if !ok {
		writeRefusal(w, http.StatusConflict, errs.Parse, "move_not_allowed", "that move is not allowed")
		return
	}
	it.State = toState
	if len(resultRaw) > 0 {
		if it.Results == nil {
			it.Results = map[string]json.RawMessage{}
		}
		it.Results[worker] = resultRaw
	}
	delete(s.entries, e.Key)
	if step.nextWorker != "" {
		lane := e.Lane
		if step.nextLane != "" {
			lane = step.nextLane
		}
		s.nextSeq++
		s.entries[it.ID] = &queueEntry{
			Key:     it.ID,
			Kind:    KindItem,
			Subject: it.ID,
			Worker:  step.nextWorker,
			Lane:    lane,
			Seq:     s.nextSeq,
			ReadyAt: s.now.Add(step.delay),
		}
	}
	ack, _ := json.Marshal(CompleteAck{ID: it.ID, State: toState, Next: step.nextWorker})
	s.finished[lease] = finishedLease{caller: worker, call: "complete", status: http.StatusOK, body: ack}
	writeJSONBytes(w, http.StatusOK, ack)
}

func (s *StandIn) handleFail(w http.ResponseWriter, worker string, fields map[string]json.RawMessage) {
	if !onlyFields(w, fields, "contract", "lease", "code") {
		return
	}
	if !requireContract(w, fields) {
		return
	}
	lease, ok := reqString(w, fields, "lease")
	if !ok {
		return
	}
	code, ok := reqString(w, fields, "code")
	if !ok {
		return
	}
	if code == string(errs.NeedsSubtool) {
		writeRefusal(w, http.StatusBadRequest, errs.Parse, "bad_field", "report a missing rule with the needs_subtool call")
		return
	}
	if code != string(errs.Parse) && code != string(errs.Timeout) {
		writeRefusal(w, http.StatusBadRequest, errs.Parse, "bad_field", "a field does not have the form this call requires")
		return
	}
	if prev, done := s.finished[lease]; done && prev.caller == worker && prev.call == "fail:"+code {
		writeJSONBytes(w, prev.status, prev.body)
		return
	}
	e := s.findHeldLease(lease, worker, true)
	if e == nil {
		writeRefusal(w, http.StatusConflict, errs.Parse, "lease_not_held", "the lease is not held")
		return
	}
	state := s.entryState(e)
	var ack FailAck
	if code == string(errs.Timeout) {
		e.Lease, e.Holder, e.Expires = "", "", time.Time{}
		ack = FailAck{ID: e.Subject, State: state, Next: e.Worker}
	} else {
		e.Held, e.Lease, e.Holder, e.Expires = true, "", "", time.Time{}
		if e.Kind == KindItem {
			s.openIncident(e.Subject, worker, string(errs.Parse), state)
		}
		ack = FailAck{ID: e.Subject, State: state, Held: true}
	}
	out, _ := json.Marshal(ack)
	s.finished[lease] = finishedLease{caller: worker, call: "fail:" + code, status: http.StatusOK, body: out}
	writeJSONBytes(w, http.StatusOK, out)
}

func (s *StandIn) handleNeedsSubtool(w http.ResponseWriter, worker string, fields map[string]json.RawMessage) {
	if !onlyFields(w, fields, "contract", "lease") {
		return
	}
	if !requireContract(w, fields) {
		return
	}
	lease, ok := reqString(w, fields, "lease")
	if !ok {
		return
	}
	if prev, done := s.finished[lease]; done && prev.caller == worker && prev.call == "needs_subtool" {
		writeJSONBytes(w, prev.status, prev.body)
		return
	}
	e := s.findHeldLease(lease, worker, true)
	if e == nil {
		writeRefusal(w, http.StatusConflict, errs.Parse, "lease_not_held", "the lease is not held")
		return
	}
	if e.Kind != KindItem {
		writeRefusal(w, http.StatusConflict, errs.Parse, "move_not_allowed", "that move is not allowed")
		return
	}
	it := s.items[e.Subject]
	if !mayPauseStates[it.State] {
		writeRefusal(w, http.StatusConflict, errs.Parse, "move_not_allowed", "that move is not allowed")
		return
	}
	pausedFrom := it.State
	it.PausedFrom = pausedFrom
	it.PausedBy = worker
	it.State = StateNeedsSubtool
	delete(s.entries, e.Key)
	s.openIncident(it.ID, worker, string(errs.NeedsSubtool), pausedFrom)

	out, _ := json.Marshal(PauseAck{ID: it.ID, State: StateNeedsSubtool})
	s.finished[lease] = finishedLease{caller: worker, call: "needs_subtool", status: http.StatusOK, body: out}
	writeJSONBytes(w, http.StatusOK, out)
}

func (s *StandIn) handleRelease(w http.ResponseWriter, caller string, fields map[string]json.RawMessage) {
	if !onlyFields(w, fields, "contract", "id") {
		return
	}
	if !requireContract(w, fields) {
		return
	}
	id, ok := reqString(w, fields, "id")
	if !ok {
		return
	}
	e, exists := s.entries[id]
	it, itemExists := s.items[id]
	if !exists || !itemExists || !e.Held {
		writeRefusal(w, http.StatusConflict, errs.Parse, "move_not_allowed", "that move is not allowed")
		return
	}
	s.nextSeq++
	e.Held = false
	e.Lane = LaneUrgent
	e.Seq = s.nextSeq
	e.Lease, e.Holder, e.Expires = "", "", time.Time{}
	e.ReadyAt = s.now
	out, _ := json.Marshal(CompleteAck{ID: it.ID, State: it.State, Next: e.Worker})
	writeJSONBytes(w, http.StatusOK, out)
}

func (s *StandIn) openIncident(itemID, worker, code, state string) {
	key := "incident:" + itemID
	s.nextSeq++
	s.entries[key] = &queueEntry{
		Key:     key,
		Kind:    KindIncident,
		Subject: itemID,
		Worker:  "bellbuoy",
		Lane:    LaneNormal,
		Seq:     s.nextSeq,
		ReadyAt: s.now,
		Incident: &IncidentView{
			ID:     itemID,
			Worker: worker,
			Code:   code,
			State:  state,
		},
	}
}

func (s *StandIn) pickNext(worker string) *queueEntry {
	laneRank := func(lane string) int {
		switch lane {
		case LaneUrgent:
			return 0
		case LaneNormal:
			return 1
		default:
			return 2
		}
	}
	var candidates []*queueEntry
	for _, e := range s.entries {
		if e.Worker != worker || e.Held || e.ReadyAt.After(s.now) {
			continue
		}
		if e.Lease != "" && e.Expires.After(s.now) {
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		ri, rj := laneRank(candidates[i].Lane), laneRank(candidates[j].Lane)
		if ri != rj {
			return ri < rj
		}
		return candidates[i].Seq < candidates[j].Seq
	})
	return candidates[0]
}

func (s *StandIn) findHeldLease(lease, worker string, allowLate bool) *queueEntry {
	for _, e := range s.entries {
		if e.Lease != lease || e.Holder != worker || e.Held {
			continue
		}
		if !allowLate && !e.Expires.After(s.now) {
			return nil
		}
		return e
	}
	return nil
}

func (s *StandIn) entryState(e *queueEntry) string {
	if e.Kind == KindIncident {
		return e.Incident.State
	}
	if it, ok := s.items[e.Subject]; ok {
		return it.State
	}
	return ""
}

func (s *StandIn) workerMayHold(worker string, e *queueEntry, state string) bool {
	if e.Kind == KindIncident {
		return worker == "bellbuoy"
	}
	for _, st := range itemSteps {
		if st.worker == worker && st.from == state {
			return true
		}
	}
	return false
}

func findStep(worker, from, to string) (itemTransition, bool) {
	for _, st := range itemSteps {
		if st.worker == worker && st.from == from && st.to == to {
			return st, true
		}
	}
	return itemTransition{}, false
}

func extractBearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	tok := strings.TrimPrefix(h, "Bearer ")
	if tok == "" || strings.ContainsAny(tok, " \t\r\n") {
		return "", false
	}
	return tok, true
}

func parseRequestObject(raw []byte) (map[string]json.RawMessage, int, errs.Code, string, string) {
	if !utf8.Valid(raw) {
		return nil, http.StatusBadRequest, errs.Parse, "not_json", "the request is not one json value"
	}
	if err := checkNoDuplicateKeys(raw); err != nil {
		return nil, http.StatusBadRequest, errs.Parse, "not_json", "the request is not one json value"
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var m map[string]json.RawMessage
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, http.StatusBadRequest, errs.Parse, "not_json", "the request is not one json value"
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, http.StatusBadRequest, errs.Parse, "not_json", "the request is not one json value"
	}
	return m, 0, "", "", ""
}

func onlyFields(w http.ResponseWriter, fields map[string]json.RawMessage, allowed ...string) bool {
	okSet := map[string]bool{}
	for _, a := range allowed {
		okSet[a] = true
	}
	for k := range fields {
		if !okSet[k] {
			writeRefusal(w, http.StatusBadRequest, errs.Parse, "unknown_field", "the request has a field this call does not define")
			return false
		}
	}
	return true
}

func requireContract(w http.ResponseWriter, fields map[string]json.RawMessage) bool {
	c, ok := reqString(w, fields, "contract")
	if !ok {
		return false
	}
	if c != ContractVersion {
		writeRefusal(w, http.StatusUnprocessableEntity, errs.NeedsSubtool, "unknown_contract", "this contract version is not known")
		return false
	}
	return true
}

func reqString(w http.ResponseWriter, fields map[string]json.RawMessage, key string) (string, bool) {
	raw, exists := fields[key]
	if !exists {
		writeRefusal(w, http.StatusBadRequest, errs.Parse, "missing_field", "a required field is missing")
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		writeRefusal(w, http.StatusBadRequest, errs.Parse, "bad_field", "a field does not have the form this call requires")
		return "", false
	}
	if s == "" {
		writeRefusal(w, http.StatusBadRequest, errs.Parse, "missing_field", "a required field is missing")
		return "", false
	}
	return s, true
}

func writeRefusal(w http.ResponseWriter, status int, code errs.Code, cause, reason string) {
	out, _ := json.Marshal(wireRefusal{
		Error:  string(code),
		Cause:  cause,
		Reason: reason,
	})
	writeJSONBytes(w, status, out)
}

func writeJSONBytes(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}
