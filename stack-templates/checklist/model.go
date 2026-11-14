package checklist

import (
	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/errs"
)

// Item is one compliance question in a checklist tool.
type Item struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	AllowNA  bool   `json:"na_allowed,omitempty"`
	Citation string `json:"citation"`
	Help     string `json:"help,omitempty"`
}

// Cite is a citation serialized into data/tool.json.
type Cite struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Tool is the complete checklist definition written to data/tool.json.
type Tool struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	ScopeLimits []string `json:"scope_limits"`
	Citations   []Cite   `json:"citations"`
	Items       []Item   `json:"items"`
}

// Expect is the expected outcome of one worked example.
type Expect struct {
	Status        string `json:"status"`
	UnmetRequired int    `json:"unmet_required"`
	YesCount      int    `json:"yes_count"`
	NoCount       int    `json:"no_count"`
	NACount       int    `json:"na_count"`
}

// Example is one worked example checked at build time and shipped in tests/.
type Example struct {
	ID      string            `json:"id"`
	Label   string            `json:"label"`
	Answers map[string]string `json:"answers"`
	Expect  Expect            `json:"expect"`
}

func parse(spec *buildspec.Spec) (*Tool, []Example, error) {
	r := spec.Reader()
	tool := &Tool{
		Slug:        spec.Slug,
		Title:       spec.Title,
		Summary:     spec.Summary,
		ScopeLimits: spec.ScopeLimits,
	}
	for _, c := range spec.Citations {
		tool.Citations = append(tool.Citations, Cite{
			ID:    c.ID,
			Label: c.Label,
			Note:  c.Note,
			URL:   c.URL,
		})
	}

	body := spec.Body
	seenIDs := map[string]bool{}
	for _, rawItem := range body.Get("items").List(1, 60) {
		o := rawItem.Object()
		it := Item{
			ID:       o.Get("id").ID(),
			Label:    o.Get("label").Text(240),
			Kind:     "yes_no",
			Required: o.Get("required").Bool(),
			Citation: o.Get("citation").ID(),
		}
		if o.Has("kind") {
			it.Kind = o.Get("kind").ID()
		}
		if o.Has("na_allowed") {
			it.AllowNA = o.Get("na_allowed").Bool()
		}
		if o.Has("help") {
			it.Help = o.Get("help").Text(300)
		}
		o.Close()

		if r.Err() == nil && it.Kind != "yes_no" {
			r.Fail(errs.NeedsSubtoolf(rawItem.Path()+".kind", "checklist item kind has no rule"))
		}
		if r.Err() == nil && seenIDs[it.ID] {
			r.Fail(errs.Parsef(rawItem.Path()+".id", "is used by an earlier item"))
		}
		seenIDs[it.ID] = true
		if r.Err() == nil && !spec.HasCitation(it.Citation) {
			r.Fail(errs.Parsef(rawItem.Path()+".citation", "does not name one of the specification's citations"))
		}
		tool.Items = append(tool.Items, it)
	}
	body.Close()
	if err := r.Err(); err != nil {
		return nil, nil, err
	}

	seenExamples := map[string]bool{}
	var examples []Example
	for _, rawEx := range spec.WorkedExamples {
		o := rawEx.Object()
		ex := Example{
			ID:      o.Get("id").ID(),
			Label:   o.Get("label").Text(160),
			Answers: map[string]string{},
		}
		ansObj := o.Get("answers").Object()
		for _, k := range ansObj.Keys() {
			ex.Answers[k] = ansObj.Get(k).ID()
		}
		ansObj.Close()

		expObj := o.Get("expect").Object()
		ex.Expect = Expect{
			Status:        expObj.Get("status").ID(),
			UnmetRequired: expObj.Get("unmet_required").Int(0, 60),
			YesCount:      expObj.Get("yes_count").Int(0, 60),
			NoCount:       expObj.Get("no_count").Int(0, 60),
			NACount:       expObj.Get("na_count").Int(0, 60),
		}
		expObj.Close()
		o.Close()
		if err := r.Err(); err != nil {
			return nil, nil, err
		}
		if seenExamples[ex.ID] {
			return nil, nil, errs.Parsef(rawEx.Path()+".id", "is used by an earlier worked example")
		}
		seenExamples[ex.ID] = true
		if ex.Expect.Status != "complete" && ex.Expect.Status != "incomplete" {
			return nil, nil, errs.Parsef(rawEx.Path()+".expect.status", "must be complete or incomplete")
		}
		for k := range ex.Answers {
			if !seenIDs[k] {
				return nil, nil, errs.Parsef(rawEx.Path()+".answers", "names an unknown checklist item")
			}
		}

		res := Evaluate(tool, ex.Answers)
		if len(res.Faults) > 0 {
			return nil, nil, errs.Parsef(rawEx.Path()+".answers", "is missing or has an invalid answer for a checklist item")
		}
		if res.Status != ex.Expect.Status ||
			res.UnmetRequired != ex.Expect.UnmetRequired ||
			res.YesCount != ex.Expect.YesCount ||
			res.NoCount != ex.Expect.NoCount ||
			res.NACount != ex.Expect.NACount {
			return nil, nil, errs.Parsef(rawEx.Path()+".expect", "does not match the checklist evaluation")
		}
		examples = append(examples, ex)
	}
	return tool, examples, nil
}
