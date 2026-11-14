package buildspec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ClearedStack/moldloft/internal/decimal"
	"github.com/ClearedStack/moldloft/internal/errs"
)

// Reader collects the first error found while a specification is read. Every
// accessor below is safe to call after an error: it returns a zero value and
// the first error is kept, so validation code reads top to bottom without an
// error check on every line. Call Err when a section has been read.
type Reader struct {
	err *errs.Error
}

// Err returns the first error recorded, or nil.
func (r *Reader) Err() error {
	if r.err == nil {
		return nil
	}
	return r.err
}

// Fail records e unless an earlier error is already held.
func (r *Reader) Fail(e *errs.Error) {
	if r.err == nil {
		r.err = e
	}
}

// Object is a JSON object together with its path inside the specification.
type Object struct {
	r      *Reader
	path   string
	fields object
	read   map[string]bool
}

// Value is any JSON value together with its path.
type Value struct {
	r    *Reader
	path string
	v    any
}

var (
	idPattern           = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	slugPattern         = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	musterItemIDPattern = regexp.MustCompile(`^PA-([0-9]{4}|[1-9][0-9]{4,8})$`)
	intPattern          = regexp.MustCompile(`^(0|[1-9][0-9]{0,3})$`)
	urlPattern          = regexp.MustCompile(`^https://[a-z0-9][a-z0-9.-]{0,99}(/[A-Za-z0-9._~/%#?&=+-]{0,150})?$`)
)

// IsID reports whether s is a well-formed identifier: a lower-case letter
// followed by up to 31 lower-case letters, digits or underscores.
func IsID(s string) bool { return idPattern.MatchString(s) }

// IsMusterItemID reports whether s matches Muster's item identifier form
// (^PA-([0-9]{4}|[1-9][0-9]{4,8})$).
func IsMusterItemID(s string) bool { return musterItemIDPattern.MatchString(s) }

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// Path is where this object sits in the specification.
func (o *Object) Path() string {
	if o == nil {
		return ""
	}
	return o.path
}

// Reader returns the error collector this object reports to.
func (o *Object) Reader() *Reader { return o.r }

// Has reports whether the object has the field.
func (o *Object) Has(key string) bool {
	if o == nil {
		return false
	}
	_, ok := o.fields[key]
	return ok
}

// Get returns a required field.
func (o *Object) Get(key string) Value {
	if o == nil {
		return Value{}
	}
	o.read[key] = true
	v, ok := o.fields[key]
	if !ok {
		o.r.Fail(errs.Parsef(join(o.path, key), "is missing"))
		return Value{r: o.r}
	}
	return Value{r: o.r, path: join(o.path, key), v: v}
}

