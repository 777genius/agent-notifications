package sharedstate

import (
	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func makePrivateFixture(path string) error { return installruntime.RestrictPrivatePath(path) }
func TestWindowsRefusesForeignReadACL(t *testing.T) {
	for _, kind := range []string{"root", "record", "lock"} {
		t.Run(kind, func(t *testing.T) {
			root := testRoot(t)
			s := openStore(t, root, keyA, runtimeA)
			path := root
			if kind == "record" {
				if err := s.Publish(endpoint()); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(s.dir, recordName)
			}
			if kind == "lock" {
				path = filepath.Join(s.dir, "start.lock")
			}
			ptr, err := windows.UTF16PtrFromString(path)
			if err != nil {
				t.Fatal(err)
			}
			h, err := windows.CreateFile(ptr, windows.WRITE_DAC|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = windows.CloseHandle(h) }()
			user, err := windows.GetCurrentProcessToken().GetTokenUser()
			if err != nil {
				t.Fatal(err)
			}
			sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FR;;;WD)")
			if err != nil {
				t.Fatal(err)
			}
			acl, _, err := sd.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			if kind == "root" || kind == "lock" {
				if _, err := Open(root, keyA, runtimeA); err == nil {
					t.Fatal("foreign read ACL accepted")
				}
			} else {
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.Read(); err == nil {
					t.Fatal("foreign readable token accepted")
				}
				if err := s.Publish(endpoint()); err == nil {
					t.Fatal("foreign readable token replaced")
				}
				after, err := os.ReadFile(path)
				if err != nil || string(before) != string(after) {
					t.Fatal("refused token changed")
				}
			}
		})
	}
}

// Publication must remain possible while another bridge finishes reading the
// immutable previous record. Denying delete-sharing on readers breaks restart.
func TestWindowsDiscoveryReadDoesNotBlockPublication(t *testing.T) {
	s := openStore(t, testRoot(t), keyA, runtimeA)
	release := locked(t, s)
	defer release()
	first := endpoint()
	if err := s.Publish(first); err != nil {
		t.Fatal(err)
	}
	d, _, err := openDirectory(s, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	reader, err := d.open(recordName, windows.GENERIC_READ, windows.OPEN_EXISTING, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	next := first
	next.PID++
	if err := s.Publish(next); err != nil {
		t.Fatalf("held discovery reader blocked atomic replacement: %v", err)
	}
	if got, err := s.Read(); err != nil || got != next {
		t.Fatal("replacement endpoint unavailable")
	}
}
