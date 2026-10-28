//go:build linux || darwin

package portablesetup

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestPiChoicesWithWizardReservation(t *testing.T) {
	b, ledger := bindingFixture(t)
	b.Integration = portable.Pi
	if _, err := (Service{}).CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, reservation, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ExpectedGeneration: snapshot.Ledger.Generation, Action: "install", Stage: "confirmed", Targets: []IntentTarget{{Client: "pi", InstallationID: b.InstallationID, BindingID: b.BindingID, Profile: b.ScopeRoot, Units: []string{"agent-notify"}}}})
	if err != nil {
		t.Fatal(err)
	}
	off, on := false, true
	if err := piauthority.CommitChoices(testCtx(t), b, piauthority.Choices{Desktop: &off, Webhook: &on}, reservation, nil); err != nil {
		t.Fatal(err)
	}
}
