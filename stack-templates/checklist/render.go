package checklist

import (
	"bytes"
	"embed"
	"encoding/json"
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

type itemView struct {
	ID       string
	N        int
	Label    string
	Required bool
	AllowNA  bool
	Help     string
	Cite     *citeView
}

type view struct {
	Title       string
	Summary     string
	Generator   string
	ScopeLimits []string
	Items       []itemView
	Citations   []citeView
	Examples    []Example
}

// Generate validates spec for the checklist pattern and produces the static
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
		Title:       t.Title,
		Summary:     t.Summary,
		Generator:   generator,
		ScopeLimits: t.ScopeLimits,
		Examples:    examples,
	}
	cites := map[string]*citeView{}
	for i, c := range t.Citations {
		cv := citeView{ID: c.ID, N: i + 1, Label: c.Label, Note: c.Note, URL: c.URL}
		v.Citations = append(v.Citations, cv)
		cites[c.ID] = &cv
	}
	for i, it := range t.Items {
		v.Items = append(v.Items, itemView{
			ID:       it.ID,
			N:        i + 1,
			Label:    it.Label,
			Required: it.Required,
			AllowNA:  it.AllowNA,
			Help:     it.Help,
			Cite:     cites[it.Citation],
		})
	}
	return v
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
