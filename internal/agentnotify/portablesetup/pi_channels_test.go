//go:build linux || darwin

package portablesetup

import (
	"encoding/json"
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
	// An unrelated product write preserves Pi, then a Pi decision preserves
	// that unrelated leaf through the same common field writer.
	s, err := installruntime.ReadPolicySnapshot(testCtx(t), b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(testCtx(t), installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: key, Consumer: consumer, RefreshOnly: true, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage, Reservation: reservation, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":false}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := piauthority.CommitChoices(testCtx(t), b, piauthority.Choices{}, reservation, nil); err != nil {
		t.Fatal(err)
	}
	s, err = installruntime.ReadPolicySnapshot(testCtx(t), b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	var route map[string]map[string]bool
	if json.Unmarshal(s.Fields["route"], &route) != nil || route[piauthority.ChannelKey]["desktop"] || !route[piauthority.ChannelKey]["webhook"] || !route["geminiNotifications"]["desktop"] || route["geminiNotifications"]["webhook"] {
		t.Fatal("cross-product channel preservation failed")
	}
	if err := piauthority.CommitChoices(testCtx(t), b, piauthority.Choices{Desktop: &on, Webhook: &off}, reservation, nil); err != nil {
		t.Fatal(err)
	}
	s, err = installruntime.ReadPolicySnapshot(testCtx(t), b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(s.Fields["route"], &route) != nil || !route[piauthority.ChannelKey]["desktop"] || route[piauthority.ChannelKey]["webhook"] || !route["geminiNotifications"]["desktop"] {
		t.Fatal("Pi write dropped another product decision")
	}
}
