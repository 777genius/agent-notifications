//go:build windows

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsAutomaticCustomRootValidation(t *testing.T) {
	for _, root := range []string{"", `relative\ProgramData`, `\\?\C:\private`} {
		if _, err := windowsAutomaticAnchorForRoot(root, true); err == nil {
			t.Fatalf("accepted %q", root)
		}
	}
	root := t.TempDir()
	if err := prepareTestRoot(root); err != nil {
		t.Fatal(err)
	}
	anchor, err := windowsAutomaticAnchorForRoot(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.native {
		t.Fatal("custom root granted native additive exemption")
	}
	if err = anchor.checkSelection(filepath.Join(anchor.dir, "config.json"), false); err != nil {
		t.Fatal(err)
	}
}

// Each assertion catches an actual security boundary: ordinary attribute-write
// grants must remain rejected, native additive rights need a pinned child, and
// DELETE_CHILD or public terminal-directory access must never be adopted.
func TestWindowsAutomaticAnchorBoundaries(t *testing.T) {
	scratch := t.TempDir()
	if err := prepareTestRoot(scratch); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(scratch, "ProgramData")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	private := "(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)"
	setACL := func(path, grants string) {
		t.Helper()
		sd, err := windows.SecurityDescriptorFromString("D:P" + grants)
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
	}
	setACL(root, private+"(A;;0x106;;;WD)") // Create children + write attributes.
	t.Cleanup(func() { setACL(root, private) })
	anchor := windowsAutomaticAnchor{root: root, dir: filepath.Join(root, "private-SID"), native: true}
	expectDenied := func(p *storeParent, err error) {
		t.Helper()
		if p != nil {
			p.close()
		}
		var ce *Error
		if !errors.As(err, &ce) || ce.Code != ConfigPermissionDenied {
			t.Fatalf("expected ACL denial, got %v", err)
		}
	}
	// The injection is package-local; production only constructs a native anchor
	// from KnownFolder metadata. No real ProgramData ACL is changed by this test.
	expectDenied(openWindowsStoreParent(anchor.dir, true, &anchor))
	if _, err = os.Stat(anchor.dir); !os.IsNotExist(err) {
		t.Fatal("empty-root rejection created target")
	}
	sentinel := filepath.Join(root, "sentinel")
	if err = os.WriteFile(sentinel, nil, 0600); err != nil {
		t.Fatal(err)
	}
	setACL(root, private+"(A;;0x100;;;WD)") // WRITE_ATTRIBUTES alone can set a reparse point.
	expectDenied(openWindowsStoreParent(anchor.dir, true, nil))
	setACL(root, private+"(A;;0x106;;;WD)")
	p, err := openWindowsStoreParent(anchor.dir, true, &anchor)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkWindowsACL(p.file, true); err != nil {
		t.Fatal(err)
	}
	p.close()
	setACL(root, private+"(A;;0x140;;;WD)") // WRITE_ATTRIBUTES + DELETE_CHILD.
	expectDenied(openWindowsStoreParent(anchor.dir, false, &anchor))
	setACL(root, private+"(A;;0x106;;;WD)")
	setACL(anchor.dir, private+"(A;;FR;;;WD)")
	expectDenied(openWindowsStoreParent(anchor.dir, false, &anchor))
	setACL(anchor.dir, private)
	// A hostile file is preserved and reported; never skipped or ACL-repaired.
	config := filepath.Join(anchor.dir, "config.json")
	original := []byte("{\"secret\":\"preserve-me\"}")
	if err = os.WriteFile(config, original, 0600); err != nil {
		t.Fatal(err)
	}
	setACL(config, "(A;;FA;;;"+user.User.Sid.String()+")(A;;FA;;;SY)(A;;FR;;;WD)")
	err = anchor.checkSelection(config, true)
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigPermissionDenied {
		t.Fatalf("hostile config accepted: %v", err)
	}
	after, err := os.ReadFile(config)
	if err != nil || string(after) != string(original) {
		t.Fatal("validation modified existing config")
	}
}
