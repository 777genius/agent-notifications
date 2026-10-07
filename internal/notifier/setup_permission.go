package notifier

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// SetupPermission is an explicit installer action. It never changes enablement,
// sends a notification, repairs an installation, or retries authorization.
func SetupPermission(ctx context.Context, root string, expected installruntime.InstalledSnapshot, request bool) (string, error) {
	if ctx == nil {
		return "unavailable", fmt.Errorf("setup context required")
	}
	ctx, cancel := context.WithTimeout(ctx, 130*time.Second)
	defer cancel()
	s, release, err := installruntime.AcquireSetupLease(ctx, root, expected)
	if err != nil {
		return "unavailable", err
	}
	defer release()
	if s.Ledger.Native == nil || s.Ledger.Native.DecoderFloor < 1 {
		return "unavailable", fmt.Errorf("unsupported managed notifier")
	}
	exe := filepath.Join(s.Ledger.Native.Path, "Contents", "MacOS", "terminal-notifier-modern")
	return setupPermissionSequence(ctx, ManagedNativeProcess{}, exe, request)
}

type setupPermissionProcess interface {
	Probe(context.Context, string) ([]byte, error)
	ProbeSetup(context.Context, string) ([]byte, error)
	ProbePermission(context.Context, string, string, string) ([]byte, error)
	RequestPermission(context.Context, string, string, string) ([]byte, error)
}

func setupPermissionSequence(ctx context.Context, p setupPermissionProcess, exe string, request bool) (string, error) {
	fail := func() (string, error) { return "unavailable", fmt.Errorf("permission_setup_unavailable") }
	if ctx == nil || ctx.Err() != nil {
		return fail()
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	data, err := p.Probe(probeCtx, exe)
	cancel()
	if err != nil {
		return fail()
	}
	caps, err := nativeprotocol.DecodeCapabilities(data)
	if err != nil || !caps.Supports(1, "none") {
		return fail()
	}
	if request {
		if ctx.Err() != nil {
			return fail()
		}
		data, err = p.ProbeSetup(ctx, exe)
		if err != nil {
			return fail()
		}
		if _, err = nativeprotocol.DecodeSetupCapabilities(data); err != nil {
			return fail()
		}
	}
	id, err := setupPermissionID()
	if err != nil {
		return fail()
	}
	nonce, err := setupPermissionID()
	if err != nil {
		return fail()
	}
	if ctx.Err() != nil {
		return fail()
	}
	probeCtx, cancel = context.WithTimeout(ctx, time.Second)
	data, err = p.ProbePermission(probeCtx, exe, id, nonce)
	cancel()
	if err != nil {
		return fail()
	}
	status, err := nativeprotocol.DecodePermission(data, id, nonce)
	if err != nil {
		return fail()
	}
	if status.Permission != "undetermined" || !request {
		return status.Permission, nil
	}
	if ctx.Err() != nil {
		return fail()
	}
	data, err = p.RequestPermission(ctx, exe, id, nonce)
	if err != nil {
		return fail()
	}
	status, err = nativeprotocol.DecodePermission(data, id, nonce)
	if err != nil {
		return fail()
	}
	return status.Permission, nil
}
func setupPermissionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
