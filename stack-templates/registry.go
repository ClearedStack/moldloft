// Package stacktemplates holds the registry of stack templates. Each template
// lives in its own folder beside this file.
package stacktemplates

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed registry.yaml
var registryYAML string

// Entry is one line of the registry.
type Entry struct {
	Name   string
	Path   string
	Status string
}

// Built reports whether the template has been written.
func (e Entry) Built() bool { return e.Status == "built" }

// Entries returns the registry in file order. registry.yaml is compiled into
// the program, so a mistake in it is a build mistake and Entries panics.
func Entries() []Entry {
	entries, err := parse(registryYAML)
	if err != nil {
		panic("stack-templates/registry.yaml: " + err.Error())
	}
	return entries
}

// parse reads the small part of YAML that registry.yaml uses: comments, one
// "templates:" key, and under it a list of maps with the string fields name,
// path and status. Anything else is an error. The standard library has no
// YAML reader and this file does not justify a dependency.
func parse(text string) ([]Entry, error) {
	var entries []Entry
	started := false
	for n, line := range strings.Split(text, "\n") {
		fail := func(msg string) ([]Entry, error) {
			return nil, fmt.Errorf("line %d: %s", n+1, msg)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !started {
			if line != "templates:" {
				return fail("expected \"templates:\"")
			}
			started = true
			continue
		}
		var field string
		switch {
		case strings.HasPrefix(line, "  - "):
			entries = append(entries, Entry{})
			field = line[4:]
		case strings.HasPrefix(line, "    ") && len(entries) > 0:
			field = line[4:]
		default:
			return fail("unexpected indentation")
		}
		key, value, ok := strings.Cut(field, ": ")
		if !ok || value == "" || strings.ContainsAny(value, " #'\":{}[]") {
			return fail("expected \"key: value\" with a plain value")
		}
		e := &entries[len(entries)-1]
		var target *string
		switch key {
		case "name":
			target = &e.Name
		case "path":
			target = &e.Path
		case "status":
			if value != "built" && value != "planned" {
				return fail("status must be built or planned")
			}
			target = &e.Status
		default:
			return fail("unknown key")
		}
		if *target != "" {
			return fail("key given twice")
		}
		*target = value
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Name == "" || e.Path == "" || e.Status == "" {
			return nil, fmt.Errorf("an entry is missing name, path or status")
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("a name is listed twice")
		}
		seen[e.Name] = true
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no templates listed")
	}
	return entries, nil
}
