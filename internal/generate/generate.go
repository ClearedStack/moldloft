// Package generate turns one build specification into the files of one
// generated repository. It is a pure function of the specification's bytes:
// it reads no clock, no environment and no network.
package generate

import (
	"encoding/json"

	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/errs"
	"github.com/ClearedStack/moldloft/internal/repotree"
	stacktemplates "github.com/ClearedStack/moldloft/stack-templates"
	"github.com/ClearedStack/moldloft/stack-templates/calculator"
	"github.com/ClearedStack/moldloft/stack-templates/checklist"
	decisiontree "github.com/ClearedStack/moldloft/stack-templates/decision-tree"
	"github.com/ClearedStack/moldloft/stack-templates/validator"
)

// Name and Version are this component's entries in component.json.
const (
	Name    = "moldloft"
	Version = "v0.1.0-draft"
)

// Meta describes the generated repository without carrying the file contents.
type Meta struct {
	ItemID         string
	Slug           string
	Template       string
	SpecSHA256     string
	ManifestSHA256 string
	FileCount      int
}

type template struct {
	matches  func(*buildspec.Spec) bool
	generate func(*buildspec.Spec, string) (repotree.Tree, error)
}

// built maps a registry name to the code of a template that exists.
var built = map[string]template{
	calculator.Name:   {calculator.Matches, calculator.Generate},
	checklist.Name:    {checklist.Matches, checklist.Generate},
	validator.Name:    {validator.Matches, validator.Generate},
	decisiontree.Name: {decisiontree.Matches, decisiontree.Generate},
}

// Generate checks raw against the contract, picks the stack template that
// matches and returns the generated repository. Its errors are *errs.Error
// values carrying E_PARSE or E_NEEDS_SUBTOOL.
func Generate(raw []byte) (repotree.Tree, error) {
	tree, _, err := GenerateWithMeta(raw)
	return tree, err
}

// GenerateWithMeta performs the same deterministic generation as Generate and
// also returns the summary metadata reported to Muster.
func GenerateWithMeta(raw []byte) (repotree.Tree, Meta, error) {
	spec, err := buildspec.Parse(raw)
	if err != nil {
		return nil, Meta{}, err
	}
	// Templates are tried in registry order and the first match wins. When
	// none matches, the item is paused; nothing is improvised.
	for _, entry := range stacktemplates.Entries() {
		t, ok := built[entry.Name]
		if !entry.Built() || !ok || !t.matches(spec) {
			continue
		}
		tree, err := t.generate(spec, Version)
		if err != nil {
			return nil, Meta{}, err
		}
		tree.Add("LICENSE", []byte(licenseText))
		tree.Add("moldloft.json", manifest(spec, entry.Name, tree))
		return tree, Meta{
			ItemID:         spec.ItemID,
			Slug:           spec.Slug,
			Template:       entry.Name,
			SpecSHA256:     spec.SHA256,
			ManifestSHA256: tree.SHA256("moldloft.json"),
			FileCount:      len(tree),
		}, nil
	}
	return nil, Meta{}, errs.NeedsSubtoolf("pattern", "no built stack template matches this pattern")
}

type manifestFile struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// manifest lists what made the repository and the hash of every other file in
// it. It holds no time and no machine name, so it is the same on every run.
func manifest(spec *buildspec.Spec, templateName string, tree repotree.Tree) []byte {
	m := struct {
		Generator        string         `json:"generator"`
		GeneratorVersion string         `json:"generator_version"`
		ContractVersion  string         `json:"contract_version"`
		Template         string         `json:"template"`
		ItemID           string         `json:"item_id"`
		Slug             string         `json:"slug"`
		SpecSHA256       string         `json:"spec_sha256"`
		Files            []manifestFile `json:"files"`
	}{Name, Version, buildspec.ContractVersion, templateName, spec.ItemID, spec.Slug, spec.SHA256, nil}
	for _, p := range tree.Paths() {
		m.Files = append(m.Files, manifestFile{p, len(tree[p]), tree.SHA256(p)})
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

// licenseText is the MIT License. The copyright line carries no year because
// a year would have to come from a clock, and the output must not depend on
// when it was generated.
const licenseText = `MIT License

Copyright (c) ClearedStack contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`
