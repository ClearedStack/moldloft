package checklist

// Fault records an unanswered or invalid checklist item.
type Fault struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// Result is the deterministic outcome of evaluating a checklist.
type Result struct {
	Status        string  `json:"status"`
	UnmetRequired int     `json:"unmet_required"`
	YesCount      int     `json:"yes_count"`
	NoCount       int     `json:"no_count"`
	NACount       int     `json:"na_count"`
	Faults        []Fault `json:"faults"`
}

// Evaluate checks answers against tool's items in item order.
func Evaluate(tool *Tool, answers map[string]string) Result {
	out := Result{Faults: []Fault{}}
	for _, it := range tool.Items {
		ans, ok := answers[it.ID]
		if !ok || ans == "" {
			out.Faults = append(out.Faults, Fault{ID: it.ID, Kind: "missing"})
			continue
		}
		switch ans {
		case "yes":
			out.YesCount++
		case "no":
			out.NoCount++
			if it.Required {
				out.UnmetRequired++
			}
		case "na":
			if !it.AllowNA {
				out.Faults = append(out.Faults, Fault{ID: it.ID, Kind: "choice"})
				continue
			}
			out.NACount++
		default:
			out.Faults = append(out.Faults, Fault{ID: it.ID, Kind: "choice"})
		}
	}
	if len(out.Faults) > 0 {
		out.Status = "pending"
		return out
	}
	if out.UnmetRequired == 0 {
		out.Status = "complete"
	} else {
		out.Status = "incomplete"
	}
	return out
}
