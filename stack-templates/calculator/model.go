package calculator

import (
	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/decimal"
	"github.com/ClearedStack/moldloft/internal/errs"
)

// The types below are the calculator's definition. They are checked here and
// then written, unchanged, to data/tool.json in the generated repository,
// where the browser reads them.

type Input struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Help     string `json:"help,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Decimals int    `json:"decimals"`
	Min      string `json:"min,omitempty"`
	Max      string `json:"max,omitempty"`
	Table    string `json:"table,omitempty"`
	Default  string `json:"default"`
}

type Constant struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Value    string `json:"value"`
	Unit     string `json:"unit,omitempty"`
	Citation string `json:"citation,omitempty"`
}

type Row struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

type Table struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Unit     string `json:"unit,omitempty"`
	Citation string `json:"citation,omitempty"`
	Rows     []Row  `json:"rows"`
}

type Step struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Op       string   `json:"op"`
	Args     []string `json:"args"`
	Decimals int      `json:"decimals"`
	Unit     string   `json:"unit,omitempty"`
	Citation string   `json:"citation,omitempty"`
}

type Citation struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	URL   string `json:"url,omitempty"`
}

type Tool struct {
	ContractVersion string     `json:"contract_version"`
	Template        string     `json:"template"`
	Slug            string     `json:"slug"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	ScopeLimits     []string   `json:"scope_limits"`
	Citations       []Citation `json:"citations"`
	Inputs          []Input    `json:"inputs"`
	Constants       []Constant `json:"constants"`
	Tables          []Table    `json:"tables"`
	Steps           []Step     `json:"steps"`
	Outputs         []string   `json:"outputs"`
}

// Example is one worked example: the inputs to enter and the results that
// must come out.
type Example struct {
	ID     string            `json:"id"`
	Label  string            `json:"label"`
	Inputs map[string]string `json:"inputs"`
	Expect map[string]string `json:"expect"`
}

// ops lists the arithmetic the template supports and how many arguments each
// takes. An operation that is not here is not guessed at: the item is paused
// with E_NEEDS_SUBTOOL.
var ops = map[string]struct{ min, max int }{
	"add": {2, 8}, // a + b + ...
	"sub": {2, 2}, // a - b
	"mul": {2, 8}, // a * b * ...
	"div": {2, 2}, // a / b
	"pct": {2, 2}, // b percent of a
	"min": {2, 8}, // the smallest
	"max": {2, 8}, // the largest
}

func (t *Tool) table(id string) *Table {
	for i := range t.Tables {
		if t.Tables[i].ID == id {
			return &t.Tables[i]
		}
	}
	return nil
}

func (t *Tool) step(id string) *Step {
	for i := range t.Steps {
		if t.Steps[i].ID == id {
			return &t.Steps[i]
		}
	}
	return nil
}

