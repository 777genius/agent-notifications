//go:build darwin

package geminiinstall

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// NativeInstallation leaves the sole effect lease with the existing Mac
// backend. It validates this Gemini binding and desktop consent under that lease.
type NativeInstallation struct {
	ControlRoot, Executable, Binding string
	Expected                         installruntime.PolicySnapshot
}

type nativeLease struct {
	bundle  string
	release func()
	once    sync.Once
}

func (l *nativeLease) BundlePath() string { return l.bundle }
func (l *nativeLease) ExecutablePath() string {
	return filepath.Join(l.bundle, "Contents", "MacOS", "terminal-notifier-modern")
}
func (l *nativeLease) Release() { l.once.Do(l.release) }

func (n NativeInstallation) Acquire(ctx context.Context) (notifier.NativeLease, error) {
	s, release, err := installruntime.AcquirePolicyLease(ctx, n.ControlRoot, n.Expected)
	if err != nil {
		return nil, err
	}
	desktop, _ := ChannelsFromSnapshot(s, n.ControlRoot, n.Executable, n.Binding, runtime.GOOS, runtime.GOARCH)
	if !desktop || s.Installation.Ledger.Native == nil || s.Installation.Ledger.Native.DecoderFloor < 1 {
		release()
		return nil, errors.New("Gemini desktop consent or verified native helper unavailable")
	}
	return &nativeLease{bundle: s.Installation.Ledger.Native.Path, release: release}, nil
}

// Explicit setup is the only caller allowed to query/request OS permission.
func SetupPermission(ctx context.Context, root string, request bool) (string, error) {
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return "unavailable", err
	}
	c, ok := s.Installation.Ledger.Consumers[consumerID]
	if !ok || len(c.Commands) == 0 || !RegisteredFromSnapshot(s, root, c.Commands[0], "", runtime.GOOS, runtime.GOARCH) || s.Installation.Ledger.Native == nil || s.Installation.Ledger.Native.DecoderFloor < 1 {
		return "unavailable", errors.New("registered Gemini installation with verified native helper required")
	}
	return notifier.SetupPermission(ctx, root, s.Installation, request)
}
