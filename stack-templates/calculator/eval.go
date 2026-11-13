package calculator

import (
	"math/big"
	"sort"

	"github.com/ClearedStack/moldloft/internal/decimal"
)

// Fault says why an input was refused or a step could not be worked out.
// The kinds are the same ones files/js/engine.js reports in the browser.
type Fault struct {
	ID   string
	Kind string
}

// Result is the outcome of one calculation: every value that could be worked
// out, written as a decimal with the declared number of places, and every
// fault met on the way.
type Result struct {
	Values map[string]string
	Faults []Fault
}

var faultReasons = map[string]string{
	"missing":        "has no value",
	"format":         "is not a plain decimal number",
	"decimals":       "has more decimal places than the input allows",
	"range":          "is outside the input's min and max",
	"choice":         "does not name a row of the input's table",
	"divide_by_zero": "makes a step divide by zero",
	"overflow":       "makes a step's result too large",
}

// limit is the first whole number a result may not reach: 10^15.
var limit = decimal.D{Units: new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil)}

// Evaluate works the calculator out for one set of raw input strings. It must
// behave exactly as evaluate in files/js/engine.js does; the golden fixture's
// worked examples are run through both.
func Evaluate(t *Tool, raw map[string]string) Result {
	res := Result{Values: map[string]string{}}
	vals := map[string]decimal.D{}
	fault := func(id, kind string) { res.Faults = append(res.Faults, Fault{id, kind}) }
	keep := func(id string, d decimal.D) {
		vals[id] = d
		res.Values[id] = d.String()
	}

	for _, in := range t.Inputs {
		text, ok := raw[in.ID]
		if !ok || text == "" {
			fault(in.ID, "missing")
			continue
		}
		if in.Kind == "choice" {
			found := false
			for _, row := range t.table(in.Table).Rows {
				if row.Key == text {
					d, _ := decimal.Parse(row.Value)
					keep(in.ID, d)
					found = true
				}
			}
			if !found {
				fault(in.ID, "choice")
			}
			continue
		}
		d, ok := decimal.Parse(text)
		lo, _ := decimal.Parse(in.Min)
		hi, _ := decimal.Parse(in.Max)
		switch {
		case !ok:
			fault(in.ID, "format")
		case d.Scale > in.Decimals:
			fault(in.ID, "decimals")
		case d.Cmp(lo) < 0 || d.Cmp(hi) > 0:
			fault(in.ID, "range")
		default:
			keep(in.ID, d.Rescale(in.Decimals))
		}
	}
	for _, c := range t.Constants {
		d, _ := decimal.Parse(c.Value)
		keep(c.ID, d)
	}

steps:
	for _, st := range t.Steps {
		args := make([]decimal.D, len(st.Args))
		for i, ref := range st.Args {
			d, ok := vals[ref]
			if !ok {
				// Something above failed and has already been reported.
				continue steps
			}
			args[i] = d
		}
		acc := args[0]
		switch st.Op {
		case "add":
			for _, a := range args[1:] {
				acc = acc.Add(a)
			}
		case "sub":
			acc = acc.Sub(args[1])
		case "mul":
			for _, a := range args[1:] {
				acc = acc.Mul(a)
			}
		case "pct":
			acc = acc.Percent(args[1])
		case "div":
			if args[1].IsZero() {
				fault(st.ID, "divide_by_zero")
				continue steps
			}
			acc = acc.Div(args[1], st.Decimals)
		case "min":
			for _, a := range args[1:] {
				if a.Cmp(acc) < 0 {
					acc = a
				}
			}
		case "max":
			for _, a := range args[1:] {
				if a.Cmp(acc) > 0 {
					acc = a
				}
			}
		}
		acc = acc.Rescale(st.Decimals)
		if acc.Abs().Cmp(limit) >= 0 {
			fault(st.ID, "overflow")
			continue
		}
		keep(st.ID, acc)
	}
	return res
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
