package validator

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

type colView struct {
	ID       string
	Label    string
	Required bool
	RuleDesc string
	Cite     *citeView
}

type tableView struct {
	ID    string
	Label string
	Codes string
	Cite  *citeView
}

type view struct {
	Title         string
	Summary       string
	Generator     string
	ScopeLimits   []string
	SampleInput   string
	DelimiterWord string
	HasHeader     bool
	Columns       []colView
	Tables        []tableView
	Citations     []citeView
	Examples      []Example
}

// Generate validates spec for the validator pattern and produces the static
// repository tree.
func Generate(spec *buildspec.Spec, generator string) (repotree.Tree, error) {
	tool, examples, err := parse(spec)
	if err != nil {
		return nil, err
	}
	tree := repotree.Tree{}
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

func buildView(t *Tool, examples []Example, generator string) view {
	v := view{
		Title:         t.Title,
		Summary:       t.Summary,
		Generator:     generator,
		ScopeLimits:   t.ScopeLimits,
		SampleInput:   examples[0].Input,
		DelimiterWord: t.Delimiter,
		HasHeader:     t.HasHeader,
		Examples:      examples,
	}
	cites := map[string]*citeView{}
	for i, c := range t.Citations {
		cv := citeView{ID: c.ID, N: i + 1, Label: c.Label, Note: c.Note, URL: c.URL}
		v.Citations = append(v.Citations, cv)
		cites[c.ID] = &cv
	}
	tableLabels := map[string]string{}
	for _, tb := range t.Tables {
		tableLabels[tb.ID] = tb.Label
		v.Tables = append(v.Tables, tableView{
			ID:    tb.ID,
			Label: tb.Label,
			Codes: strings.Join(tb.Codes, ", "),
			Cite:  cites[tb.Citation],
		})
	}
	for _, col := range t.Columns {
		v.Columns = append(v.Columns, colView{
			ID:       col.ID,
			Label:    col.Label,
			Required: col.Required,
			RuleDesc: describeRule(col, tableLabels),
			Cite:     cites[col.Citation],
		})
	}
	return v
}

func describeRule(col Column, tableLabels map[string]string) string {
	switch col.Rule {
	case "text":
		return fmt.Sprintf("Plain text (%d to %d characters)", col.MinLen, col.MaxLen)
	case "digits":
		return fmt.Sprintf("Digits 0-9 (%d to %d characters)", col.MinLen, col.MaxLen)
	case "upper_code":
		if col.Prefix != "" {
			return fmt.Sprintf("Upper-case code starting with %s (%d to %d characters)", col.Prefix, col.MinLen, col.MaxLen)
		}
		return fmt.Sprintf("Upper-case code A-Z, 0-9 or hyphen (%d to %d characters)", col.MinLen, col.MaxLen)
	case "in_table":
		return "Listed in " + tableLabels[col.Table]
	default:
		return col.Rule
	}
}

func mustJSON(v any) []byte {
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
