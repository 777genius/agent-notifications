package windowscallback

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name + ".wne")
	if e != nil {
		t.Fatal(e)
	}
	return b
}

// Breakage: decoding/re-encoding corrupts opaque UTF-8 or treats percent/slash
// as URI syntax; version/kind/trailing data slips into a retained callback.
func TestIndependentGoldenRecord(t *testing.T) {
	b := golden(t, "record")
	r, e := DecodeRecord(b)
	if e != nil {
		t.Fatal(e)
	}
	if r.Generation != strings.Repeat("a", 32) || r.Reference != strings.Repeat("b", 32) || r.Provider != "codex" || r.ThreadID != "a/b%2F?#雪" || r.SnapshotSHA256 != strings.Repeat("c", 64) {
		t.Fatalf("decoded fixture mismatch: %+v", r)
	}
	encoded, e := EncodeRecord(r)
	if e != nil || !bytes.Equal(encoded, b) {
		t.Fatal("record bytes changed", e)
	}
	u, e := ThreadURI(r.ThreadID)
	if e != nil || u != "codex://threads/a%2Fb%252F%3F%23%E9%9B%AA" {
		t.Fatalf("opaque ID changed: %q %v", u, e)
	}
	for _, mutate := range []func([]byte) []byte{
		func(b []byte) []byte { b[8] = 2; return b }, func(b []byte) []byte { b[10] = 1; return b }, func(b []byte) []byte { b[11] = 1; return b },
		func(b []byte) []byte { return append(b, 0) }, func(b []byte) []byte { return b[:len(b)-1] }, func(b []byte) []byte { b[12] = 255; return b },
	} {
		if _, e := DecodeRecord(mutate(append([]byte(nil), b...))); e == nil {
			t.Fatal("malformed envelope accepted")
		}
	}
}
func TestIndependentGoldenSnapshot(t *testing.T) {
	b := golden(t, "snapshot")
	s, e := DecodeSnapshot(b)
	if e != nil {
		t.Fatal(e)
	}
	if s.CanonicalRoot != `\\?\C:\Retained\aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` || s.OwnerSID != "S-1-5-21-1" || s.FullName != "OpenAI.Codex_26.930.7945.0_x64__2p2nqsd0c76g0" {
		t.Fatalf("snapshot mismatch: %+v", s)
	}
	encoded, e := EncodeSnapshot(s)
	if e != nil || !bytes.Equal(encoded, b) {
		t.Fatal("snapshot bytes changed", e)
	}
	r, e := DecodeRecord(golden(t, "record"))
	if e != nil {
		t.Fatal(e)
	}
	if e = r.BoundTo(s, strings.Repeat("c", 64)); e != nil {
		t.Fatal(e)
	}
	s.Generation = strings.Repeat("b", 32)
	if r.BoundTo(s, strings.Repeat("c", 64)) == nil {
		t.Fatal("foreign generation accepted")
	}
	s.Generation = r.Generation
	if r.BoundTo(s, strings.Repeat("d", 64)) == nil {
		t.Fatal("foreign snapshot digest accepted")
	}
	s.Publisher = "owner supplied"
	if s.Validate() == nil {
		t.Fatal("foreign publisher accepted")
	}
}

// Breakage: malformed UTF-8, controls, ADS/relative roots or unbounded input
// authorize a record before the native physical custody checks can run.
func TestStrictFieldValidation(t *testing.T) {
	r, _ := DecodeRecord(golden(t, "record"))
	for _, id := range []string{"", ".", "..", "\x00", "\x7f", "\u0080", "\xc0\xaf", "\xed\xa0\x80", "\xf4\x90\x80\x80", strings.Repeat("x", 4097)} {
		r.ThreadID = id
		if _, e := EncodeRecord(r); e == nil {
			t.Fatal("bad thread admitted")
		}
	}
	s, _ := DecodeSnapshot(golden(t, "snapshot"))
	for _, root := range []string{`C:\relative`, `\\?\UNC\host\share`, `\\?\C:\a\..\b`, `\\?\C:\a:stream`, `\\?\C:\a\`, `\\?\C:\a.`} {
		s.CanonicalRoot = root
		if s.Validate() == nil {
			t.Fatalf("invalid root %q", root)
		}
	}
}

// Breakage: the unqualified checkpoint accidentally enables a native call even
// when trusted helper bytes are available. None never composes this port.
func TestPortRemainsUnavailable(t *testing.T) {
	if e := (Port{}).CheckReadiness(context.Background(), Binding{"must-not-open", ""}, 123); e != ErrUnavailable {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := (Port{}).CheckReadiness(ctx, Binding{}, 123); e != context.Canceled {
		t.Fatal(e)
	}
}

// Breakage: a complete dot-segment is normalized by the URI consumer to a
// different resource even though ordinary embedded dots remain opaque input.
func TestOpaqueDotSegmentRejected(t *testing.T) {
	for _, id := range []string{".", ".."} {
		if _, e := ThreadURI(id); e == nil {
			t.Fatalf("dot segment %q accepted", id)
		}
	}
	for _, id := range []string{"a.b", "a..b", "../x"} {
		if _, e := ThreadURI(id); e != nil {
			t.Fatalf("non-dot-segment rejected: %q", id)
		}
	}
}
