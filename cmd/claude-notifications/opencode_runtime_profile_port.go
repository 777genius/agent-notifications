package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var errRuntimePort = errors.New("live_image_unverified")

// Private revocation port for the subsequent composition owner. Close is called
// only after synchronous probe/effect settlement. It never supplies authority.
// Leases are bound to acquisition context/deadline, used serially, and must not
// outlive Close. A supplied context can shorten that lifetime, never renew it.
type runtimeImageLease struct {
	origin  context.Context
	image   runtimeLiveImage
	read    func(context.Context) (runtimeLiveImage, error)
	release func()
	closed  bool
	revoked bool
}

func (h *runtimeImageLease) Revalidate(ctx context.Context) (runtimeLiveImage, error) {
	if h == nil || h.closed || h.revoked {
		return runtimeLiveImage{}, errRuntimePort
	}
	if h.origin == nil || ctx == nil || h.origin.Err() != nil || ctx.Err() != nil {
		h.revoked = true
		return runtimeLiveImage{}, errRuntimePort
	}
	next, err := h.read(ctx)
	if h.origin.Err() != nil || ctx.Err() != nil || err != nil || next != h.image {
		h.revoked = true
		return runtimeLiveImage{}, errRuntimePort
	}
	return next, nil
}
func (h *runtimeImageLease) Close() {
	if h != nil && !h.closed {
		h.closed = true
		if h.release != nil {
			h.release()
		}
	}
}
func runtimeFileHash(ctx context.Context, f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", errRuntimePort
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	for {
		if ctx.Err() != nil {
			return "", errRuntimePort
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if err != nil {
			return "", errRuntimePort
		}
	}
}
func runtimeSameMetadata(a, b os.FileInfo) bool {
	return a != nil && b != nil && a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime() == b.ModTime()
}

// kern.procargs2 is saved argv consistency only. Stop at the complete second
// argument; never interpret the remaining environment as image authority. A
// full-capacity result may be an environment tail rather than the argv prefix.
func runtimeDarwinServePrefix(raw []byte) bool {
	if len(raw) < 4 || len(raw) >= 4096 {
		return false
	}
	argc := binary.LittleEndian.Uint32(raw[:4])
	if argc < 2 || argc > 4096 {
		return false
	}
	rest := raw[4:]
	i := strings.IndexByte(string(rest), 0)
	if i <= 0 || !utf8.Valid(rest[:i]) {
		return false
	}
	rest = rest[i+1:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for arg := 0; arg < 2; arg++ {
		i = strings.IndexByte(string(rest), 0)
		if i <= 0 || !utf8.Valid(rest[:i]) {
			return false
		}
		if arg == 1 && string(rest[:i]) != "serve" {
			return false
		}
		rest = rest[i+1:]
	}
	return true
}
func runtimeStrictUTF16(raw []byte) (string, bool) {
	if len(raw) == 0 || len(raw)%2 != 0 {
		return "", false
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(raw[2*i:])
		if u[i] == 0 {
			return "", false
		}
	}
	for i := 0; i < len(u); i++ {
		v := u[i]
		if v >= 0xd800 && v <= 0xdbff {
			i++
			if i >= len(u) || u[i] < 0xdc00 || u[i] > 0xdfff {
				return "", false
			}
		} else if v >= 0xdc00 && v <= 0xdfff {
			return "", false
		}
	}
	return string(utf16.Decode(u)), true
}

// Class60 on the only supported Windows ABI (native amd64). The pointer is
// checked as an integer offset before any payload access, never dereferenced.
func runtimeWindowsCommandLine(raw []byte, base uint64) (string, bool) {
	if len(raw) < 16 || len(raw) > 4096 {
		return "", false
	}
	n := uint64(binary.LittleEndian.Uint16(raw))
	m := uint64(binary.LittleEndian.Uint16(raw[2:]))
	p := binary.LittleEndian.Uint64(raw[8:])
	if n == 0 || n%2 != 0 || m%2 != 0 || m < n || p < base || p%2 != 0 {
		return "", false
	}
	off := p - base
	if off < 16 || off > uint64(len(raw)) || m > uint64(len(raw))-off {
		return "", false
	}
	return runtimeStrictUTF16(raw[off : off+n])
}
func runtimeWindowsMachine(process, native, pe uint16, arch string) bool {
	return arch == "amd64" && process == 0 && native == 0x8664 && pe == native
}
func runtimeDarwinMachine(cpu, subtype uint32, arch string) bool {
	return (arch == "amd64" && cpu == 0x01000007 && subtype == 3) || (arch == "arm64" && cpu == 0x0100000c && subtype == 0)
}

func runtimeRegionAdvance(address, begin, size uint64) (uint64, bool) {
	if begin < address || size == 0 || begin > ^uint64(0)-size {
		return 0, false
	}
	return begin + size, true
}

// Read only fixed image headers. Full debug/pe or debug/macho parsers can allocate
// from untrusted symbol/load-command sizes before the candidate hash is bound.
func runtimePEHeader(f io.ReaderAt, size int64) (uint16, bool) {
	var dos [64]byte
	if size < 90 {
		return 0, false
	}
	if _, e := f.ReadAt(dos[:], 0); e != nil || dos[0] != 'M' || dos[1] != 'Z' {
		return 0, false
	}
	offset := int64(binary.LittleEndian.Uint32(dos[60:]))
	if offset < 64 || offset > 1<<20 || offset > size-26 {
		return 0, false
	}
	var coff [26]byte
	if _, e := f.ReadAt(coff[:], offset); e != nil || string(coff[:4]) != "PE\x00\x00" {
		return 0, false
	}
	optional := binary.LittleEndian.Uint16(coff[20:])
	flags := binary.LittleEndian.Uint16(coff[22:])
	if optional != 240 || int64(optional) > size-offset-24 || binary.LittleEndian.Uint16(coff[24:]) != 0x20b || binary.LittleEndian.Uint16(coff[6:]) == 0 || flags&2 == 0 || flags&0x2000 != 0 {
		return 0, false
	}
	return binary.LittleEndian.Uint16(coff[4:]), true
}
func runtimeMachHeader(f io.ReaderAt, size int64) (uint32, uint32, bool) {
	var header [32]byte
	if size < 32 {
		return 0, 0, false
	}
	if _, e := f.ReadAt(header[:], 0); e != nil || binary.LittleEndian.Uint32(header[:]) != 0xfeedfacf || binary.LittleEndian.Uint32(header[12:]) != 2 {
		return 0, 0, false
	}
	commands := binary.LittleEndian.Uint32(header[16:])
	bytes := binary.LittleEndian.Uint32(header[20:])
	if commands == 0 || bytes < 8 || commands > bytes/8 || int64(bytes) > size-32 {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint32(header[4:]), binary.LittleEndian.Uint32(header[8:]), true
}
