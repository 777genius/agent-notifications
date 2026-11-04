//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/testenv"
)

// A real native-list observation changes a resumed uninstall from unattested to
// attested. Corrupt its owned intent during that observation: the durable patch
// must fail before the wizard enters removal, retaining the installed facts.
func TestUninstallStopsWhenObservedAttestationCannotBePersisted(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, global, _, generation := managedRuntime(t)
	root := filepath.Dir(control)
	probe := buildProbe(t)
	pkg, profile, scope := filepath.Join(root, "package"), filepath.Join(root, "codex"), filepath.Join(root, "scope")
	writePackage(t, pkg, probe)
	for _, path := range []string{profile, scope} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	uapRoot := filepath.Join(root, "uap")
	mat, err := portablesetup.NewMaterializer(portablesetup.UAPRoots{StateFile: filepath.Join(uapRoot, "state", "state-v2.json"), LockFile: filepath.Join(uapRoot, "state", "mutation.lock"), OperationsDir: filepath.Join(uapRoot, "state", "operations"), PluginDataBase: filepath.Join(uapRoot, "plugin-data"), ManagedRoot: filepath.Join(uapRoot, "managed"), HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	id := portablesetup.Identity{InstallationID: "00000000-0000-4000-8000-000000000398", ComponentID: snap.Ledger.ID, Owner: "existing-installer", ScopeRoot: scope, ControlRoot: control, GlobalConfig: global, RuntimeRoot: runtimeRoot, Primary: "bin/claude-notifications"}
	binding, err := mat.Install(ctx, portablesetup.MaterializeRequest{Identity: id, Integration: portable.Codex, ExpectedGeneration: generation, PackageRoot: pkg, ClientConfigRoot: profile, ClientExecutable: probe, OperationID: "TEST-attestation-install"})
	if err != nil {
		t.Fatal("install real portable fixture", err)
	}
	req := Request{Action: ActionUninstall, Agents: []string{"codex"}, Yes: true, Hooks: boolPtr(false), AgentNotify: boolPtr(true), ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, ScopeRoot: scope, Primary: id.Primary, InstallationID: id.InstallationID, CodexHome: profile, ClientExecutable: probe, Helper: probe}
	held, err := Run(ctx, req)
	if err == nil || held.Reason != "external_uninstall_required" || held.reservation == nil {
		t.Fatalf("create unattested pending uninstall: %+v %v", held, err)
	}
	intentPath := portablesetup.IntentPath(control)
	intentBefore, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := portablesetup.ReadIntent(control)
	if err != nil || intent.ExternalUninstalled {
		t.Fatalf("fixture already attested: %+v %v", intent, err)
	}
	before, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(mat.Roots.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	ledgerBefore, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	fault := filepath.Join(root, "TEST-fail-attestation-patch")
	if err := os.WriteFile(fault, []byte("enabled"), 0600); err != nil {
		t.Fatal(err)
	}
	observed := filepath.Join(root, "TEST-native-list-observed")
	req.ClientExecutable = testenv.GoFixture(t, fmt.Sprintf(`package main
import ("fmt"; "os"; "strings")
func main() {
 if strings.Join(os.Args[1:], " ") != "plugin list --json" { os.Exit(1) }
 if _, err := os.Stat(%q); err == nil {
  if err := os.WriteFile(%q, []byte("{"), 0600); err != nil { panic(err) }
 }
 if err := os.WriteFile(%q, []byte("listed absent"), 0600); err != nil { panic(err) }
 fmt.Println("{\"installed\":[]}")
}
`, fault, intentPath, observed))
	var phases []string
	req.Progress = func(phase string) { phases = append(phases, phase) }
	got, err := Run(ctx, req)
	var snapshotErr *installruntime.SnapshotError
	if err == nil || !errors.As(err, &snapshotErr) || snapshotErr.Code != "managed_file_changed" || snapshotErr.Path != intentPath || got.Outcome != "incomplete" || got.Reason != err.Error() || slices.Contains(phases, "agent-notify") || slices.Contains(phases, "hooks") {
		t.Fatalf("failed durable attestation reached removal or lost cause: %+v err=%v phases=%v", got, err, phases)
	}
	if got.reservation == nil || *got.reservation != *held.reservation {
		t.Fatal("failed attestation lost pending reservation")
	}
	if _, err := os.Stat(observed); err != nil {
		t.Fatal("native absence was not observed", err)
	}
	if ledger, err := os.ReadFile(filepath.Join(control, "ownership.json")); err != nil || !bytes.Equal(ledger, ledgerBefore) {
		t.Fatalf("failed patch changed runtime ownership: %v", err)
	}
	if state, err := os.ReadFile(mat.Roots.StateFile); err != nil || !bytes.Equal(state, stateBefore) {
		t.Fatalf("failed patch changed UAP clients: %v", err)
	}
	if exact, err := portable.ExactLocator(binding); err != nil || !exact {
		t.Fatalf("failed patch revoked the locator: %t %v", exact, err)
	}
	// Restore exactly the pre-fault owned bytes. A successful durable patch
	// must advance the existing reservation and reach the removal phase. Cancel
	// there to bound this test to attestation, before native uninstall execution.
	if err := os.WriteFile(intentPath, intentBefore, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fault); err != nil {
		t.Fatal(err)
	}
	phases = nil
	continuedCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req.Progress = func(phase string) {
		phases = append(phases, phase)
		if phase == "agent-notify" {
			cancel()
		}
	}
	continued, err := Run(continuedCtx, req)
	if !errors.Is(err, context.Canceled) || continued.Outcome != "incomplete" || !slices.Contains(phases, "agent-notify") {
		t.Fatalf("successful persisted attestation did not continue: %+v %v phases=%v", continued, err, phases)
	}
	intent, err = portablesetup.ReadIntent(control)
	if err != nil || !intent.ExternalUninstalled {
		t.Fatalf("successful continuation lost durable attestation: %+v %v", intent, err)
	}
	after, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || after.Ledger.PendingMutation == nil || *after.Ledger.PendingMutation != *before.Ledger.PendingMutation || !portable.ExactCommittedBinding(after.Ledger, binding) || after.Ledger.Generation <= before.Ledger.Generation {
		t.Fatalf("successful patch lost reservation or changed installed ownership: %+v %v", after.Ledger, err)
	}
}
