// Package checklist is the stack template for structured yes/no compliance
// checklists with regulatory citations and in-memory browser state.
package checklist

import "github.com/ClearedStack/moldloft/internal/buildspec"

// Name is this template's entry in component.json and registry.yaml.
const Name = "checklist"

// Matches reports whether spec asks for the checklist stack template.
func Matches(spec *buildspec.Spec) bool {
	return spec.Pattern == Name
}
