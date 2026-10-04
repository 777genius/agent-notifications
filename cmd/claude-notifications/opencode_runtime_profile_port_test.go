package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func testRuntimeUTF16(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[2*i:], v)
	}
	return b
}
func testRuntimeCommand(s string) []byte {
	b := testRuntimeUTF16(s)
	raw := make([]byte, 16+len(b)+2)
	binary.LittleEndian.PutUint16(raw, uint16(len(b)))
	binary.LittleEndian.PutUint16(raw[2:], uint16(len(b)+2))
	binary.LittleEndian.PutUint64(raw[8:], 0x1000+16)
	copy(raw[16:], b)
	return raw
}
func TestRuntimeWindowsCommandLineContainedCompleteUnicode(t *testing.T) {
	command := `"C:\TEST\😀\fixture.exe" serve`
	raw := testRuntimeCommand(command)
	if got, ok := runtimeWindowsCommandLine(raw, 0x1000); !ok || got != command {
		t.Fatal("valid complete Unicode rejected")
	}
	counted := append([]byte(nil), raw[:len(raw)-2]...)
	binary.LittleEndian.PutUint16(counted[2:], binary.LittleEndian.Uint16(counted))
	if got, ok := runtimeWindowsCommandLine(counted, 0x1000); !ok || got != command {
		t.Fatal("complete counted UNICODE_STRING rejected")
	}
	// Red failure: accepting any of these lets a truncated/foreign native pointer
	// or replacement UTF16 bytes become saved-argv consistency evidence.
	cases := map[string]func([]byte) []byte{
		"shortHeader":      func(b []byte) []byte { return b[:15] },
		"oversize":         func(b []byte) []byte { return append(b, make([]byte, 4097)...) },
		"oddLength":        func(b []byte) []byte { b[0] |= 1; return b },
		"oddMaximum":       func(b []byte) []byte { b[2] |= 1; return b },
		"smallMaximum":     func(b []byte) []byte { binary.LittleEndian.PutUint16(b[2:], 2); return b },
		"outsideMaximum":   func(b []byte) []byte { binary.LittleEndian.PutUint16(b[2:], 4096); return b },
		"beforeBuffer":     func(b []byte) []byte { binary.LittleEndian.PutUint64(b[8:], 0xffe); return b },
		"insideHeader":     func(b []byte) []byte { binary.LittleEndian.PutUint64(b[8:], 0x1002); return b },
		"pastBuffer":       func(b []byte) []byte { binary.LittleEndian.PutUint64(b[8:], 0x2000); return b },
		"overflowPointer":  func(b []byte) []byte { binary.LittleEndian.PutUint64(b[8:], ^uint64(0)-1); return b },
		"unaligned":        func(b []byte) []byte { binary.LittleEndian.PutUint64(b[8:], 0x1011); return b },
		"truncatedPayload": func(b []byte) []byte { return b[:len(b)-3] },
		"embeddedNull":     func(b []byte) []byte { b[16] = 0; b[17] = 0; return b },
		"surrogate":        func(b []byte) []byte { binary.LittleEndian.PutUint16(b[16:], 0xdc00); return b },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := mutate(append([]byte(nil), raw...))
			if _, ok := runtimeWindowsCommandLine(b, 0x1000); ok {
				t.Fatal("malformed native command accepted")
			}
		})
	}
	if _, ok := runtimeWindowsCommandLine(raw, ^uint64(0)-8); ok {
		t.Fatal("base overflow accepted")
	}
}
func TestRuntimeDarwinOnlyCompleteServePrefix(t *testing.T) {
	raw := append([]byte{2, 0, 0, 0}, []byte("/TEST/fixture\x00\x00/TEST/fixture\x00serve\x00ENV=private\x00")...)
	if !runtimeDarwinServePrefix(raw) {
		t.Fatal("complete prefix rejected")
	}
	cases := [][]byte{
		nil, raw[:3], raw[:len(raw)-len("ENV=private\x00")-1],
		append([]byte{0, 0, 0, 0}, raw[4:]...), append([]byte{1, 0, 0, 0}, raw[4:]...),
		append([]byte{255, 255, 255, 255}, raw[4:]...),
		[]byte("\x02\x00\x00\x00/TEST/fixture\x00fixture\x00tui\x00"),
		[]byte("\x02\x00\x00\x00\xff\x00fixture\x00serve\x00"),
		append(append([]byte(nil), raw...), make([]byte, 64<<10-len(raw))...),
	}
	for i, b := range cases {
		if runtimeDarwinServePrefix(b) {
			t.Fatalf("malformed/truncated prefix accepted: %d", i)
		}
	}
}

