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
		file + `:stream`,
		file + `:zone.identifier`,
	} {
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
