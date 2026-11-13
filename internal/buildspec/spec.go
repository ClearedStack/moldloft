// Package buildspec reads a clean-room build specification and checks the
// part of it that is the same for every stack template.
//
// SPEC.md names the contract (CONTRACT_VERSION=v1) without defining its
// fields. The shape read here is Moldloft's working proposal for v1; it is
// written out in docs/build-spec-v1.md and flagged as an open question.
package buildspec

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/ClearedStack/moldloft/internal/errs"
)

// ContractVersion is the only contract version Moldloft accepts.
const ContractVersion = "v1"

// Citation is one regulatory citation the generated tool shows.
type Citation struct {
	ID    string
	Label string
	Note  string
	URL   string
}

// Spec is a build specification after the common checks.
type Spec struct {
	// SHA256 is the hash of the specification exactly as it was received.
	SHA256      string
	ItemID      string
	Slug        string
	Title       string
	Summary     string
	Pattern     string
	Citations   []Citation
	ScopeLimits []string
	// Body and WorkedExamples are specific to the pattern. They are handed to
	// the stack template unread.
	Body           *Object
	WorkedExamples []Value

	reader *Reader
}

// Reader returns the error collector shared by Body and WorkedExamples.
func (s *Spec) Reader() *Reader { return s.reader }

// HasCitation reports whether id names one of the spec's citations.
func (s *Spec) HasCitation(id string) bool {
	for _, c := range s.Citations {
		if c.ID == id {
			return true
		}
	}
	return false
}

// Parse reads raw and checks every field outside the pattern-specific body.
func Parse(raw []byte) (*Spec, error) {
	fields, perr := decode(raw)
	if perr != nil {
		return nil, perr
	}
	sum := sha256.Sum256(raw)
	r := &Reader{}
	top := &Object{r: r, fields: fields, read: map[string]bool{}}
	s := &Spec{SHA256: hex.EncodeToString(sum[:]), reader: r}

	if v := top.Get("contract_version"); true {
		if got, ok := v.str(); ok && got != ContractVersion {
			r.Fail(errs.Parsef(v.path, "is not %s", ContractVersion))
		}
	}
	s.ItemID = top.Get("item_id").Slug()

	tool := top.Get("tool").Object()
	s.Slug = tool.Get("slug").Slug()
	s.Title = tool.Get("title").Text(80)
	s.Summary = tool.Get("summary").Text(400)
	tool.Close()

	s.Pattern = top.Get("pattern").Slug()

	seen := map[string]bool{}
	for _, item := range top.Get("citations").List(1, 50) {
		o := item.Object()
		c := Citation{ID: o.Get("id").ID(), Label: o.Get("label").Text(120)}
		if o.Has("note") {
			c.Note = o.Get("note").Text(400)
		}
		if o.Has("url") {
			c.URL = o.Get("url").URL()
		}
		o.Close()
		if seen[c.ID] && r.err == nil {
			r.Fail(errs.Parsef(item.path+".id", "is used by an earlier citation"))
		}
		seen[c.ID] = true
		s.Citations = append(s.Citations, c)
	}
	for _, item := range top.Get("scope_limits").List(1, 20) {
		s.ScopeLimits = append(s.ScopeLimits, item.Text(300))
	}
	s.Body = top.Get("body").Object()
	s.WorkedExamples = top.Get("worked_examples").List(1, 50)
	top.Close()

	if err := r.Err(); err != nil {
		return nil, err
	}
	return s, nil
}
