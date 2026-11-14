package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtures = "../../stack-templates/calculator/fixtures/"

func runCLI(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func listing(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = info.Mode().String() + "\n" + string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestGenerateWritesTheGoldenFolderEveryTime(t *testing.T) {
	spec := fixtures + "golden/sample-rate-buildup/spec.json"
	first, second := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, dir := range []string{first, second} {
		if code, out, errOut := runCLI("generate", "-spec", spec, "-out", dir); code != 0 || out != "generated 16 files\n" || errOut != "" {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	}
	a, b := listing(t, first), listing(t, second)
	want := listing(t, fixtures+"golden/sample-rate-buildup/expected")
	if len(a) != len(want) || len(b) != len(want) {
		t.Fatalf("wrote %d and %d files, fixture has %d", len(a), len(b), len(want))
	}
	for p, w := range want {
		if a[p] != b[p] {
			t.Errorf("%s differs between two runs", p)
		}
		// Compare contents only; a checkout may not keep the fixture's mode.
		if a[p][strings.IndexByte(a[p], '\n'):] != w[strings.IndexByte(w, '\n'):] {
			t.Errorf("%s differs from the fixture", p)
		}
		if !strings.HasPrefix(a[p], "-rw-r--r--\n") {
			t.Errorf("%s was not written with mode 0644", p)
		}
	}
}

func TestRefusedSpecWritesNothing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	code, _, errOut := runCLI("generate", "-spec", fixtures+"rejects/step-op-unknown.json", "-out", out)
	if code != 1 || !strings.HasPrefix(errOut, "E_NEEDS_SUBTOOL: ") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("the output folder was created for a refused specification")
	}
}

func TestLocalProblemsExitTwoAndNameNoPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	spec := fixtures + "golden/sample-rate-buildup/spec.json"
	cases := [][]string{
		{},
		{"generate"},
		{"generate", "-spec", spec},
		{"generate", "-spec", spec, "-out", dir, "extra"},
		{"generate", "-spec", filepath.Join(dir, "missing.json"), "-out", filepath.Join(dir, "o")},
		{"generate", "-spec", spec, "-out", dir},
	}
	for _, args := range cases {
		code, _, errOut := runCLI(args...)
		if code != 2 || errOut == "" || strings.Contains(errOut, dir) {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
	if code, out, _ := runCLI("version"); code != 0 || out != "moldloft v0.1.0-draft\n" {
		t.Errorf("version: exit %d, stdout %q", code, out)
	}
}

func TestOversizedSpecIsParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte(" "), 1<<20+1), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI("generate", "-spec", path, "-out", filepath.Join(t.TempDir(), "o"))
	if code != 1 || errOut != "E_PARSE: spec: is larger than the 1 MiB limit\n" {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestRunStandaloneDrainsSeededItems(t *testing.T) {
	rawSpec, err := os.ReadFile("../../stack-templates/checklist/fixtures/golden/sample-solicitation-review/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	seedJSON := `[{"id":"PA-0101","state":"approved","lane":"urgent","next":"moldloft","results":{"waybill":` + string(rawSpec) + `}}]`
	dir := t.TempDir()
	seedFile := filepath.Join(dir, "seed.json")
	if err := os.WriteFile(seedFile, []byte(seedJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	spoolDir := filepath.Join(dir, "spool")
	code, out, errOut := runCLI("run", "-standalone", "-once", "-seed", seedFile, "-spool", spoolDir)
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if !strings.Contains(out, "event=completed item=PA-0101 state=building") {
		t.Errorf("unexpected stdout %q", out)
	}
}

func TestRunRejectsBadConfigAndContractVersionWithoutLeakingPaths(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := runCLI("run", "-spool", dir)
	if code != 2 || strings.Contains(errOut, dir) {
		t.Errorf("missing muster: exit %d, stderr %q", code, errOut)
	}

	t.Setenv("CONTRACT_VERSION", "v99")
	code, _, errOut = runCLI("run", "-standalone", "-once", "-spool", dir)
	if code != 1 || !strings.HasPrefix(errOut, "E_NEEDS_SUBTOOL:") {
		t.Errorf("bad CONTRACT_VERSION: exit %d, stderr %q", code, errOut)
	}
}

const fakeToken = "fake-token-0123456789abcdef"

func TestRunPlainforgeCredentialsHalfSetRefusesToStart(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(fakeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []map[string]string{
		{"MOLDLOFT_PLAINFORGE_USER": "bot"},
		{"MOLDLOFT_PLAINFORGE_TOKEN_FILE": tokenFile},
		{"MOLDLOFT_PLAINFORGE_USER": "bot", "MOLDLOFT_PLAINFORGE_TOKEN_FILE": filepath.Join(dir, "missing")},
	}
	for _, env := range cases {
		for k, v := range env {
			t.Setenv(k, v)
		}
		code, out, errOut := runCLI("run", "-standalone", "-once", "-spool", filepath.Join(dir, "spool"))
		if code != 2 || errOut == "" || strings.Contains(errOut+out, dir) || strings.Contains(errOut+out, fakeToken) {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", env, code, out, errOut)
		}
		for k := range env {
			t.Setenv(k, "")
		}
	}
}

func TestRunRefusedPushNeverPrintsTheToken(t *testing.T) {
	var auth []string
	forge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		http.Error(w, "denied "+fakeToken, http.StatusUnauthorized)
	}))
	defer forge.Close()

	rawSpec, err := os.ReadFile("../../stack-templates/checklist/fixtures/golden/sample-solicitation-review/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	seedFile := filepath.Join(dir, "seed.json")
	seed := `[{"id":"PA-0101","state":"approved","lane":"urgent","next":"moldloft","results":{"waybill":` + string(rawSpec) + `}}]`
	tokenFile := filepath.Join(dir, "token")
	for p, c := range map[string]string{seedFile: seed, tokenFile: fakeToken + "\n"} {
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MOLDLOFT_PLAINFORGE_USER", "bot")
	t.Setenv("MOLDLOFT_PLAINFORGE_TOKEN_FILE", tokenFile)

	_, out, errOut := runCLI("run", "-standalone", "-once", "-seed", seedFile, "-spool", filepath.Join(dir, "spool"), "-plainforge", forge.URL+"/ClearedStack")
	if len(auth) == 0 || auth[0] == "" {
		t.Fatalf("no basic auth reached the forge: %v", auth)
	}
	for _, text := range []string{out, errOut} {
		if strings.Contains(text, fakeToken) || strings.Contains(text, "Basic ") {
			t.Fatalf("output leaks the credential: %q", text)
		}
	}
}
