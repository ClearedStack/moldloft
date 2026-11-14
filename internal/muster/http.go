package muster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ClearedStack/moldloft/internal/errs"
)

// DefaultTokenFile is where Kubernetes mounts a pod's ServiceAccount token.
const DefaultTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// TokenSource returns the bearer token to present on one Muster request.
type TokenSource func() (string, error)

// FileTokenSource reads the bearer token from path on every call so rotated
// projected ServiceAccount tokens are picked up without restarting the pod.
// Errors never repeat path or token contents.
func FileTokenSource(path string) TokenSource {
	return func() (string, error) {
		f, err := os.Open(path)
		if err != nil {
			return "", errs.Parsef("token", "service account token could not be opened")
		}
		defer f.Close()
		const maxTokenBytes = 16 << 10
		raw, err := io.ReadAll(io.LimitReader(f, maxTokenBytes+1))
		if err != nil || len(raw) > maxTokenBytes {
			return "", errs.Parsef("token", "service account token could not be read")
		}
		tok := strings.TrimSpace(string(raw))
		if tok == "" || strings.ContainsAny(tok, " \t\r\n") {
			return "", errs.Parsef("token", "service account token is empty or malformed")
		}
		return tok, nil
	}
}

// StaticTokenSource returns a fixed token in tests.
func StaticTokenSource(token string) TokenSource {
	return func() (string, error) {
		if token == "" {
			return "", nil
		}
		return token, nil
	}
}

// HTTPClient speaks Muster's contract v1 over cluster-private HTTP.
type HTTPClient struct {
	baseURL string
	token   TokenSource
	http    *http.Client
}

// NewHTTPClient builds a Muster HTTP client. It never follows redirects and
// never uses an HTTP proxy from the environment.
func NewHTTPClient(baseURL string, token TokenSource, timeout time.Duration) (*HTTPClient, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return nil, errors.New("muster base address must start with http:// or https://")
	}
	if token == nil {
		token = FileTokenSource(DefaultTokenFile)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}
	return &HTTPClient{
		baseURL: trimmed,
		token:   token,
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type leaseReq struct {
	Contract string `json:"contract"`
}

type heartbeatReq struct {
	Contract string `json:"contract"`
	Lease    string `json:"lease"`
}

type completeReq struct {
	Contract string          `json:"contract"`
	Lease    string          `json:"lease"`
	State    string          `json:"state"`
	Result   json.RawMessage `json:"result,omitempty"`
}

type failReq struct {
	Contract string    `json:"contract"`
	Lease    string    `json:"lease"`
	Code     errs.Code `json:"code"`
}

// Lease calls POST /v1/lease. It returns (nil, nil) on 204 No Content.
func (c *HTTPClient) Lease(ctx context.Context) (*Grant, error) {
	status, raw, err := c.post(ctx, "/v1/lease", leaseReq{Contract: ContractVersion})
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		if len(bytes.TrimSpace(raw)) != 0 {
			return nil, errs.Parsef("muster", "204 response carried a body")
		}
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, parseRefusal(status, raw)
	}
	var grant Grant
	if err := decodeStrict(raw, &grant); err != nil {
		return nil, err
	}
	if grant.Lease == "" || grant.Kind == "" || grant.ID == "" || grant.State == "" || grant.Lane == "" || grant.Attempt < 1 || grant.Expires == "" {
		return nil, errs.Parsef("muster", "lease grant is missing required fields")
	}
	return &grant, nil
}

// Heartbeat calls POST /v1/heartbeat.
func (c *HTTPClient) Heartbeat(ctx context.Context, lease string) (Beat, error) {
	if lease == "" {
		return Beat{}, errs.Parsef("lease", "is empty")
	}
	status, raw, err := c.post(ctx, "/v1/heartbeat", heartbeatReq{Contract: ContractVersion, Lease: lease})
	if err != nil {
		return Beat{}, err
	}
	if status != http.StatusOK {
		return Beat{}, parseRefusal(status, raw)
	}
	var beat Beat
	if err := decodeStrict(raw, &beat); err != nil {
		return Beat{}, err
	}
	if beat.Lease == "" || beat.Expires == "" {
		return Beat{}, errs.Parsef("muster", "heartbeat response is missing required fields")
	}
	return beat, nil
}

// Complete calls POST /v1/complete.
func (c *HTTPClient) Complete(ctx context.Context, lease, state string, result json.RawMessage) (CompleteAck, error) {
	if lease == "" || state == "" {
		return CompleteAck{}, errs.Parsef("complete", "lease and state are required")
	}
	var trimmed json.RawMessage
	if len(bytes.TrimSpace(result)) > 0 {
		if !isJSONObject(result) {
			return CompleteAck{}, errs.Parsef("result", "must be a JSON object")
		}
		trimmed = bytes.TrimSpace(result)
	}
	status, raw, err := c.post(ctx, "/v1/complete", completeReq{
		Contract: ContractVersion,
		Lease:    lease,
		State:    state,
		Result:   trimmed,
	})
	if err != nil {
		return CompleteAck{}, err
	}
	if status != http.StatusOK {
		return CompleteAck{}, parseRefusal(status, raw)
	}
	var ack CompleteAck
	if err := decodeStrict(raw, &ack); err != nil {
		return CompleteAck{}, err
	}
	if ack.ID == "" || ack.State == "" {
		return CompleteAck{}, errs.Parsef("muster", "complete response is missing required fields")
	}
	return ack, nil
}

