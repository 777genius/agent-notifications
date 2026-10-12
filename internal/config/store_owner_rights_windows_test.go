//go:build windows

package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// This is red when OWNER RIGHTS is mistaken for an unrelated trustee, or
// read-only access to an ancestor is mistaken for permission to alter it.
func TestWindowsOwnerRightsConfiguration(t *testing.T) {
	env := storeEnv(t)
	home := env.Vars["USERPROFILE"]
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
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
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
	}
	private := "(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)"
	ownerRights := private + "(A;OICI;FA;;;OW)"
	// Stand in for CodexSandboxUsers using an actual unrelated principal.
	setACL(home, ownerRights+"(A;;FRFX;;;WD)")
	t.Cleanup(func() { setACL(home, private) })
	r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if err != nil {
		t.Fatalf("init under owner-rights/readable ancestor: %v", err)
	}
	setACL(r.Selection.Path, "(A;;FA;;;"+user.User.Sid.String()+")(A;;FA;;;SY)(A;;FA;;;OW)")
	for _, d := range nativePermissionDiagnostics(r.Selection.Path) {
		t.Fatalf("private owner-rights config diagnosed as public: %+v", d)
	}
	if _, _, err := ReadDocument(ReadRequest{Env: env, ReadSnapshot: ReadFileSnapshot}); err != nil {
		t.Fatalf("managed hook read: %v", err)
	}
	if _, err := ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}}); err != nil {
		t.Fatalf("owner-rights config edit: %v", err)
	}
	// Public read remains diagnostic and public write remains rejected.
	setACL(r.Selection.Path, private+"(A;;FR;;;WD)")
	if ds := nativePermissionDiagnostics(r.Selection.Path); len(ds) != 1 || ds[0].Code != ConfigPublicReadable {
		t.Fatalf("public read was hidden: %+v", ds)
	}
	before, err := os.ReadFile(r.Selection.Path)
	if err != nil {
		t.Fatal(err)
	}
	setACL(home, ownerRights+"(A;;FW;;;WD)")
	_, err = EnsureInitialized(context.Background(), InitRequest{Env: EnvSnapshot{GOOS: "windows", Vars: map[string]string{OverrideEnv: filepath.Join(home, "unsafe", "config.json")}, Lstat: os.Lstat, ReadDir: os.ReadDir, Canonicalize: canonicalParent}})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigPermissionDenied {
		t.Fatalf("public-writable ancestor accepted: %v", err)
	}
	after, err := os.ReadFile(r.Selection.Path)
	if err != nil || string(before) != string(after) {
		t.Fatal("rejected initialization changed existing config")
	}
}
