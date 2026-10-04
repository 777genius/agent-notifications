//go:build windows

package installruntime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Red condition: a cache temp inherits foreign read access at creation, allowing
// a reader to retain access to later bytes after its DACL is made private.
func TestWindowsPrivateCacheCreationExcludesInheritedReaders(t *testing.T) {
	root, _ := windowsTestDirectoryWithForeignACE(t, "OICI", 0x001200A9)
	handles, err := privateCacheRootHandles(root)
	defer closeWindowsParents(handles)
	if err != nil {
		t.Fatalf("read-only inheritable root was rejected: %v", err)
	}
	handle, err := windowsCreatePrivateCacheAt(handles[len(handles)-1], ".cache-creation-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windowsDeleteHandle(handle); err != nil {
			t.Errorf("delete TEST cache temp: %v", err)
		}
		if err := windows.CloseHandle(handle); err != nil {
			t.Errorf("close TEST cache temp: %v", err)
		}
	})
	// Inspect the still-empty creation handle before any bytes or ACL mutation.
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("created temp lacks exact current-user owner: %v", err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("created temp inherited its root DACL: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 3 {
		t.Fatalf("created temp lacks three private grants: %v", err)
	}
	want := map[string]bool{user.User.Sid.String(): false, "S-1-5-18": false, "S-1-5-32-544": false}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		// FILE_ALL_ACCESS is 0x001F01FF; no inherited/foreign ACE may remain.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 || ace.Mask != 0x001F01FF {
			t.Fatalf("created temp has an unexpected access grant: %+v", ace)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		seen, expected := want[sid]
		if !expected || seen {
			t.Fatalf("created temp grants a foreign/duplicate principal: %s", sid)
		}
		want[sid] = true
	}
}

// Red condition: an open session permits its root or an ancestor to move,
// letting the absolute lock path resolve to a different cache from its handles.
func TestWindowsPrivateCacheRootPinsPathUntilClose(t *testing.T) {
	ancestor := filepath.Join(t.TempDir(), "ancestor")
	root := filepath.Join(ancestor, "cache")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := RestrictPrivatePath(root); err != nil {
		t.Fatal(err)
	}
	session, err := OpenPrivateCacheRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, path := range []string{root, ancestor} {
		if err := os.Rename(path, path+"-moved"); err == nil {
			// Repair a regressing implementation's TEST tree before reporting failure.
			_ = os.Rename(path+"-moved", path)
			t.Fatalf("retained cache session permitted path rename: %s", path)
		}
	}
	session.Close()
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatalf("root not released: %v", err)
	}
	if err := os.Rename(ancestor, ancestor+"-moved"); err != nil {
		t.Fatalf("ancestor not released: %v", err)
	}
	if _, err := session.Read("observations.json", 100); err == nil {
		t.Fatal("closed session read succeeded")
	}
	if err := session.Write("observations.json", []byte("{}")); err == nil {
		t.Fatal("closed session write succeeded")
	}
}

// Red condition: an ACL changed after session creation grants foreign writes,
// but a retained root security decision permits reading or publication anyway.
func TestWindowsPrivateCacheRootRechecksACL(t *testing.T) {
	root, handle := windowsTestDirectoryWithForeignACE(t, "", 0x001200A9)
	if err := WritePrivateCacheDocument(root, "observations.json", []byte("prior")); err != nil {
		t.Fatal(err)
	}
	session, err := OpenPrivateCacheRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	prepared, err := session.PrepareWrite(context.Background(), "observations.json")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	original, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		dacl, _, err := original.DACL()
		if err == nil {
			err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
		}
		if err != nil {
			t.Errorf("restore TEST root ACL: %v", err)
		}
	}()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Read("observations.json", 100); err == nil {
		t.Fatal("changed root ACL permitted read")
	}
	if err := prepared.WriteContext(context.Background(), []byte("next")); err == nil {
		t.Fatal("changed root ACL permitted publication")
	}
	data, err := os.ReadFile(filepath.Join(root, "observations.json"))
	if err != nil || !bytes.Equal(data, []byte("prior")) {
		t.Fatalf("rejected publication changed cache: %q / %v", data, err)
	}
}

// Red condition: a consumed preparation permits a second replacement or leaves
// its temporary file behind after the checked publication close.
func TestWindowsPreparedCacheWriteIsSingleUse(t *testing.T) {
	root := t.TempDir()
	if err := RestrictPrivatePath(root); err != nil {
		t.Fatal(err)
	}
	session, err := OpenPrivateCacheRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	prepared, err := session.PrepareWrite(context.Background(), "observations.json")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if err := prepared.WriteContext(context.Background(), []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := prepared.WriteContext(context.Background(), []byte("second")); err == nil {
		t.Fatal("consumed preparation published twice")
	}
	data, err := session.Read("observations.json", 100)
	if err != nil || !bytes.Equal(data, []byte("first")) {
		t.Fatalf("second write changed document: %q/%v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "observations.json" {
		t.Fatalf("publication left temp: %v/%v", entries, err)
	}
}

// Diagnostic only: compare the same locked read/publication workload, including
// initial root validation in both paths. No machine-dependent timing gate.
func BenchmarkWindowsPrivateCacheRoot(b *testing.B) {
	for _, retained := range []bool{false, true} {
		name := "wrappers"
		if retained {
			name = "retained"
		}
		b.Run(name, func(b *testing.B) {
			root := b.TempDir()
			if err := RestrictPrivatePath(root); err != nil {
				b.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			unlock, err := Lock(ctx, filepath.Join(root, ".observations.lock"))
			if err != nil {
				b.Fatal(err)
			}
			defer unlock()
			data := []byte(`{"boot":"benchmark","entries":[]}`)
			if err := WritePrivateCacheDocument(root, "observations.json", data); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for range b.N {
				if retained {
					session, err := OpenPrivateCacheRoot(root)
					if err != nil {
						b.Fatal(err)
					}
					got, readErr := session.Read("observations.json", 100)
					var writeErr error
					if readErr == nil {
						writeErr = session.Write("observations.json", data)
					}
					session.Close()
					if readErr != nil || writeErr != nil || !bytes.Equal(got, data) {
						b.Fatalf("read/write: %v / %v", readErr, writeErr)
					}
				} else {
					if err := CheckPrivateCacheRoot(root); err != nil {
						b.Fatal(err)
					}
					got, err := ReadPrivateCacheDocument(root, "observations.json", 100)
					if err != nil || !bytes.Equal(got, data) {
						b.Fatalf("read: %v", err)
					}
					if err := WritePrivateCacheDocument(root, "observations.json", data); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
