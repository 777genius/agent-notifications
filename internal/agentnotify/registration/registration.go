// Package registration performs pure, bounded managed MCP configuration edits.
// Changed documents are semantically serialized; comments and formatting may change.
// Prior ownership must come from trusted installer state, never configuration content.
package registration

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/strictjson"
	"github.com/pelletier/go-toml/v2"
)

const MaxBytes = 1 << 20
const Server = "agent_notifications"

type Provider string

const (
	Codex  Provider = "codex"
	Claude Provider = "claude"
)

var (
	ErrInvalid   = errors.New("invalid_registration_config")
	ErrLimit     = errors.New("registration_size_limit")
	ErrConflict  = errors.New("managed_transport_conflict")
	ErrDuplicate = errors.New("duplicate_executable_conflict")
)

// Transport records exact field presence as well as values. Codex has no type field;
// Claude uses explicit stdio. An absent args field differs from an empty array.
type Transport struct {
	Command     string
	Args        []string
	ArgsPresent bool
	Type        string
	TypePresent bool
}
type Ownership struct {
	Provider  Provider
	Server    string
	Transport Transport
}
type Request struct {
	Provider Provider
	Input    []byte // Zero length means missing configuration.
	Command  string // Trusted desired executable, compared literally (no filesystem lookup).
	Args     []string
	Previous *Ownership
	Remove   bool
}
type Result struct {
	Bytes   []byte
	Owned   *Ownership // Persist with the configuration through the installer's CAS.
	Changed bool
}

func validString(s string) bool { return utf8.ValidString(s) && !bytes.ContainsRune([]byte(s), 0) }
func validTransport(t Transport) bool {
	if t.Command == "" || !validString(t.Command) || len(t.Command) > MaxBytes || (!t.ArgsPresent && len(t.Args) != 0) || (!t.TypePresent && t.Type != "") {
		return false
	}
	n := len(t.Command) + len(t.Type)
	for _, a := range t.Args {
		n += len(a)
		if !validString(a) || n > MaxBytes {
			return false
		}
	}
	return len(t.Args) <= 10000 && validString(t.Type)
}
func equal(a, b Transport) bool {
	if a.Command != b.Command || a.ArgsPresent != b.ArgsPresent || a.Type != b.Type || a.TypePresent != b.TypePresent || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	return true
}
func transport(m map[string]any) (Transport, bool) {
	var t Transport
	var ok bool
	t.Command, ok = m["command"].(string)
	if !ok {
		return t, false
	}
	if v, exists := m["args"]; exists {
		t.ArgsPresent = true
		a, ok := v.([]any)
		if !ok {
			return t, false
		}
		for _, v := range a {
			s, ok := v.(string)
			if !ok {
				return t, false
			}
			t.Args = append(t.Args, s)
		}
	}
	if v, exists := m["type"]; exists {
		t.TypePresent = true
		t.Type, ok = v.(string)
		if !ok {
			return t, false
		}
	}
	return t, validTransport(t)
}