// parse checks the pattern-specific part of the specification and returns the
// calculator's definition and its worked examples.
func parse(spec *buildspec.Spec) (*Tool, []Example, error) {
	r := spec.Reader()
	body := spec.Body
	t := &Tool{
		ContractVersion: buildspec.ContractVersion,
		Template:        Name,
		Slug:            spec.Slug,
		Title:           spec.Title,
		Summary:         spec.Summary,
		ScopeLimits:     spec.ScopeLimits,
		Constants:       []Constant{},
		Tables:          []Table{},
	}
	for _, c := range spec.Citations {
		t.Citations = append(t.Citations, Citation(c))
	}

	// citation reads an optional reference to one of the spec's citations.
	citation := func(o *buildspec.Object) string {
		if !o.Has("citation") {
			return ""
		}
		v := o.Get("citation")
		id := v.ID()
		if id != "" && !spec.HasCitation(id) {
			r.Fail(errs.Parsef(v.Path(), "does not name a citation"))
		}
		return id
	}
	optText := func(o *buildspec.Object, key string, max int) string {
		if !o.Has(key) {
			return ""
		}
		return o.Get(key).Text(max)
	}
	// names holds every identifier a step may refer to. Inputs, constants and
	// steps share one set of names.
	names := map[string]bool{}
	claim := func(v buildspec.Value) string {
		id := v.ID()
		if id != "" && names[id] {
			r.Fail(errs.Parsef(v.Path(), "is used by an earlier input, constant or step"))
		}
		names[id] = true
		return id
	}

	// Tables come first so that choice inputs can be checked against them.
	if body.Has("tables") {
		tableIDs := map[string]bool{}
		for _, item := range body.Get("tables").List(0, 20) {
			o := item.Object()
			tb := Table{ID: o.Get("id").ID(), Label: o.Get("label").Text(120), Unit: optText(o, "unit", 40), Citation: citation(o)}
			if tb.ID != "" && tableIDs[tb.ID] {
				r.Fail(errs.Parsef(item.Path()+".id", "is used by an earlier table"))
			}
			tableIDs[tb.ID] = true
			keys := map[string]bool{}
			for _, rowItem := range o.Get("rows").List(1, 200) {
				ro := rowItem.Object()
				row := Row{Key: ro.Get("key").ID(), Label: ro.Get("label").Text(120)}
				_, row.Value = ro.Get("value").Decimal()
				ro.Close()
				if row.Key != "" && keys[row.Key] {
					r.Fail(errs.Parsef(rowItem.Path()+".key", "is used by an earlier row"))
				}
				keys[row.Key] = true
				tb.Rows = append(tb.Rows, row)
			}
			o.Close()
			t.Tables = append(t.Tables, tb)
		}
	}

	for _, item := range body.Get("inputs").List(1, 40) {
		o := item.Object()
		in := Input{ID: claim(o.Get("id")), Label: o.Get("label").Text(120), Help: optText(o, "help", 300)}
		kind := o.Get("kind")
		in.Kind = kind.ID()
		switch in.Kind {
		case "number":
			in.Unit = optText(o, "unit", 40)
			in.Decimals = o.Get("decimals").Int(0, decimal.MaxScale)
			lo, loText := o.Get("min").Decimal()
			hi, hiText := o.Get("max").Decimal()
			def := o.Get("default")
			d, defText := def.Decimal()
			in.Min, in.Max, in.Default = loText, hiText, defText
			if r.Err() == nil {
				switch {
				case lo.Scale > in.Decimals:
					r.Fail(errs.Parsef(item.Path()+".min", "has more decimal places than the input allows"))
				case hi.Scale > in.Decimals:
					r.Fail(errs.Parsef(item.Path()+".max", "has more decimal places than the input allows"))
				case lo.Cmp(hi) > 0:
					r.Fail(errs.Parsef(item.Path()+".min", "is greater than max"))
				case d.Scale > in.Decimals:
					r.Fail(errs.Parsef(def.Path(), "has more decimal places than the input allows"))
				case d.Cmp(lo) < 0 || d.Cmp(hi) > 0:
					r.Fail(errs.Parsef(def.Path(), "is outside min and max"))
				}
			}
		case "choice":
			tv := o.Get("table")
			in.Table = tv.ID()
			def := o.Get("default")
			in.Default = def.ID()
			if r.Err() == nil {
				tb := t.table(in.Table)
				if tb == nil {
					r.Fail(errs.Parsef(tv.Path(), "does not name a table"))
				} else {
					in.Unit = tb.Unit
					found := false
					for _, row := range tb.Rows {
						found = found || row.Key == in.Default
					}
					if !found {
						r.Fail(errs.Parsef(def.Path(), "does not name a row of the table"))
					}
				}
			}
		case "":
		default:
			if r.Err() == nil {
				r.Fail(errs.NeedsSubtoolf(kind.Path(), "the calculator template has no rule for this kind of input"))
			}
		}
		o.Close()
		t.Inputs = append(t.Inputs, in)
	}

	if body.Has("constants") {
		for _, item := range body.Get("constants").List(0, 40) {
			o := item.Object()
			c := Constant{ID: claim(o.Get("id")), Label: o.Get("label").Text(120), Unit: optText(o, "unit", 40), Citation: citation(o)}
			_, c.Value = o.Get("value").Decimal()
			o.Close()
			t.Constants = append(t.Constants, c)
		}
	}

	for _, item := range body.Get("steps").List(1, 80) {
		o := item.Object()
		st := Step{Label: o.Get("label").Text(120), Unit: optText(o, "unit", 40), Citation: citation(o)}
		opv := o.Get("op")
		st.Op = opv.ID()
		rule, known := ops[st.Op]
		if st.Op != "" && !known && r.Err() == nil {
			r.Fail(errs.NeedsSubtoolf(opv.Path(), "the calculator template has no rule for this operation"))
		}
		argv := o.Get("args")
		for _, a := range argv.List(1, 8) {
			ref := a.ID()
			// A step may only use what is defined above it, so the steps are
			// already in the order they must be worked out.
			if ref != "" && !names[ref] {
				r.Fail(errs.Parsef(a.Path(), "does not name an input, a constant or an earlier step"))
			}
			st.Args = append(st.Args, ref)
		}
		if known && r.Err() == nil && (len(st.Args) < rule.min || len(st.Args) > rule.max) {
			r.Fail(errs.Parsef(argv.Path(), "is the wrong number of arguments for the operation"))
		}
		st.Decimals = o.Get("decimals").Int(0, decimal.MaxScale)
		st.ID = claim(o.Get("id"))
		o.Close()
		t.Steps = append(t.Steps, st)
	}

	seenOut := map[string]bool{}
	for _, item := range body.Get("outputs").List(1, 10) {
		id := item.ID()
		if r.Err() == nil {
			switch {
			case t.step(id) == nil:
				r.Fail(errs.Parsef(item.Path(), "does not name a step"))
			case seenOut[id]:
				r.Fail(errs.Parsef(item.Path(), "is listed twice"))
			}
		}
		seenOut[id] = true
		t.Outputs = append(t.Outputs, id)
	}
	body.Close()
	if err := r.Err(); err != nil {
		return nil, nil, err
	}

	examples, err := parseExamples(spec, t)
	if err != nil {
		return nil, nil, err
	}
	return t, examples, nil
}

