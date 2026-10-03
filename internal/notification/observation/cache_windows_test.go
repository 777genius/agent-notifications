//go:build windows

package observation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/windows"
)

func assertWindowsCachePrivateOwner(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatal("fixture is not owned by the exact current-user SID")
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("fixture lacks protected private DACL: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 3 {
		t.Fatalf("fixture lacks user/SYSTEM/Administrators DACL: %v", err)
	}
}

// Red condition: a missing file loses its absence classification, a second
// name can alias trusted bytes, or the retained document bound is bypassed.
func TestWindowsCacheDocumentBoundsAndIdentity(t *testing.T) {
	c, _ := cacheFixture(t)
	if err := installruntime.RestrictPrivatePath(c.Root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release, err := installruntime.Lock(ctx, filepath.Join(c.Root, ".observations.lock"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	assertWindowsCachePrivateOwner(t, c.Root)
	assertWindowsCachePrivateOwner(t, filepath.Join(c.Root, ".observations.lock"))
	if data, err := readCache(c.Root); !os.IsNotExist(err) || data != nil {
		t.Fatalf("missing document: %v", err)
	}
	if err := writeCache(c.Root, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Root, "observations.json")
	assertWindowsCachePrivateOwner(t, path)
	alias := filepath.Join(c.Root, "alias.json")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if data, err := readCache(c.Root); err == nil || data != nil {
		t.Fatalf("hardlinked document accepted: %v", err)
	}
	// Red condition: replacement trusts a hardlinked target, discarding the
	// checked document name while another name still refers to its old inode.
	if err := writeCache(c.Root, []byte("replacement")); err == nil {
		t.Fatal("hardlinked replacement target accepted")
	}
	for _, name := range []string{path, alias} {
		if data, err := os.ReadFile(name); err != nil || string(data) != "{}" {
			t.Fatalf("rejected replacement mutated %s: %q/%v", name, data, err)
		}
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if data, err := readCache(c.Root); err != nil || string(data) != "{}" {
		t.Fatalf("single-link control rejected: %v", err)
	}
	if err := writeCache(c.Root, []byte(strings.Repeat(" ", cacheBytes+1))); err != nil {
		t.Fatal(err)
	}
	assertWindowsCachePrivateOwner(t, path)
	if data, err := readCache(c.Root); err == nil || data != nil {
		t.Fatalf("oversized document accepted: %v", err)
	}
}
