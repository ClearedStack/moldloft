package plainforge

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

// maxTokenBytes bounds the token file read.
const maxTokenBytes = 4096

// SetBasicAuth makes every later request carry HTTP basic auth, which Forgejo
// accepts for Git over HTTP. Call it before the first Commit.
func (c *HTTPCommitter) SetBasicAuth(user, token string) {
	c.user, c.token = user, token
}

// LoadCredentials reads the optional push credentials. With neither value set
// it returns set=false and no error. With only one set, or a token file that
// cannot be read or is empty, it returns an error with a fixed message that
// names neither the path nor any content.
func LoadCredentials(user, tokenFile string) (u, token string, set bool, err error) {
	user, tokenFile = strings.TrimSpace(user), strings.TrimSpace(tokenFile)
	if user == "" && tokenFile == "" {
		return "", "", false, nil
	}
	if user == "" || tokenFile == "" {
		return "", "", false, errors.New("MOLDLOFT_PLAINFORGE_USER and MOLDLOFT_PLAINFORGE_TOKEN_FILE must be set together")
	}
	f, err := os.Open(tokenFile)
	if err != nil {
		return "", "", false, errors.New("the plainforge token file could not be opened")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxTokenBytes+1))
	token = strings.TrimSpace(string(raw))
	if err != nil || len(raw) > maxTokenBytes || token == "" {
		return "", "", false, errors.New("the plainforge token file could not be read or is empty")
	}
	return user, token, true, nil
}

func (c *HTTPCommitter) authorize(req *http.Request) {
	if c.token != "" {
		req.SetBasicAuth(c.user, c.token)
	}
}
