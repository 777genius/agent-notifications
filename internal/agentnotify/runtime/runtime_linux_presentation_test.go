//go:build linux

package runtime

import (
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/notifier"
)

// The Linux MCP composition must enable presentation without changing other
// delivery ports or the managed installation fence.
func TestProductionLinuxMCPPresentationFactory(t *testing.T) {
	o := options(t)
	m := notifier.ManagedInstallation{ControlRoot: o.ControlRoot, Expected: snapshot("A").Installation}
	got := defaultDeliveryFactory(m, o.SpoolRoot, o.BootClock)
	fenced, ok := got.(*sessionDelivery)
	if !ok || !reflect.DeepEqual(fenced.installation, m) {
		t.Fatalf("Linux MCP lost managed installation fence: %T", got)
	}
	port, ok := fenced.Delivery.(*notifier.FreedesktopDelivery)
	if !ok || !port.AgentNotifyPresentation || port.Clock != o.BootClock {
		t.Fatalf("Linux MCP presentation not enabled in production: %T", fenced.Delivery)
	}
}
