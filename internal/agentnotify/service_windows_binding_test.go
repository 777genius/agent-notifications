//go:build linux || darwin

package agentnotify

import (
	"context"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/notification"
)

// Breakage: the normal service rejects a Windows target, loses its binding in
// durable admission, or readiness's later policy mutation changes delivery.
// Actual journal persistence runs; the native effect port only captures input.
func TestServiceWindowsBindingAdmissionAndReplay(t *testing.T) {
	f := setup(t, journal.Limits{})
	binding := notification.WindowsBinding{SnapshotPath: `\\?\C:\TEST\generation.wne`, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	f.policy.Route.Platform = "windows"
	f.policy.Route.ApplicationPath = ""
	f.policy.Route.TeamID = ""
	f.policy.Route.Windows = binding
	nav := notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "selected_windows_generation", Reason: "configured_codex_desktop"}
	f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
		if r.Target.Windows != binding {
			t.Fatal("readiness lost binding", r.Target)
		}
		f.policy.Route.Windows.SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "installed_readiness_verified", Navigation: nav}
	})
	f.s.deps.Delivery = deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
		f.effects.Add(1)
		if r.Target.Windows != binding {
			t.Fatal("delivery mixed binding", r.Target)
		}
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted", Backend: "fixture", Navigation: nav}
	})
	p, o := payload("TEST-windows-value"), caller()
	p.Navigation = notification.Required
	first := f.notify(p, o)
	requireStatus(t, first, "submitted")
	source, session := o.Scope()
	stored, e := f.store.Lookup(testContext(t), journal.Key{Source: source, Session: session, Kind: journal.Explicit, Request: *p.RequestID}, digest(p))
	if e != nil || !stored.Found || stored.Record.Receipt.Decision.Target.Application != binding.SnapshotPath || stored.Record.Receipt.Decision.Target.Identity != binding.SHA256 {
		t.Fatal(stored, e)
	}
	again := f.notify(p, o)
	if !again.Replayed || again.TrackingID != first.TrackingID || f.effects.Load() != 1 {
		t.Fatal(again, f.effects.Load())
	}
}
