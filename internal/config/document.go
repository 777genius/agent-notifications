package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxDocumentBytes = 4 << 20
const MaxDocumentDepth = 128

// Document owns immutable original bytes. Accessors return independent copies.
// Runtime expansion must never be persisted as a Document.
type Document struct {
	original  []byte
	schema    int
	ambiguous string
	revision  string
}

func (d Document) Bytes() []byte      { return bytes.Clone(d.original) }
func (d Document) SchemaVersion() int { return d.schema }
func (d Document) Revision() string   { return d.revision }
func (d Document) Raw() map[string]json.RawMessage {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(d.original, &raw)
	return raw
}

// ValidateEditable rejects ambiguous known-field casing while preserving
// historical unambiguous case-insensitive reads. It also validates known
// settings structurally, treating environment placeholders as nonempty.
func (d Document) ValidateEditable() error {
	if d.ambiguous != "" {
		return &Error{Code: ConfigInvalid, Pointer: d.ambiguous}
	}
	_, err := d.Effective(AssetContext{LookupEnv: func(string) (string, bool) { return "CONFIG_ENV_PLACEHOLDER", true }})
	return err
}

// ParseDocument performs bounded strict parsing without converting numbers to float64.
// physicalPath/existence form part of the opaque revision used by future CAS writers.
func ParseDocument(data []byte, physicalPath string, exists bool) (Document, error) {
	fail := func(code Code, off int64) (Document, error) {
		return Document{}, &Error{Code: code, Path: physicalPath, Offset: off}
	}
	if len(data) > MaxDocumentBytes || !utf8.Valid(data) || bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return fail(ConfigInvalid, 0)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSON(dec, 0); err != nil {
		return fail(ConfigInvalid, dec.InputOffset())
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail(ConfigInvalid, dec.InputOffset())
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || raw == nil {
		return fail(ConfigInvalid, 0)
	}
	schema := 1
	// schemaVersion is metadata, with the same historical case matching as Go fields.
	count := 0
	for k, v := range raw {
		if strings.EqualFold(k, "schemaVersion") {
			count++
			n := string(v)
			valid := n != ""
			for _, c := range n {
				if c < '0' || c > '9' {
					valid = false
				}
			}
			if !valid || n == "0" {
				return fail(ConfigInvalid, 0)
			}
			if n != "1" && n != "2" {
				return fail(ConfigUnsupportedSchema, 0)
			}
			schema, _ = strconv.Atoi(n)
		}
	}
	if count > 1 {
		return fail(ConfigInvalid, 0)
	}
	if err := validateAgents(raw, schema); err != nil {
		return fail(ConfigInvalid, 0)
	}
	// Typed decode checks known-field types while ignoring additive unknown fields.
	var typed Config
	if decodeTyped(data, &typed) != nil {
		return fail(ConfigInvalid, 0)
	}
	d := Document{original: bytes.Clone(data), schema: schema}
	d.ambiguous = ambiguousFields(data, reflect.TypeOf(Config{}), "")
	if schema == 2 && d.ambiguous != "" {
		return fail(ConfigInvalid, 0)
	}
	h := sha256.New()
	h.Write([]byte(physicalPath))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatBool(exists)))
	h.Write([]byte{0})
	h.Write(data)
	d.revision = hex.EncodeToString(h.Sum(nil))
	return d, nil
}

func walkJSON(d *json.Decoder, depth int) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= MaxDocumentDepth {
		return &Error{Code: ConfigInvalid}
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return &Error{Code: ConfigInvalid}
			}
			seen[s] = true
			if err := walkJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := walkJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return &Error{Code: ConfigInvalid}
	}
	_, err = d.Token()
	return err
}

func pointerPart(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func ambiguousFields(data []byte, typ reflect.Type, ptr string) string {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		var a []json.RawMessage
		if json.Unmarshal(data, &a) != nil {
			return ""
		}
		for i, v := range a {
			if p := ambiguousFields(v, typ.Elem(), ptr+"/"+strconv.Itoa(i)); p != "" {
				return p
			}
		}
		return ""
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return ""
	}
	if typ.Kind() == reflect.Map {
		for k, v := range obj {
			if p := ambiguousFields(v, typ.Elem(), ptr+"/"+pointerPart(k)); p != "" {
				return p
			}
		}
		return ""
	}
	if typ.Kind() != reflect.Struct {
		return ""
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" {
			name = f.Name
		}
		count := 0
		for k, v := range obj {
			if strings.EqualFold(k, name) {
				count++
				if p := ambiguousFields(v, f.Type, ptr+"/"+pointerPart(k)); p != "" {
					return p
				}
			}
		}
		if count > 1 {
			return ptr + "/" + pointerPart(name)
		}
	}
	return ""
}

func decodeTyped(data []byte, c *Config) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(c)
}
