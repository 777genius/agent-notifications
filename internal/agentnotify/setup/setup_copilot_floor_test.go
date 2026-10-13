//go:build linux || darwin

package setup

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// RED on public 5d808: after a real Local policy commit, Apply rejects the
// supported floor with newer_installer_required before configuring rates.
func TestConfigureAfterCommittedLocalPolicyFloor(t *testing.T) {
	ctx := contextFor(t)
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"), Owner: "existing-installer", ConsumerID: "sibling", GlobalConfig: filepath.Join(base, "global.json"), Platform: "linux"}
	primary := filepath.Join(o.RuntimeRoot, "primary")
	l, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: o.ControlRoot, RuntimeRoot: o.RuntimeRoot, Owner: o.Owner, ConsumerID: o.ConsumerID, Consumer: installruntime.Consumer{Registration: "TEST-sibling"}, Files: []installruntime.File{{Path: primary, Data: []byte("inert TEST " + installruntime.LocalWriterProtocolMarker), Mode: 0700}}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, o.GlobalConfig, globalConfig, 0600)
	o.JournalClock = journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "TEST-floor", Seconds: 100, Available: true} })
	initial, err := Apply(ctx, o, Request{ExpectedGeneration: l.Generation, Enabled: ptr(true), Route: &Route{}})
	if err != nil || !initial.Enabled || initial.Namespace == "" {
		t.Fatalf("initial Linux none setup: %+v %v", initial, err)
	}
	l = snapshot(t, o).Installation.Ledger
	b := portable.Binding{Version: 1, Integration: portable.CopilotVSCode, InstallationID: "TEST-install", BindingID: "TEST-local", ScopeID: "TEST-profile", ComponentID: l.ID, Owner: l.Owner, ScopeRoot: base, DataRoot: base, ControlRoot: o.ControlRoot, GlobalConfig: filepath.Join(o.ControlRoot, "agent-notifications.json"), RuntimeRoot: o.RuntimeRoot, Primary: "primary"}
	key, consumer, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: o.ControlRoot, RuntimeRoot: o.RuntimeRoot, Owner: o.Owner, ConsumerID: key, Consumer: consumer, ExpectedGeneration: &l.Generation, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":false,"manual":{"enabled":false},"foreign":7}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if l.WriterFloor != installruntime.LocalPolicyWriterFloor {
		t.Fatalf("Local commit floor: %d", l.WriterFloor)
	}
	before := snapshot(t, o)
	assets := tree(t, o.RuntimeRoot)
	journalState := tree(t, filepath.Join(o.ControlRoot, "state"))
	global := string(read(t, o.GlobalConfig))
	result, err := Apply(ctx, o, Request{ExpectedGeneration: l.Generation, Rates: &Rates{Burst: ptr(3)}})
	if err != nil || result.Reason != "configured" || !result.Enabled || result.Generation != l.Generation+1 || result.Namespace != initial.Namespace {
		t.Fatalf("supported Local floor must configure already provisioned setup: %+v %v", result, err)
	}
	after := snapshot(t, o)
	if !reflect.DeepEqual(before.Installation.Ledger.Consumers, after.Installation.Ledger.Consumers) || !reflect.DeepEqual(before.Installation.Ledger.Files, after.Installation.Ledger.Files) || !reflect.DeepEqual(assets, tree(t, o.RuntimeRoot)) || global != string(read(t, o.GlobalConfig)) || string(before.Fields["route"]) != string(after.Fields["route"]) {
		t.Fatal("Configure changed consumers/assets/global config/Local consent")
	}
	var rates Rates
	if err := json.Unmarshal(after.Fields["rates"], &rates); err != nil || rates.Burst == nil || *rates.Burst != 3 {
		t.Fatalf("rates were not committed: %s %v", after.Fields["rates"], err)
	}
	if !reflect.DeepEqual(journalState, tree(t, filepath.Join(o.ControlRoot, "state"))) {
		t.Fatal("configure changed existing journal provisioning")
	}
	if after.Installation.Ledger.Native != nil {
		t.Fatal("configure provisioned native")
	}
	// Supported floor must not turn a hosted platform selector into Darwin
	// qualification. No native fixture is fabricated for this test.
	o.Platform = "darwin"
	unchanged := tree(t, base)
	_, err = Apply(ctx, o, Request{ExpectedGeneration: result.Generation, Rates: &Rates{Burst: ptr(4)}})
	wantReason(t, err, "qualified_runtime_required")
	if !reflect.DeepEqual(unchanged, tree(t, base)) {
		t.Fatal("missing Darwin native caused effects")
	}
	// A future ledger is an input refusal fixture, never a fabricated floor3.
	future := after.Installation.Ledger
	future.WriterFloor = installruntime.SupportedWriterFloor + 1
	raw, err := json.Marshal(future)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(o.ControlRoot, "ownership.json"), string(raw), 0600)
	o.Platform = "linux"
	unchanged = tree(t, base)
	_, err = Apply(ctx, o, Request{ExpectedGeneration: future.Generation, Rates: &Rates{Burst: ptr(4)}})
	if err == nil {
		t.Fatal("unsupported future floor accepted")
	}
	if !reflect.DeepEqual(unchanged, tree(t, base)) {
		t.Fatal("unsupported floor caused effects")
	}
}