// Default TUI has argc=1. Environment text must not change its entry, and
// local run requires every declared argument to be complete before parsing.
func TestRuntimeDarwinStockLocalEntriesUseOnlyCompleteArgv(t *testing.T) {
	pack := func(args []string, environment string) []byte {
		raw := make([]byte, 4)
		binary.LittleEndian.PutUint32(raw, uint32(len(args)))
		raw = append(raw, []byte("/TEST/opencode\x00\x00")...)
		for _, arg := range args {
			raw = append(raw, []byte(arg+"\x00")...)
		}
		return append(raw, []byte(environment)...)
	}
	tui := pack([]string{"/TEST/opencode"}, "--attach=http://TEST.invalid\x00serve\x00")
	if got := runtimeDarwinEntry(tui); got != "tui" {
		t.Fatalf("stock argc=1 default rejected or environment interpreted: %q", got)
	}
	run := pack([]string{"/TEST/opencode", "run", "hello TEST"}, "")
	if got := runtimeDarwinEntry(run); got != "run" {
		t.Fatalf("stock local run rejected: %q", got)
	}
	for _, raw := range [][]byte{run[:len(run)-1], pack([]string{"/TEST/opencode", "run", "--attach=http://TEST.invalid"}, ""), append(tui, make([]byte, 64<<10-len(tui))...)} {
		if runtimeDarwinEntry(raw) != "" {
			t.Fatal("incomplete, remote or capacity-sized argv became local authority")
		}
	}
}

// Red failure: a capacity-sized KERN_PROCARGS2 result can be environment tail,
// even when the saved argc and tail strings look like an executable/serve prefix.
func TestRuntimeDarwinFullCapacityTailCannotProveServe(t *testing.T) {
	raw := make([]byte, 64<<10)
	binary.LittleEndian.PutUint32(raw, 2)
	copy(raw[4:], "/TEST/environment-tail\x00fixture\x00serve\x00")
	if !runtimeDarwinServePrefix(raw[:len(raw)-1]) {
		t.Fatal("fixture lacks a complete apparent serve prefix below capacity")
	}
	if runtimeDarwinServePrefix(raw) {
		t.Fatal("full-capacity environment tail became serve evidence")
	}
}

// Red on the original reader: a valid short serve argv was denied solely
// because the saved environment exceeded its 4KiB total retrieval buffer.
func TestRuntimeDarwinLargeEnvironmentKeepsBoundedArgvAuthority(t *testing.T) {
	prefix := append([]byte{2, 0, 0, 0}, []byte("/TEST/opencode\x00\x00/TEST/opencode\x00serve\x00")...)
	for _, size := range []int{8192, 12288} {
		environment := []byte("PAD=" + strings.Repeat("x", size) + "\x00not-serve\x00--attach=TEST\x00\xff")
		raw := append(append([]byte(nil), prefix...), environment...)
		if got := runtimeDarwinEntry(raw); got != "serve" {
			t.Fatalf("valid serve with %d environment bytes rejected: %q", size, got)
		}
	}
	unsupported := append([]byte{2, 0, 0, 0}, []byte("/TEST/opencode\x00\x00/TEST/opencode\x00attach\x00")...)
	unsupported = append(unsupported, []byte("PAD="+strings.Repeat("x", 12288)+"\x00serve\x00")...)
	longArgv := append([]byte{2, 0, 0, 0}, []byte("/TEST/opencode\x00\x00"+strings.Repeat("x", 4096)+"\x00serve\x00")...)
	badUTF8 := append([]byte(nil), prefix...)
	badUTF8[len(badUTF8)-2] = 0xff
	for _, raw := range [][]byte{prefix[:len(prefix)-1], badUTF8, unsupported, longArgv} {
		if runtimeDarwinEntry(raw) != "" {
			t.Fatal("truncated, malformed, unsupported or oversized argv supplied entry authority")
		}
	}
}

