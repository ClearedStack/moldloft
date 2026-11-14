package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	stacktemplates "github.com/ClearedStack/moldloft/stack-templates"
)

// Names come from the registry: the constants here must equal component.json.
func TestNameAndVersionMatchComponentJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "component.json"))
	if err != nil {
		t.Fatal(err)
	}
	var component struct {
		Slug    string `json:"slug"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &component); err != nil {
		t.Fatal(err)
	}
	if component.Slug != Name || component.Version != Version {
		t.Errorf("code says %s %s, component.json says %s %s", Name, Version, component.Slug, component.Version)
	}
}

// Every registry entry marked built must have code, and nothing else may.
func TestBuiltTemplatesMatchRegistry(t *testing.T) {
	count := 0
	for _, e := range stacktemplates.Entries() {
		_, ok := built[e.Name]
		if ok != e.Built() {
			t.Errorf("%s: registry status is %s but code present is %v", e.Name, e.Status, ok)
		}
		if ok {
			count++
		}
	}
	if count != len(built) {
		t.Errorf("%d templates have code but only %d are in the registry", len(built), count)
	}
}
