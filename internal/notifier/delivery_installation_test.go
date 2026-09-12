//go:build darwin || linux

package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func pr3ManagedFixture(t *testing.T) ManagedInstallation {
	t.Helper()
	return pr3ProducedFixture(t, true)
}

func pr3ProducedFixture(t *testing.T, qualified bool) ManagedInstallation {
	t.Helper()
	root := t.TempDir()
	os.Chmod(root, 0700)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "Fixture.app")
	for _, dir := range []string{"Contents/MacOS", "Contents/Resources"} {
		if err := os.MkdirAll(filepath.Join(source, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "Contents/MacOS/terminal-notifier-modern"), []byte("inert-never-execute\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// No external attestation: the real producer never executes these inert bytes.
	if err := os.WriteFile(filepath.Join(source, "Contents/Resources/managed-runtime.json"), []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	change, err := installruntime.StageNative(ctx, root, source)
	if err != nil {
		t.Fatal(err)
	}
	// Model only the Darwin qualification result. Fingerprint, directory identity
	// and attestation binding are produced by StageNative, not invented here.
	if qualified {
		change.After.DecoderFloor = 1
	}
	r := installruntime.Request{ControlRoot: root, RuntimeRoot: filepath.Join(root, "bin"), Owner: "fixture-installer", ConsumerID: "fixture", Native: change}
	ledger, err := installruntime.Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	r.Native = nil
	r.PolicyEnabled = &enabled
	r.ExpectedGeneration = &ledger.Generation
	if _, err := installruntime.Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	return ManagedInstallation{ControlRoot: root, Expected: snapshot.Installation}
}
func TestPR3ManagedLeasePinsTreeAndReleasesLock(t *testing.T) {
	m := pr3ManagedFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := m.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lease.BundlePath() != m.Expected.Ledger.Native.Path || !strings.HasPrefix(lease.ExecutablePath(), m.Expected.Ledger.Native.Path+"/") {
		t.Fatal("unstable bundle selected")
	}
	blocked, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if release, err := lockPrivate(blocked, filepath.Join(m.ControlRoot, ".component-install.lock"), false); err == nil {
		release()
		t.Fatal("install not pinned")
	}
	stop()
	lease.Release()
	lease.Release()
	release, err := lockPrivate(ctx, filepath.Join(m.ControlRoot, ".component-install.lock"), false)
	if err != nil {
		t.Fatal("lease not released", err)
	}
	release()
}
func TestPR3ManagedOldAndTamperedTreesNeverProbe(t *testing.T) {
	for _, kind := range []string{"old", "fingerprint", "mode", "symlink", "manifest", "outside"} {
		t.Run(kind, func(t *testing.T) {
			m := pr3ManagedFixture(t)
			switch kind {
			case "old":
				m.Expected.Ledger.Native.DecoderFloor = 0
			case "fingerprint":
				os.WriteFile(filepath.Join(m.Expected.Ledger.Native.Path, "Contents/MacOS/terminal-notifier-modern"), []byte("replacement"), 0755)
			case "mode":
				os.Chmod(filepath.Join(m.Expected.Ledger.Native.Path, "Contents/MacOS/terminal-notifier-modern"), 0644)
			case "symlink":
				os.Symlink("missing", filepath.Join(m.Expected.Ledger.Native.Path, "foreign"))
			case "manifest":
				os.Remove(filepath.Join(m.Expected.Ledger.Native.Path, "Contents/Resources/managed-runtime.json"))
			case "outside":
				m.ControlRoot = t.TempDir()
			}
			h := newPR3Harness(t)
			h.delivery.Installation = m
			got := h.delivery.Deliver(context.Background(), pr3Request())
			if got.Reason != "unsupported_notifier" || h.probes+h.launches+h.prepares != 0 {
				t.Fatalf("unverified tree probed %+v", got)
			}
		})
	}
}
func TestPR3ManagedLockConsumesOriginalDeadline(t *testing.T) {
	m := pr3ManagedFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := lockPrivate(ctx, filepath.Join(m.ControlRoot, ".component-install.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	h := newPR3Harness(t)
	h.delivery.Installation = m
	request := pr3Request()
	request.Deadline.NotAfter = 100.025
	got := h.delivery.Deliver(context.Background(), request)
	if got.Reason != "expired" || h.probes+h.launches+h.prepares != 0 {
		t.Fatal("lock gave a new budget")
	}
}
func TestPR3StaleCleanupPreservesNewNonce(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	s := &PrivateNativeSpool{Root: root, Clock: &pr3Clock{now: 100}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	encode := func(string) ([]byte, error) { return []byte("{}"), nil }
	old, err := s.Prepare(ctx, pr3Request(), encode)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Expire(old); err != nil {
		t.Fatal(err)
	}
	current, err := s.Prepare(ctx, pr3Request(), encode)
	if err != nil {
		t.Fatal(err)
	}
	if old.Nonce == current.Nonce {
		t.Fatal("nonce reused")
	}
	if err = s.Expire(old); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(current.RequestPath); err != nil {
		t.Fatal("stale owner removed current attempt")
	}
}
func TestPR3SpoolCapacityAndConcurrentAttempts(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	s := &PrivateNativeSpool{Root: root, Clock: &pr3Clock{now: 100}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	encode := func(string) ([]byte, error) { return []byte("{}"), nil }
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := s.Prepare(ctx, pr3Request(), encode); results <- err }()
	}
	failures := 0
	for i := 0; i < 2; i++ {
		if <-results != nil {
			failures++
		}
	}
	if failures != 1 {
		t.Fatal("duplicate attempt admitted")
	}
	for i := 1; i < maxSpoolAttempts; i++ {
		r := pr3Request()
		r.CorrelationID, _ = uuidString()
		if _, err := s.Prepare(ctx, r, encode); err != nil {
			t.Fatal(err)
		}
	}
	r := pr3Request()
	r.CorrelationID, _ = uuidString()
	if _, err := s.Prepare(ctx, r, encode); err == nil {
		t.Fatal("full spool accepted more work")
	}
}

type vanishedNativeEntry struct{ os.DirEntry }

func (vanishedNativeEntry) Info() (os.FileInfo, error) { return nil, os.ErrNotExist }
func TestPR3NativeConsumptionDuringSpoolAccounting(t *testing.T) {
	if bytes, err := spoolEntrySize(vanishedNativeEntry{}); err != nil || bytes != 0 {
		t.Fatal("normal native consumption rejected unrelated request", err)
	}
}

func TestManagedSnapshotFencesAfterReadiness(t *testing.T) {
	for _, kind := range []string{"disable", "generation", "recovery", "owner"} {
		t.Run(kind, func(t *testing.T) {
			m := pr3ManagedFixture(t)
			h := newPR3Harness(t)
			h.delivery.Installation = m
			if out := h.delivery.CheckReadiness(context.Background(), pr3Request()); out.Status != "ready" {
				t.Fatal(out)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			// Commit can run now: readiness has released the component lease before
			// any durable admission. Delivery must subsequently reacquire this snapshot.
			r := installruntime.Request{ControlRoot: m.ControlRoot, RuntimeRoot: m.Expected.Ledger.RuntimeRoot, Owner: m.Expected.Ledger.Owner, ConsumerID: "fixture", ExpectedGeneration: &m.Expected.Ledger.Generation}
			switch kind {
			case "disable":
				disabled := false
				r.PolicyEnabled = &disabled
			case "recovery":
				r.Fault = func(string) error { return errors.New("isolated crash") }
			case "owner":
				// Simulate an ownership edit without inventing another manifest.
				ledger := m.Expected.Ledger
				ledger.Owner = "replacement-owner"
				data, err := json.Marshal(ledger)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(m.ControlRoot, "ownership.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "owner" {
				_, err := installruntime.Commit(ctx, r)
				if (kind == "recovery") != (err != nil) {
					t.Fatalf("mutation: %v", err)
				}
			}
			h.probes, h.launches, h.prepares = 0, 0, 0
			if out := h.delivery.CheckReadiness(context.Background(), pr3Request()); out.Status == "ready" {
				t.Fatal(out)
			}
			out := h.delivery.Deliver(context.Background(), pr3Request())
			if out.Reason != "unsupported_notifier" || h.probes+h.launches+h.prepares != 0 {
				t.Fatalf("stale snapshot ran: %+v", out)
			}
			release, err := installruntime.LockExisting(ctx, filepath.Join(m.ControlRoot, ".component-install.lock"))
			if err != nil {
				t.Fatal("error leaked lease", err)
			}
			release()
		})
	}
}

func TestManagedMissingStateNeverCreated(t *testing.T) {
	for _, kind := range []string{"root", "lock", "ledger"} {
		t.Run(kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "missing")
			if kind != "root" {
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "ledger" {
				if err := os.WriteFile(filepath.Join(root, ".component-install.lock"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			m := ManagedInstallation{ControlRoot: root}
			if l, err := m.Acquire(ctx); err == nil {
				l.Release()
				t.Fatal("missing accepted")
			}
			if kind == "root" {
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatal("root created")
				}
				return
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "ledger" {
				want = 1
			}
			if len(entries) != want {
				t.Fatal("state created", entries)
			}
		})
	}
}

func TestManagedReleaseOnce(t *testing.T) {
	calls := 0
	l := &managedNativeLease{unlock: func() { calls++ }}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); l.Release() }()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestManagedProducerLegacyNeverProbesAndReleases(t *testing.T) {
	m := pr3ProducedFixture(t, false)
	h := newPR3Harness(t)
	h.delivery.Installation = m
	if out := h.delivery.Deliver(context.Background(), pr3Request()); out.Reason != "unsupported_notifier" || h.probes+h.launches+h.prepares != 0 {
		t.Fatal(out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := installruntime.LockExisting(ctx, filepath.Join(m.ControlRoot, ".component-install.lock"))
	if err != nil {
		t.Fatal("adapter rejection leaked lease", err)
	}
	release()
}

func TestManagedAcquireCancellation(t *testing.T) {
	m := pr3ManagedFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := installruntime.LockExisting(ctx, filepath.Join(m.ControlRoot, ".component-install.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	start := time.Now()
	if lease, err := m.Acquire(short); !errors.Is(err, context.DeadlineExceeded) {
		if lease != nil {
			lease.Release()
		}
		t.Fatalf("cancellation: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("unbounded lock wait")
	}
}

func TestManagedUnchangedReadinessThenDelivery(t *testing.T) {
	m := pr3ManagedFixture(t)
	h := newPR3Harness(t)
	h.delivery.Installation = m
	if out := h.delivery.CheckReadiness(context.Background(), pr3Request()); out.Status != "ready" {
		t.Fatal(out)
	}
	if out := h.delivery.Deliver(context.Background(), pr3Request()); out.Status != "submitted" || h.launches != 1 {
		t.Fatal(out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := m.Acquire(ctx)
	if err != nil {
		t.Fatal("delivery retained lease", err)
	}
	lease.Release()
}
