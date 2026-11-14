package decisiontree_test

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ClearedStack/moldloft/internal/buildspec"
	"github.com/ClearedStack/moldloft/internal/errs"
	"github.com/ClearedStack/moldloft/internal/generate"
	decisiontree "github.com/ClearedStack/moldloft/stack-templates/decision-tree"
)

var update = flag.Bool("update", false, "rewrite the expected output of the golden fixtures")

func goldenDirs(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob("fixtures/golden/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no golden fixtures found: %v", err)
	}
	return dirs
}

func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	tree := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		tree[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestMatchingRule(t *testing.T) {
	if !decisiontree.Matches(&buildspec.Spec{Pattern: "decision-tree"}) {
		t.Error("pattern decision-tree must match")
	}
	if decisiontree.Matches(&buildspec.Spec{Pattern: "calculator"}) {
		t.Error("pattern calculator must not match decision-tree")
	}
}

func TestGolden(t *testing.T) {
	for _, dir := range goldenDirs(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, "spec.json"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := generate.Generate(raw)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			again, err := generate.Generate(raw)
			if err != nil {
				t.Fatalf("second Generate: %v", err)
			}
			for _, p := range got.Paths() {
				if !bytes.Equal(got[p], again[p]) {
					t.Errorf("%s differed between two runs", p)
				}
			}

			expected := filepath.Join(dir, "expected")
			if *update {
				if err := os.RemoveAll(expected); err != nil {
					t.Fatal(err)
				}
				if err := got.Write(expected); err != nil {
					t.Fatal(err)
				}
			}
			want := readTree(t, expected)
			for _, p := range got.Paths() {
				w, ok := want[p]
				switch {
				case !ok:
					t.Errorf("%s was generated but is not in the fixture", p)
				case !bytes.Equal(got[p], w):
					t.Errorf("%s differs from the fixture", p)
				}
			}
			for p := range want {
				if _, ok := got[p]; !ok {
					t.Errorf("%s is in the fixture but was not generated", p)
				}
			}
		})
	}
}

func TestGoldenOutputRulesAndNode(t *testing.T) {
	const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'self'; connect-src 'self'; form-action 'none'">`
	node, nodeErr := exec.LookPath("node")
	for _, dir := range goldenDirs(t) {
		tree := readTree(t, filepath.Join(dir, "expected"))
		if !bytes.HasPrefix(tree["LICENSE"], []byte("MIT License\n")) {
			t.Errorf("%s: LICENSE is missing or is not MIT", dir)
		}
		for p, data := range tree {
			text := string(data)
			if strings.HasSuffix(p, ".html") {
				if !strings.Contains(text, csp) {
					t.Errorf("%s: no content security policy", p)
				}
				for _, banned := range []string{"<style", " style=", "<script>", " onclick=", " onload=", "<iframe", "<img"} {
					if strings.Contains(text, banned) {
						t.Errorf("%s: contains %q", p, banned)
					}
				}
			}
			isData := p == "js/data.js" || p == "tests/worked-examples.js"
			if (strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css")) && !isData {
				for _, banned := range []string{"http:", "https:", "fetch(", "XMLHttpRequest", "WebSocket", "sendBeacon", "localStorage", "sessionStorage", "document.cookie", "eval(", "import(", "@import", "url("} {
					if strings.Contains(text, banned) {
						t.Errorf("%s: contains %q", p, banned)
					}
				}
			}
		}
		if nodeErr == nil {
			cmd := exec.Command(node, "tests/run.js")
			cmd.Dir = filepath.Join(dir, "expected")
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.HasSuffix(string(out), "All checks passed.\n") {
				t.Errorf("%s: node tests/run.js failed: %v\n%s", dir, err, out)
			}
		}
	}
}

func TestRejects(t *testing.T) {
	specs, _ := filepath.Glob("fixtures/rejects/*.json")
	wants, _ := filepath.Glob("fixtures/rejects/*.want")
	if len(specs) == 0 || len(specs) != len(wants) {
		t.Fatalf("found %d reject specs and %d expected errors", len(specs), len(wants))
	}
	sort.Strings(specs)
	codes := map[errs.Code]int{}
	for _, specPath := range specs {
		name := strings.TrimSuffix(filepath.Base(specPath), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(specPath)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(specPath, ".json") + ".want")
			if err != nil {
				t.Fatal(err)
			}
			tree, err := generate.Generate(raw)
			if err == nil || tree != nil {
				t.Fatalf("the specification was accepted")
			}
			if got := err.Error() + "\n"; got != string(want) {
				t.Errorf("error is\n  %swant\n  %s", got, want)
			}
			codes[errs.CodeOf(err)]++
		})
	}
	if codes[errs.Parse] == 0 || codes[errs.NeedsSubtool] == 0 || len(codes) != 2 {
		t.Errorf("rejects must cover E_PARSE and E_NEEDS_SUBTOOL, got %v", codes)
	}
}
