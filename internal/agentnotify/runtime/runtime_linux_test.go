//go:build linux

package runtime

import (
	"testing"

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
