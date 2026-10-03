package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// Retain the real kernel lease/profile check; additionally honor the context,
// as the production Local authority lease does. No affirmative proof is minted.
type completionReasonLease struct {
	*localBoundaryLease
	t          *testing.T
	onComplete func()
	releases   int
}

func (l *completionReasonLease) Complete(ctx context.Context) error {
	if l.onComplete != nil {
		l.onComplete()
	}
	err := l.localBoundaryLease.Complete(ctx)
	if err != nil {
		return err
	}
	return ctx.Err()
}
func (l *completionReasonLease) Release() {
	l.releases++
	if l.complete != 1 || l.releases != 1 || l.released {
		l.t.Error("Complete must run exactly once before Release")
	}
	l.localBoundaryLease.Release()
}

type completionReasonInstallation struct {
	*localBoundaryInstallation
	retained *completionReasonLease
}

func (i *completionReasonInstallation) Acquire(ctx context.Context) (NativeLease, error) {
	_, err := i.localBoundaryInstallation.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return i.retained, nil
}

type completionReasonProcess struct {
	localBoundaryProcess
	onProbe   func()
	onLaunch  func()
	noReceipt bool
}

func (p *completionReasonProcess) Probe(ctx context.Context, path string) ([]byte, error) {
	if p.onProbe != nil {
		p.onProbe()
	}
	return p.localBoundaryProcess.Probe(ctx, path)
}
func (p *completionReasonProcess) Launch(ctx context.Context, bundle, request, receipt string) (bool, error) {
	if p.noReceipt {
		p.launches++
		p.onLaunch()
		return true, errors.New("TEST possible handoff without receipt")
	}
	handed, err := p.localBoundaryProcess.Launch(ctx, bundle, request, receipt)
	if p.onLaunch != nil {
		p.onLaunch()
	}
	return handed, err
}

// RED on public5d808: cancelled Complete overwrites an already truthful reason.
// All termination is deterministic at existing process/spool/lease seams.
func TestLocalCompletionPreservesTerminationReason(t *testing.T) {
	for _, phase := range []string{"cancel-before", "expire-before", "cancel-after", "timeout-after", "receipt-cancel", "active-drift"} {
		t.Run(phase, func(t *testing.T) {
			base, err := installruntime.CanonicalPath(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root, run := filepath.Join(base, "control"), filepath.Join(base, "runtime")
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: "TEST", ConsumerID: "TEST", Consumer: installruntime.Consumer{Registration: "TEST"}}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := installruntime.ReadInstalledSnapshot(root)
			if err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(base, "profile")
			if err := os.WriteFile(profile, []byte("owned-profile"), 0600); err != nil {
				t.Fatal(err)
			}
			rawLease := &localBoundaryLease{profile: profile}
			retained := &completionReasonLease{localBoundaryLease: rawLease, t: t}
			installed := &localBoundaryInstallation{root: root, snapshot: snapshot, lease: rawLease}
			installation := &completionReasonInstallation{localBoundaryInstallation: installed, retained: retained}
			clock := &pr3Clock{now: 100}
			spoolRoot := filepath.Join(base, "spool")
			if err := os.Mkdir(spoolRoot, 0700); err != nil {
				t.Fatal(err)
			}
			spool := &localBoundarySpool{PrivateNativeSpool: &PrivateNativeSpool{Root: spoolRoot, Clock: clock}}
			process := &completionReasonProcess{}
			status, reason, launches, expires := "rejected", "expired", 0, 0
			switch phase {
			case "cancel-before":
				process.onProbe = cancel
			case "expire-before":
				spool.drift = func() { clock.set(104); cancel() }
				expires = 1
			case "cancel-after":
				process.noReceipt = true
				process.onLaunch = cancel
				status, reason, launches = "unknown", "handoff_unconfirmed", 1
			case "timeout-after":
				process.noReceipt = true
				process.onLaunch = func() { clock.set(104); cancel() }
				status, reason, launches, expires = "unknown", "timeout", 1, 1
			case "receipt-cancel":
				retained.onComplete = cancel
				status, reason, launches = "unknown", "handoff_unconfirmed", 1
			case "active-drift":
				process.onLaunch = func() {
					if err := os.WriteFile(profile, []byte("foreign-profile"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				status, reason, launches = "unknown", "authority_changed", 1
			}
			request := pr3Request()
			request.Navigation = notification.None
			request.Target = notification.DesktopTarget{}
			request.Deadline.NotAfter = 104 // Original four-second admission, never renewed.
			delivery := &StructuredDelivery{Installation: installation, Clock: clock, Process: process, Spool: spool}
			got := delivery.Deliver(ctx, request)
			if got.Status != status || got.Reason != reason || got.RetrySafe {
				t.Fatalf("phase %s: want %s/%s retrySafe=false, got %+v", phase, status, reason, got)
			}
			if installation.acquires != 1 || rawLease.complete != 1 || !rawLease.released || retained.releases != 1 || process.launches != launches || spool.expires != expires {
				t.Fatalf("lease/handoff accounting: acquires=%d complete=%d releases=%d launches=%d expires=%d", installation.acquires, rawLease.complete, retained.releases, process.launches, spool.expires)
			}
			if launches == 1 && expires == 0 {
				raw, err := os.ReadFile(spool.prepared.RequestPath)
				if err != nil {
					t.Fatal(err)
				}
				var wire nativeprotocol.Request
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatal(err)
				}
				ownerRaw, err := os.ReadFile(filepath.Join(spool.prepared.Directory, "owner.json"))
				if err != nil {
					t.Fatal(err)
				}
				var owner spoolOwner
				if err := json.Unmarshal(ownerRaw, &owner); err != nil {
					t.Fatal(err)
				}
				if wire.Nonce != spool.prepared.Nonce || wire.NotAfter != 104 || wire.BootID != request.Deadline.BootID || owner.Nonce != wire.Nonce || owner.NotAfter != 104 {
					t.Fatal("unknown handoff renewed nonce/deadline/TTL or lost spool ownership")
				}
			}
			// Verify the real retained component lock was released even after cancel.
			verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer verifyCancel()
			if _, release, err := installruntime.AcquireSetupLease(verifyCtx, root, snapshot); err != nil {
				t.Fatal(err)
			} else {
				release()
			}
		})
	}
}
