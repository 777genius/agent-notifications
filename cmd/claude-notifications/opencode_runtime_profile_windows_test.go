//go:build windows

package main

import (
	"context"
	"os"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

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
	if _, e := runtimeWindowsBirth(windows.InvalidHandle); e == nil {
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
