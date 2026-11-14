package stacktemplates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The registry must list exactly the sub-tools component.json names, in the
// same order, and a template's folder must exist exactly when it is built.
func TestRegistryMatchesComponentJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "component.json"))
	if err != nil {
		t.Fatal(err)
	}
	var component struct {
		SubTools []string `json:"subTools"`
	}
	if err := json.Unmarshal(raw, &component); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range Entries() {
		names = append(names, e.Name)
		_, err := os.Stat(e.Path)
		if e.Built() != (err == nil) {
			t.Errorf("%s: status is %s but folder present is %v", e.Name, e.Status, err == nil)
		}
	}
	if !reflect.DeepEqual(names, component.SubTools) {
		t.Errorf("registry lists %v, component.json lists %v", names, component.SubTools)
	}
}

func TestParseRefusesWhatItDoesNotUnderstand(t *testing.T) {
	bad := []string{
		"",
		"tools:\n  - name: a\n    path: a\n    status: built\n",
		"templates:\n  - name: a\n    path: a\n",
		"templates:\n  - name: a\n    path: a\n    status: done\n",
		"templates:\n  - name: a\n    path: a\n    status: built\n    extra: x\n",
		"templates:\n  - name: a\n    path: a\n    status: built\n  - name: a\n    path: b\n    status: built\n",
		"templates:\n  - name: \"a\"\n    path: a\n    status: built\n",
		"templates:\n- name: a\n  path: a\n  status: built\n",
	}
	for _, text := range bad {
		if _, err := parse(text); err == nil {
			t.Errorf("parse accepted:\n%s", text)
		}
	}
}
