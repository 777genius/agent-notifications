// Package strictjson validates bounded JSON without normalizing strings.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// ErrInvalid deliberately contains no caller-controlled text.
var ErrInvalid = errors.New("invalid_json")

// Budget limits raw bytes, nesting and entries in each container.
type Budget struct{ Bytes, Depth, Entries int }

// Validate checks a complete JSON value before permissive typed decoding.
func Validate(b []byte, budget Budget) error {
	if budget.Bytes < 1 || budget.Depth < 1 || budget.Entries < 1 || len(b) > budget.Bytes {
		return ErrInvalid
	}
	if err := validate(b, budget); err != nil {
		return ErrInvalid
	}
	return nil
}

func validate(b []byte, budget Budget) error {
	if !utf8.Valid(b) {
		return ErrInvalid
	}
	// encoding/json replaces isolated UTF-16 surrogates; disallow that ambiguity.
	for i := 0; i < len(b); i++ {
		if b[i] != '"' {
			continue
		}
		i++
		for i < len(b) && b[i] != '"' {
			if b[i] == '\\' {
				i++
				if i >= len(b) {
					return ErrInvalid
				}
				if b[i] == 'u' {
					if i+4 >= len(b) {
						return ErrInvalid
					}
					var v uint16
					for j := 1; j <= 4; j++ {
						n, ok := nibble(b[i+j])
						if !ok {
							return ErrInvalid
						}
						v = v*16 + uint16(n)
					}
					i += 4
					if v >= 0xd800 && v <= 0xdbff {
						if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
							return ErrInvalid
						}
						var w uint16
						for j := 3; j <= 6; j++ {
							n, ok := nibble(b[i+j])
							if !ok {
								return ErrInvalid
							}
							w = w*16 + uint16(n)
						}
						if w < 0xdc00 || w > 0xdfff {
							return ErrInvalid
						}
						i += 6
					} else if v >= 0xdc00 && v <= 0xdfff {
						return ErrInvalid
					}
				}
			}
			i++
		}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := jsonValue(d, 0, budget); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrInvalid
	}
	return nil
}
func nibble(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}
func jsonValue(d *json.Decoder, depth int, budget Budget) error {
	if depth > budget.Depth {
		return ErrInvalid
	}
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return ErrInvalid
			}
			if len(seen) >= budget.Entries {
				return ErrInvalid
			}
			seen[s] = true
			if e = jsonValue(d, depth+1, budget); e != nil {
				return e
			}
		}
	case '[':
		count := 0
		for d.More() {
			count++
			if count > budget.Entries {
				return ErrInvalid
			}
			if e = jsonValue(d, depth+1, budget); e != nil {
				return e
			}
		}
	default:
		return ErrInvalid
	}
	end, e := d.Token()
	if e != nil {
		return e
	}
	if (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return ErrInvalid
	}
	return nil
}
