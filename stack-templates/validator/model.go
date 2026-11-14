package validator

import (
	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/errs"
)

// LookupTable holds an allowed set of code strings.
type LookupTable struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Citation string   `json:"citation,omitempty"`
	Codes    []string `json:"codes"`
}

// Column defines one column of a delimited input record.
type Column struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Rule     string `json:"rule"`
	Citation string `json:"citation"`
	MinLen   int    `json:"min_len,omitempty"`
	MaxLen   int    `json:"max_len,omitempty"`
	Table    string `json:"table,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
}

// Cite is one citation serialized into data/tool.json.
type Cite struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Tool is the complete validator definition written to data/tool.json.
type Tool struct {
	Slug        string        `json:"slug"`
	Title       string        `json:"title"`
	Summary     string        `json:"summary"`
	ScopeLimits []string      `json:"scope_limits"`
	Citations   []Cite        `json:"citations"`
	Delimiter   string        `json:"delimiter"`
	Separator   string        `json:"separator"`
	HasHeader   bool          `json:"has_header"`
	Tables      []LookupTable `json:"tables,omitempty"`
	Columns     []Column      `json:"columns"`
}

// Expect is the expected outcome of one worked example.
type Expect struct {
	Valid      bool `json:"valid"`
	RowCount   int  `json:"row_count"`
	ErrorCount int  `json:"error_count"`
}

// Example is one worked example checked at build time and shipped in tests/.
type Example struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Input  string `json:"input"`
	Expect Expect `json:"expect"`
}

func separatorFor(delim string) (string, bool) {
	switch delim {
	case "comma":
		return ",", true
	case "pipe":
		return "|", true
	case "colon":
		return ":", true
	case "tab":
		return "\t", true
	default:
		return "", false
	}
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
	tool.Delimiter = body.Get("delimiter").ID()
	sep, delimOK := separatorFor(tool.Delimiter)
	if r.Err() == nil && !delimOK {
		r.Fail(errs.NeedsSubtoolf(body.Path()+".delimiter", "delimiter has no rule"))
	}
	tool.Separator = sep
	tool.HasHeader = body.Get("has_header").Bool()

	tablesByID := map[string]*LookupTable{}
	if body.Has("tables") {
		for _, rawTable := range body.Get("tables").List(1, 20) {
			o := rawTable.Object()
			tb := LookupTable{
				ID:    o.Get("id").ID(),
				Label: o.Get("label").Text(120),
			}
			if o.Has("citation") {
				tb.Citation = o.Get("citation").ID()
			}
			seenCode := map[string]bool{}
			for _, cv := range o.Get("codes").List(1, 500) {
				code := cv.Text(64)
				if r.Err() == nil && seenCode[code] {
					r.Fail(errs.Parsef(cv.Path(), "repeats an earlier code in the same table"))
				}
				seenCode[code] = true
				tb.Codes = append(tb.Codes, code)
			}
			o.Close()
			if r.Err() == nil && tablesByID[tb.ID] != nil {
				r.Fail(errs.Parsef(rawTable.Path()+".id", "is used by an earlier table"))
			}
			if r.Err() == nil && tb.Citation != "" && !spec.HasCitation(tb.Citation) {
				r.Fail(errs.Parsef(rawTable.Path()+".citation", "does not name one of the specification's citations"))
			}
			tbCopy := tb
			tablesByID[tb.ID] = &tbCopy
			tool.Tables = append(tool.Tables, tb)
		}
	}

	seenCols := map[string]bool{}
	for _, rawCol := range body.Get("columns").List(1, 20) {
		o := rawCol.Object()
		col := Column{
			ID:       o.Get("id").ID(),
			Label:    o.Get("label").Text(120),
			Required: o.Get("required").Bool(),
			Rule:     o.Get("rule").ID(),
			Citation: o.Get("citation").ID(),
		}
		if o.Has("min_len") {
			col.MinLen = o.Get("min_len").Int(0, 200)
		}
		if o.Has("max_len") {
			col.MaxLen = o.Get("max_len").Int(1, 200)
		}
		if o.Has("table") {
			col.Table = o.Get("table").ID()
		}
		if o.Has("prefix") {
			col.Prefix = o.Get("prefix").Text(16)
		}
		o.Close()

		if r.Err() == nil && seenCols[col.ID] {
			r.Fail(errs.Parsef(rawCol.Path()+".id", "is used by an earlier column"))
		}
		seenCols[col.ID] = true
		if r.Err() == nil && !spec.HasCitation(col.Citation) {
			r.Fail(errs.Parsef(rawCol.Path()+".citation", "does not name one of the specification's citations"))
		}
		if r.Err() == nil {
			switch col.Rule {
			case "text", "digits", "upper_code":
				if col.MaxLen == 0 {
					col.MaxLen = 64
				}
				if col.MinLen > col.MaxLen {
					r.Fail(errs.Parsef(rawCol.Path()+".min_len", "is greater than max_len"))
				}
				if col.Table != "" {
					r.Fail(errs.Parsef(rawCol.Path()+".table", "is only allowed when rule is in_table"))
				}
			case "in_table":
				if col.Table == "" || tablesByID[col.Table] == nil {
					r.Fail(errs.Parsef(rawCol.Path()+".table", "does not name one of the validator tables"))
				}
			default:
				r.Fail(errs.NeedsSubtoolf(rawCol.Path()+".rule", "column rule has no rule"))
			}
		}
		tool.Columns = append(tool.Columns, col)
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
			ID:    o.Get("id").ID(),
			Label: o.Get("label").Text(160),
			Input: o.Get("input").RawString(8192, true),
		}
		expObj := o.Get("expect").Object()
		ex.Expect = Expect{
			Valid:      expObj.Get("valid").Bool(),
			RowCount:   expObj.Get("row_count").Int(0, 500),
			ErrorCount: expObj.Get("error_count").Int(0, 1000),
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

		res := Evaluate(tool, ex.Input)
		if res.Valid != ex.Expect.Valid || res.RowCount != ex.Expect.RowCount || res.ErrorCount != ex.Expect.ErrorCount {
			return nil, nil, errs.Parsef(rawEx.Path()+".expect", "does not match the validator evaluation")
		}
		examples = append(examples, ex)
	}
	return tool, examples, nil
}