// Red failure: cancelling acquisition then using a fresh context must deny on
// the FIRST recheck, including cancellation during an otherwise equal file read.
func TestRuntimeLeaseOriginAndSuppliedCancellationStayRevoked(t *testing.T) {
	for _, phase := range []string{"originBefore", "suppliedBefore", "originDuring", "suppliedDuring"} {
		t.Run(phase, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "held-image")
			if e := os.WriteFile(name, []byte("TEST immutable image"), 0600); e != nil {
				t.Fatal(e)
			}
			f, e := os.Open(name)
			if e != nil {
				t.Fatal(e)
			}
			origin, cancelOrigin := context.WithCancel(context.Background())
			defer cancelOrigin()
			supplied, cancelSupplied := context.WithCancel(context.Background())
			defer cancelSupplied()
			hash, e := runtimeFileHash(origin, f)
			if e != nil {
				t.Fatal(e)
			}
			image := runtimeLiveImage{SHA256: hash}
			reads, releases := 0, 0
			lease := &runtimeImageLease{origin: origin, image: image, release: func() { releases++; _ = f.Close() }}
			defer lease.Close()
			lease.read = func(ctx context.Context) (runtimeLiveImage, error) {
				reads++
				hash, err := runtimeFileHash(ctx, f)
				if reads == 2 && phase == "originDuring" {
					cancelOrigin()
				}
				if reads == 2 && phase == "suppliedDuring" {
					cancelSupplied()
				}
				return runtimeLiveImage{SHA256: hash}, err
			}
			if got, err := lease.Revalidate(supplied); err != nil || got != image {
				t.Fatal("live held file did not revalidate", err)
			}
			if phase == "originBefore" {
				cancelOrigin()
				supplied = context.Background()
			}
			if phase == "suppliedBefore" {
				cancelSupplied()
			}
			if _, err := lease.Revalidate(supplied); err == nil {
				t.Fatal("cancelled invocation revalidated with unchanged file")
			}
			settledReads := reads
			if _, err := lease.Revalidate(context.Background()); err == nil || reads != settledReads {
				t.Fatal("fresh context renewed revoked lease")
			}
			if strings.HasSuffix(phase, "Before") && reads != 1 {
				t.Fatal("cancelled context entered file read")
			}
			if releases != 0 {
				t.Fatal("revocation released file before caller settlement")
			}
			lease.Close()
			lease.Close()
			if releases != 1 {
				t.Fatal("settled lease did not close exactly once")
			}
		})
	}
}
func TestRuntimeNativeArchitectureClosed(t *testing.T) {
	if !runtimeWindowsMachine(0, 0x8664, 0x8664, "amd64") || !runtimeDarwinMachine(0x01000007, 3, "amd64") || !runtimeDarwinMachine(0x0100000c, 0, "arm64") {
		t.Fatal("supported architecture rejected")
	}
	for _, v := range [][3]uint16{{0x14c, 0x8664, 0x14c}, {0, 0xaa64, 0xaa64}, {0, 0x8664, 0x14c}, {0, 0, 0}, {0x8664, 0x8664, 0x8664}} {
		if runtimeWindowsMachine(v[0], v[1], v[2], "amd64") {
			t.Fatal("mixed/unknown Windows architecture accepted")
		}
	}
	for _, v := range [][2]uint32{{0x01000007, 8}, {0x0100000c, 2}, {7, 3}, {0, 0}} {
		if runtimeDarwinMachine(v[0], v[1], "amd64") || runtimeDarwinMachine(v[0], v[1], "arm64") {
			t.Fatal("ambiguous Darwin architecture accepted")
		}
	}
	if runtimeWindowsMachine(0, 0x8664, 0x8664, "arm64") || runtimeDarwinMachine(0x01000007, 3, "arm64") {
		t.Fatal("helper/parent architecture mismatch accepted")
	}
}
func TestRuntimeUTF16RejectsLossyDecoding(t *testing.T) {
	for _, raw := range [][]byte{{0}, {0, 0}, {0, 0xd8}, {0, 0xdc}, {0, 0xd8, 65, 0}} {
		if _, ok := runtimeStrictUTF16(raw); ok {
			t.Fatal("lossy decoding accepted")
		}
	}
	s := "C:\\TEST\\é😀"
	if got, ok := runtimeStrictUTF16(testRuntimeUTF16(s)); !ok || got != s {
		t.Fatal("valid Unicode changed")
	}
}

