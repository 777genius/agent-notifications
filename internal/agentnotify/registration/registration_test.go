package registration

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestLifecyclePreservation(t *testing.T) {
	fixtures := map[Provider]string{
		Codex: `title = "foreign"
"quoted.key" = { dotted = [{ a = 1 }, { a = 2 }] }
date = 1979-05-27
stamp = 1979-05-27T07:32:00Z
local = 1979-05-27T07:32:00
clock = 07:32:00
[mcp_servers.remote]
url = "https://example.invalid/mcp"
[mcp_servers.agent_notifications]
command = "old"
args = []
enabled = false
disabled_tools = ["danger"]
enabled_tools = ["notify"]
startup_timeout_sec = 123
tool_timeout_sec = 456
cwd = "private workspace"
env = { TOKEN = "fixture" }
approvals = { notify = "ask" }
unknown = { nested = [true, false] }
`,
		Claude: `{"foreign":{"large":123456789012345678901234567890,"array":[null,true,"hi"]},"mcpServers":{"remote":{"type":"http","url":"https://example.invalid"},"agent_notifications":{"command":"old","args":[],"type":"stdio","enabled":false,"disabled_tools":["danger"],"enabled_tools":["notify"],"timeout":123,"cwd":"private workspace","env":{"TOKEN":"fixture"},"approvals":{"notify":"ask"},"unknown":{"nested":[true,false]}}}}`,
	}
	for p, fixture := range fixtures {
		t.Run(string(p), func(t *testing.T) {
			raw := []byte(fixture)
			before, _ := decode(p, raw)
			key := "mcp_servers"
			if p == Claude {
				key = "mcpServers"
			}
			tr, _ := transport(before[key].(map[string]any)[Server].(map[string]any))
			prior := &Ownership{p, Server, tr}
			priorCopy := *prior
			req := Request{Provider: p, Input: raw, Command: "/path with spaces/quote\"$`\\exe", Args: []string{"mcp", "a\nb", "$(touch nope)", "雪"}, Previous: prior}
			got, err := Apply(req)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Changed {
				t.Fatal("not changed")
			}
			if string(raw) != fixture || !reflect.DeepEqual(*prior, priorCopy) {
				t.Fatal("mutated input")
			}
			after, _ := decode(p, got.Bytes)
			oldEntry := before[key].(map[string]any)[Server].(map[string]any)
			newEntry := after[key].(map[string]any)[Server].(map[string]any)
			for _, k := range []string{"command", "args", "type"} {
				delete(oldEntry, k)
				delete(newEntry, k)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("foreign fields changed")
			}
			req.Input = got.Bytes
			req.Previous = got.Owned
			again, err := Apply(req)
			if err != nil || again.Changed || !bytes.Equal(again.Bytes, got.Bytes) {
				t.Fatalf("no-op: %v", err)
			}
			req.Remove = true
			removed, err := Apply(req)
			if err != nil || !removed.Changed || removed.Owned != nil {
				t.Fatalf("remove: %v", err)
			}
			remaining, _ := decode(p, removed.Bytes)
			delete(before[key].(map[string]any), Server)
			if !reflect.DeepEqual(before, remaining) {
				t.Fatal("remove lost foreign data")
			}
			req.Input = removed.Bytes
			req.Previous = nil
			twice, err := Apply(req)
			if err != nil || twice.Changed || !bytes.Equal(twice.Bytes, removed.Bytes) {
				t.Fatalf("remove no-op: %v", err)
			}
		})
	}
}
func TestCreateAndConflicts(t *testing.T) {
	for _, p := range []Provider{Codex, Claude} {
		t.Run(string(p), func(t *testing.T) {
			r := Request{Provider: p, Command: "exe", Args: []string{"mcp"}}
			first, e := Apply(r)
			if e != nil {
				t.Fatal(e)
			}
			r.Input = first.Bytes
			if _, e = Apply(r); !errors.Is(e, ErrConflict) {
				t.Fatal("adopt", e)
			}
			r.Previous = first.Owned
			r.Command = "next"
			updated, e := Apply(r)
			if e != nil {
				t.Fatal(e)
			}
			r.Input = updated.Bytes
			if _, e = Apply(r); !errors.Is(e, ErrConflict) {
				t.Fatal("stale", e)
			}
			r.Remove = true
			if _, e = Apply(r); !errors.Is(e, ErrConflict) {
				t.Fatal("stale remove", e)
			}
			r.Previous = &Ownership{Provider: "other", Server: Server, Transport: first.Owned.Transport}
			if _, e = Apply(r); !errors.Is(e, ErrConflict) {
				t.Fatal("provider binding", e)
			}
			r.Previous = &Ownership{Provider: p, Server: "other", Transport: first.Owned.Transport}
			if _, e = Apply(r); !errors.Is(e, ErrConflict) {
				t.Fatal("server binding", e)
			}
			r = Request{Provider: p, Command: "exe"}
			if p == Codex {
				r.Input = []byte("[mcp_servers.other]\ncommand='exe'\nargs=['different']")
			} else {
				r.Input = []byte(`{"mcpServers":{"other":{"command":"exe","args":["different"]}}}`)
			}
			if _, e = Apply(r); !errors.Is(e, ErrDuplicate) {
				t.Fatal("duplicate", e)
			}
		})
	}
}
func TestFailClosed(t *testing.T) {
	cases := map[Provider][]string{
		Claude: {`null`, `[]`, `{"mcpServers":null}`, `{"mcpServers":[]}`, `{"mcpServers":{"x":null}}`, `{"mcpServers":{"x":[]}}`, `{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"mcpServers":{"x":{"command":null}}}`, `{"mcpServers":{"x":{"args":[1]}}}`, `{"mcpServers":{"x":{"type":null}}}`, `{"a":"\ud800"}`, `{broken secret`},
		Codex:  {"a=1\na=2", "mcp_servers=1", "[mcp_servers]\nx=1", "[[mcp_servers.x]]\ncommand='x'", "[mcp_servers.x]\nargs=[1]", "[mcp_servers.x]\ncommand=1", "[mcp_servers.x]\ntype=1", "secret = ["},
	}
	for p, inputs := range cases {
		for _, s := range inputs {
			_, e := Apply(Request{Provider: p, Input: []byte(s), Command: "exe"})
			if !errors.Is(e, ErrInvalid) {
				t.Fatalf("%s %q: %v", p, s, e)
			}
		}
	}
	if _, e := Apply(Request{Provider: Codex, Input: bytes.Repeat([]byte(" "), MaxBytes+1), Command: "exe"}); e != ErrLimit {
		t.Fatal(e)
	}
	if _, e := Apply(Request{Provider: "ambient", Command: "exe"}); e != ErrInvalid {
		t.Fatal(e)
	}
	if _, e := Apply(Request{Provider: Claude, Command: "exe\x00"}); e != ErrInvalid {
		t.Fatal(e)
	}
	// Escaping expands an otherwise bounded request beyond the output limit.
	if _, e := Apply(Request{Provider: Claude, Command: "exe", Args: []string{strings.Repeat("\x01", MaxBytes/2)}}); e != ErrLimit {
		t.Fatal(e)
	}
}
func TestExactNoopAndPresence(t *testing.T) {
	raw := []byte("# retain this comment\n[mcp_servers.agent_notifications]\ncommand='exe'\nargs=[]\nenabled=false\n")
	prior := &Ownership{Codex, Server, Transport{Command: "exe", ArgsPresent: true}}
	got, e := Apply(Request{Provider: Codex, Input: raw, Command: "exe", Previous: prior})
	if e != nil || got.Changed || !bytes.Equal(raw, got.Bytes) {
		t.Fatal(e)
	}
	prior.Transport.ArgsPresent = false
	if _, e = Apply(Request{Provider: Codex, Input: raw, Command: "exe", Previous: prior}); e != ErrConflict {
		t.Fatal(e)
	}
}
func TestConcurrentPure(t *testing.T) {
	r := Request{Provider: Codex, Command: "exe", Args: []string{"mcp"}}
	first, e := Apply(r)
	if e != nil {
		t.Fatal(e)
	}
	r.Input = first.Bytes
	r.Previous = first.Owned
	r.Command = "updated"
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := Apply(r); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
}

