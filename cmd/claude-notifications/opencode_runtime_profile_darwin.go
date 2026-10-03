//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -lproc
#include "opencode_runtime_profile_darwin.h"
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"runtime"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/unix"
)

type runtimeDarwinProcess struct {
	PID, Parent, CPU, Subtype uint32
	Seconds, Micros           uint64
	Name                      string
}
type runtimeDarwinSnapshot struct {
	Process       runtimeDarwinProcess
	SHA           string
	Device, Inode uint64
	Size          int64
	Modified      string
	Mode          uint32
}

func runtimeDarwinProcessInfo(pid int) (runtimeDarwinProcess, error) {
	var p C.struct_an_runtime_process
	buf := make([]byte, 4096)
	if C.an_runtime_process(C.int(pid), &p, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf))) != 1 {
		return runtimeDarwinProcess{}, errRuntimePort
	}
	end := 0
	for end < len(buf) && buf[end] != 0 {
		end++
	}
	if end == 0 || end == len(buf) || !utf8.Valid(buf[:end]) {
		return runtimeDarwinProcess{}, errRuntimePort
	}
	name := string(buf[:end])
	if !canonicalPrivatePath(name) {
		return runtimeDarwinProcess{}, errRuntimePort
	}
	return runtimeDarwinProcess{uint32(p.pid), uint32(p.parent), uint32(p.cpu), uint32(p.subtype), uint64(p.seconds), uint64(p.micros), name}, nil
}
func runtimeDarwinNativeEntry(pid int) string {
	buf := make([]byte, 4096)
	var n C.size_t
	if C.an_runtime_args(C.int(pid), (*C.uchar)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), &n) != 1 || uint64(n) > uint64(len(buf)) {
		return ""
	}
	return runtimeDarwinEntry(buf[:int(n)])
}
func runtimeDarwinMapped(ctx context.Context, pid int, device, inode uint64) bool {
	var address uint64
	for i := 0; i < 1024; i++ {
		if ctx.Err() != nil {
			return false
		}
		var r C.struct_an_runtime_region
		if C.an_runtime_region(C.int(pid), C.uint64_t(address), &r) != 1 {
			return false
		}
		begin, size := uint64(r.address), uint64(r.size)
		next, ok := runtimeRegionAdvance(address, begin, size)
		if !ok {
			return false
		}
		if r.executable != 0 && uint64(r.device) == device && uint64(r.inode) == inode {
			return true
		}
		address = next
	}
	return false
}
func verifyRuntimeLiveImage(ctx context.Context, in runtimeProfileInput) (runtimeLiveImage, error) {
	h, err := holdRuntimeLiveImage(ctx, in)
	if err != nil {
		return runtimeLiveImage{}, err
	}
	defer h.Close()
	return h.image, nil
}
func holdRuntimeLiveImage(ctx context.Context, in runtimeProfileInput) (*runtimeImageLease, error) {
	if ctx.Err() != nil || in.NativePID <= 0 || uint64(in.NativePID) > 0x7fffffff || in.NativePID != os.Getppid() || !runtimeEntryRequest(in.Entry) || !canonicalPrivatePath(in.HostExecutable) || in.PublicExecPath != in.HostExecutable {
		return nil, errRuntimePort
	}
	watch := runtimeDarwinWatch(in.NativePID)
	if watch < 0 {
		return nil, errRuntimePort
	}
	closeWatch := func() { _ = unix.Close(watch) }
	process, err := runtimeDarwinProcessInfo(in.NativePID)
	if err != nil {
		closeWatch()
		return nil, errRuntimePort
	}
	image, err := os.Open(process.Name)
	if err != nil {
		closeWatch()
		return nil, errRuntimePort
	}
	target, err := os.Open(in.HostExecutable)
	if err != nil {
		image.Close()
		closeWatch()
		return nil, errRuntimePort
	}
	release := func() { target.Close(); image.Close(); closeWatch() }
	// SDK vnode continuity plus retained files is proof under the trusted
	// cooperative immutable-image assumption, never arbitrary RAM immutability.
	read := func(ctx context.Context) (runtimeLiveImage, error) {
		fail := func() (runtimeLiveImage, error) { return runtimeLiveImage{}, errRuntimePort }
		if ctx.Err() != nil || os.Getppid() != in.NativePID || !runtimeDarwinUnchanged(watch) {
			return fail()
		}
		live, e := runtimeDarwinProcessInfo(in.NativePID)
		entry := runtimeDarwinNativeEntry(in.NativePID)
		if e != nil || live != process || !runtimeDarwinMachine(live.CPU, live.Subtype, runtime.GOARCH) || !runtimeEntryMatches(in.Entry, entry) {
			return fail()
		}
		a, e := image.Stat()
		b, e1 := target.Stat()
		if e != nil || e1 != nil || !runtimeSameMetadata(a, b) {
			return fail()
		}
		stat, ok := a.Sys().(*syscall.Stat_t)
		if !ok || stat.Ino == 0 {
			return fail()
		}
		device, inode := uint64(uint32(stat.Dev)), stat.Ino
		cpu, subtype, headerOK := runtimeMachHeader(image, a.Size())
		if !headerOK {
			return fail()
		}
		if !runtimeDarwinMachine(cpu, subtype, runtime.GOARCH) || cpu != live.CPU || subtype != live.Subtype || !runtimeDarwinMapped(ctx, in.NativePID, device, inode) {
			return fail()
		}
		ha, e := runtimeFileHash(ctx, image)
		hb, e1 := runtimeFileHash(ctx, target)
		end, e2 := runtimeDarwinProcessInfo(in.NativePID)
		na, e3 := os.Stat(live.Name)
		nb, e4 := os.Stat(in.HostExecutable)
		if e != nil || e1 != nil || e2 != nil || e3 != nil || e4 != nil || ha != hb || end != live || !runtimeSameMetadata(a, na) || !runtimeSameMetadata(b, nb) || os.Getppid() != in.NativePID || runtimeDarwinNativeEntry(in.NativePID) != entry || !runtimeDarwinMapped(ctx, in.NativePID, device, inode) || ctx.Err() != nil || !runtimeDarwinUnchanged(watch) {
			return fail()
		}
		encoded, _ := json.Marshal(runtimeDarwinSnapshot{live, ha, device, inode, a.Size(), a.ModTime().UTC().Format(time.RFC3339Nano), uint32(a.Mode())})
		return runtimeLiveImage{GOOS: "darwin", GOARCH: runtime.GOARCH, Entry: entry, SHA256: ha, NativePID: in.NativePID, fingerprint: sha256.Sum256(encoded)}, nil
	}
	first, err := read(ctx)
	if err != nil {
		release()
		return nil, err
	}
	return &runtimeImageLease{origin: ctx, image: first, read: read, release: release}, nil
}

func runtimeDarwinWatch(pid int) int     { return int(C.an_runtime_watch(C.int(pid))) }
func runtimeDarwinUnchanged(fd int) bool { return C.an_runtime_unchanged(C.int(fd)) == 1 }
