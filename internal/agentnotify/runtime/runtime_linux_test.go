//go:build linux

package runtime

import (
	"context"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func TestLinuxProductionClocksShareBootIdentity(t *testing.T) {
	o := options(t)
	o.BootClock = nil
	o.JournalClock = nil
	b := backend(t, o)
	now := b.Clock().Now()
	if now.BootID == "" {
		t.Fatal("production linux boot clock unavailable")
	}
	sample := b.opts.JournalClock.Sample()
	if !sample.Available || sample.Boot != now.BootID {
		t.Fatalf("clocks diverged journal=%+v boot=%+v", sample, now)
	}
}

func TestLinuxProductionFactoryUsesFreedesktop(t *testing.T) {
	o := options(t)
	o.DeliveryFactory = nil
	b := backend(t, o)
	d, ok := b.opts.DeliveryFactory(notifier.ManagedInstallation{}, o.SpoolRoot, o.BootClock).(*notifier.FreedesktopDelivery)
	if !ok || d.Clock != o.BootClock {
		t.Fatal("linux factory still uses macos native delivery")
	}
}

func TestLinuxSessionEligibleWithoutNative(t *testing.T) {
	o := options(t)
	o.BootClock = nil
	snap := snapshot("/none")
	snap.Installation.Ledger.Native = nil
	o.ReadSnapshot = func(context.Context, string) (installruntime.PolicySnapshot, error) { return snap, nil }
	b := backend(t, o)
	s := b.Status(context.Background())
	if s.OfflineCapability != "eligible" || s.Configuration != "configured" {
		t.Fatal(s)
	}
}

func TestLinuxProductionNotifyNoneWithDefaultClocks(t *testing.T) {
	o := options(t)
	o.JournalClock = journal.DefaultClock()
	o.BootClock = nil
	initialize(t, o)
	var sent int
	o.DeliveryFactory = func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery {
		return fakeDelivery{send: func(notification.Request) string { sent++; return "submitted" }}
	}
	b := backend(t, o)
	now := b.Clock().Now()
	if now.BootID == "" {
		t.Fatal("production clock unavailable")
	}
	id := "linux-none"
	got := b.Notify(context.Background(), agentnotify.Payload{Title: "test", Body: "literal", Category: "info", RequestID: &id, Navigation: notification.None}, origin.Context{Provider: "codex", Namespace: "tests", SessionID: id, Provenance: origin.ClientMetadata, Locality: origin.Local, Interface: origin.Desktop}, notification.Deadline{BootID: now.BootID, NotAfter: now.NotAfter + 10})
	if got.Status != "submitted" || sent != 1 {
		t.Fatal(got, sent)
	}
}