// Fail calls POST /v1/fail with E_PARSE or E_TIMEOUT. Missing rules must use
// NeedsSubtool instead.
func (c *HTTPClient) Fail(ctx context.Context, lease string, code errs.Code) (FailAck, error) {
	if lease == "" {
		return FailAck{}, errs.Parsef("lease", "is empty")
	}
	if code != errs.Parse && code != errs.Timeout {
		return FailAck{}, errs.Parsef("code", "fail requires E_PARSE or E_TIMEOUT")
	}
	status, raw, err := c.post(ctx, "/v1/fail", failReq{
		Contract: ContractVersion,
		Lease:    lease,
		Code:     code,
	})
	if err != nil {
		return FailAck{}, err
	}
	if status != http.StatusOK {
		return FailAck{}, parseRefusal(status, raw)
	}
	var ack FailAck
	if err := decodeStrict(raw, &ack); err != nil {
		return FailAck{}, err
	}
	if ack.ID == "" || ack.State == "" {
		return FailAck{}, errs.Parsef("muster", "fail response is missing required fields")
	}
	return ack, nil
}

// NeedsSubtool calls POST /v1/needs_subtool.
func (c *HTTPClient) NeedsSubtool(ctx context.Context, lease string) (PauseAck, error) {
	if lease == "" {
		return PauseAck{}, errs.Parsef("lease", "is empty")
	}
	status, raw, err := c.post(ctx, "/v1/needs_subtool", heartbeatReq{
		Contract: ContractVersion,
		Lease:    lease,
	})
	if err != nil {
		return PauseAck{}, err
	}
	if status != http.StatusOK {
		return PauseAck{}, parseRefusal(status, raw)
	}
	var ack PauseAck
	if err := decodeStrict(raw, &ack); err != nil {
		return PauseAck{}, err
	}
	if ack.ID == "" || ack.State == "" {
		return PauseAck{}, errs.Parsef("muster", "needs_subtool response is missing required fields")
	}
	return ack, nil
}

func (c *HTTPClient) post(ctx context.Context, path string, payload any) (int, []byte, error) {
	tok, err := c.token()
	if err != nil {
		return 0, nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, errs.Parsef("request", "could not encode JSON request")
	}
	if len(body) > MaxWorkerRequestBytes {
		return 0, nil, errs.Parsef("request", "exceeds Muster 64 KiB body limit")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errs.Parsef("request", "could not build HTTP request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, errs.Timeoutf("muster", "request to Muster did not complete in time")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return 0, nil, errs.Timeoutf("muster", "response body from Muster could not be read")
	}
	if len(raw) > MaxResponseBytes {
		return 0, nil, errs.Parsef("muster", "response body exceeded safety limit")
	}
	return resp.StatusCode, raw, nil
}

type wireRefusal struct {
	Error  string `json:"error"`
	Cause  string `json:"cause"`
	Reason string `json:"reason"`
}

func parseRefusal(status int, raw []byte) error {
	var w wireRefusal
	if err := decodeStrict(raw, &w); err != nil {
		if status == http.StatusTooManyRequests || status >= 500 {
			return errs.Timeoutf("muster", "returned a temporary error status")
		}
		return errs.Parsef("muster", "returned an unexpected response")
	}
	code := errs.Code(w.Error)
	if code != errs.Parse && code != errs.NeedsSubtool && code != errs.Timeout {
		return errs.Parsef("muster", "returned an unknown public error code")
	}
	if !knownCauses[w.Cause] || w.Reason == "" {
		return errs.Parsef("muster", "returned an unknown refusal cause")
	}
	return &Refusal{Status: status, Code: code, Cause: w.Cause}
}

func decodeStrict(raw []byte, dst any) error {
	if !utf8.Valid(raw) {
		return errs.Parsef("muster", "response is not valid UTF-8")
	}
	if err := checkNoDuplicateKeys(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errs.Parsef("muster", "response does not match contract v1")
	}
	if _, err := dec.Token(); err != io.EOF {
		return errs.Parsef("muster", "response has trailing data")
	}
	return nil
}

func checkNoDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return checkValueNoDup(dec, 0)
}

func checkValueNoDup(dec *json.Decoder, depth int) error {
	if depth > 32 {
		return errs.Parsef("muster", "response is nested too deeply")
	}
	tok, err := dec.Token()
	if err != nil {
		return errs.Parsef("muster", "response is not valid JSON")
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return errs.Parsef("muster", "response is not valid JSON")
			}
			k, ok := kt.(string)
			if !ok {
				return errs.Parsef("muster", "response is not valid JSON")
			}
			if seen[k] {
				return errs.Parsef("muster", "response repeats a field name")
			}
			seen[k] = true
			if err := checkValueNoDup(dec, depth+1); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return errs.Parsef("muster", "response is not valid JSON")
		}
	case '[':
		for dec.More() {
			if err := checkValueNoDup(dec, depth+1); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return errs.Parsef("muster", "response is not valid JSON")
		}
	}
	return nil
}

func isJSONObject(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	if err := checkNoDuplicateKeys(raw); err != nil {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}