// Keys returns every field name, sorted, and marks them all as read. It is
// for objects whose field names are identifiers chosen by the specification,
// such as the inputs of a worked example. A name that is not a well-formed
// identifier is an error.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	keys := make([]string, 0, len(o.fields))
	for k := range o.fields {
		if !IsID(k) {
			o.r.Fail(errs.Parsef(o.path, "has a field name that is not a well-formed identifier"))
			return nil
		}
		o.read[k] = true
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Close reports a field the caller never read: one the contract does not
// define.
func (o *Object) Close() {
	if o == nil {
		return
	}
	for k := range o.fields {
		if !o.read[k] {
			o.r.Fail(errs.Parsef(pathOrSpec(o.path), "has a field the contract does not define"))
			return
		}
	}
}

func pathOrSpec(path string) string {
	if path == "" {
		return "spec"
	}
	return path
}

// Path is where this value sits in the specification.
func (v Value) Path() string { return v.path }

func (v Value) ok() bool { return v.r != nil && v.path != "" }

// Object reads the value as a JSON object.
func (v Value) Object() *Object {
	if !v.ok() {
		return nil
	}
	fields, ok := v.v.(object)
	if !ok {
		v.r.Fail(errs.Parsef(v.path, "is not an object"))
		return nil
	}
	return &Object{r: v.r, path: v.path, fields: fields, read: map[string]bool{}}
}

// List reads the value as a JSON array holding between min and max items.
func (v Value) List(min, max int) []Value {
	if !v.ok() {
		return nil
	}
	items, ok := v.v.([]any)
	if !ok {
		v.r.Fail(errs.Parsef(v.path, "is not a list"))
		return nil
	}
	if len(items) < min || len(items) > max {
		v.r.Fail(errs.Parsef(v.path, "must hold between %d and %d items", min, max))
		return nil
	}
	out := make([]Value, len(items))
	for i, item := range items {
		out[i] = Value{r: v.r, path: fmt.Sprintf("%s[%d]", v.path, i), v: item}
	}
	return out
}

func (v Value) str() (string, bool) {
	if !v.ok() {
		return "", false
	}
	s, ok := v.v.(string)
	if !ok {
		v.r.Fail(errs.Parsef(v.path, "is not a string"))
		return "", false
	}
	return s, true
}

// Text reads a line of display text of at most maxRunes characters. It must
// not be empty, start or end with a space, hold a line break or any other
// control or invisible formatting character, or look like markup.
func (v Value) Text(maxRunes int) string {
	s, ok := v.str()
	if !ok {
		return ""
	}
	if reason := textFault(s, maxRunes); reason != "" {
		v.r.Fail(errs.Parsef(v.path, "%s", reason))
		return ""
	}
	return s
}

func textFault(s string, maxRunes int) string {
	if s == "" {
		return "is empty"
	}
	if utf8.RuneCountInString(s) > maxRunes {
		return fmt.Sprintf("is longer than %d characters", maxRunes)
	}
	if strings.TrimSpace(s) != s {
		return "starts or ends with a space"
	}
	runes := []rune(s)
	for i, c := range runes {
		if unicode.IsControl(c) || unicode.In(c, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs) || c == utf8.RuneError {
			return "holds a control or invisible character"
		}
		if c == '<' && i+1 < len(runes) {
			if n := runes[i+1]; n == '/' || n == '!' || n == '?' || unicode.IsLetter(n) {
				return "looks like markup, which a clean-room specification may not carry"
			}
		}
	}
	return ""
}

// ID reads an identifier.
func (v Value) ID() string {
	s, ok := v.str()
	if !ok {
		return ""
	}
	if !IsID(s) {
		v.r.Fail(errs.Parsef(v.path, "is not a well-formed identifier"))
		return ""
	}
	return s
}

// Slug reads a name made of lower-case letters, digits and hyphens.
func (v Value) Slug() string {
	s, ok := v.str()
	if !ok {
		return ""
	}
	if !isSlug(s) {
		v.r.Fail(errs.Parsef(v.path, "is not a well-formed name"))
		return ""
	}
	return s
}

func isSlug(s string) bool {
	return slugPattern.MatchString(s) && !strings.HasSuffix(s, "-") && !strings.Contains(s, "--")
}

// ItemID reads a pipeline item identifier: Muster's PA-0001 form or a
// standalone fixture name.
func (v Value) ItemID() string {
	s, ok := v.str()
	if !ok {
		return ""
	}
	if !IsMusterItemID(s) && !isSlug(s) {
		v.r.Fail(errs.Parsef(v.path, "is not a well-formed item identifier"))
		return ""
	}
	return s
}

// RawString reads a JSON string of at most maxRunes characters with no invalid
// UTF-8, no invisible/control characters (except '\n' when allowNewlines is
// true), and no markup.
func (v Value) RawString(maxRunes int, allowNewlines bool) string {
	s, ok := v.str()
	if !ok {
		return ""
	}
	if utf8.RuneCountInString(s) > maxRunes {
		v.r.Fail(errs.Parsef(v.path, "is longer than %d characters", maxRunes))
		return ""
	}
	runes := []rune(s)
	for i, c := range runes {
		if c == '\n' && allowNewlines {
			continue
		}
		if unicode.IsControl(c) || unicode.In(c, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs) || c == utf8.RuneError {
			v.r.Fail(errs.Parsef(v.path, "holds a control or invisible character"))
			return ""
		}
		if c == '<' && i+1 < len(runes) {
			if n := runes[i+1]; n == '/' || n == '!' || n == '?' || unicode.IsLetter(n) {
				v.r.Fail(errs.Parsef(v.path, "looks like markup, which a clean-room specification may not carry"))
				return ""
			}
		}
	}
	return s
}

// Bool reads a JSON boolean.
func (v Value) Bool() bool {
	if !v.ok() {
		return false
	}
	b, ok := v.v.(bool)
	if !ok {
		v.r.Fail(errs.Parsef(v.path, "is not a boolean"))
		return false
	}
	return b
}

// URL reads an https address written in plain ASCII.
func (v Value) URL() string {
	s, ok := v.str()
	if !ok {
		return ""
	}
	if !urlPattern.MatchString(s) {
		v.r.Fail(errs.Parsef(v.path, "is not a plain https address"))
		return ""
	}
	return s
}

// Decimal reads a decimal number written as a string, such as "12.50".
func (v Value) Decimal() (decimal.D, string) {
	s, ok := v.str()
	if !ok {
		return decimal.D{}, ""
	}
	d, ok := decimal.Parse(s)
	if !ok {
		v.r.Fail(errs.Parsef(v.path, "is not a plain decimal number written as a string"))
		return decimal.D{}, ""
	}
	return d, s
}

// Int reads a whole JSON number between min and max.
func (v Value) Int(min, max int) int {
	if !v.ok() {
		return 0
	}
	num, ok := v.v.(json.Number)
	if !ok || !intPattern.MatchString(num.String()) {
		v.r.Fail(errs.Parsef(v.path, "is not a whole number"))
		return 0
	}
	n := 0
	for _, c := range num.String() {
		n = n*10 + int(c-'0')
	}
	if n < min || n > max {
		v.r.Fail(errs.Parsef(v.path, "must be between %d and %d", min, max))
		return 0
	}
	return n
}
