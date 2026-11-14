package calculator

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ClearedStack/moldloft/internal/buildspec"
)

func goldenTool(t *testing.T) *Tool {
	t.Helper()
	raw, err := os.ReadFile("fixtures/golden/sample-rate-buildup/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := buildspec.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	tool, _, err := parse(spec)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestMatchingRule(t *testing.T) {
	if !Matches(&buildspec.Spec{Pattern: "calculator"}) {
		t.Error("pattern calculator must match")
	}
	for _, p := range []string{"", "Calculator", "calculators", "checklist", "rate-calculator"} {
		if Matches(&buildspec.Spec{Pattern: p}) {
			t.Errorf("pattern %q must not match", p)
		}
	}
}

func TestEvaluateReportsFaults(t *testing.T) {
	tool := goldenTool(t)
	cases := []struct {
		raw  map[string]string
		want []Fault
	}{
		{map[string]string{}, []Fault{{"base_rate", "missing"}, {"hours", "missing"}, {"fee_tier", "missing"}}},
		{map[string]string{"base_rate": "1,000", "hours": "1.25", "fee_tier": "medium"}, []Fault{{"base_rate", "format"}, {"hours", "decimals"}, {"fee_tier", "choice"}}},
		{map[string]string{"base_rate": "-0.01", "hours": "10000.1", "fee_tier": "low"}, []Fault{{"base_rate", "range"}, {"hours", "range"}}},
	}
	for _, c := range cases {
		got := Evaluate(tool, c.raw)
		if fmt.Sprint(got.Faults) != fmt.Sprint(c.want) {
			t.Errorf("%v: faults %v, want %v", c.raw, got.Faults, c.want)
		}
		if _, ok := got.Values["total"]; ok {
			t.Errorf("%v: a total was worked out from refused inputs", c.raw)
		}
	}
}

// TestGoAndJavaScriptAgree works 2,000 fixed pseudo-random input sets out with
// the Go evaluator and then with the JavaScript engine, and requires the same
// values and the same faults from both. The seed is fixed, so the cases are
// the same on every run. Skipped, visibly, on a machine without Node.js.
func TestGoAndJavaScriptAgree(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; Go and JavaScript arithmetic were not compared")
	}
	tool := goldenTool(t)
	rng := rand.New(rand.NewSource(20240607))
	odd := []string{"", "abc", "1e3", "-5", "12.345", "500.01", "0", "500", "10000", "0.005", " 7", "07.50", "+1", "1.", "low", "9999999999999999"}
	tiers := []string{"low", "standard", "high", "high", "standard", "none"}
	type result struct {
		Raw    map[string]string   `json:"raw"`
		Values map[string]string   `json:"values"`
		Faults []map[string]string `json:"faults"`
	}
	var cases []result
	for i := 0; i < 2000; i++ {
		pick := func(max, places int) string {
			if rng.Intn(12) == 0 {
				return odd[rng.Intn(len(odd))]
			}
			scale := 1
			for j := 0; j < places; j++ {
				scale *= 10
			}
			n := rng.Intn(max*scale + 1)
			return fmt.Sprintf("%d.%0*d", n/scale, places, n%scale)
		}
		raw := map[string]string{"base_rate": pick(500, 2), "hours": pick(10000, 1), "fee_tier": tiers[rng.Intn(len(tiers))]}
		got := Evaluate(tool, raw)
		faults := []map[string]string{}
		for _, f := range got.Faults {
			faults = append(faults, map[string]string{"id": f.ID, "kind": f.Kind})
		}
		cases = append(cases, result{raw, got.Values, faults})
	}
	data, err := json.Marshal(map[string]any{"tool": tool, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "testdata/parity.js", path).CombinedOutput()
	if err != nil || string(out) != "same 2000\n" {
		t.Errorf("node testdata/parity.js: %v\n%s", err, out)
	}
}

func TestDateGoAndJavaScriptAgree(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; Go and JavaScript date arithmetic were not compared")
	}
	rawSpec, err := os.ReadFile("fixtures/golden/sample-period-days/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := buildspec.Parse(rawSpec)
	if err != nil {
		t.Fatal(err)
	}
	tool, _, err := parse(spec)
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(20260315))
	oddDates := []string{
		"", "2025-02-29", "2024-02-29", "2019-12-31", "2036-01-01", "2025-4-1",
		"2025/04/01", "0000-01-01", "2100-02-29", "2025-04-31", " 2025-01-01", "abc",
	}
	type result struct {
		Raw    map[string]string   `json:"raw"`
		Values map[string]string   `json:"values"`
		Faults []map[string]string `json:"faults"`
	}
	var cases []result
	for i := 0; i < 1000; i++ {
		pickDate := func() string {
			if rng.Intn(6) == 0 {
				return oddDates[rng.Intn(len(oddDates))]
			}
			y := 2020 + rng.Intn(16)
			m := 1 + rng.Intn(12)
			d := 1 + rng.Intn(31)
			return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
		}
		rate := fmt.Sprintf("%d.%02d", rng.Intn(500), rng.Intn(100))
		if rng.Intn(15) == 0 {
			rate = "-1.00"
		}
		raw := map[string]string{"start_date": pickDate(), "end_date": pickDate(), "daily_rate": rate}
		got := Evaluate(tool, raw)
		faults := []map[string]string{}
		for _, f := range got.Faults {
			faults = append(faults, map[string]string{"id": f.ID, "kind": f.Kind})
		}
		cases = append(cases, result{raw, got.Values, faults})
	}
	data, err := json.Marshal(map[string]any{"tool": tool, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "date-cases.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "testdata/parity.js", path).CombinedOutput()
	if err != nil || string(out) != "same 1000\n" {
		t.Errorf("node testdata/parity.js (dates): %v\n%s", err, out)
	}
}
