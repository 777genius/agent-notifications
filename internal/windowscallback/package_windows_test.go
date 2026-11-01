//go:build windows

package windowscallback

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
	"unsafe"
)

// Independent documented x64 PACKAGE_ID layout: reserved/arch at0/4, version
// at8, four pointers at16/24/32/40. No DLL/package queries execute in this test.
func fullIDFixture() []byte {
	b := make([]byte, 512)
	base := uintptr(unsafe.Pointer(&b[0]))
	at := 48
	binary.LittleEndian.PutUint32(b[4:], 9)
	binary.LittleEndian.PutUint64(b[8:], 26<<48|930<<32|7945<<16)
	for _, field := range []struct {
		offset int
		text   string
	}{{16, "OpenAI.Codex"}, {24, VendorPublisher}, {40, "2p2nqsd0c76g0"}} {
		binary.LittleEndian.PutUint64(b[field.offset:], uint64(base+uintptr(at)))
		for _, u := range append(utf16.Encode([]rune(field.text)), 0) {
			binary.LittleEndian.PutUint16(b[at:], u)
			at += 2
		}
	}
	return b
}

// Breakage: nullable resourceId refuses a legitimate main package, or foreign
// pointer/UTF16/resource/architecture data becomes the sole launch target.
func TestIndependentFULLPackageABI(t *testing.T) {
	const full = "OpenAI.Codex_26.930.7945.0_x64__2p2nqsd0c76g0"
	b := fullIDFixture()
	if got, e := decodePackageID(b, full); e != nil || got != full {
		t.Fatal(got, e)
	}
	for _, mutate := range []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint64(b[16:], 1) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 12) },
		func(b []byte) { binary.LittleEndian.PutUint16(b[48:], 0xd800) },
		func(b []byte) { binary.LittleEndian.PutUint64(b[32:], binary.LittleEndian.Uint64(b[16:])) },
	} {
		b = fullIDFixture()
		mutate(b)
		if _, e := decodePackageID(b, full); e == nil {
			t.Fatal("invalid FULL identity admitted")
		}
	}
	if _, e := decodePackageID(fullIDFixture(), "OpenAI.Codex_26.930.7945.1_x64__2p2nqsd0c76g0"); e == nil {
		t.Fatal("different version admitted")
	}
}
