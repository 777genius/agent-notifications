//go:build darwin

package opencodeinstall

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/777genius/agent-notifications/internal/desktopinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// NativeInstallation is bound to one event's policy snapshot. Its lease checks
// OpenCode registration and desktop consent independently of portable enablement
// while holding both managed locks through native handoff.
type NativeInstallation struct {
	ControlRoot  string
	Expected     installruntime.PolicySnapshot
	Executable   string
	GOOS, GOARCH string
}

var _ notifier.NativeInstallation = NativeInstallation{}

// LeasedNativeInstallation consumes only the already validated registration
// lease. StructuredDelivery retains its manifest/probe/spool validation normally.
func (l *RegistrationLease) NativeInstallation(ctx context.Context) notifier.NativeInstallation {
	return leasedNativeInstallation{lease: l, ctx: ctx}
}

type leasedNativeInstallation struct {
	lease *RegistrationLease
	ctx   context.Context
}

func (n leasedNativeInstallation) Acquire(ctx context.Context) (notifier.NativeLease, error) {
	l := n.lease
	l.mu.Lock()
	defer l.mu.Unlock()
	native := l.snapshot.Installation.Ledger.Native
	if ctx.Err() != nil || n.ctx == nil || n.ctx.Err() != nil || l.ctx == nil || l.ctx.Err() != nil || l.closed || l.nativeUsed || !l.desktop || native == nil || native.DecoderFloor < 1 {
		return nil, errors.New("admitted native installation unavailable")
	}
	l.nativeUsed = true
	l.refs++
	return &nativeLease{bundle: native.Path, release: func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.dropReference()
	}}, nil
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
	if n.ControlRoot == "" || n.Executable == "" {
		return nil, errors.New("OpenCode native installation requires control root and executable")
	}
	s, release, err := installruntime.AcquirePolicyLease(ctx, n.ControlRoot, n.Expected)
	if err != nil {
		return nil, err
	}
	goos, goarch := n.GOOS, n.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	desktop, _ := ChannelsFromSnapshot(s, n.Executable, goos, goarch)
	if !desktop || s.Installation.Ledger.Native == nil || s.Installation.Ledger.Native.DecoderFloor < 1 {
		release()
		return nil, errors.New("OpenCode desktop consent or verified native helper unavailable")
	}
	return &nativeLease{bundle: s.Installation.Ledger.Native.Path, release: release}, nil
}

// PrepareNativeSpool owns a separate OpenCode-only private namespace. Portable
// setup may later initialize controlRoot/state without colliding with it. Keep
// the spool on remove so a previously admitted bounded callback can complete.
func PrepareNativeSpool(ctx context.Context, controlRoot string) (string, error) {
	return PrepareNativeSpoolFor(ctx, controlRoot, OpenCodeDesktop)
}

// PrepareNativeSpoolFor preserves the shared platform adapter entry point.
func PrepareNativeSpoolFor(ctx context.Context, controlRoot string, product DesktopProduct) (string, error) {
	return desktopinstall.PrepareNativeSpoolFor(ctx, controlRoot, product)
}

// SetupPermission is an explicit OpenCode setup operation. Registration and
// trusted native evidence are checked before the helper may probe or request OS
// authorization. No event handler calls this function.
func SetupPermission(ctx context.Context, controlRoot string, request bool) (string, error) {
	s, err := installruntime.ReadPolicySnapshot(ctx, controlRoot)
	if err != nil {
		return "unavailable", err
	}
	c, ok := s.Installation.Ledger.Consumers[consumerID]
	if !ok || len(c.Commands) == 0 || !RegisteredFromSnapshot(s, c.Commands[0], runtime.GOOS, runtime.GOARCH) ||
		s.Installation.Ledger.Native == nil || s.Installation.Ledger.Native.DecoderFloor < 1 {
		return "unavailable", errors.New("registered OpenCode installation with verified native helper required")
	}
	return notifier.SetupPermission(ctx, controlRoot, s.Installation, request)
}
