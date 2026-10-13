//go:build windows

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsOSPermissionFailureHasNoFabricatedTrustee(t *testing.T) {
	env := storeEnv(t)
	path := filepath.Join(env.Vars["USERPROFILE"], "read-only-file")
	if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FR;;;" + user.User.Sid.String() + ")")
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
	f, err := winOpen(path, windows.GENERIC_WRITE, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
	if f != nil {
		f.Close()
		t.Fatal("write unexpectedly allowed")
	}
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigPermissionDenied {
		t.Fatalf("wrong failure: %v", err)
	}
	detail := ce.PermissionFailure()
	if detail == nil || detail.ObjectPath != path || detail.Reason != "os_access_denied" || detail.TrusteeSID != "" || detail.OwnerSID != "" || detail.AccessMask != 0 {
		t.Fatalf("unsafe OS diagnostic: %+v", detail)
	}
	if !portableGuardUnavailable(err) {
		t.Fatal("typed OS denial changed portable guard fallback")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "unchanged" {
		t.Fatalf("denial changed contents: %q %v", data, err)
	}
}
