package deploy_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDeployManifestsSatisfyHardeningAndContractRules(t *testing.T) {
	moldloftYAML, err := os.ReadFile("moldloft.yaml")
	if err != nil {
		t.Fatal(err)
	}
	netpolYAML, err := os.ReadFile("networkpolicy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	koYAML, err := os.ReadFile(filepath.Join("..", ".ko.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	mText := string(moldloftYAML)
	nText := string(netpolYAML)
	kText := string(koYAML)

	// Namespace and workload basics.
	mustContain := []struct {
		file string
		text string
		want string
	}{
		{"moldloft.yaml", mText, "namespace: cs-workers"},
		{"moldloft.yaml", mText, "runAsNonRoot: true"},
		{"moldloft.yaml", mText, "readOnlyRootFilesystem: true"},
		{"moldloft.yaml", mText, "allowPrivilegeEscalation: false"},
		{"moldloft.yaml", mText, `drop: ["ALL"]`},
		{"moldloft.yaml", mText, "type: RuntimeDefault"},
		{"moldloft.yaml", mText, "key: kubernetes.io/arch"},
		{"moldloft.yaml", mText, `values: ["arm64"]`},
		{"moldloft.yaml", mText, "key: kubernetes.io/hostname"},
		{"moldloft.yaml", mText, "operator: NotIn"},
		{"moldloft.yaml", mText, `values: ["cm3588-plus"]`},
		{"moldloft.yaml", mText, "cpu: 10m"},
		{"moldloft.yaml", mText, "memory: 24Mi"},
		{"moldloft.yaml", mText, "memory: 30Mi"},
		{"moldloft.yaml", mText, "mountPath: /var/spool/cs"},
		{"networkpolicy.yaml", nText, "name: default-deny"},
		{"networkpolicy.yaml", nText, "namespace: cs-workers"},
		{"networkpolicy.yaml", nText, "podSelector: {}"},
		{"networkpolicy.yaml", nText, "app.kubernetes.io/name: muster"},
		{"networkpolicy.yaml", nText, "kubernetes.io/metadata.name: clearedstack"},
		{"networkpolicy.yaml", nText, "kubernetes.io/metadata.name: kube-system"},
		{".ko.yaml", kText, "CGO_ENABLED=0"},
	}
	for _, tc := range mustContain {
		if !strings.Contains(tc.text, tc.want) {
			t.Errorf("%s is missing %q", tc.file, tc.want)
		}
	}

	// Verify container image is pinned by sha256 digest and carries no mutable tag.
	digestRe := regexp.MustCompile(`(?m)^\s*image:\s+[a-z0-9.-]+(:[0-9]+)?/[a-z0-9./-]+@sha256:[0-9a-f]{64}\s*$`)
	if !digestRe.MatchString(mText) {
		t.Errorf("moldloft.yaml image must be pinned by @sha256:<64-hex> digest without a mutable tag")
	}

	// Verify no Secret manifests or credential strings exist anywhere in deploy/.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range entries {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(d.Name())
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		for _, banned := range []string{"kind: Secret", "password", "PRIVATE KEY", ":latest"} {
			if strings.Contains(s, banned) {
				t.Errorf("%s contains forbidden text %q", d.Name(), banned)
			}
		}
	}
}

func TestStdlibOnlyAndNoEmDashes(t *testing.T) {
	goMod, err := os.ReadFile(filepath.Join("..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	modText := string(goMod)
	if !strings.Contains(modText, "module github.com/ClearedStack/moldloft") {
		t.Errorf("unexpected module path in go.mod:\n%s", modText)
	}
	if strings.Contains(modText, "require") {
		t.Errorf("go.mod must use the Go standard library only:\n%s", modText)
	}

	root := filepath.Join("..")
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		// SPEC.md is a read-only mirror of clearedstack.com on main.
		if rel == "SPEC.md" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "\u2014") || strings.Contains(string(raw), "&"+"mdash;") {
			t.Errorf("%s contains an em dash", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
