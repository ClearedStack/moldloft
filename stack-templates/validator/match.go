// Package validator is the stack template for checking codes, tables and local
// files entirely in browser memory against bundled rules.
package validator

import "github.com/ClearedStack/moldloft/internal/buildspec"

// Name is this template's entry in component.json and registry.yaml.
const Name = "validator"

// Matches reports whether spec asks for the validator stack template.
func Matches(spec *buildspec.Spec) bool {
	return spec.Pattern == Name
}
