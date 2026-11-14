package decisiontree

import (
	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/errs"
)

// Option is one selectable branch under a question.
type Option struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Next  string `json:"next"`
}

// Question is one branching node in the decision graph.
type Question struct {
	ID       string   `json:"id"`
	Prompt   string   `json:"prompt"`
	Help     string   `json:"help,omitempty"`
	Citation string   `json:"citation"`
	Options  []Option `json:"options"`
}

// Outcome is one terminal determination in the decision graph.
type Outcome struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Citation string `json:"citation"`
}

// Cite is one citation serialized into data/tool.json.
type Cite struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Tool is the complete decision-tree definition written to data/tool.json.
type Tool struct {
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	ScopeLimits []string   `json:"scope_limits"`
	Citations   []Cite     `json:"citations"`
	Start       string     `json:"start"`
	Questions   []Question `json:"questions"`
	Outcomes    []Outcome  `json:"outcomes"`
}

// Expect is the expected terminal outcome of one worked example.
type Expect struct {
	Outcome string `json:"outcome"`
	Status  string `json:"status"`
	Steps   int    `json:"steps"`
}

// Example is one worked example checked at build time and shipped in tests/.
type Example struct {
	ID      string            `json:"id"`
	Label   string            `json:"label"`
	Answers map[string]string `json:"answers"`
	Expect  Expect            `json:"expect"`
}

