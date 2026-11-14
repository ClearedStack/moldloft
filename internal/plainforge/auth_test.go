package plainforge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClearedStack/moldloft/internal/errs"
)

const (
	testUser  = "moldloft-bot"
	testToken = "fake-token-0123456789abcdef"
)

// requireBasic wraps next so that only the right user and token get through.
func requireBasic(next http.Handler, seen *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Path)
		u, p, ok := r.BasicAuth()
		if !ok || u != testUser || p != testToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="forge"`)
			http.Error(w, "Unauthorized "+p, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestBasicAuthOnBothRequestsAndOrgPathPrefix(t *testing.T) {
	standIn := NewStandIn()
	var seen []string
	srv := httptest.NewServer(requireBasic(standIn, &seen))
	defer srv.Close()

	for i, suffix := range []string{"/ClearedStack", "/ClearedStack/"} {
		c, err := NewHTTPCommitter(srv.URL+suffix, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.SetBasicAuth(testUser, testToken)
		seen = nil
		slug := "tool" + string(rune('a'+i))
		r, err := c.Commit(context.Background(), slug, "1111111111111111", sampleTree())
		if err != nil {
			t.Fatalf("%q: %v", suffix, err)
		}
		if standIn.Head("ClearedStack/"+slug) != r.CommitSHA1 {
			t.Fatalf("%q: push did not land under the org path", suffix)
		}
		want := []string{"/ClearedStack/" + slug + ".git/info/refs", "/ClearedStack/" + slug + ".git/git-receive-pack"}
		if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
			t.Fatalf("%q: requests %v, want %v", suffix, seen, want)
		}
	}
}

func TestWrongOrMissingCredentialsGiveFixedParseErrorWithoutToken(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(requireBasic(NewStandIn(), &seen))
	defer srv.Close()

	for name, setup := range map[string]func(*HTTPCommitter){
		"none":        func(*HTTPCommitter) {},
		"wrong token": func(c *HTTPCommitter) { c.SetBasicAuth(testUser, testToken+"x") },
	} {
		c, _ := NewHTTPCommitter(srv.URL, 5*time.Second)
		setup(c)
		_, err := c.Commit(context.Background(), "tool", "1111111111111111", sampleTree())
		if errs.CodeOf(err) != errs.Parse || !strings.Contains(err.Error(), "credentials were refused") {
			t.Fatalf("%s: got %v, want E_PARSE credentials were refused", name, err)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatalf("%s: error contains the token: %v", name, err)
		}
	}
}

func TestForbiddenPushIsParseAndMissingRepoStaysParseNotCredentials(t *testing.T) {
	for status, wantCreds := range map[int]bool{http.StatusForbidden: true, http.StatusNotFound: false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "body "+testToken, status)
		}))
		c, _ := NewHTTPCommitter(srv.URL, 5*time.Second)
		c.SetBasicAuth(testUser, testToken)
		_, err := c.Commit(context.Background(), "tool", "1111111111111111", sampleTree())
		srv.Close()
		if errs.CodeOf(err) != errs.Parse {
			t.Fatalf("%d: got %v, want E_PARSE", status, err)
		}
		if got := strings.Contains(err.Error(), "credentials were refused"); got != wantCreds {
			t.Fatalf("%d: credentials message = %v, want %v (%v)", status, got, wantCreds, err)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatalf("%d: error contains the token", status)
		}
	}
}

func TestNoCredentialsSendsNoAuthorizationHeader(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		NewStandIn().ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, _ := NewHTTPCommitter(srv.URL, 5*time.Second)
	if _, err := c.Commit(context.Background(), "tool", "1111111111111111", sampleTree()); err != nil {
		t.Fatal(err)
	}
	for _, h := range got {
		if h != "" {
			t.Fatalf("unset credentials still sent Authorization %q", h)
		}
	}
}

func TestLoadCredentials(t *testing.T) {
	write := func(files map[string]string) string {
		dir := t.TempDir()
		for n, c := range files {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	dir := write(map[string]string{"user": testUser + "\n", "token": "  " + testToken + "\n"})
	user, token, set, err := LoadCredentials(dir)
	if err != nil || !set || user != testUser || token != testToken {
		t.Fatalf("both present: %q %q %v %v", user, token, set, err)
	}

	for name, d := range map[string]string{
		"unset":   "",
		"missing": filepath.Join(t.TempDir(), "no-such-dir"),
		"empty":   write(nil),
	} {
		if _, _, set, err := LoadCredentials(d); set || err != nil {
			t.Fatalf("%s dir: set=%v err=%v, want no credentials and no error", name, set, err)
		}
	}

	for name, d := range map[string]string{
		"only user":   write(map[string]string{"user": testUser}),
		"only token":  write(map[string]string{"token": testToken}),
		"empty user":  write(map[string]string{"user": "\n", "token": testToken}),
		"empty token": write(map[string]string{"user": testUser, "token": " "}),
	} {
		_, _, set, err := LoadCredentials(d)
		if err == nil || set {
			t.Fatalf("%s: want an error", name)
		}
		if strings.Contains(err.Error(), d) || strings.Contains(err.Error(), testToken) {
			t.Fatalf("%s: error leaks a path or the token: %v", name, err)
		}
	}
}
