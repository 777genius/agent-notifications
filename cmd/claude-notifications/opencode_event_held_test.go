package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

type heldFixtureClock struct{}

func (heldFixtureClock) Now() (string, float64, error) {
	return "12345678-1234-1234-1234-123456789ABC", 100, nil
}

type heldFixtureLease struct{ release func() }

func (l heldFixtureLease) BundlePath() string     { return "/TEST/held.app" }
func (l heldFixtureLease) ExecutablePath() string { return "/TEST/held.app/Contents/MacOS/notifier" }
func (l heldFixtureLease) Release()               { l.release() }

type heldFixtureInstallation struct{ lease notifier.NativeLease }

func (i *heldFixtureInstallation) Acquire(context.Context) (notifier.NativeLease, error) {
	if i.lease == nil {
		return nil, errors.New("single_use")
	}
	lease := i.lease
	i.lease = nil
	return lease, nil
}

type heldFixtureProcess struct {
	entered chan struct{}
	settle  chan struct{}
}

func (p heldFixtureProcess) Probe(context.Context, string) ([]byte, error) {
	return []byte(`{"schemaVersion":1,"protocolVersions":[1],"actionKinds":["none"],"receiptSupport":true,"backend":"macos.usernotifications","explicitFeatureEnabledByDefault":false}`), nil
}
func (p heldFixtureProcess) ProbePermission(_ context.Context, _ string, id, nonce string) ([]byte, error) {
	return json.Marshal(map[string]any{"schemaVersion": 1, "correlationID": id, "nonce": nonce, "backend": "macos.usernotifications", "permission": "allowed"})
}
func (p heldFixtureProcess) Launch(context.Context, string, string, string) (bool, error) {
	close(p.entered)
	<-p.settle
	return true, nil
}

type heldFixtureSpool struct{ wire []byte }

func (s *heldFixtureSpool) Prepare(_ context.Context, _ notification.Request, encode func(string) ([]byte, error)) (notifier.NativeAttempt, error) {
	wire, err := encode("00000000-0000-4000-8000-000000000002")
	s.wire = wire
	return notifier.NativeAttempt{RequestPath: "/TEST/request", ReceiptPath: "/TEST/receipt", Nonce: "00000000-0000-4000-8000-000000000002"}, err
}
func (s *heldFixtureSpool) Receipt(notifier.NativeAttempt) ([]byte, error) {
	return nil, errors.New("unknown")
}
func (s *heldFixtureSpool) Expire(notifier.NativeAttempt) error { return nil }

// Uses the real StructuredDelivery boundary with a prior-held TEST filesystem
// fence. This proves adapter IO/reference ordering, not native Mac qualification.
func TestOpenCodeHeldNativeBoundaryDoesNotReenterOrReleaseActiveIO(t *testing.T) {
	root := t.TempDir()
	lock := filepath.Join(root, "held.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := installruntime.Lock(ctx, lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	held := &heldFixtureInstallation{lease: heldFixtureLease{release: release}}
	boundary := newOpenCodeHeldDesktop(held, filepath.Join(root, "spool"))
	port := boundary.port.(*notifier.StructuredDelivery)
	if port.Installation != held {
		t.Fatal("constructor replaced held admission reference")
	}
	process := heldFixtureProcess{entered: make(chan struct{}), settle: make(chan struct{})}
	spool := &heldFixtureSpool{}
	port.Clock = heldFixtureClock{}
	port.Process = process
	port.Spool = spool
	boundary.clock = port.Clock
	r := notification.Request{Content: notification.Content{Title: "OpenCode", Body: "Task completed", Category: "info"}, CorrelationID: "00000000-0000-4000-8000-000000000001",
		Deadline: notification.Deadline{BootID: strings.ToLower("12345678-1234-1234-1234-123456789ABC"), NotAfter: 109.75}, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}, Silent: true, Navigation: notification.None}
	op, abort := context.WithCancel(ctx)
	defer abort()
	done := make(chan notification.Receipt, 1)
	go func() { done <- boundary.Deliver(op, r) }()
	select {
	case <-process.entered:
	case <-ctx.Done():
		t.Fatal("held adapter recursively acquired its prior fence")
	}
	abort()
	short, stop := context.WithTimeout(ctx, 40*time.Millisecond)
	next, err := installruntime.LockExisting(short, lock)
	stop()
	if err == nil {
		next()
		t.Fatal("native extra reference released before actual IO")
	}
	close(process.settle)
	select {
	case got := <-done:
		if got.Status != "unknown" {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("settled provider not joined")
	}
	next, err = installruntime.LockExisting(ctx, lock)
	if err != nil {
		t.Fatal("native reference not released after IO", err)
	}
	next()
	var request struct {
		Boot     string  `json:"bootID"`
		Deadline float64 `json:"notAfter"`
		Silent   bool    `json:"silent"`
		Action   string  `json:"action"`
	}
	if json.Unmarshal(spool.wire, &request) != nil || request.Deadline != 109.75 || request.Boot != "12345678-1234-1234-1234-123456789ABC" || !request.Silent || request.Action != "none" {
		t.Fatal("held transport altered original deadline/boot/privacy", string(spool.wire))
	}
}
