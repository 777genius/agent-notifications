package sharedstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A mode-0600 inode with a readable ACL is still public. This catches a
// permissions-only validator, which would disclose the endpoint credential.
func TestDarwinRefusesACLDespitePrivateMode(t *testing.T) {
	for _, node := range []string{"root", "record"} {
		t.Run(node, func(t *testing.T) {
			root := testRoot(t)
			s := openStore(t, root, keyA, runtimeA)
			path := root
			if node == "record" {
				if err := s.Publish(endpoint()); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(s.dir, recordName)
			}
			if out, err := exec.Command("/bin/chmod", "+a", "everyone allow read", path).CombinedOutput(); err != nil {
				t.Fatalf("ACL fixture: %v %s", err, out)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0077 != 0 {
				t.Fatal("ACL fixture changed private mode")
			}
			if node == "root" {
				if _, err := Open(root, keyA, runtimeA); err == nil {
					t.Fatal("private-mode root ACL accepted")
				}
			} else {
				if _, err := s.Read(); err == nil {
					t.Fatal("private-mode record ACL accepted")
				}
				if err := s.Publish(endpoint()); err == nil {
					t.Fatal("ACL record overwritten")
				}
			}
		})
	}
}