func TestRuntimeRegionProgressCannotWrapOrStall(t *testing.T) {
	if next, ok := runtimeRegionAdvance(8, 16, 4); !ok || next != 20 {
		t.Fatal("valid region progression rejected")
	}
	for _, v := range [][3]uint64{{8, 7, 4}, {8, 8, 0}, {0, ^uint64(0), 1}, {0, ^uint64(0) - 1, 3}} {
		if _, ok := runtimeRegionAdvance(v[0], v[1], v[2]); ok {
			t.Fatal("wrapped/stalled native region accepted")
		}
	}
}

func TestRuntimeImageHeadersReadFixedBoundedData(t *testing.T) {
	pe := make([]byte, 64+24+240)
	pe[0] = 'M'
	pe[1] = 'Z'
	binary.LittleEndian.PutUint32(pe[60:], 64)
	copy(pe[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(pe[68:], 0x8664)
	binary.LittleEndian.PutUint16(pe[70:], 1)
	binary.LittleEndian.PutUint16(pe[84:], 240)
	binary.LittleEndian.PutUint16(pe[86:], 2)
	binary.LittleEndian.PutUint16(pe[88:], 0x20b)
	if machine, ok := runtimePEHeader(bytes.NewReader(pe), int64(len(pe))); !ok || machine != 0x8664 {
		t.Fatal("valid native PE header rejected")
	}
	for _, offset := range []uint32{0, 63, 1<<20 + 1, ^uint32(0)} {
		bad := append([]byte(nil), pe...)
		binary.LittleEndian.PutUint32(bad[60:], offset)
		if _, ok := runtimePEHeader(bytes.NewReader(bad), int64(len(bad))); ok {
			t.Fatal("unbounded/outside PE header offset accepted")
		}
	}
	bad := append([]byte(nil), pe...)
	binary.LittleEndian.PutUint16(bad[88:], 0x10b)
	if _, ok := runtimePEHeader(bytes.NewReader(bad), int64(len(bad))); ok {
		t.Fatal("PE32 architecture ambiguity accepted")
	}
	if _, ok := runtimePEHeader(bytes.NewReader(pe[:80]), int64(len(pe))); ok {
		t.Fatal("truncated PE header accepted")
	}
	mach := make([]byte, 40)
	binary.LittleEndian.PutUint32(mach, 0xfeedfacf)
	binary.LittleEndian.PutUint32(mach[4:], 0x0100000c)
	binary.LittleEndian.PutUint32(mach[12:], 2)
	binary.LittleEndian.PutUint32(mach[16:], 1)
	binary.LittleEndian.PutUint32(mach[20:], 8)
	if cpu, sub, ok := runtimeMachHeader(bytes.NewReader(mach), int64(len(mach))); !ok || cpu != 0x0100000c || sub != 0 {
		t.Fatal("valid Mach64 header rejected")
	}
	for _, count := range []uint32{0, 2, ^uint32(0)} {
		bad := append([]byte(nil), mach...)
		binary.LittleEndian.PutUint32(bad[16:], count)
		if _, _, ok := runtimeMachHeader(bytes.NewReader(bad), int64(len(bad))); ok {
			t.Fatal("outside Mach command bounds accepted")
		}
	}
	if _, _, ok := runtimeMachHeader(bytes.NewReader(mach[:16]), int64(len(mach))); ok {
		t.Fatal("truncated Mach header accepted")
	}
}
