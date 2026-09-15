package config

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"sort"
	"strings"
)

// Edits contains supported leaf JSON pointers. Values remain raw JSON; null is
// a value, and removal is requested only through Remove.
type Edits struct {
	Set    map[string]json.RawMessage `json:"set,omitempty"`
	Remove []string                   `json:"remove,omitempty"`
}

func editParts(pointer string) ([]string, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return nil, &Error{Code: ConfigInvalid}
	}
	parts := strings.Split(pointer[1:], "/")
	for i, p := range parts {
		for j := 0; j < len(p); j++ {
			if p[j] == '~' {
				j++
				if j == len(p) || (p[j] != '0' && p[j] != '1') {
					return nil, &Error{Code: ConfigInvalid}
				}
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	typ := reflect.TypeOf(Config{})
	for _, part := range parts {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		switch typ.Kind() {
		case reflect.Struct:
			found := false
			for j := 0; j < typ.NumField(); j++ {
				f := typ.Field(j)
				if strings.Split(f.Tag.Get("json"), ",")[0] == part {
					typ = f.Type
					found = true
					break
				}
			}
			if !found {
				return nil, &Error{Code: ConfigInvalid}
			}
		case reflect.Map:
			if part == "" {
				return nil, &Error{Code: ConfigInvalid}
			}
			typ = typ.Elem()
		default:
			return nil, &Error{Code: ConfigInvalid}
		}
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	// Objects may only be changed through their supported leaves. Whole arrays
	// (suppressFilters) and explicitly named payload values are intentional units.
	if typ.Kind() == reflect.Struct || typ.Kind() == reflect.Map {
		return nil, &Error{Code: ConfigInvalid}
	}
	return parts, nil
}

func sameJSON(a, b []byte) bool {
	var x, y any
	da := json.NewDecoder(bytes.NewReader(a))
	da.UseNumber()
	db := json.NewDecoder(bytes.NewReader(b))
	db.UseNumber()
	return da.Decode(&x) == nil && db.Decode(&y) == nil && reflect.DeepEqual(comparableJSON(x), comparableJSON(y))
}

// Compare decimal values exactly without float64 rounding or allocating a
// coefficient expanded to an attacker-controlled exponent (for example 1e999999).
func comparableJSON(v any) any {
	switch v := v.(type) {
	case json.Number:
		s := string(v)
		negative := strings.HasPrefix(s, "-")
		s = strings.TrimPrefix(s, "-")
		exponent := new(big.Int)
		if i := strings.IndexAny(s, "eE"); i >= 0 {
			exponent.SetString(s[i+1:], 10)
			s = s[:i]
		}
		fraction := 0
		if i := strings.IndexByte(s, '.'); i >= 0 {
			fraction = len(s) - i - 1
			s = s[:i] + s[i+1:]
		}
		s = strings.TrimLeft(s, "0")
		type decimal struct{ coefficient, exponent string }
		if s == "" {
			return decimal{"0", "0"}
		}
		coefficient := strings.TrimRight(s, "0")
		exponent.Add(exponent, big.NewInt(int64(len(s)-len(coefficient)-fraction)))
		if negative {
			coefficient = "-" + coefficient
		}
		return decimal{coefficient, exponent.String()}
	case map[string]any:
		for k, value := range v {
			v[k] = comparableJSON(value)
		}
		return v
	case []any:
		for i, value := range v {
			v[i] = comparableJSON(value)
		}
		return v
	default:
		return v
	}
}

func patchObject(raw []byte, parts []string, value json.RawMessage, remove bool, typ reflect.Type) ([]byte, bool, error) {
	obj := map[string]json.RawMessage{}
	// Optional config objects may be null. There is no descendant to remove;
	// setting a supported descendant materializes just that object and leaf.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if remove {
			return raw, false, nil
		}
		raw = nil
	}
	if len(raw) != 0 && (json.Unmarshal(raw, &obj) != nil || obj == nil) {
		return nil, false, &Error{Code: ConfigInvalid}
	}
	key := parts[0]
	// Preserve historical unambiguous casing instead of adding a shadow field.
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Struct {
		for k := range obj {
			if strings.EqualFold(k, key) {
				key = k
				break
			}
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if strings.EqualFold(strings.Split(f.Tag.Get("json"), ",")[0], key) {
				typ = f.Type
				break
			}
		}
	} else if typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	old, exists := obj[key]
	if len(parts) == 1 {
		if remove {
			if !exists {
				return raw, false, nil
			}
			delete(obj, key)
		} else {
			if exists && sameJSON(old, value) {
				return raw, false, nil
			}
			obj[key] = bytes.Clone(value)
		}
	} else {
		if !exists && remove {
			return raw, false, nil
		}
		next, changed, err := patchObject(old, parts[1:], value, remove, typ)
		if err != nil || !changed {
			return raw, false, err
		}
		obj[key] = next
	}
	out, err := json.Marshal(obj)
	return out, true, err
}

// ApplyRawEdits is pure: it validates the complete resulting runtime view but
// returns only raw storage bytes. A semantic no-op returns the original bytes.
func ApplyRawEdits(d Document, edits Edits, assets AssetContext) (Document, error) {
	if err := d.ValidateEditable(); err != nil {
		return Document{}, err
	}
	paths := make([]string, 0, len(edits.Set)+len(edits.Remove))
	for p := range edits.Set {
		paths = append(paths, p)
	}
	paths = append(paths, edits.Remove...)
	sort.Strings(paths)
	parsed := make(map[string][]string, len(paths))
	for i, p := range paths {
		parts, err := editParts(p)
		if err != nil {
			return Document{}, err
		}
		parsed[p] = parts
		if i > 0 && (p == paths[i-1] || strings.HasPrefix(p, paths[i-1]+"/")) {
			return Document{}, &Error{Code: ConfigInvalid}
		}
	}
	raw := d.Bytes()
	changed := false
	for _, p := range paths {
		value, set := edits.Set[p]
		if set {
			if !json.Valid(value) {
				return Document{}, &Error{Code: ConfigInvalid}
			}
			// Reuse strict bounded token validation, including duplicate keys/depth.
			probe := append([]byte(`{"value":`), value...)
			probe = append(probe, '}')
			if _, err := ParseDocument(probe, "", false); err != nil {
				return Document{}, &Error{Code: ConfigInvalid}
			}
		}
		next, c, err := patchObject(raw, parsed[p], value, !set, reflect.TypeOf(Config{}))
		if err != nil {
			return Document{}, err
		}
		if c {
			raw = next
			changed = true
		}
	}
	if !changed {
		if _, err := d.Effective(assets); err != nil {
			return Document{}, err
		}
		return d, nil
	}
	// RawMessage keeps integer spellings and all untouched data; encoding/json
	// sorts object keys in the branches decoded for edits.
	var tree any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return Document{}, &Error{Code: ConfigInvalid}
	}
	normalized, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return Document{}, &Error{Code: ConfigInvalid}
	}
	next, err := ParseDocument(append(normalized, '\n'), "", true)
	if err != nil {
		return Document{}, err
	}
	if err = next.ValidateEditable(); err != nil {
		return Document{}, err
	}
	if _, err = next.Effective(assets); err != nil {
		return Document{}, err
	}
	return next, nil
}
