package errs

import (
	"errors"
	"testing"
)

func TestErrorFormattingAndCodeOf(t *testing.T) {
	cases := []struct {
		err  error
		code Code
		text string
	}{
		{Parsef("tool.slug", "is missing"), Parse, "E_PARSE: tool.slug: is missing"},
		{NeedsSubtoolf("pattern", "no rule"), NeedsSubtool, "E_NEEDS_SUBTOOL: pattern: no rule"},
		{Timeoutf("", "request timed out"), Timeout, "E_TIMEOUT: request timed out"},
		{errors.New("plain"), "", "plain"},
	}
	for _, c := range cases {
		if got := CodeOf(c.err); got != c.code {
			t.Errorf("CodeOf(%v) = %q, want %q", c.err, got, c.code)
		}
		if got := c.err.Error(); got != c.text {
			t.Errorf("Error() = %q, want %q", got, c.text)
		}
	}
}