// parseExamples reads the worked examples and works each one out. An example
// whose stated results differ from the calculation makes the whole
// specification E_PARSE: either the rule or the example is wrong, and Moldloft
// cannot tell which.
func parseExamples(spec *buildspec.Spec, t *Tool) ([]Example, error) {
	r := spec.Reader()
	var examples []Example
	ids := map[string]bool{}
	for _, item := range spec.WorkedExamples {
		o := item.Object()
		ex := Example{ID: o.Get("id").ID(), Label: o.Get("label").Text(160), Inputs: map[string]string{}, Expect: map[string]string{}}
		if ex.ID != "" && ids[ex.ID] {
			r.Fail(errs.Parsef(item.Path()+".id", "is used by an earlier worked example"))
		}
		ids[ex.ID] = true

		inputs := o.Get("inputs").Object()
		for _, in := range t.Inputs {
			if r.Err() != nil {
				break
			}
			if !inputs.Has(in.ID) {
				r.Fail(errs.Parsef(inputs.Path(), "does not give a value for every input"))
			}
		}
		for _, key := range inputs.Keys() {
			v := inputs.Get(key)
			if in := inputOf(t, key); in == nil {
				r.Fail(errs.Parsef(inputs.Path(), "gives a value for something that is not an input"))
			} else if in.Kind == "choice" {
				ex.Inputs[key] = v.ID()
			} else {
				_, ex.Inputs[key] = v.Decimal()
			}
		}

		expect := o.Get("expect").Object()
		for _, out := range t.Outputs {
			if r.Err() != nil {
				break
			}
			if !expect.Has(out) {
				r.Fail(errs.Parsef(expect.Path(), "does not state a result for every output"))
			}
		}
		for _, key := range expect.Keys() {
			v := expect.Get(key)
			if t.step(key) == nil {
				r.Fail(errs.Parsef(expect.Path(), "states a result for something that is not a step"))
			}
			_, ex.Expect[key] = v.Decimal()
		}
		o.Close()
		if err := r.Err(); err != nil {
			return nil, err
		}

		result := Evaluate(t, ex.Inputs)
		if len(result.Faults) > 0 {
			f := result.Faults[0]
			where := inputs.Path() + "." + f.ID
			if t.step(f.ID) != nil {
				where = item.Path()
			}
			return nil, errs.Parsef(where, "%s", faultReasons[f.Kind])
		}
		for _, key := range sortedKeys(ex.Expect) {
			if result.Values[key] != ex.Expect[key] {
				return nil, errs.Parsef(expect.Path()+"."+key, "does not match the calculation")
			}
		}
		examples = append(examples, ex)
	}
	return examples, nil
}

func inputOf(t *Tool, id string) *Input {
	for i := range t.Inputs {
		if t.Inputs[i].ID == id {
			return &t.Inputs[i]
		}
	}
	return nil
}
