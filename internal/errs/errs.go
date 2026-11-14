// Package errs holds the three public error codes Moldloft may report.
//
// An error message carries a field path inside the build specification and a
// fixed reason. It never repeats a value from the input, and it never names a
// machine, an address or a credential.
package errs

import (
	"errors"
	"fmt"
)

// Code is one of the three public error codes.
type Code string

const (
	// Timeout: something outside Moldloft took too long.
	Timeout Code = "E_TIMEOUT"
	// Parse: the data was broken.
	Parse Code = "E_PARSE"
	// NeedsSubtool: the data was valid and no rule covers it.
	NeedsSubtool Code = "E_NEEDS_SUBTOOL"
)

// Error is a public error: a code, the path of the field at fault and a fixed
// reason.
type Error struct {
	Code   Code
	Path   string
	Reason string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Reason)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Path, e.Reason)
}

// Timeoutf builds an E_TIMEOUT error for the field or boundary at path.
func Timeoutf(path, format string, args ...any) *Error {
	return &Error{Code: Timeout, Path: path, Reason: fmt.Sprintf(format, args...)}
}

// Parsef builds an E_PARSE error for the field at path.
func Parsef(path, format string, args ...any) *Error {
	return &Error{Code: Parse, Path: path, Reason: fmt.Sprintf(format, args...)}
}

// NeedsSubtoolf builds an E_NEEDS_SUBTOOL error for the field at path.
func NeedsSubtoolf(path, format string, args ...any) *Error {
	return &Error{Code: NeedsSubtool, Path: path, Reason: fmt.Sprintf(format, args...)}
}

// CodeOf returns the public code carried by err, or "" when err is not a
// public error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
