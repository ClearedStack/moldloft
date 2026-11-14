package plainforge

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ClearedStack/moldloft/internal/errs"
	"github.com/ClearedStack/moldloft/internal/repotree"
)

const maxGitResponseBytes = 1 << 20

// HTTPCommitter pushes generated repositories to Plainforge over Git's smart
// HTTP git-receive-pack protocol without requiring an external git binary.
type HTTPCommitter struct {
	baseURL string
	http    *http.Client
}

// NewHTTPCommitter builds a Plainforge smart HTTP client. It never follows
// redirects and never uses an HTTP proxy from the environment.
func NewHTTPCommitter(baseURL string, timeout time.Duration) (*HTTPCommitter, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return nil, errors.New("plainforge base address must start with http:// or https://")
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &HTTPCommitter{
		baseURL: trimmed,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy:               nil,
				MaxIdleConns:        4,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     30 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Commit queries refs/heads/main on Plainforge and pushes a deterministic
// initial or fast-forward commit.
func (c *HTTPCommitter) Commit(ctx context.Context, slug, specSHA256 string, tree repotree.Tree) (Receipt, error) {
	oldMain, err := c.fetchMainRef(ctx, slug)
	if err != nil {
		return Receipt{}, err
	}
	// If an identical initial commit is already at the tip of main from an
	// earlier attempt, treat the push as already committed.
	initial := BuildBundle(slug, specSHA256, "", tree)
	if oldMain == initial.CommitSHA1 {
		return Receipt{
			TreeSHA1:   initial.TreeSHA1,
			CommitSHA1: initial.CommitSHA1,
			Committed:  true,
		}, nil
	}

	var bundle Bundle
	if oldMain == ZeroHash {
		bundle = initial
	} else {
		bundle = BuildBundle(slug, specSHA256, oldMain, tree)
	}
	if err := c.pushBundle(ctx, slug, oldMain, bundle); err != nil {
		return Receipt{}, err
	}
	return Receipt{
		TreeSHA1:   bundle.TreeSHA1,
		CommitSHA1: bundle.CommitSHA1,
		Committed:  true,
	}, nil
}

func (c *HTTPCommitter) fetchMainRef(ctx context.Context, slug string) (string, error) {
	url := fmt.Sprintf("%s/%s.git/info/refs?service=git-receive-pack", c.baseURL, slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", errs.Parsef("plainforge", "could not build ref discovery request")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", errs.Timeoutf("plainforge", "ref discovery timed out")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGitResponseBytes+1))
	if err != nil || len(raw) > maxGitResponseBytes {
		return "", errs.Timeoutf("plainforge", "ref discovery response could not be read")
	}
	if resp.StatusCode != http.StatusOK {
		return "", classifyHTTPFailure(resp.StatusCode, raw)
	}
	packets, err := readPktLines(raw)
	if err != nil {
		return "", errs.Parsef("plainforge", "ref discovery returned invalid pkt-lines")
	}
	for _, pkt := range packets {
		if pkt == nil {
			continue // flush-pkt
		}
		line := strings.TrimSuffix(string(pkt), "\n")
		if strings.HasPrefix(line, "#") {
			continue
		}
		if nul := strings.IndexByte(line, 0); nul >= 0 {
			line = line[:nul]
		}
		hash, ref, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if ref == "refs/heads/main" {
			if !isHex40(hash) {
				return "", errs.Parsef("plainforge", "advertised main ref hash is invalid")
			}
			return hash, nil
		}
	}
	return ZeroHash, nil
}

func (c *HTTPCommitter) pushBundle(ctx context.Context, slug, oldMain string, bundle Bundle) error {
	var body bytes.Buffer
	cmd := fmt.Sprintf("%s %s refs/heads/main\x00report-status\n", oldMain, bundle.CommitSHA1)
	writePktLine(&body, []byte(cmd))
	body.WriteString("0000")
	body.Write(bundle.Pack)

	url := fmt.Sprintf("%s/%s.git/git-receive-pack", c.baseURL, slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return errs.Parsef("plainforge", "could not build receive-pack request")
	}
	req.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	req.Header.Set("Accept", "application/x-git-receive-pack-result")

	resp, err := c.http.Do(req)
	if err != nil {
		return errs.Timeoutf("plainforge", "receive-pack push timed out")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGitResponseBytes+1))
	if err != nil || len(raw) > maxGitResponseBytes {
		return errs.Timeoutf("plainforge", "receive-pack response could not be read")
	}
	if resp.StatusCode != http.StatusOK {
		return classifyHTTPFailure(resp.StatusCode, raw)
	}
	return verifyReceivePackReport(raw)
}