// bounded checks decoded TOML nesting too, without interpreting TOML syntax.
func bounded(v any, depth int) bool {
	if depth > 64 {
		return false
	}
	switch x := v.(type) {
	case map[string]any:
		if len(x) > 10000 {
			return false
		}
		for _, v := range x {
			if !bounded(v, depth+1) {
				return false
			}
		}
	case []any:
		if len(x) > 10000 {
			return false
		}
		for _, v := range x {
			if !bounded(v, depth+1) {
				return false
			}
		}
	}
	return true
}
func decode(p Provider, b []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(b) == 0 {
		return m, nil
	}
	if p == Claude {
		if strictjson.Validate(b, strictjson.Budget{Bytes: MaxBytes, Depth: 64, Entries: 10000}) != nil {
			return nil, ErrInvalid
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if d.Decode(&m) != nil {
			return nil, ErrInvalid
		}
	} else {
		if toml.Unmarshal(b, &m) != nil {
			return nil, ErrInvalid
		}
	}
	if m == nil || !bounded(m, 0) {
		return nil, ErrInvalid
	}
	return m, nil
}

// Apply installs, updates, or explicitly removes only the matching managed server.
// It performs no I/O, executable resolution, consent, activation, or persistence.
func Apply(r Request) (Result, error) {
	fail := func(e error) (Result, error) { return Result{}, e }
	if r.Provider != Codex && r.Provider != Claude {
		return fail(ErrInvalid)
	}
	if len(r.Input) > MaxBytes {
		return fail(ErrLimit)
	}
	desired := Transport{Command: r.Command, Args: append([]string{}, r.Args...), ArgsPresent: true}
	if r.Provider == Claude {
		desired.Type = "stdio"
		desired.TypePresent = true
	}
	if !r.Remove && !validTransport(desired) {
		return fail(ErrInvalid)
	}
	if r.Previous != nil && (r.Previous.Provider != r.Provider || r.Previous.Server != Server || !validTransport(r.Previous.Transport)) {
		return fail(ErrConflict)
	}
	m, e := decode(r.Provider, r.Input)
	if e != nil {
		return fail(e)
	}
	key := "mcp_servers"
	if r.Provider == Claude {
		key = "mcpServers"
	}
	servers := map[string]any{}
	if v, exists := m[key]; exists {
		var ok bool
		servers, ok = v.(map[string]any)
		if !ok || servers == nil {
			return fail(ErrInvalid)
		}
	}
	for name, v := range servers {
		entry, ok := v.(map[string]any)
		if !ok || entry == nil {
			return fail(ErrInvalid)
		}
		// Reject malformed known transport fields even on unrelated entries, but allow
		// remote transports and preserve unknown provider-specific properties.
		if v, ok := entry["command"]; ok {
			if s, ok := v.(string); !ok || s == "" {
				return fail(ErrInvalid)
			}
		}
		if v, ok := entry["args"]; ok {
			a, ok := v.([]any)
			if !ok {
				return fail(ErrInvalid)
			}
			for _, v := range a {
				if _, ok := v.(string); !ok {
					return fail(ErrInvalid)
				}
			}
		}
		if v, ok := entry["type"]; ok {
			if _, ok := v.(string); !ok {
				return fail(ErrInvalid)
			}
		}
		if !r.Remove && name != Server && entry["command"] == desired.Command {
			return fail(ErrDuplicate)
		}
	}
	v, exists := servers[Server]
	if exists {
		current, ok := transport(v.(map[string]any))
		if !ok || r.Previous == nil || !equal(current, r.Previous.Transport) {
			return fail(ErrConflict)
		}
	}
	result := Result{Bytes: append([]byte(nil), r.Input...)}
	if r.Remove {
		if !exists {
			return result, nil
		}
		delete(servers, Server)
	} else {
		result.Owned = &Ownership{Provider: r.Provider, Server: Server, Transport: desired}
		if exists {
			current, _ := transport(v.(map[string]any))
			if equal(current, desired) {
				return result, nil
			}
		}
		entry := map[string]any{}
		if exists {
			entry = v.(map[string]any)
		}
		entry["command"] = desired.Command
		entry["args"] = desired.Args
		if desired.TypePresent {
			entry["type"] = desired.Type
		} else {
			delete(entry, "type")
		}
		servers[Server] = entry
	}
	m[key] = servers
	var out []byte
	if r.Provider == Claude {
		out, e = json.MarshalIndent(m, "", "  ")
	} else {
		out, e = toml.Marshal(m)
	}
	if e != nil {
		return fail(ErrInvalid)
	}
	if len(out) > MaxBytes {
		return fail(ErrLimit)
	}
	// Fail closed if serialization cannot preserve every semantic value.
	round, e := decode(r.Provider, out)
	if e != nil {
		return fail(e)
	}
	// Normalize desired []string to the decoder's []any before comparison.
	if !r.Remove {
		a := make([]any, len(desired.Args))
		for i, s := range desired.Args {
			a[i] = s
		}
		servers[Server].(map[string]any)["args"] = a
	}
	if !semanticEqual(m, round) {
		return fail(ErrInvalid)
	}
	result.Bytes = out
	result.Changed = true
	return result, nil
}

// TOML NaN is semantically stable even though IEEE NaN is unequal to itself.
func semanticEqual(a, b any) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		return ok && (x == y || math.IsNaN(x) && math.IsNaN(y))
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !semanticEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !semanticEqual(v, y[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