func TestTransportTampering(t *testing.T) {
	for _, p := range []Provider{Codex, Claude} {
		first, e := Apply(Request{Provider: p, Command: "exe", Args: []string{"mcp"}})
		if e != nil {
			t.Fatal(e)
		}
		for _, field := range []string{"command", "args", "type"} {
			prior := *first.Owned
			prior.Transport.Args = append([]string(nil), prior.Transport.Args...)
			switch field {
			case "command":
				prior.Transport.Command = "changed"
			case "args":
				prior.Transport.Args[0] = "changed"
			case "type":
				prior.Transport.TypePresent = !prior.Transport.TypePresent
				prior.Transport.Type = ""
			}
			for _, remove := range []bool{false, true} {
				_, e := Apply(Request{Provider: p, Input: first.Bytes, Command: "exe", Args: []string{"mcp"}, Previous: &prior, Remove: remove})
				if e != ErrConflict {
					t.Fatalf("%s %s remove=%v: %v", p, field, remove, e)
				}
			}
		}
	}
}
func FuzzApply(f *testing.F) {
	for _, s := range []string{"", `{}`, `null`, `{"mcpServers":{}}`, "[mcp_servers]\n", "x=1979-05-27"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, p := range []Provider{Codex, Claude} {
			r, e := Apply(Request{Provider: p, Input: b, Command: "fixture", Args: []string{"mcp"}})
			if e == nil {
				if len(r.Bytes) > MaxBytes {
					t.Fatal("unbounded")
				}
				again, e := Apply(Request{Provider: p, Input: r.Bytes, Command: "fixture", Args: []string{"mcp"}, Previous: r.Owned})
				if e != nil || again.Changed || !bytes.Equal(r.Bytes, again.Bytes) {
					t.Fatal("not idempotent", e)
				}
			}
		}
	})
}

func TestTOMLSpecialFloats(t *testing.T) {
	r, e := Apply(Request{Provider: Codex, Input: []byte("nan_value=nan\npositive=+inf\nnegative=-inf\n"), Command: "exe"})
	if e != nil {
		t.Fatal(e)
	}
	m, e := decode(Codex, r.Bytes)
	if e != nil || len(m) != 4 {
		t.Fatal("special floats lost", e)
	}
}
