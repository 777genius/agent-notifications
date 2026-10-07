//go:build windows

package geminievent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/geminisource"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/webhook"
	"golang.org/x/sys/windows"
)

func init() {
	writeCacheFixture = func(root string, data []byte) error {
		return installruntime.WriteConfinedExclusive(root, "observations.json", data)
	}
}

func privateWindowsCacheFixture(t *testing.T) (Consumer, geminisource.Facts, notification.Deadline) {
	t.Helper()
	c, facts, deadline := consumerFixture(t)
	if err := installruntime.RestrictPrivatePath(c.Cache.Root); err != nil {
		t.Fatal(err)
	}
	// Creating the synthetic private lock is fixture preparation. Actual claims
	// below retain their independent production ClaimBudget.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release, err := installruntime.Lock(ctx, filepath.Join(c.Cache.Root, ".observations.lock"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	assertWindowsCachePrivateOwner(t, c.Cache.Root)
	assertWindowsCachePrivateOwner(t, filepath.Join(c.Cache.Root, ".observations.lock"))
	return c, facts, deadline
}

func windowsCacheSecurity(t *testing.T, path string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd
}

func assertWindowsCachePrivateOwner(t *testing.T, path string) {
	t.Helper()
	sd := windowsCacheSecurity(t, path)
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

// Set only the synthetic fixture's DACL. No inheritance flags: changing the
// root must leave the existing private document and normal lock unchanged.
func windowsCacheEveryoneACE(t *testing.T, path, rights string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"D:P(A;;FA;;;%s)(A;;FA;;;SY)(A;;FA;;;BA)(A;;%s;;;WD)", user.User.Sid.String(), rights))
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// Red condition: forged attempted bits in an Everyone Modify root/document
// yield a duplicate or any effect instead of neutral cache_unavailable.
func TestWindowsCacheRejectsEveryoneModifyBeforeEffect(t *testing.T) {
	for _, target := range []string{"root", "document"} {
		t.Run(target, func(t *testing.T) {
			c, facts, deadline := privateWindowsCacheFixture(t)
			boot := sha256.Sum256([]byte("test-boot"))
			forged, err := json.Marshal(cacheState{Boot: hex.EncodeToString(boot[:]),
				Entries: []cacheEntry{{Key: marker(c.Binding, facts), Until: 70, Bits: 3}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = installruntime.WriteConfinedExclusive(c.Cache.Root, "observations.json", forged); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(c.Cache.Root, "observations.json")
			assertWindowsCachePrivateOwner(t, path)
			// The exact same bytes are valid before weakening one object's ACL.
			if data, err := installruntime.ReadPrivateCacheDocument(c.Cache.Root, "observations.json", 48*1024); err != nil || !bytes.Equal(data, forged) {
				t.Fatalf("invalid control document: %v", err)
			}
			if claimed, err := c.Cache.claim(context.Background(), c.Binding, facts, DesktopChannel); err != nil || claimed {
				t.Fatalf("forged bits are not a valid duplicate fixture: %v", err)
			}
			unsafePath := path
			if target == "root" {
				unsafePath = c.Cache.Root
			}
			windowsCacheEveryoneACE(t, unsafePath, "0x001301bf") // Windows Modify
			securityBefore := windowsCacheSecurity(t, unsafePath).String()
			lockPath := filepath.Join(c.Cache.Root, ".observations.lock")
			assertWindowsCachePrivateOwner(t, lockPath)
			lockBefore := windowsCacheSecurity(t, lockPath).String()
			if target == "root" && installruntime.CheckPrivateCacheRoot(c.Cache.Root) == nil {
				t.Fatal("Everyone Modify root accepted")
			}
			if data, err := installruntime.ReadPrivateCacheDocument(c.Cache.Root, "observations.json", 48*1024); err == nil || data != nil {
				t.Fatalf("unsafe cache bytes returned: %v", err)
			}
			c.Gate = testGate{channels: Channels{true, true}, check: func(context.Context, Binding, Channel) bool {
				t.Error("unsafe cache reached effect recheck")
				return true
			}}
			c.Desktop = deliveryFunc(func(context.Context, notification.Request) notification.Receipt {
				t.Error("unsafe cache reached desktop effect")
				return notification.Receipt{Status: "submitted"}
			})
			c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
				t.Error("unsafe cache reached webhook effect")
				return nil
			}
			for _, timestamp := range []string{facts.Timestamp, "2026-10-01T05:00:01Z"} {
				facts.Timestamp = timestamp
				got := c.Consume(context.Background(), facts, deadline)
				want := Receipt{Status: "suppressed", Reason: "cache_unavailable", Desktop: "cache_unavailable", Webhook: "cache_unavailable"}
				if got != want {
					t.Fatalf("unsafe cache receipt: %+v", got)
				}
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, forged) || windowsCacheSecurity(t, unsafePath).String() != securityBefore || windowsCacheSecurity(t, lockPath).String() != lockBefore {
				t.Fatalf("observation processing mutated unsafe fixture: %v", err)
			}
		})
	}
}

// Red condition: the reused validator rejects a valid exact-owner private
// cache, resets durable bits, or accidentally strengthens its read-only policy.
func TestWindowsCachePrivateOwnerControl(t *testing.T) {
	for _, foreignRead := range []bool{false, true} {
		t.Run(fmt.Sprint("foreignRead=", foreignRead), func(t *testing.T) {
			c, facts, deadline := privateWindowsCacheFixture(t)
			if foreignRead {
				windowsCacheEveryoneACE(t, c.Cache.Root, "FRFX")
			}
			if err := installruntime.CheckPrivateCacheRoot(c.Cache.Root); err != nil {
				t.Fatal(err)
			}
			// This test owns ACL acceptance and read-only duplicate admission.
			// Prepare valid attempted bits outside the claim's filesystem budget;
			// first delivery and cold-cache publication have separate strict tests.
			boot := sha256.Sum256([]byte("test-boot"))
			seed, err := json.Marshal(cacheState{Boot: hex.EncodeToString(boot[:]),
				Entries: []cacheEntry{{Key: marker(c.Binding, facts), Until: 70, Bits: 3}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := installruntime.WriteConfinedExclusive(c.Cache.Root, "observations.json", seed); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(c.Cache.Root, "observations.json")
			assertWindowsCachePrivateOwner(t, path)
			if foreignRead {
				windowsCacheEveryoneACE(t, path, "FRFX")
			}
			if data, err := installruntime.ReadPrivateCacheDocument(c.Cache.Root, "observations.json", 48*1024); err != nil || !bytes.Equal(data, seed) {
				t.Fatalf("valid private/read-only cache rejected: %v", err)
			}
			lockPath := filepath.Join(c.Cache.Root, ".observations.lock")
			rootBefore := windowsCacheSecurity(t, c.Cache.Root).String()
			documentBefore := windowsCacheSecurity(t, path).String()
			lockBefore := windowsCacheSecurity(t, lockPath).String()
			effects := 0
			c.Desktop = deliveryFunc(func(context.Context, notification.Request) notification.Receipt {
				effects++
				return notification.Receipt{Status: "submitted"}
			})
			c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { effects++; return nil }
			// Reopen via another RecentCache value to check persisted channel bits.
			c.Cache = &RecentCache{Root: c.Cache.Root, Clock: c.Clock}
			want := Receipt{Status: "suppressed", Reason: "no_attempt", Desktop: "duplicate", Webhook: "duplicate"}
			if got := c.Consume(context.Background(), facts, deadline); got != want || effects != 0 {
				t.Fatalf("private cache lost persisted bits: %+v / %d", got, effects)
			}
			if data, err := installruntime.ReadPrivateCacheDocument(c.Cache.Root, "observations.json", 48*1024); err != nil || !bytes.Equal(data, seed) ||
				windowsCacheSecurity(t, c.Cache.Root).String() != rootBefore ||
				windowsCacheSecurity(t, path).String() != documentBefore ||
				windowsCacheSecurity(t, lockPath).String() != lockBefore {
				t.Fatalf("duplicate admission mutated private cache bytes/ACL: %v", err)
			}
		})
	}
}