func verifyReceivePackReport(raw []byte) error {
	packets, err := readPktLines(raw)
	if err != nil {
		return errs.Parsef("plainforge", "receive-pack returned invalid pkt-lines")
	}
	var lines []string
	var sideband bytes.Buffer
	for _, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		switch pkt[0] {
		case 1:
			sideband.Write(pkt[1:])
		case 2:
			// Progress message; ignore.
		case 3:
			return classifyTextCode(string(pkt[1:]))
		default:
			lines = append(lines, strings.TrimSuffix(string(pkt), "\n"))
		}
	}
	if sideband.Len() > 0 {
		inner, err := readPktLines(sideband.Bytes())
		if err != nil {
			return errs.Parsef("plainforge", "receive-pack returned invalid sideband pkt-lines")
		}
		for _, pkt := range inner {
			if len(pkt) > 0 {
				lines = append(lines, strings.TrimSuffix(string(pkt), "\n"))
			}
		}
	}
	unpackOK := false
	refOK := false
	for _, line := range lines {
		switch {
		case line == "unpack ok":
			unpackOK = true
		case strings.HasPrefix(line, "unpack "):
			return classifyTextCode(line)
		case line == "ok refs/heads/main":
			refOK = true
		case strings.HasPrefix(line, "ng refs/heads/main "):
			return classifyTextCode(line)
		}
	}
	if !unpackOK || !refOK {
		return errs.Parsef("plainforge", "receive-pack did not confirm refs/heads/main")
	}
	return nil
}

func classifyHTTPFailure(status int, body []byte) error {
	if status == http.StatusTooManyRequests || status >= 500 {
		return errs.Timeoutf("plainforge", "returned temporary error status")
	}
	if status == http.StatusUnprocessableEntity {
		return errs.NeedsSubtoolf("plainforge", "has no rule for this push")
	}
	return classifyTextCode(string(body))
}

func classifyTextCode(text string) error {
	switch {
	case strings.Contains(text, string(errs.Timeout)):
		return errs.Timeoutf("plainforge", "push timed out")
	case strings.Contains(text, string(errs.NeedsSubtool)):
		return errs.NeedsSubtoolf("plainforge", "push requires an unsupported rule")
	default:
		return errs.Parsef("plainforge", "refused the repository push")
	}
}

func writePktLine(w *bytes.Buffer, payload []byte) {
	fmt.Fprintf(w, "%04x", len(payload)+4)
	w.Write(payload)
}

func readPktLines(raw []byte) ([][]byte, error) {
	var out [][]byte
	for len(raw) > 0 {
		if len(raw) < 4 {
			return nil, errors.New("short pkt-line header")
		}
		hdr := string(raw[:4])
		raw = raw[4:]
		n, err := strconv.ParseUint(hdr, 16, 16)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			out = append(out, nil)
			continue
		}
		if n < 4 || int(n-4) > len(raw) {
			return nil, errors.New("invalid pkt-line length")
		}
		ln := int(n - 4)
		out = append(out, raw[:ln])
		raw = raw[ln:]
	}
	return out, nil
}

func isHex40(s string) bool {
	if len(s) != 40 || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
