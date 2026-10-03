//go:build windows

package observation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/windows"
)

// Red condition: failure to create the next document destroys an earlier
// attempted bit and permits that marker to be delivered again after recovery.
func TestWindowsCacheFailedPreparationPreservesAttempt(t *testing.T) {
	c, _ := cacheFixture(t)
	if err := installruntime.RestrictPrivatePath(c.Root); err != nil {
		t.Fatal(err)
	}
	markerA, markerB := strings.Repeat("1", 64), strings.Repeat("2", 64)
	bootHash := sha256.Sum256([]byte("test-boot"))
	// Specify the wire contract independently of cacheState/recordAttempt. The
	// existing durable fixture publisher runs outside the production claim budget.
	seed := []byte(fmt.Sprintf(`{"boot":%q,"entries":[{"key":%q,"until":70,"bits":1}]}`,
		hex.EncodeToString(bootHash[:]), markerA))
	if err := installruntime.WriteConfinedExclusive(c.Root, "observations.json", seed); err != nil {
		t.Fatal(err)
	}
	lockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	release, err := installruntime.Lock(lockCtx, filepath.Join(c.Root, ".observations.lock"))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	release()
	// Warm and verify the actual duplicate-read path before applying the fault.
	if claimed, err := c.Claim(context.Background(), markerA, 1); claimed || err != nil {
		t.Fatalf("seeded attempt was not a duplicate: %v/%v", claimed, err)
	}
	path := filepath.Join(c.Root, "observations.json")
	assertWindowsCachePrivateOwner(t, c.Root)
	assertWindowsCachePrivateOwner(t, path)
	assertWindowsCachePrivateOwner(t, filepath.Join(c.Root, ".observations.lock"))

	name, err := windows.UTF16PtrFromString(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(handle) })
	original, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	restore := func() error {
		dacl, _, err := original.DACL()
		if err != nil {
			return err
		}
		return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil, nil, dacl, nil)
	}
	t.Cleanup(func() {
		if err := restore(); err != nil {
			t.Errorf("restore TEST root DACL: %v", err)
		}
	})
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// Directory FILE_ADD_FILE is 0x2. The deny ACE applies only to this fresh
	// TEST root; the existing protected file/lock still allow read and delete.
	denied, err := windows.SecurityDescriptorFromString("D:P(D;;0x00000002;;;" + user.User.Sid.String() +
		")(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := denied.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if err = checkCacheRoot(c.Root); err != nil {
		t.Fatalf("creation fault made the private root unreadable: %v", err)
	}
	if claimed, err := c.Claim(context.Background(), markerB, 1); claimed || err == nil || err.Error() != "cache_unavailable" {
		t.Fatalf("failed preparation granted delivery: %v/%v", claimed, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, seed) {
		t.Fatalf("failed preparation lost prior attempt: bytes=%q, error=%v", got, err)
	}
	if err = restore(); err != nil {
		t.Fatal(err)
	}
	if claimed, err := c.Claim(context.Background(), markerA, 1); claimed || err != nil {
		t.Fatalf("prior attempt was re-admitted after recovery: %v/%v", claimed, err)
	}
}
