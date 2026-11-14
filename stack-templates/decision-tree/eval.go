package decisiontree

import "strings"

// Step records one answered question along the evaluated branch.
type Step struct {
	Question string `json:"question"`
	Option   string `json:"option"`
	Label    string `json:"label"`
}

// Fault records an invalid option key supplied for a question.
type Fault struct {
	Question string `json:"question"`
	Kind     string `json:"kind"`
}

// Result is the deterministic outcome of walking the decision tree.
type Result struct {
	Complete        bool    `json:"complete"`
	CurrentQuestion string  `json:"current_question,omitempty"`
	Outcome         string  `json:"outcome,omitempty"`
	Title           string  `json:"title,omitempty"`
	Summary         string  `json:"summary,omitempty"`
	Status          string  `json:"status,omitempty"`
	Citation        string  `json:"citation,omitempty"`
	Steps           int     `json:"steps"`
	Path            []Step  `json:"path"`
	Faults          []Fault `json:"faults"`
}

// Evaluate walks tool starting from tool.Start using answers.
func Evaluate(tool *Tool, answers map[string]string) Result {
	questions := map[string]Question{}
	for _, q := range tool.Questions {
		questions[q.ID] = q
	}
	outcomes := map[string]Outcome{}
	for _, out := range tool.Outcomes {
		outcomes[out.ID] = out
	}

	path := []Step{}
	faults := []Fault{}
	seen := map[string]bool{}
	cur := tool.Start

	for {
		if seen[cur] {
			faults = append(faults, Fault{Question: cur, Kind: "cycle"})
			return Result{Complete: false, CurrentQuestion: cur, Steps: len(path), Path: path, Faults: faults}
		}
		seen[cur] = true
		q, ok := questions[cur]
		if !ok {
			faults = append(faults, Fault{Question: cur, Kind: "unknown_question"})
			return Result{Complete: false, CurrentQuestion: cur, Steps: len(path), Path: path, Faults: faults}
		}
		ans := strings.TrimSpace(answers[cur])
		if ans == "" {
			return Result{
				Complete:        false,
				CurrentQuestion: cur,
				Steps:           len(path),
				Path:            path,
				Faults:          faults,
			}
		}
		var matched *Option
		for i := range q.Options {
			if q.Options[i].Key == ans {
				matched = &q.Options[i]
				break
			}
		}
		if matched == nil {
			faults = append(faults, Fault{Question: cur, Kind: "invalid_option"})
			return Result{
				Complete:        false,
				CurrentQuestion: cur,
				Steps:           len(path),
				Path:            path,
				Faults:          faults,
			}
		}
		path = append(path, Step{
			Question: q.ID,
			Option:   matched.Key,
			Label:    matched.Label,
		})
		if out, isOut := outcomes[matched.Next]; isOut {
			return Result{
				Complete: true,
				Outcome:  out.ID,
				Title:    out.Title,
				Summary:  out.Summary,
				Status:   out.Status,
				Citation: out.Citation,
				Steps:    len(path),
				Path:     path,
				Faults:   faults,
			}
		}
		cur = matched.Next
	}
}
