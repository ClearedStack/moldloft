// Package decimal is exact fixed-point decimal arithmetic.
//
// A generated calculator does its sums twice: once here, when Moldloft checks
// the worked examples, and once in the browser, in js/decimal.js. Both sides
// use whole numbers of arbitrary size and the same rounding rule, so they
// cannot drift apart the way binary floating point would.
//
// Rounding is always "half away from zero": 0.125 to two places is 0.13 and
// -0.125 is -0.13.
package decimal

import (
	"math/big"
	"regexp"
	"strings"
)

// MaxScale is the largest number of digits allowed after the decimal point.
const MaxScale = 9

// D is the number Units / 10^Scale.
type D struct {
	Units *big.Int
	Scale int
}

var pattern = regexp.MustCompile(`^-?[0-9]{1,15}(\.[0-9]{1,9})?$`)

// Parse reads a plain decimal such as "12", "-0.5" or "1234.5600". It accepts
// up to 15 digits before the point and up to 9 after it, and nothing else: no
// exponent, no thousands separator, no leading plus sign.
func Parse(text string) (D, bool) {
	if !pattern.MatchString(text) {
		return D{}, false
	}
	scale := 0
	if dot := strings.IndexByte(text, '.'); dot >= 0 {
		scale = len(text) - dot - 1
	}
	units, ok := new(big.Int).SetString(strings.Replace(text, ".", "", 1), 10)
	if !ok {
		return D{}, false
	}
	return D{Units: units, Scale: scale}, true
}

// String writes the number with exactly Scale digits after the point.
func (d D) String() string {
	digits := new(big.Int).Abs(d.Units).String()
	if d.Scale > 0 {
		if len(digits) <= d.Scale {
			digits = strings.Repeat("0", d.Scale-len(digits)+1) + digits
		}
		cut := len(digits) - d.Scale
		digits = digits[:cut] + "." + digits[cut:]
	}
	if d.Units.Sign() < 0 {
		return "-" + digits
	}
	return digits
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// divRound divides n by den and rounds half away from zero. den must not be
// zero.
func divRound(n, den *big.Int) *big.Int {
	negative := (n.Sign() < 0) != (den.Sign() < 0)
	a := new(big.Int).Abs(n)
	b := new(big.Int).Abs(den)
	top := new(big.Int).Lsh(a, 1)
	top.Add(top, b)
	q := top.Quo(top, new(big.Int).Lsh(b, 1))
	if negative {
		q.Neg(q)
	}
	return q
}

// Rescale returns d with the given number of digits after the point, rounding
// half away from zero when digits are dropped.
func (d D) Rescale(scale int) D {
	if scale >= d.Scale {
		return D{Units: new(big.Int).Mul(d.Units, pow10(scale-d.Scale)), Scale: scale}
	}
	return D{Units: divRound(d.Units, pow10(d.Scale-scale)), Scale: scale}
}

// Cmp compares two numbers: -1, 0 or +1.
func (d D) Cmp(o D) int {
	s := max(d.Scale, o.Scale)
	return d.Rescale(s).Units.Cmp(o.Rescale(s).Units)
}

// IsZero reports whether d is zero.
func (d D) IsZero() bool { return d.Units.Sign() == 0 }

// Add returns d + o, exactly.
func (d D) Add(o D) D {
	s := max(d.Scale, o.Scale)
	return D{Units: new(big.Int).Add(d.Rescale(s).Units, o.Rescale(s).Units), Scale: s}
}

// Sub returns d - o, exactly.
func (d D) Sub(o D) D {
	s := max(d.Scale, o.Scale)
	return D{Units: new(big.Int).Sub(d.Rescale(s).Units, o.Rescale(s).Units), Scale: s}
}

// Mul returns d * o, exactly.
func (d D) Mul(o D) D {
	return D{Units: new(big.Int).Mul(d.Units, o.Units), Scale: d.Scale + o.Scale}
}

// Percent returns o percent of d, exactly: d * o / 100.
func (d D) Percent(o D) D {
	return D{Units: new(big.Int).Mul(d.Units, o.Units), Scale: d.Scale + o.Scale + 2}
}

// Div returns d / o rounded to scale digits. The caller must check that o is
// not zero.
func (d D) Div(o D, scale int) D {
	n := new(big.Int).Mul(d.Units, pow10(o.Scale+scale))
	den := new(big.Int).Mul(o.Units, pow10(d.Scale))
	return D{Units: divRound(n, den), Scale: scale}
}

// Abs returns the size of d without its sign.
func (d D) Abs() D {
	return D{Units: new(big.Int).Abs(d.Units), Scale: d.Scale}
}

var isoDatePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

var monthDays = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

func decDigits(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func isLeapYear(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

// ParseISODate parses a strict proleptic Gregorian YYYY-MM-DD date from
// 0001-01-01 through 9999-12-31 and returns its exact integer civil day count
// using pure integer arithmetic (with zero wall-clock or timezone calls).
func ParseISODate(text string) (int64, bool) {
	if !isoDatePattern.MatchString(text) {
		return 0, false
	}
	y := decDigits(text[0:4])
	m := decDigits(text[5:7])
	d := decDigits(text[8:10])
	if y < 1 || m < 1 || m > 12 {
		return 0, false
	}
	maxDay := monthDays[m-1]
	if m == 2 && isLeapYear(y) {
		maxDay = 29
	}
	if d < 1 || d > maxDay {
		return 0, false
	}
	// Howard Hinnant's civil-to-days formula, shifting the year start to March 1.
	yAdj := y
	if m <= 2 {
		yAdj--
	}
	era := yAdj / 400
	yoe := yAdj - era*400
	mp := m + 9
	if m > 2 {
		mp = m - 3
	}
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int64(era)*146097 + int64(doe), true
}
