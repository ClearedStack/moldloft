package buildspec

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"github.com/ClearedStack/moldloft/internal/errs"
)

// MaxSpecBytes is the largest build specification Moldloft will read.
const MaxSpecBytes = 1 << 20

const maxDepth = 16

// forbiddenKeys are field names that mark raw observation material. The
// specification forbids such material from ever reaching Moldloft, so a spec
// carrying one of these names is refused outright, wherever the name appears.
var forbiddenKeys = map[string]bool{
	"html": true, "raw_html": true,
	"screenshot": true, "screenshots": true,
	"snapshot": true, "snapshots": true,
	"observation": true, "observations": true,
}

type object map[string]any

// decode reads raw as one JSON object. Unlike encoding/json alone it refuses
// duplicate keys, invalid UTF-8, trailing data, forbidden field names and
// nesting deeper than maxDepth.
func decode(raw []byte) (object, *errs.Error) {
	if len(raw) > MaxSpecBytes {
		return nil, errs.Parsef("spec", "is larger than the 1 MiB limit")
	}
	if !utf8.Valid(raw) {
		return nil, errs.Parsef("spec", "is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, perr := decodeValue(dec, 0)
	if perr != nil {
		return nil, perr
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errs.Parsef("spec", "has data after the end of the JSON object")
	}
	top, ok := v.(object)
	if !ok {
		return nil, errs.Parsef("spec", "is not a JSON object")
	}
	return top, nil
}

func decodeValue(dec *json.Decoder, depth int) (any, *errs.Error) {
	notJSON := errs.Parsef("spec", "is not valid JSON")
	if depth > maxDepth {
		return nil, errs.Parsef("spec", "is nested too deeply")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, notJSON
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := object{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, notJSON
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, notJSON
			}
			if forbiddenKeys[key] {
				return nil, errs.Parsef("spec", "contains a forbidden raw observation field")
			}
			if _, dup := obj[key]; dup {
				return nil, errs.Parsef("spec", "repeats a field name inside one object")
			}
			val, perr := decodeValue(dec, depth+1)
			if perr != nil {
				return nil, perr
			}
			obj[key] = val
		}
		if _, err := dec.Token(); err != nil {
			return nil, notJSON
		}
		return obj, nil
	case '[':
		list := []any{}
		for dec.More() {
			val, perr := decodeValue(dec, depth+1)
			if perr != nil {
				return nil, perr
			}
			list = append(list, val)
		}
		if _, err := dec.Token(); err != nil {
			return nil, notJSON
		}
		return list, nil
	}
	return nil, notJSON
}
