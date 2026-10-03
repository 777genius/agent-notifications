//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

var errRuntimeLiveImage = errors.New("live_image_unverified")

func verifyRuntimeLiveImage(ctx context.Context, in runtimeProfileInput) (runtimeLiveImage, error) {
	fail := func() (runtimeLiveImage, error) { return runtimeLiveImage{}, errRuntimeLiveImage }
	if ctx.Err() != nil || in.NativePID <= 0 || in.NativePID != os.Getppid() || in.Entry != "serve" || !canonicalPrivatePath(in.HostExecutable) || in.PublicExecPath != in.HostExecutable {
		return fail()
	}
	proc := "/proc/" + strconv.Itoa(in.NativePID)
	start, err := runtimeProcessStart(proc)
	if err != nil {
		return fail()
	}
	// Reject a wrong entry or argv wrapper; this is live binding, not authority
	// for arbitrary TEST bytes, TUI, desktop, or a version threshold.
	args, err := os.Open(proc + "/cmdline")
	if err != nil {
		return fail()
	}
	cmdline, err := io.ReadAll(io.LimitReader(args, 4097))
	_ = args.Close()
	argv := strings.Split(string(cmdline), "\x00")
	if err != nil || len(cmdline) > 4096 || len(argv) < 3 || argv[1] != "serve" {
		return fail()
	}
	image, err := os.Open(proc + "/exe")
	if err != nil {
		return fail()
	}
	defer func() { _ = image.Close() }()
	target, err := os.Open(in.HostExecutable)
	if err != nil {
		return fail()
	}
	defer func() { _ = target.Close() }()
	a, errA := image.Stat()
	b, errB := target.Stat()
	if errA != nil || errB != nil || !a.Mode().IsRegular() || !os.SameFile(a, b) {
		return fail()
	}
	sa, ok := a.Sys().(*syscall.Stat_t)
	if !ok {
		return fail()
	}
	hash := func(f *os.File) (string, error) {
		h := sha256.New()
		buf := make([]byte, 64<<10)
		for {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			n, e := f.Read(buf)
			if n > 0 {
				_, _ = h.Write(buf[:n])
			}
			if e == io.EOF {
				return hex.EncodeToString(h.Sum(nil)), nil
			}
			if e != nil {
				return "", e
			}
		}
	}
	ha, e := hash(image)
	hb, f := hash(target)
	// Re-open names to reject rename/replacement while hashing opened handles.
	endStart, startErr := runtimeProcessStart(proc)
	endImage, e1 := os.Stat(proc + "/exe")
	endTarget, e2 := os.Stat(in.HostExecutable)
	if e != nil || f != nil || startErr != nil || endStart != start || ha != hb || e1 != nil || e2 != nil || !os.SameFile(a, endImage) || !os.SameFile(b, endTarget) ||
		endTarget.Size() != b.Size() || !endTarget.ModTime().Equal(b.ModTime()) || in.NativePID != os.Getppid() || ctx.Err() != nil {
		return fail()
	}
	return runtimeLiveImage{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Entry: in.Entry, SHA256: ha, Device: uint64(sa.Dev), Inode: sa.Ino, ProcessStartTick: start, NativePID: in.NativePID}, nil
}

func runtimeProcessStart(proc string) (uint64, error) {
	f, err := os.Open(proc + "/stat")
	if err != nil {
		return 0, errRuntimeLiveImage
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	end := strings.LastIndexByte(string(raw), ')')
	if err != nil || len(raw) > 4096 || end < 0 {
		return 0, errRuntimeLiveImage
	}
	fields := strings.Fields(string(raw[end+1:])) // state is field3; starttime is field22.
	if len(fields) < 20 {
		return 0, errRuntimeLiveImage
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 || strconv.FormatUint(start, 10) != fields[19] {
		return 0, errRuntimeLiveImage
	}
	return start, nil
}
