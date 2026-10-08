//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func portFixtureRoot(t *testing.T, dir string) string {
	t.Helper()
	// Resolve existing short Windows ancestors before deriving ledger/image names.
	root, e := filepath.EvalSymlinks(dir)
	if e != nil || !runtimeWindowsPath(root) {
		t.Fatal("canonical bounded TEST DOS root unavailable", e)
	}
	return root
}

func portProcessSettled(pid int) bool {
	h, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e == windows.ERROR_INVALID_PARAMETER {
		return true
	}
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	w, e := windows.WaitForSingleObject(h, 0)
	return e == nil && w == windows.WAIT_OBJECT_0
}
func portAssertProcessSettled(t *testing.T, pid int) {
	t.Helper()
	if !portProcessSettled(pid) {
		t.Fatal("receipt preceded actual probe Wait")
	}
}
func portPrepareStartFailure(t *testing.T, name string) {
	t.Helper()
	// Deny only execution of these disposable TEST bytes, preserving the read
	// proof and running parent. Restore nothing outside this private fixture.
	sd, e := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	dacl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	world, e := windows.CreateWellKnownSid(windows.WinWorldSid)
	if e != nil {
		t.Fatal(e)
	}
	denied, e := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.FILE_EXECUTE, AccessMode: windows.DENY_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP, TrusteeValue: windows.TrusteeValueFromSID(world)}}}, dacl)
	runtime.KeepAlive(world)
	runtime.KeepAlive(sd)
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, denied, nil); e != nil {
		t.Fatal(e)
	}
}
func TestRuntimeWindowsBirthAndParentAreActual(t *testing.T) {
	if _, e := runtimeWindowsLive(windows.CurrentProcess(), uint32(os.Getpid())); e == nil {
		t.Fatal("self handle masqueraded as parent")
	}
	if birth, e := runtimeWindowsBirth(windows.CurrentProcess()); e != nil || birth == 0 {
		t.Fatal("current-process pseudo handle lost actual birth", e)
	}
	if _, e := runtimeWindowsBirth(windows.Handle(0)); e == nil {
		t.Fatal("invalid process birth accepted")
	}
	if _, e := verifyRuntimeLiveImage(context.Background(), runtimeProfileInput{NativePID: os.Getpid(), Entry: "serve"}); e == nil {
		t.Fatal("invalid target accepted")
	}
}
func TestRuntimeWindowsPathsDenyAmbiguousSemantics(t *testing.T) {
	for _, s := range []string{`\\server\share\image.exe`, `\\?\C:\TEST\image.exe`, `C:\TEST\image.exe:stream`, `C:\TEST\..\image.exe`, `C:/TEST/image.exe`, "C:\\TEST\\\xff.exe"} {
		if runtimeWindowsPath(s) {
			t.Fatal("ambiguous path accepted")
		}
	}
	if !runtimeWindowsPath(`C:\TEST\é😀\image.exe`) {
		t.Fatal("canonical Unicode local path rejected")
	}
}

// Red: a one-attempt reader returns the real sharing error after handle release.
func TestRuntimePortDoneReadWaitsForSharingRelease(t *testing.T) {
	name := filepath.Join(t.TempDir(), "TEST-published.done")
	if e := os.WriteFile(name, []byte("ok"), 0600); e != nil {
		t.Fatal(e)
	}
	path, e := windows.UTF16PtrFromString(name)
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(path, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatal(e)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			_ = windows.CloseHandle(h)
		}
	})
	status, e := portReadDone(name, func(readErr error) {
		if released {
			return
		}
		if !errors.Is(readErr, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("no-share TEST handle did not deny the actual read: %v", readErr)
		}
		if e := windows.CloseHandle(h); e != nil {
			t.Fatal(e)
		}
		released = true
	})
	if e != nil || string(status) != "ok" || !released {
		t.Fatalf("closed TEST publication was not read after sharing release: status=%q err=%v released=%t", status, e, released)
	}
}
