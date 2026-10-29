package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// Context sends keep their legacy presentation/focus arguments inside the
// existing private native request transport. They never ask for OS permission.
// The native app checks this owner's birth identity and original deadline even
// when LaunchServices starts it after the launcher has already been cancelled.
type contextNativeProcess struct {
	PID               int    `json:"pid"`
	StartSeconds      uint64 `json:"startSeconds"`
	StartMicroseconds uint64 `json:"startMicroseconds"`
}

func (p contextNativeProcess) valid() bool {
	return p.PID > 0 && p.StartSeconds > 0 && p.StartMicroseconds < 1_000_000
}

type contextNativeRequest struct {
	SchemaVersion int                  `json:"schemaVersion"`
	CorrelationID string               `json:"correlationID"`
	Nonce         string               `json:"nonce"`
	BootID        string               `json:"bootID"`
	NotAfter      float64              `json:"notAfter"`
	Owner         contextNativeProcess `json:"owner"`
	Arguments     []string             `json:"arguments"`
}

type contextNativeStarted struct {
	SchemaVersion int                  `json:"schemaVersion"`
	CorrelationID string               `json:"correlationID"`
	Nonce         string               `json:"nonce"`
	Process       contextNativeProcess `json:"process"`
}

type contextNativeOperations struct {
	owner  func() (contextNativeProcess, error)
	stop   func(contextNativeProcess, string) error
	probe  func(context.Context) ([]byte, error)
	launch func(context.Context, string, string) error
}

func decodeContextNative(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid_context_native_envelope")
	}
	return nil
}

func runContextNativeApp(ctx context.Context, clock BootClock, executable string, args []string, operations contextNativeOperations) error {
	if ctx == nil || clock == nil || operations.owner == nil || operations.stop == nil || operations.probe == nil || operations.launch == nil {
		return errors.New("invalid_context_native_operation")
	}
	end, ok := ctx.Deadline()
	remaining := time.Until(end)
	if !ok || remaining <= 0 || remaining > 15*time.Second || ctx.Err() != nil {
		return errors.New("invalid_context_native_deadline")
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	data, err := operations.probe(probeCtx)
	cancel()
	var capabilities struct {
		SchemaVersion int   `json:"schemaVersion"`
		Versions      []int `json:"contextNotificationVersions"`
	}
	if err != nil || decodeContextNative(data, &capabilities) != nil || capabilities.SchemaVersion != 1 || len(capabilities.Versions) != 1 || capabilities.Versions[0] != 1 {
		return errors.New("context_native_helper_required")
	}
	owner, err := operations.owner()
	if err != nil || !owner.valid() {
		return errors.New("context_native_owner_unavailable")
	}
	boot, now, err := clock.Now()
	remaining = time.Until(end)
	if err != nil || boot == "" || now < 0 || remaining <= 0 || ctx.Err() != nil {
		return errors.New("context_native_clock_unavailable")
	}
	id, err := uuidString()
	if err != nil {
		return err
	}
	nonce, err := uuidString()
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "agent-notifications-context-")
	if err != nil {
		return err
	}
	defer func(root string) { _ = os.RemoveAll(root) }(root)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	directory := filepath.Join(root, id)
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	request, receipt := filepath.Join(directory, nonce+".request"), filepath.Join(directory, nonce+".receipt")
	body, err := json.Marshal(contextNativeRequest{1, id, nonce, boot, now + remaining.Seconds(), owner, args})
	if err != nil || len(body) > nativeprotocol.MaxEnvelopeBytes {
		return errors.New("invalid_context_native_request")
	}
	if err := writePrivate(request, body); err != nil {
		return err
	}
	// Revoke before inspecting the started receipt. A native reader publishes
	// its exact process identity before attempting an effect; if publication
	// races cancellation, its missing request/claim check denies the effect.
	revoke := func() {
		_ = os.Remove(request)
		_ = os.Remove(filepath.Join(directory, nonce+".claim"))
	}
	// A LaunchServices app is independent of open. Revoke immediately on
	// cancellation, while the launcher is still being terminated and joined.
	revoked := make(chan struct{})
	stopRevocation := context.AfterFunc(ctx, func() { revoke(); close(revoked) })
	defer func() {
		if !stopRevocation() {
			<-revoked
		}
	}()
	stop := func() error {
		revoke()
		data, err := readPrivate(filepath.Join(directory, nonce+".started"), 1024)
		if os.IsNotExist(err) {
			return nil
		}
		var started contextNativeStarted
		if err != nil || decodeContextNative(data, &started) != nil || started.SchemaVersion != 1 || started.CorrelationID != id || started.Nonce != nonce || !started.Process.valid() || started.Process.PID == owner.PID {
			return errors.New("invalid_context_native_process")
		}
		return operations.stop(started.Process, executable)
	}
	if ctx.Err() != nil {
		revoke()
		return ctx.Err()
	}
	// The launcher is joined once; the app has a separate, exact process owner.
	// Neither path retries an uncertain native handoff.
	launchErr := operations.launch(ctx, request, receipt)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		// A correlated terminal receipt releases observation ownership. A click
		// already accepted by the app drains under its existing callback owner;
		// waiting for open -W would instead kill that user action at this budget.
		data, err = readPrivate(receipt, nativeprotocol.MaxEnvelopeBytes)
		if err == nil {
			result, decodeErr := nativeprotocol.DecodeReceipt(data, id, nonce)
			if decodeErr != nil {
				return errors.Join(decodeErr, stop())
			}
			if result.Status == "submitted" {
				return nil
			}
			if result.Reason == "permission_denied" || result.Reason == "activation_required" || result.Reason == "expired" {
				return ErrDesktopSuppressed
			}
			return errors.New("context_native_handoff_unconfirmed")
		}
		// OwnedRequest publishes via an exclusive hardlink and then removes its
		// temporary name. readPrivate deliberately refuses the brief nlink=2
		// interval; keep that check and wait for the sealed receipt, within the
		// same deadline. An unreadable/unsafe receipt can never admit a retry.
		if launchErr != nil || ctx.Err() != nil {
			return errors.Join(launchErr, ctx.Err(), stop())
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
