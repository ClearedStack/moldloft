package buildspec

import (
	"strings"
	"testing"

	"github.com/ClearedStack/moldloft/internal/errs"
)

func TestMusterItemIDsAndWaybillUnwrap(t *testing.T) {
	validIDs := []string{"PA-0001", "PA-0417", "PA-10000", "PA-999999999"}
	for _, id := range validIDs {
		if !IsMusterItemID(id) {
			t.Errorf("IsMusterItemID(%q) = false, want true", id)
		}
	}
	invalidIDs := []string{"PA-001", "PA-01000", "EP-0001", "pa-0001", "PA-1000000000"}
	for _, id := range invalidIDs {
		if IsMusterItemID(id) {
			t.Errorf("IsMusterItemID(%q) = true, want false", id)
		}
	}

	specJSON := `{
		"contract_version": "v1",
		"item_id": "PA-0001",
		"tool": {"slug": "sample-tool", "title": "Sample tool", "summary": "Checks item id parsing."},
		"pattern": "calculator",
		"citations": [{"id": "rule_a", "label": "Sample Rule 1.1 (fictional)"}],
		"scope_limits": ["Made-up test scope limit."],
		"body": {"inputs": []},
		"worked_examples": [{"id": "ex_1"}]
	}`
	spec, err := Parse([]byte(specJSON))
	if err != nil {
		t.Fatalf("Parse with PA-0001 failed: %v", err)
	}
	if spec.ItemID != "PA-0001" {
		t.Fatalf("ItemID = %q, want PA-0001", spec.ItemID)
	}

	direct, err := UnwrapWaybillResult([]byte(specJSON))
	if err != nil || string(direct) != specJSON {
		t.Fatalf("UnwrapWaybillResult direct failed: %v", err)
	}

	wrapped := `{"slug":"sample-tool","entry":{"title":"Sample tool","summary":"Summary."},"spec":` + specJSON + `}`
	unwrapped, err := UnwrapWaybillResult([]byte(wrapped))
	if err != nil {
		t.Fatalf("UnwrapWaybillResult wrapped failed: %v", err)
	}
	if !strings.Contains(string(unwrapped), `"PA-0001"`) {
		t.Fatalf("unexpected unwrapped spec: %s", unwrapped)
	}

	poisoned := `{"raw_html":"<p>vendor</p>","spec":` + specJSON + `}`
	if _, err := UnwrapWaybillResult([]byte(poisoned)); errs.CodeOf(err) != errs.Parse {
		t.Fatalf("poisoned envelope got %v, want E_PARSE", err)
	}
}
