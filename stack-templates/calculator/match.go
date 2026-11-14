// Package calculator is the calculator stack template: numbers in, each step
// of the arithmetic and the cited totals out.
//
// The template has three parts:
//
//	match.go   the matching rule
//	model.go, eval.go, render.go and files/   the logic
//	fixtures/  saved inputs and the exact output expected for each
package calculator

import "github.com/ClearedStack/moldloft/internal/buildspec"

// Name is this template's name in component.json and registry.yaml.
const Name = "calculator"

// Matches is the matching rule: the specification's "pattern" field is exactly
// "calculator". Nothing is inferred from the rest of the specification.
func Matches(spec *buildspec.Spec) bool {
	return spec.Pattern == Name
}
