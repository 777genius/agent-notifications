//go:build windows

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsSecureContextPrivateOwnedFile(t *testing.T) {
	root := contextWindowsTestRoot(t)
	path := filepath.Join(root, "context.json")
	raw := `{"provider":"codex","session":"TEST/<nonce>/percent%雪","locality":"local","interface":"desktop"}`
	contextWindowsPrivateFile(t, path, []byte(raw))
	got, err := ReadSecureContext(context.Background(), path)
	if err != nil || string(got) != raw {
		t.Fatalf("private file rejected: %q %v", got, err)
	}
}

func TestWindowsSecureContextPrivateOwnerWriteAccess(t *testing.T) {
	root := contextWindowsTestRoot(t)
	path := filepath.Join(root, "context.json")
	raw := []byte(`{"provider":"codex","session":"TEST/private-write"}`)
	contextWindowsPrivateFile(t, path, raw)
	contextWindowsSetDACL(t, path, "D:P(A;;FA;;;"+contextWindowsTokenSID(t)+")(A;;FA;;;SY)")
	got, err := ReadSecureContext(context.Background(), path)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("private owner write access rejected: %q %v", got, err)
	}
}

func TestWindowsSecureContextRejectsForeignAndBroadACL(t *testing.T) {
	for _, test := range []struct {
		name string
		acl  string
	}{
		{"foreign", `D:P(A;;FR;;;OWNER)(A;;FR;;;SY)(A;;FR;;;BU)`},
		{"foreign_write", `D:P(A;;FR;;;OWNER)(A;;FR;;;SY)(A;;FW;;;BU)`},
		{"owner_rights", `D:P(A;;FR;;;OWNER)(A;;FR;;;SY)(A;;FR;;;S-1-3-4)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := contextWindowsTestRoot(t)
			path := filepath.Join(root, "context.json")
			contextWindowsPrivateFile(t, path, []byte(`{"provider":"codex"}`))
			contextWindowsSetDACL(t, path, strings.ReplaceAll(test.acl, "OWNER", contextWindowsTokenSID(t)))
			if data, err := ReadSecureContext(context.Background(), path); err == nil || data != nil {
				t.Fatalf("accepted ACL %s", test.acl)
			}
		})
	}
}

func TestWindowsSecureContextRejectsReparseAndAliases(t *testing.T) {
	root := contextWindowsTestRoot(t)
	file := filepath.Join(root, "context.json")
	contextWindowsPrivateFile(t, file, []byte(`{"provider":"codex"}`))

	finalLink := filepath.Join(root, "final-link")
	if err := os.Symlink(file, finalLink); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
	if data, err := ReadSecureContext(context.Background(), finalLink); err == nil || data != nil {
		t.Fatal("final symlink accepted")
	}

	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(parent, "context.json")
	contextWindowsPrivateFile(t, inner, []byte(`{"provider":"codex"}`))
	parentLink := filepath.Join(root, "parent-link")
	if err := os.Symlink(parent, parentLink); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
	if data, err := ReadSecureContext(context.Background(), filepath.Join(parentLink, "context.json")); err == nil || data != nil {
		t.Fatal("reparse ancestor accepted")
	}

}

func TestWindowsSecureContextRejectsPathAliases(t *testing.T) {
	root := contextWindowsTestRoot(t)
	file := filepath.Join(root, "context.json")
	contextWindowsPrivateFile(t, file, []byte(`{"provider":"codex"}`))

	for _, path := range []string{
		"",
		`context.json`,
		`C:relative\context.json`,
		`\\server\share\context.json`,
		`\\?\C:\context.json`,
		`C:\..\context.json`,
		`C:/context.json`,
		`C:\context.json.`,
		`C:\context.json `,
		`C:\NUL`,
		`C:\COM1.json`,
		`C:\COM¹.json`,
		`C:\LPT³`,
		"C:\\bad\x01name",
		file + `:stream`,
		file + `:zone.identifier`,
	} {
		if _, _, err := contextWindowsPath(path); err == nil {
			t.Fatalf("unsafe path admitted before opening: %q", path)
		}
		if data, err := ReadSecureContext(context.Background(), path); err == nil || data != nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
}

func TestWindowsSecureContextRejectsHardlinkOversizeAndCancel(t *testing.T) {
	root := contextWindowsTestRoot(t)
	path := filepath.Join(root, "context.json")
	contextWindowsPrivateFile(t, path, []byte(`{"provider":"codex"}`))
	hardlink := filepath.Join(root, "hardlink")
	// Creating the negative hardlink fixture needs owner write attributes on
	// newer Windows/Go versions. It stays private and owned throughout.
	contextWindowsSetDACL(t, path, "D:P(A;;FA;;;"+contextWindowsTokenSID(t)+")(A;;FR;;;SY)")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadSecureContext(context.Background(), path); err == nil || data != nil {
		t.Fatal("hardlink accepted")
	}
	if err := os.Remove(hardlink); err != nil {
		t.Fatal(err)
	}

	oversize := filepath.Join(root, "oversize.json")
	contextWindowsPrivateFile(t, oversize, []byte(strings.Repeat(" ", ContextLimit+1)))
	if data, err := ReadSecureContext(context.Background(), oversize); err == nil || data != nil {
		t.Fatal("oversize accepted")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if data, err := ReadSecureContext(ctx, path); err == nil || data != nil {
		t.Fatal("cancelled read accepted")
	}
}

func TestWindowsSecureContextHoldsSubstitutableParentsAndCollectsHandles(t *testing.T) {
	root := contextWindowsTestRoot(t)
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "context.json")
	contextWindowsPrivateFile(t, path, []byte(`{"provider":"codex"}`))
	custody, err := contextWindowsHold(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(parent, parent+"-moved"); err == nil {
		custody.Close()
		t.Fatal("held parent accepted substitution")
	}
	if err = os.Remove(path); err == nil {
		custody.Close()
		t.Fatal("held final accepted deletion")
	}
	custody.Close()

	custody, err = contextWindowsHold(path)
	if err != nil {
		t.Fatal(err)
	}
	handles := append([]windows.Handle(nil), custody.parents...)
	fileHandle := windows.Handle(custody.file.Fd())
	custody.Close()
	for _, handle := range append(handles, fileHandle) {
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(handle, &info) == nil {
			t.Fatal("custody handle leaked")
		}
	}
}

func TestWindowsContextEnvelopeRemainsCallerAsserted(t *testing.T) {
	raw := []byte(`{"provider":"codex","session":"TEST/<nonce>/percent%雪","locality":"local","interface":"desktop"}`)
	origin, ok := envelope(raw)
	if !ok || origin.Provenance != "caller_asserted" || origin.AnonymousCaller != "local_cli" {
		t.Fatalf("caller envelope changed: %#v %t", origin, ok)
	}
}

func contextWindowsTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// Go's temporary directory may use the runner's 8.3 user-directory
	// spelling. Positive fixtures must use the actual opened directory name;
	// the product deliberately refuses physical-path aliases.
	name, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	physical, pathErr := contextWindowsPhysicalPath(handle)
	closeErr := windows.CloseHandle(handle)
	if pathErr != nil || closeErr != nil || !strings.HasPrefix(physical, `\\?\`) {
		t.Fatalf("resolve private TEST directory: %v %v", pathErr, closeErr)
	}
	root = strings.TrimPrefix(physical, `\\?\`)
	if !filepath.IsAbs(root) || filepath.VolumeName(root) == "" {
		t.Fatalf("TEST root is not a local absolute path: %q", root)
	}
	rootName, err := windows.UTF16PtrFromString(filepath.VolumeName(root) + `\`)
	if err != nil {
		t.Fatal(err)
	}
	if windows.GetDriveType(rootName) != windows.DRIVE_FIXED {
		t.Fatalf("TEST root is not on a fixed local drive: %q", root)
	}
	return root
}

func contextWindowsPrivateFile(t *testing.T, path string, data []byte) {
	t.Helper()
	sid := contextWindowsTokenSID(t)
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	grants := "(A;;FA;;;" + sid + ")"
	if !token.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		grants += "(A;;FA;;;SY)"
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P" + grants)
	if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	security := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_WRITE|windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL,
		0,
		security,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	var written uint32
	err = windows.WriteFile(handle, data, &written, nil)
	privateGrants := "(A;;FR;;;" + sid + ")"
	if !token.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		privateGrants += "(A;;FR;;;SY)"
	}
	private, privateErr := windows.SecurityDescriptorFromString("O:" + sid + "D:P" + privateGrants)
	if privateErr != nil {
		_ = windows.CloseHandle(handle)
		t.Fatal(privateErr)
	}
	owner, _, ownerErr := private.Owner()
	dacl, _, daclErr := private.DACL()
	if daclErr != nil {
		ownerErr = daclErr
	}
	flags := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if ownerErr == nil && daclErr == nil {
		if ownerErr = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, flags, owner, nil, dacl, nil); ownerErr != nil {
			if e := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, owner, nil, nil, nil); e != nil {
				ownerErr = e
			} else {
				ownerErr = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
			}
		}
	}
	closeErr := windows.CloseHandle(handle)
	if err != nil || ownerErr != nil || closeErr != nil || int(written) != len(data) {
		t.Fatalf("write private TEST file: %v %v %v %d/%d", err, ownerErr, closeErr, written, len(data))
	}
}

func contextWindowsTokenSID(t *testing.T) string {
	t.Helper()
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return token.User.Sid.String()
}

func contextWindowsSetDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatal(err)
	}
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSecureContextNativeACELayoutAndMalformedBounds(t *testing.T) {
	if unsafe.Sizeof(windows.ACE_HEADER{}) != 4 || unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart) != 8 {
		t.Fatal("unexpected Windows ACE layout")
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FR;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err = windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	sid, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		t.Fatal(err)
	}
	if int(ace.Header.AceSize) != 8+sid.Len() || ace.Header.AceSize != 20 {
		t.Fatalf("actual SYSTEM ACE size=%d SID length=%d", ace.Header.AceSize, sid.Len())
	}
	t.Logf("native ACE_HEADER=%d SidStart=%d SYSTEM ACE=%d SID=%d", unsafe.Sizeof(windows.ACE_HEADER{}), unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart), ace.Header.AceSize, sid.Len())
	parsed, err := contextWindowsAllowedACE(ace)
	if err != nil || parsed.SID != "S-1-5-18" {
		t.Fatalf("valid native ACE refused: %#v %v", parsed, err)
	}
	for _, test := range []string{"truncated", "short_header", "wrong_type", "excess_subauthorities"} {
		t.Run(test, func(t *testing.T) {
			storage := make([]uint32, (int(ace.Header.AceSize)+3)/4)
			data := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), int(ace.Header.AceSize))
			copy(data, unsafe.Slice((*byte)(unsafe.Pointer(ace)), len(data)))
			candidate := (*windows.ACCESS_ALLOWED_ACE)(unsafe.Pointer(&storage[0]))
			switch test {
			case "truncated":
				candidate.Header.AceSize -= 4
			case "short_header":
				candidate.Header.AceSize = 12
			case "wrong_type":
				candidate.Header.AceType = windows.ACCESS_DENIED_ACE_TYPE
			case "excess_subauthorities":
				data[9] = 16
			}
			if _, err := contextWindowsAllowedACE(candidate); err == nil {
				t.Fatal("malformed ACE accepted")
			}
		})
	}
	if _, err := contextWindowsAllowedACE(nil); err == nil {
		t.Fatal("nil ACE accepted")
	}
}

func TestWindowsSecureContextPrivatePrincipalPolicy(t *testing.T) {
	user, err := windows.StringToSid("S-1-5-21-42-43-44-1001")
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		t.Fatal(err)
	}
	state := contextWindowsFileState{Links: 1}
	ownerACE := contextWindowsACE{Type: windows.ACCESS_ALLOWED_ACE_TYPE, Mask: 0x001f01ff, SID: user.String()}
	systemACE := ownerACE
	systemACE.SID = system.String()
	private := func(owner string, aces ...contextWindowsACE) contextWindowsSecurity {
		return contextWindowsSecurity{Control: windows.SE_DACL_PRESENT | windows.SE_DACL_PROTECTED, Owner: owner, ACEs: aces}
	}
	if !contextWindowsPrivateForSID(state, private(user.String(), ownerACE, systemACE), user) {
		t.Fatal("private owner/SYSTEM policy refused")
	}
	for _, test := range []struct {
		name     string
		security contextWindowsSecurity
	}{
		{"foreign_owner", private(system.String(), ownerACE, systemACE)},
		{"missing_owner", private(user.String(), systemACE)},
		{"missing_system", private(user.String(), ownerACE)},
		{"duplicate_system", private(user.String(), ownerACE, systemACE, systemACE)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if contextWindowsPrivateForSID(state, test.security, user) {
				t.Fatal("invalid principal policy accepted")
			}
		})
	}
	for _, test := range []string{"deny", "inherited", "unsupported_mask", "foreign"} {
		t.Run(test, func(t *testing.T) {
			bad := ownerACE
			switch test {
			case "deny":
				bad.Type = windows.ACCESS_DENIED_ACE_TYPE
			case "inherited":
				bad.Flags = windows.INHERITED_ACE
			case "unsupported_mask":
				bad.Mask = 0x01000000
			case "foreign":
				bad.SID = "S-1-1-0"
			}
			if contextWindowsPrivateForSID(state, private(user.String(), bad, systemACE), user) {
				t.Fatal("invalid ACE accepted")
			}
		})
	}
	for _, count := range []int{1, 2, 3} {
		aces := make([]contextWindowsACE, count)
		for i := range aces {
			aces[i] = systemACE
		}
		if got := contextWindowsPrivateForSID(state, private(system.String(), aces...), system); got != (count <= 2) {
			t.Fatalf("LocalSystem grant count=%d admitted=%t", count, got)
		}
	}
}
