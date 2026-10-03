//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

type runtimeWindowsFileID struct {
	Volume uint64
	ID     [16]byte
}
type runtimeWindowsProcess struct {
	PID                uint32
	Birth, HelperBirth uint64
}
type runtimeWindowsSnapshot struct {
	Process            runtimeWindowsProcess
	Name, Command, SHA string
	ID                 runtimeWindowsFileID
	Size               int64
	Modified           string
	Mode               uint32
}

func runtimeWindowsParent() (uint32, error) {
	var p windows.PROCESS_BASIC_INFORMATION
	var n uint32
	size := uint32(unsafe.Sizeof(p))
	if runtime.GOARCH != "amd64" || windows.NtQueryInformationProcess(windows.CurrentProcess(), windows.ProcessBasicInformation, unsafe.Pointer(&p), size, &n) != nil || n != size || p.UniqueProcessId != uintptr(os.Getpid()) || p.InheritedFromUniqueProcessId == 0 || p.InheritedFromUniqueProcessId > 0xffffffff || p.InheritedFromUniqueProcessId == p.UniqueProcessId {
		return 0, errRuntimePort
	}
	return uint32(p.InheritedFromUniqueProcessId), nil
}
func runtimeWindowsBirth(h windows.Handle) (uint64, error) {
	var birth, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &birth, &exit, &kernel, &user) != nil {
		return 0, errRuntimePort
	}
	v := uint64(birth.HighDateTime)<<32 | uint64(birth.LowDateTime)
	if v == 0 {
		return 0, errRuntimePort
	}
	return v, nil
}
func runtimeWindowsLive(h windows.Handle, pid uint32) (runtimeWindowsProcess, error) {
	parent, err := runtimeWindowsParent()
	id, e := windows.GetProcessId(h)
	wait, w := windows.WaitForSingleObject(h, 0)
	birth, b := runtimeWindowsBirth(h)
	helper, c := runtimeWindowsBirth(windows.CurrentProcess())
	if err != nil || e != nil || w != nil || b != nil || c != nil || parent != pid || id != pid || wait != uint32(windows.WAIT_TIMEOUT) || birth > helper {
		return runtimeWindowsProcess{}, errRuntimePort
	}
	return runtimeWindowsProcess{pid, birth, helper}, nil
}
func runtimeWindowsPath(s string) bool {
	return utf8.ValidString(s) && len(utf16.Encode([]rune(s))) < 2048 && canonicalPrivatePath(s) && len(s) >= 4 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) && s[1:3] == ":\\" && !strings.ContainsAny(s[3:], ":/") && !strings.ContainsAny(s, "\x00")
}
func runtimeWindowsImageName(h windows.Handle) (string, error) {
	buf := make([]uint16, 2048)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil || n == 0 || n >= uint32(len(buf)) {
		return "", errRuntimePort
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), int(n)*2)
	name, ok := runtimeStrictUTF16(raw)
	if !ok || !runtimeWindowsPath(name) {
		return "", errRuntimePort
	}
	return name, nil
}
func runtimeWindowsNativeCommand(h windows.Handle) (string, error) {
	// An aligned fixed owned allocation, with no size-probe/retry or PEB read.
	buf := make([]uint64, 512)
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), 4096)
	var n uint32
	err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(raw)), &n)
	if err != nil || n < 16 || n > uint32(len(raw)) {
		return "", errRuntimePort
	}
	command, ok := runtimeWindowsCommandLine(raw[:n], uint64(uintptr(unsafe.Pointer(&buf[0]))))
	runtime.KeepAlive(buf)
	if !ok {
		return "", errRuntimePort
	}
	argv, err := windows.DecomposeCommandLine(command)
	if err != nil || runtimeNativeEntry(argv) == "" {
		return "", errRuntimePort
	}
	return command, nil
}
func runtimeWindowsOpen(name string) (*os.File, error) {
	if !runtimeWindowsPath(name) {
		return nil, errRuntimePort
	}
	root, _ := windows.UTF16PtrFromString(name[:3])
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return nil, errRuntimePort
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, errRuntimePort
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errRuntimePort
	}
	f := os.NewFile(uintptr(h), name)
	var info windows.ByHandleFileInformation
	buf := make([]uint16, 2048)
	n, e := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	typ, t := windows.GetFileType(h)
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || typ != windows.FILE_TYPE_DISK || t != nil || e != nil || n == 0 || n >= uint32(len(buf)) {
		f.Close()
		return nil, errRuntimePort
	}
	final, ok := runtimeStrictUTF16(unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), int(n)*2))
	// Only the local DOS spelling is accepted. Final-name equality is a guard;
	// full native file identity below is what binds the two retained handles.
	if !ok || !strings.EqualFold(final, "\\\\?\\"+name) {
		f.Close()
		return nil, errRuntimePort
	}
	return f, nil
}
func runtimeWindowsID(f *os.File) (runtimeWindowsFileID, error) {
	var id runtimeWindowsFileID
	err := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id)))
	if err != nil || id.Volume == 0 || id.ID == [16]byte{} {
		return runtimeWindowsFileID{}, errRuntimePort
	}
	return id, nil
}
func verifyRuntimeLiveImage(ctx context.Context, in runtimeProfileInput) (runtimeLiveImage, error) {
	held, err := holdRuntimeLiveImage(ctx, in)
	if err != nil {
		return runtimeLiveImage{}, err
	}
	defer held.Close()
	return held.image, nil
}
func holdRuntimeLiveImage(ctx context.Context, in runtimeProfileInput) (*runtimeImageLease, error) {
	if ctx.Err() != nil || in.NativePID <= 0 || uint64(in.NativePID) > 0xffffffff || !runtimeEntryRequest(in.Entry) || in.PublicExecPath != in.HostExecutable || !runtimeWindowsPath(in.HostExecutable) {
		return nil, errRuntimePort
	}
	parent, err := runtimeWindowsParent()
	if err != nil || parent != uint32(in.NativePID) {
		return nil, errRuntimePort
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, parent)
	if err != nil {
		return nil, errRuntimePort
	}
	closeProcess := func() { _ = windows.CloseHandle(process) }
	name, err := runtimeWindowsImageName(process)
	if err != nil {
		closeProcess()
		return nil, errRuntimePort
	}
	image, err := runtimeWindowsOpen(name)
	if err != nil {
		closeProcess()
		return nil, errRuntimePort
	}
	target, err := runtimeWindowsOpen(in.HostExecutable)
	if err != nil {
		image.Close()
		closeProcess()
		return nil, errRuntimePort
	}
	release := func() { target.Close(); image.Close(); closeProcess() }
	// Trusted cooperating immutable image assumption: this binds the process
	// instance and on-disk image; it cannot prove arbitrary mapped RAM immutable.
	read := func(ctx context.Context) (runtimeLiveImage, error) {
		fail := func() (runtimeLiveImage, error) { return runtimeLiveImage{}, errRuntimePort }
		if ctx.Err() != nil {
			return fail()
		}
		live, e := runtimeWindowsLive(process, parent)
		current, e1 := runtimeWindowsImageName(process)
		command, e2 := runtimeWindowsNativeCommand(process)
		argv, argvErr := windows.DecomposeCommandLine(command)
		entry := runtimeNativeEntry(argv)
		var emulated, native uint16
		if e != nil || e1 != nil || e2 != nil || argvErr != nil || !runtimeEntryMatches(in.Entry, entry) || current != name || windows.IsWow64Process2(process, &emulated, &native) != nil {
			return fail()
		}
		headerStat, headerErr := image.Stat()
		if headerErr != nil {
			return fail()
		}
		machine, ok := runtimePEHeader(image, headerStat.Size())
		if !ok || !runtimeWindowsMachine(emulated, native, machine, runtime.GOARCH) {
			return fail()
		}
		id, e := runtimeWindowsID(image)
		tid, e1 := runtimeWindowsID(target)
		a, e2 := image.Stat()
		b, e3 := target.Stat()
		if e != nil || e1 != nil || e2 != nil || e3 != nil || id != tid || !runtimeSameMetadata(a, b) {
			return fail()
		}
		ha, e := runtimeFileHash(ctx, image)
		hb, e1 := runtimeFileHash(ctx, target)
		// Reopen both names while the original image, target and process stay held.
		named, e2 := runtimeWindowsOpen(current)
		if e2 != nil {
			return fail()
		}
		defer named.Close()
		candidate, e3 := runtimeWindowsOpen(in.HostExecutable)
		if e3 != nil {
			return fail()
		}
		defer candidate.Close()
		nid, e4 := runtimeWindowsID(named)
		cid, e5 := runtimeWindowsID(candidate)
		na, e6 := named.Stat()
		nb, e7 := candidate.Stat()
		end, e8 := runtimeWindowsLive(process, parent)
		endName, e9 := runtimeWindowsImageName(process)
		endCommand, e10 := runtimeWindowsNativeCommand(process)
		if e != nil || e1 != nil || e4 != nil || e5 != nil || e6 != nil || e7 != nil || e8 != nil || e9 != nil || e10 != nil || ha != hb || id != nid || tid != cid || !runtimeSameMetadata(a, na) || !runtimeSameMetadata(b, nb) || end != live || endName != current || endCommand != command || ctx.Err() != nil {
			return fail()
		}
		encoded, _ := json.Marshal(runtimeWindowsSnapshot{live, current, command, ha, id, a.Size(), a.ModTime().UTC().Format(time.RFC3339Nano), uint32(a.Mode())})
		return runtimeLiveImage{GOOS: "windows", GOARCH: "amd64", Entry: entry, SHA256: ha, NativePID: in.NativePID, fingerprint: sha256.Sum256(encoded)}, nil
	}
	first, err := read(ctx)
	if err != nil {
		release()
		return nil, err
	}
	return &runtimeImageLease{origin: ctx, image: first, read: read, release: release}, nil
}
