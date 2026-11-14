package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wiring is the Moldloft container's credential env and the Secret mount, as
// written in moldloft.yaml.
type wiring struct {
	literal   map[string]string // env name -> literal value
	secretKey map[string]string // env name -> secretKeyRef key
	mountPath string            // mountPath of the plainforge-credentials volume
}

func parseWiring(t *testing.T, text string) wiring {
	t.Helper()
	w := wiring{literal: map[string]string{}, secretKey: map[string]string{}}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if name, ok := strings.CutPrefix(s, "- name: "); ok && strings.HasPrefix(name, "MOLDLOFT_PLAINFORGE_") {
			for j := i + 1; j < len(lines) && j < i+8; j++ {
				n := strings.TrimSpace(lines[j])
				if strings.HasPrefix(n, "- name:") {
					break
				}
				if v, ok := strings.CutPrefix(n, "value: "); ok {
					w.literal[name] = v
				}
				if k, ok := strings.CutPrefix(n, "key: "); ok {
					w.secretKey[name] = k
				}
			}
		}
		if s == "- name: plainforge-credentials" && i+1 < len(lines) {
			if p, ok := strings.CutPrefix(strings.TrimSpace(lines[i+1]), "mountPath: "); ok {
				w.mountPath = p
			}
		}
	}
	return w
}

// env builds the environment the kubelet would give the container when the
// Secret's files are in dir (the mount, mapped from w.mountPath).
func (w wiring) env(dir string) []string {
	var out []string
	for k, v := range w.literal {
		out = append(out, k+"="+strings.Replace(v, w.mountPath, dir, 1))
	}
	for k, key := range w.secretKey {
		// An optional secretKeyRef is unset when the key is absent.
		if b, err := os.ReadFile(filepath.Join(dir, key)); err == nil {
			out = append(out, k+"="+strings.TrimSpace(string(b)))
		}
	}
	return out
}

func TestManifestCredentialWiringStartsWithAndWithoutTheSecret(t *testing.T) {
	raw, err := os.ReadFile("moldloft.yaml")
	if err != nil {
		t.Fatal(err)
	}
	w := parseWiring(t, string(raw))
	if w.mountPath == "" || len(w.literal)+len(w.secretKey) == 0 {
		t.Fatalf("credential wiring not found in moldloft.yaml: %+v", w)
	}

	bin := filepath.Join(t.TempDir(), "moldloft")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/moldloft").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	start := func(files map[string]string) (int, string) {
		mount := filepath.Join(t.TempDir(), "mount")
		if files != nil {
			if err := os.MkdirAll(mount, 0o755); err != nil {
				t.Fatal(err)
			}
			for n, c := range files {
				if err := os.WriteFile(filepath.Join(mount, n), []byte(c), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		cmd := exec.Command(bin, "run", "-standalone", "-once", "-spool", t.TempDir())
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, w.env(mount)...)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, string(out)
	}

	const tok = "fake-token-0123456789abcdef"
	cases := []struct {
		name  string
		files map[string]string // nil: the optional Secret is absent
		want  int
	}{
		{"secret absent", nil, 0},
		{"secret present", map[string]string{"user": "bot", "token": tok + "\n"}, 0},
		{"only user", map[string]string{"user": "bot"}, 2},
		{"only token", map[string]string{"token": tok}, 2},
	}
	for _, c := range cases {
		code, out := start(c.files)
		if code != c.want || strings.Contains(out, tok) {
			t.Errorf("%s: exit %d, want %d; output %q", c.name, code, c.want, out)
		}
	}
}
