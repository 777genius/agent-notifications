package installruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentSetupLeaseIntegrityAndFence(t *testing.T) {
	for _, mode := range []string{"disabled", "tampered", "recovery", "missing-lock"} {
		t.Run(mode, func(t *testing.T) {
			ctx, r := request(t)
			r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "primary"), Data: []byte("owned"), Mode: 0700}}
			if _, err := Commit(ctx, r); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(r.ControlRoot, ".component-install.lock")
			switch mode {
			case "tampered":
				if err := os.WriteFile(r.Files[0].Path, []byte("changed"), os.FileMode(r.Files[0].Mode)); err != nil {
					t.Fatal(err)
				}
			case "recovery":
				if err := os.WriteFile(filepath.Join(r.ControlRoot, "transaction.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-lock":
				if err := os.Remove(lock); err != nil {
					t.Fatal(err)
				}
			}
			current, release, err := AcquireCurrentSetupLease(ctx, r.ControlRoot)
			if mode != "disabled" {
				if err == nil {
					release()
					t.Fatal("invalid installation admitted")
				}
				if mode == "missing-lock" {
					if _, err := os.Lstat(lock); !os.IsNotExist(err) {
						t.Fatal("missing lock recreated")
					}
					return
				}
				// A failed integrity check must release the acquired lock.
				unlock, err := LockExisting(ctx, lock)
				if err != nil {
					t.Fatal(err)
				}
				unlock()
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if current.Enabled || current.Ledger.ID == "" {
				t.Fatal("setup changed enablement or lost installation")
			}
			peer, peerRelease, err := AcquireCurrentSetupLease(ctx, r.ControlRoot)
			if err != nil {
				t.Fatal("read-only startup blocked another reader", err)
			}
			peerRelease()
			if peer.Ledger.Generation != current.Ledger.Generation {
				t.Fatal("readers observed different pinned installations")
			}
			blocked, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			defer cancel()
			if _, err := Commit(blocked, r); err == nil {
				t.Fatal("writer bypassed pinned setup lease")
			}
		})
	}
}

func TestSetupLeaseDoesNotEnableDelivery(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	s, err := ReadInstalledSnapshot(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	current, release, err := AcquireSetupLease(ctx, r.ControlRoot, s)
	if err != nil {
		t.Fatal(err)
	}
	if current.Enabled {
		t.Fatal("setup enabled policy")
	}
	release()
	if _, release, err := AcquireInstalledLease(ctx, r.ControlRoot, s); err == nil {
		release()
		t.Fatal("disabled delivery admitted")
	}
	stale := s
	stale.Ledger.Generation++
	if _, release, err := AcquireSetupLease(ctx, r.ControlRoot, stale); err == nil {
		release()
		t.Fatal("stale setup admitted")
	}
	lock := filepath.Join(r.ControlRoot, ".component-install.lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if _, release, err := AcquireSetupLease(ctx, r.ControlRoot, s); err == nil {
		release()
		t.Fatal("lost lock accepted")
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatal("lock recreated")
	}
}