func validOutcomeStatus(s string) bool {
	return s == "eligible" || s == "ineligible" || s == "review"
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
	tool.Start = body.Get("start").ID()

	questionsByID := map[string]*Question{}
	outcomesByID := map[string]*Outcome{}

	for _, rawQ := range body.Get("questions").List(1, 40) {
		o := rawQ.Object()
		kind := "choice"
		if o.Has("kind") {
			kind = o.Get("kind").ID()
		}
		q := Question{
			ID:       o.Get("id").ID(),
			Prompt:   o.Get("prompt").Text(240),
			Citation: o.Get("citation").ID(),
		}
		if o.Has("help") {
			q.Help = o.Get("help").Text(240)
		}
		seenOpt := map[string]bool{}
		for _, rawOpt := range o.Get("options").List(2, 10) {
			oo := rawOpt.Object()
			opt := Option{
				Key:   oo.Get("key").ID(),
				Label: oo.Get("label").Text(120),
				Next:  oo.Get("next").ID(),
			}
			oo.Close()
			if r.Err() == nil && seenOpt[opt.Key] {
				r.Fail(errs.Parsef(rawOpt.Path()+".key", "is used by an earlier option on this question"))
			}
			seenOpt[opt.Key] = true
			q.Options = append(q.Options, opt)
		}
		o.Close()

		if r.Err() == nil && kind != "choice" {
			r.Fail(errs.NeedsSubtoolf(rawQ.Path()+".kind", "question kind has no rule"))
		}
		if r.Err() == nil && questionsByID[q.ID] != nil {
			r.Fail(errs.Parsef(rawQ.Path()+".id", "is used by an earlier question"))
		}
		if r.Err() == nil && !spec.HasCitation(q.Citation) {
			r.Fail(errs.Parsef(rawQ.Path()+".citation", "does not name one of the specification's citations"))
		}
		qCopy := q
		questionsByID[q.ID] = &qCopy
		tool.Questions = append(tool.Questions, q)
	}

	for _, rawOut := range body.Get("outcomes").List(1, 20) {
		o := rawOut.Object()
		out := Outcome{
			ID:       o.Get("id").ID(),
			Title:    o.Get("title").Text(120),
			Summary:  o.Get("summary").Text(300),
			Status:   o.Get("status").ID(),
			Citation: o.Get("citation").ID(),
		}
		o.Close()
		if r.Err() == nil && (questionsByID[out.ID] != nil || outcomesByID[out.ID] != nil) {
			r.Fail(errs.Parsef(rawOut.Path()+".id", "is already used by a question or earlier outcome"))
		}
		if r.Err() == nil && !validOutcomeStatus(out.Status) {
			r.Fail(errs.Parsef(rawOut.Path()+".status", "must be eligible, ineligible or review"))
		}
		if r.Err() == nil && !spec.HasCitation(out.Citation) {
			r.Fail(errs.Parsef(rawOut.Path()+".citation", "does not name one of the specification's citations"))
		}
		outCopy := out
		outcomesByID[out.ID] = &outCopy
		tool.Outcomes = append(tool.Outcomes, out)
	}
	body.Close()
	if err := r.Err(); err != nil {
		return nil, nil, err
	}

	if questionsByID[tool.Start] == nil {
		return nil, nil, errs.Parsef("body.start", "does not name one of the decision-tree questions")
	}
	for qi, q := range tool.Questions {
		for oi, opt := range q.Options {
			if questionsByID[opt.Next] == nil && outcomesByID[opt.Next] == nil {
				return nil, nil, errs.Parsef(
					indexPath("body.questions", qi)+".options"+indexSuffix(oi)+".next",
					"does not name a question or outcome",
				)
			}
		}
	}

	// Verify the graph reachable from start is acyclic and reaches every node.
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	qState := map[string]int{}
	reachedOutcomes := map[string]bool{}
	var dfs func(id string) error
	dfs = func(id string) error {
		switch qState[id] {
		case visiting:
			return errs.Parsef("body.questions", "decision graph contains a cycle")
		case visited:
			return nil
		}
		qState[id] = visiting
		q := questionsByID[id]
		for _, opt := range q.Options {
			if outcomesByID[opt.Next] != nil {
				reachedOutcomes[opt.Next] = true
				continue
			}
			if err := dfs(opt.Next); err != nil {
				return err
			}
		}
		qState[id] = visited
		return nil
	}
	if err := dfs(tool.Start); err != nil {
		return nil, nil, err
	}
	for qi, q := range tool.Questions {
		if qState[q.ID] != visited {
			return nil, nil, errs.Parsef(indexPath("body.questions", qi)+".id", "is not reachable from start")
		}
	}
	for oi, out := range tool.Outcomes {
		if !reachedOutcomes[out.ID] {
			return nil, nil, errs.Parsef(indexPath("body.outcomes", oi)+".id", "is not reachable from start")
		}
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
			val := ansObj.Get(k).ID()
			if questionsByID[k] == nil && r.Err() == nil {
				r.Fail(errs.Parsef(rawEx.Path()+".answers."+k, "names a question that is not in this decision tree"))
			}
			ex.Answers[k] = val
		}
		ansObj.Close()
		expObj := o.Get("expect").Object()
		ex.Expect = Expect{
			Outcome: expObj.Get("outcome").ID(),
			Status:  expObj.Get("status").ID(),
			Steps:   expObj.Get("steps").Int(1, 40),
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

		res := Evaluate(tool, ex.Answers)
		if !res.Complete || len(res.Faults) > 0 {
			return nil, nil, errs.Parsef(rawEx.Path()+".answers", "does not reach an outcome cleanly")
		}
		usedQuestions := map[string]bool{}
		for _, step := range res.Path {
			usedQuestions[step.Question] = true
		}
		for k := range ex.Answers {
			if !usedQuestions[k] {
				return nil, nil, errs.Parsef(rawEx.Path()+".answers."+k, "answers a question not visited along this branch")
			}
		}
		if res.Outcome != ex.Expect.Outcome || res.Status != ex.Expect.Status || res.Steps != ex.Expect.Steps {
			return nil, nil, errs.Parsef(rawEx.Path()+".expect", "does not match the decision-tree evaluation")
		}
		examples = append(examples, ex)
	}
	return tool, examples, nil
}

func indexPath(prefix string, i int) string {
	return prefix + indexSuffix(i)
}

func indexSuffix(i int) string {
	var digits [20]byte
	pos := len(digits)
	n := i
	if n == 0 {
		return "[0]"
	}
	for n > 0 {
		pos--
		digits[pos] = byte('0' + n%10)
		n /= 10
	}
	return "[" + string(digits[pos:]) + "]"
}
