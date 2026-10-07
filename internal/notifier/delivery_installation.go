package notifier

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// ManagedInstallation belongs to one request. Expected is the unchanged,
// immutable Installation from that request's ReadPolicySnapshot. Use the same
// adapter for readiness and delivery, never a startup-global snapshot. Acquire
// must run without a journal lock and never refreshes a stale snapshot.
// The kernel owns root, permanent lock, generation, owner and artifact checks.
type ManagedInstallation struct {
	ControlRoot string
	Expected    installruntime.InstalledSnapshot
}
type managedNativeLease struct {
	bundle string
	unlock func()
	once   sync.Once
}

func (l *managedNativeLease) BundlePath() string { return l.bundle }
func (l *managedNativeLease) ExecutablePath() string {
	return filepath.Join(l.bundle, "Contents", "MacOS", "terminal-notifier-modern")
}
func (l *managedNativeLease) Release() { l.once.Do(l.unlock) }
func (m ManagedInstallation) Acquire(ctx context.Context) (NativeLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.ControlRoot == "" {
		return nil, errors.New("missing managed control root")
	}
	current, release, err := installruntime.AcquireInstalledLease(ctx, m.ControlRoot, m.Expected)
	if err != nil {
		return nil, err
	}
	// Retained legacy records are valid installations but cannot consume this
	// protocol. The producer qualifies the floor; shared verification binds its
	// record and attestation to the installed tree.
	if current.Ledger.Native == nil || current.Ledger.Native.DecoderFloor < 1 {
		release()
		return nil, errors.New("unsupported managed notifier")
	}
	return &managedNativeLease{bundle: current.Ledger.Native.Path, unlock: release}, nil
}
