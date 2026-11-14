package decimal

import "testing"

func must(t *testing.T, s string) D {
	t.Helper()
	d, ok := Parse(s)
	if !ok {
		t.Fatalf("Parse(%q) refused", s)
	}
	return d
}

func TestParseAndString(t *testing.T) {
	good := map[string]string{
		"0": "0", "12": "12", "-0.5": "-0.5", "0012.340": "12.340",
		"-0": "0", "-0.00": "0.00", "0.000000001": "0.000000001",
		"999999999999999.999999999": "999999999999999.999999999",
	}
	for in, want := range good {
		if got := must(t, in).String(); got != want {
			t.Errorf("Parse(%q).String() = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "+1", "1.", ".5", "1e3", "1,000", " 1", "1 ", "--1", "1.0000000001", "1234567890123456", "0x10", "١"} {
		if _, ok := Parse(in); ok {
			t.Errorf("Parse(%q) accepted, want refused", in)
		}
	}
}

func TestRescaleRoundsHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		in    string
		scale int
		want  string
	}{
		{"0.125", 2, "0.13"}, {"-0.125", 2, "-0.13"},
		{"0.124", 2, "0.12"}, {"-0.124", 2, "-0.12"},
		{"0.5", 0, "1"}, {"-0.5", 0, "-1"}, {"0.4", 0, "0"}, {"-0.4", 0, "0"},
		{"2.5", 0, "3"}, {"1.005", 2, "1.01"}, {"7", 3, "7.000"},
	}
	for _, c := range cases {
		if got := must(t, c.in).Rescale(c.scale).String(); got != c.want {
			t.Errorf("%s to %d places = %s, want %s", c.in, c.scale, got, c.want)
		}
	}
}

func TestArithmetic(t *testing.T) {
	a, b := must(t, "40.00"), must(t, "32.5")
	check := func(name, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	check("add", a.Add(b).String(), "72.50")
	check("sub", b.Sub(a).String(), "-7.50")
	check("mul", a.Mul(b).String(), "1300.000")
	check("percent", a.Percent(b).String(), "13.00000")
	check("div", must(t, "10").Div(must(t, "3"), 4).String(), "3.3333")
	check("div", must(t, "-20").Div(must(t, "3"), 2).String(), "-6.67")
	check("div", must(t, "1").Div(must(t, "-8"), 2).String(), "-0.13")
	check("div", must(t, "0.1").Div(must(t, "0.3"), 3).String(), "0.333")
	if a.Cmp(b) != 1 || b.Cmp(a) != -1 || a.Cmp(must(t, "40")) != 0 {
		t.Error("Cmp is wrong")
	}
	if !must(t, "0.00").IsZero() || a.IsZero() {
		t.Error("IsZero is wrong")
	}
}

func TestParseISODate(t *testing.T) {
	mustDate := func(s string) int64 {
		t.Helper()
		d, ok := ParseISODate(s)
		if !ok {
			t.Fatalf("ParseISODate(%q) refused", s)
		}
		return d
	}
	if got := mustDate("2024-03-01") - mustDate("2024-02-28"); got != 2 {
		t.Errorf("leap year 2024 Feb->Mar diff = %d, want 2", got)
	}
	if got := mustDate("2025-03-01") - mustDate("2025-02-28"); got != 1 {
		t.Errorf("common year 2025 Feb->Mar diff = %d, want 1", got)
	}
	if got := mustDate("2000-03-01") - mustDate("2000-02-28"); got != 2 {
		t.Errorf("400-year leap 2000 Feb->Mar diff = %d, want 2", got)
	}
	if got := mustDate("2100-03-01") - mustDate("2100-02-28"); got != 1 {
		t.Errorf("century non-leap 2100 Feb->Mar diff = %d, want 1", got)
	}
	if got := mustDate("2026-01-01") - mustDate("2026-01-31"); got != -30 {
		t.Errorf("reverse date diff = %d, want -30", got)
	}
	_ = mustDate("0001-01-01")
	_ = mustDate("9999-12-31")

	bad := []string{
		"", "2025-2-1", "2025/02/01", "0000-01-01", "2025-00-10", "2025-13-10",
		"2025-01-00", "2025-01-32", "2025-02-29", "2100-02-29", "2025-04-31",
		" 2025-01-01", "2025-01-01 ", "2025-01-01T00:00:00Z",
	}
	for _, s := range bad {
		if _, ok := ParseISODate(s); ok {
			t.Errorf("ParseISODate(%q) accepted, want refused", s)
		}
	}
}
