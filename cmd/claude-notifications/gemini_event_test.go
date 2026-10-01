//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/geminiinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
)

type heldGeminiDesktop struct{ entered, finish chan struct{} }

func (p heldGeminiDesktop) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	close(p.entered)
	select {
	case <-p.finish:
	case <-ctx.Done():
	}
	return notification.Receipt{Status: "submitted", CorrelationID: r.CorrelationID}
}

// A recursive policy lock would prevent entry; an omitted lease would let remove
// revoke while the effect still holds its bounded request. Both break handoff.
func TestGeminiDesktopOwnsOneRetainedLease(t *testing.T) {
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, runtimeRoot := filepath.Join(base, "control"), filepath.Join(base, "runtime")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if runGeminiSetup([]string{"install", "--control-root", root, "--runtime-root", runtimeRoot, "--config-root", filepath.Join(base, "TEST-profile", ".gemini"), "--binary", executable, "--desktop"}, &output) != 0 {
		t.Fatal(output.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Installation.Ledger.Consumers["gemini-notifications"]
	var receipt struct {
		Binding string `json:"binding"`
	}
	data, err := os.ReadFile(c.Registration)
	if err != nil || json.Unmarshal(data, &receipt) != nil {
		t.Fatal("unreadable owned receipt", err)
	}
	gate := geminiGate{args: geminiEventArgs{ControlRoot: root, Binding: receipt.Binding}, executable: c.Commands[0], expected: s}
	port := heldGeminiDesktop{entered: make(chan struct{}), finish: make(chan struct{})}
	delivered := make(chan notification.Receipt, 1)
	go func() {
		delivered <- (geminiLeasedDesktop{gate: gate, port: port}).Deliver(ctx, notification.Request{CorrelationID: "fixture"})
	}()
	select {
	case <-port.entered:
	case <-ctx.Done():
		t.Fatal("effect failed to enter; possible recursive policy lease")
	}
	revoked := make(chan error, 1)
	go func() { revoked <- geminiinstall.RevokeChannels(ctx, root, runtimeRoot) }()
	select {
	case err := <-revoked:
		t.Fatalf("revocation passed active effect lease: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	close(port.finish)
	if got := <-delivered; got.Status != "submitted" {
		t.Fatal(got)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	// The old loaded CLI's snapshot cannot hand off after remove's new generation.
	if _, err := gate.acquire(ctx, "desktop"); err == nil {
		t.Fatal("stale loaded CLI admitted")
	}
	if gate.Recheck(ctx, gate.binding(), "desktop") {
		t.Fatal("read-only recheck ignored revocation")
	}
}

func TestGeminiCompositionRejectsQuietlyBeforeRegistration(t *testing.T) {
	var output bytes.Buffer
	code := runGeminiEvent([]string{"--event", "AfterAgent", "--control-root", filepath.Join(t.TempDir(), "missing"), "--binding", "TEST"}, io.NopCloser(bytes.NewBufferString(`{"session_id":"fixture","hook_event_name":"Notification","notification_type":"ToolPermission"}`)), &output)
	if code != 0 || output.String() != "{}\n" {
		t.Fatalf("neutral response: %d %q", code, output.String())
	}
}
