// Package windowscallback owns the retained Windows callback contract. It does
// not install a generation or qualify a route for notification delivery.
package windowscallback

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	ReaderVersion   uint16 = 1
	MaxEnvelope            = 64 * 1024
	VendorName             = "OpenAI.Codex"
	VendorPublisher        = "CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B"
	VendorFamily           = "OpenAI.Codex_2p2nqsd0c76g0"
)

var ErrEnvelope = errors.New("invalid WinEnvelope1")

// Snapshot is an immutable generation binding, not proof of installed readiness.
type Snapshot struct {
	Generation, CanonicalRoot, OwnerSID, HelperSHA256, AUMID, CLSID string
	VendorName, Publisher, Family, FullName                         string
}
type Record struct {
	Generation, Reference, Provider, ThreadID, SnapshotSHA256 string
}

func hexValue(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func text(s string, max int) bool {
	if s == "" || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if c < 32 || (c >= 127 && c <= 159) {
			return false
		}
	}
	return true
}
func (s Snapshot) Validate() error {
	if !hexValue(s.Generation, 32) || !text(s.CanonicalRoot, 32768) || !rootGrammar(s.CanonicalRoot) || !text(s.OwnerSID, 256) || !strings.HasPrefix(s.OwnerSID, "S-1-") || !hexValue(s.HelperSHA256, 64) || !text(s.AUMID, 128) {
		return ErrEnvelope
	}
	if len(s.CLSID) != 38 || s.CLSID[0] != '{' || s.CLSID[37] != '}' {
		return ErrEnvelope
	}
	for i, c := range s.CLSID[1:37] {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return ErrEnvelope
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return ErrEnvelope
		}
	}
	if s.VendorName != VendorName || s.Publisher != VendorPublisher || s.Family != VendorFamily || !text(s.FullName, 256) || !strings.HasPrefix(s.FullName, VendorName+"_") || !strings.HasSuffix(s.FullName, "__2p2nqsd0c76g0") {
		return ErrEnvelope
	}
	return nil
}
func (r Record) Validate() error {
	if !hexValue(r.Generation, 32) || !hexValue(r.Reference, 32) || r.Provider != "codex" || !threadID(r.ThreadID) || !hexValue(r.SnapshotSHA256, 64) {
		return ErrEnvelope
	}
	return nil
}
func encode(kind byte, fields []string) ([]byte, error) {
	b := bytes.NewBufferString("WNCB0001")
	_ = binary.Write(b, binary.LittleEndian, ReaderVersion)
	b.WriteByte(kind)
	b.WriteByte(0)
	for _, f := range fields {
		_ = binary.Write(b, binary.LittleEndian, uint32(len(f)))
		b.WriteString(f)
	}
	if b.Len() > MaxEnvelope {
		return nil, ErrEnvelope
	}
	return b.Bytes(), nil
}
func decode(b []byte, kind byte, n int) ([]string, error) {
	if len(b) < 12 || len(b) > MaxEnvelope || string(b[:8]) != "WNCB0001" || binary.LittleEndian.Uint16(b[8:10]) != ReaderVersion || b[10] != kind || b[11] != 0 {
		return nil, ErrEnvelope
	}
	b = b[12:]
	fields := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if len(b) < 4 {
			return nil, ErrEnvelope
		}
		size := binary.LittleEndian.Uint32(b[:4])
		b = b[4:]
		if uint64(size) > uint64(len(b)) {
			return nil, ErrEnvelope
		}
		fields = append(fields, string(b[:size]))
		b = b[size:]
	}
	if len(b) != 0 {
		return nil, ErrEnvelope
	}
	return fields, nil
}
func EncodeSnapshot(s Snapshot) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return encode(1, []string{s.Generation, s.CanonicalRoot, s.OwnerSID, s.HelperSHA256, s.AUMID, s.CLSID, s.VendorName, s.Publisher, s.Family, s.FullName})
}
func DecodeSnapshot(b []byte) (Snapshot, error) {
	f, e := decode(b, 1, 10)
	if e != nil {
		return Snapshot{}, e
	}
	s := Snapshot{f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7], f[8], f[9]}
	return s, s.Validate()
}
func EncodeRecord(r Record) ([]byte, error) {
	if e := r.Validate(); e != nil {
		return nil, e
	}
	return encode(2, []string{r.Generation, r.Reference, r.Provider, r.ThreadID, r.SnapshotSHA256})
}
func DecodeRecord(b []byte) (Record, error) {
	f, e := decode(b, 2, 5)
	if e != nil {
		return Record{}, e
	}
	r := Record{f[0], f[1], f[2], f[3], f[4]}
	return r, r.Validate()
}
func (r Record) BoundTo(s Snapshot, digest string) error {
	if e := r.Validate(); e != nil {
		return e
	}
	if e := s.Validate(); e != nil {
		return e
	}
	if r.Generation != s.Generation || r.SnapshotSHA256 != digest {
		return ErrEnvelope
	}
	return nil
}

// ThreadURI escapes UTF-8 bytes, including percent, slash, query and fragment.
// Existing percent sequences are opaque input, never decoded or trusted.
func ThreadURI(id string) (string, error) {
	if !threadID(id) {
		return "", ErrEnvelope
	}
	var b strings.Builder
	b.WriteString("codex://threads/")
	for _, c := range []byte(id) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", rune(c)) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String(), nil
}
func ActivationArgument(reference string) (string, error) {
	if !hexValue(reference, 32) {
		return "", ErrEnvelope
	}
	return "WinEnvelope1:open_thread:" + reference, nil
}

func rootGrammar(root string) bool {
	if !text(root, 32768) || !strings.HasPrefix(root, `\\?\`) || len(root) < 7 {
		return false
	}
	asciiDrive := (root[4] >= 'A' && root[4] <= 'Z') || (root[4] >= 'a' && root[4] <= 'z')
	if !asciiDrive || root[5] != ':' || root[6] != '\\' || strings.Contains(root, "/") {
		return false
	}
	for _, part := range strings.Split(root[7:], `\`) {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:*?"<>|`) {
			return false
		}
	}
	return true
}

func threadID(id string) bool { return text(id, 4096) && id != "." && id != ".." }
