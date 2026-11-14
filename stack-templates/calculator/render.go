package calculator

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"strings"
	texttemplate "text/template"

	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/repotree"
)

// files holds everything that is copied into a generated repository unchanged.
//
//go:embed files
var files embed.FS

//go:embed templates
var templates embed.FS

var (
	indexTmpl  = htmltemplate.Must(htmltemplate.ParseFS(templates, "templates/index.html.tmpl"))
	readmeTmpl = texttemplate.Must(texttemplate.ParseFS(templates, "templates/README.md.tmpl"))
)

type citeView struct {
	ID    string
	N     int
	Label string
	Note  string
	URL   string
}

type optionView struct {
	Key, Label, Value string
	Selected          bool
}

type inputView struct {
	ID, Label, Unit, Default, Hint string
	Options                        []optionView
}

type rowView struct {
	ID, Label, Unit, Value, How string
	N                           int
	Total                       bool
	Cite                        *citeView
	Rows                        []Row
}

type view struct {
	Title, Summary, Generator string
	ScopeLimits               []string
	Inputs                    []inputView
	Outputs                   []rowView
	Steps                     []rowView
	Constants                 []rowView
	Tables                    []rowView
	Citations                 []citeView
	Examples                  []Example
}

// Generate checks the calculator part of spec and returns the files of the
// generated repository. generator is Moldloft's own version, shown in the
// page footer and the README.
func Generate(spec *buildspec.Spec, generator string) (repotree.Tree, error) {
	tool, examples, err := parse(spec)
	if err != nil {
		return nil, err
	}
	tree := repotree.Tree{}

	// The fixed files.
	err = fs.WalkDir(files, "files", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		tree.Add(strings.TrimPrefix(name, "files/"), data)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The data, once as JSON and once as a script that sets a global.
	toolJSON := mustJSON(tool)
	examplesJSON := mustJSON(examples)
	tree.Add("data/tool.json", toolJSON)
	tree.Add("js/data.js", asScript("MOLDLOFT_TOOL", toolJSON))
	tree.Add("tests/worked-examples.json", examplesJSON)
	tree.Add("tests/worked-examples.js", asScript("MOLDLOFT_EXAMPLES", examplesJSON))

	v := buildView(tool, examples, generator)
	var page, readme bytes.Buffer
	if err := indexTmpl.Execute(&page, v); err != nil {
		return nil, err
	}
	if err := readmeTmpl.Execute(&readme, v); err != nil {
		return nil, err
	}
	tree.Add("index.html", page.Bytes())
	tree.Add("README.md", readme.Bytes())
	return tree, nil
}

func mustJSON(v any) []byte {
	// encoding/json writes struct fields in declaration order and map keys in
	// sorted order, and escapes <, > and & so the text is also safe in a script.
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func asScript(global string, jsonText []byte) []byte {
	var b bytes.Buffer
	b.WriteString("// Generated from the .json file of the same name. Do not edit.\n")
	b.WriteString("globalThis." + global + " = ")
	b.Write(bytes.TrimRight(jsonText, "\n"))
	b.WriteString(";\n")
	return b.Bytes()
}

func buildView(t *Tool, examples []Example, generator string) view {
	v := view{Title: t.Title, Summary: t.Summary, Generator: generator, ScopeLimits: t.ScopeLimits, Examples: examples}
	cites := map[string]*citeView{}
	for i, c := range t.Citations {
		cv := citeView{ID: c.ID, N: i + 1, Label: c.Label, Note: c.Note, URL: c.URL}
		v.Citations = append(v.Citations, cv)
		cites[c.ID] = &cv
	}
	labels := map[string]string{}

	for _, in := range t.Inputs {
		labels[in.ID] = in.Label
		iv := inputView{ID: in.ID, Label: in.Label, Unit: in.Unit, Default: in.Default}
		var hint string
		if in.Kind == "choice" {
			tb := t.table(in.Table)
			for _, row := range tb.Rows {
				iv.Options = append(iv.Options, optionView{row.Key, row.Label, row.Value, row.Key == in.Default})
			}
			hint = "Choose one. The values are listed under “" + tb.Label + "” below."
		} else {
			hint = fmt.Sprintf("From %s to %s, %s.", in.Min, in.Max, placesAllowed(in.Decimals))
		}
		if in.Help != "" {
			hint = in.Help + " " + hint
		}
		iv.Hint = hint
		v.Inputs = append(v.Inputs, iv)
	}
	for _, c := range t.Constants {
		labels[c.ID] = c.Label
		v.Constants = append(v.Constants, rowView{ID: c.ID, Label: c.Label, Unit: c.Unit, Value: c.Value, Cite: cites[c.Citation]})
	}
	for _, tb := range t.Tables {
		v.Tables = append(v.Tables, rowView{ID: tb.ID, Label: tb.Label, Unit: tb.Unit, Cite: cites[tb.Citation], Rows: tb.Rows})
	}
	isOutput := map[string]bool{}
	for _, id := range t.Outputs {
		isOutput[id] = true
	}
	for i, st := range t.Steps {
		labels[st.ID] = st.Label
		names := make([]string, len(st.Args))
		for j, ref := range st.Args {
			names[j] = labels[ref]
		}
		v.Steps = append(v.Steps, rowView{
			ID: st.ID, N: i + 1, Label: st.Label, Unit: st.Unit,
			How: how(st.Op, names) + ", " + roundedTo(st.Decimals) + ".", Total: isOutput[st.ID], Cite: cites[st.Citation],
		})
	}
	for _, id := range t.Outputs {
		st := t.step(id)
		v.Outputs = append(v.Outputs, rowView{ID: st.ID, Label: st.Label, Unit: st.Unit})
	}
	return v
}

// how writes one step's arithmetic in words, using the labels of what it uses.
func how(op string, names []string) string {
	switch op {
	case "add":
		return strings.Join(names, " + ")
	case "sub":
		return names[0] + " − " + names[1]
	case "mul":
		return strings.Join(names, " × ")
	case "div":
		return names[0] + " ÷ " + names[1]
	case "pct":
		return names[0] + " × " + names[1] + " ÷ 100"
	case "min":
		return "The smallest of: " + strings.Join(names, "; ")
	case "max":
		return "The largest of: " + strings.Join(names, "; ")
	}
	panic("calculator: no wording for operation " + op)
}

func roundedTo(places int) string {
	switch places {
	case 0:
		return "rounded to a whole number"
	case 1:
		return "rounded to 1 decimal place"
	}
	return fmt.Sprintf("rounded to %d decimal places", places)
}

func placesAllowed(places int) string {
	switch places {
	case 0:
		return "whole numbers only"
	case 1:
		return "with up to 1 decimal place"
	}
	return fmt.Sprintf("with up to %d decimal places", places)
}
