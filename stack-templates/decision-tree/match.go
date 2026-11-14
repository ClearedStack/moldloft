// Package decisiontree generates standalone guided branching decision-tree
// tools. Every question, option, branch target and outcome comes from the
// specification; cycles, dangling targets and unreachable nodes are rejected
// at build time.
package decisiontree

import "github.com/ClearedStack/moldloft/internal/buildspec"

// Name is the entry in stack-templates/registry.yaml and component.json.
const Name = "decision-tree"

// Matches reports whether this template handles spec.
func Matches(spec *buildspec.Spec) bool {
	return spec.Pattern == Name
}
