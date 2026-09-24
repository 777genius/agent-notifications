//go:build linux || darwin

package portablesetup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func legacyFixture(t *testing.T) (portable.Binding, installruntime.Ledger) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := portable.Binding{
		Version: 1, Integration: portable.Codex, InstallationID: "uap-install", BindingID: "codex-binding",
		ScopeID: "user", Owner: "existing-installer", ScopeRoot: filepath.Join(root, "scope"),
		DataRoot: filepath.Join(root, "data"), ControlRoot: filepath.Join(root, "control"),
		RuntimeRoot: filepath.Join(root, "runtime"), Primary: "primary",
	}
	b.GlobalConfig = filepath.Join(b.RuntimeRoot, "global", "config.json")
	for _, dir := range []string{b.ScopeRoot, b.DataRoot, filepath.Dir(b.GlobalConfig)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := installruntime.Commit(testCtx(t), installruntime.Request{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner,
		ConsumerID: "existing", Files: []installruntime.File{{
			Path: filepath.Join(b.RuntimeRoot, filepath.FromSlash(portable.PlatformPrimary())),
			Data: []byte(installruntime.WriterProtocolMarker), Mode: 0700,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b.ComponentID = ledger.ID
	return b, ledger
}

func commitLegacy(t *testing.T, b portable.Binding, generation uint64) uint64 {
	t.Helper()
	if _, err := (Service{}).CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: generation}); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	return snap.Ledger.Generation
}

func migrationReservation(t *testing.T, old, replacement portable.Binding) (uint64, *installruntime.PendingMutation) {
	return migrationReservationWithTarget(t, old, replacement, nil)
}

func migrationReservationWithTarget(t *testing.T, old, replacement portable.Binding, mutate func(*IntentTarget)) (uint64, *installruntime.PendingMutation) {
	t.Helper()
	oldKey, _, _, err := old.Registration()
	if err != nil {
		t.Fatal(err)
	}
	newKey, _, _, err := replacement.Registration()
	if err != nil {
		t.Fatal(err)
	}
	target := IntentTarget{
		Client: string(old.Integration), InstallationID: old.InstallationID, BindingID: old.BindingID,
		DataReceiptID: "receipt", OldConsumerKey: oldKey, NewConsumerKey: newKey,
		OldBinding: &old, NewBinding: &replacement,
	}
	if mutate != nil {
		mutate(&target)
	}
	ledger, res, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{
		ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner,
		Action: "update", Primary: replacement.Primary,
		Targets: []IntentTarget{target},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Generation, res
}

func TestReplaceCommittedBindingRejectsUnreservedOrMismatchedIntent(t *testing.T) {
	for _, scenario := range []string{"unreserved", "wrong-key", "wrong-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			old, ledger := legacyFixture(t)
			_ = commitLegacy(t, old, ledger.Generation)
			replacement := old
			replacement.Primary = portable.PlatformPrimary()
			var mutate func(*IntentTarget)
			switch scenario {
			case "wrong-key":
				mutate = func(target *IntentTarget) { target.NewConsumerKey = "portable:foreign" }
			case "wrong-receipt":
				mutate = func(target *IntentTarget) { target.DataReceiptID = "" }
			}
			gen, res := migrationReservationWithTarget(t, old, replacement, mutate)
			gen = commitReplacement(t, replacement, gen, res)
			if scenario == "unreserved" {
				res = nil
			}
			if err := (Service{}).ReplaceCommittedBinding(testCtx(t), old, Request{Binding: replacement, ExpectedGeneration: gen, Reservation: res}); err == nil {
				t.Fatal("replacement accepted without exact reserved migration intent")
			}
			snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			if !portable.ExactCommittedBinding(snap.Ledger, old) {
				t.Fatal("old consumer changed despite refused intent")
			}
		})
	}
}

func commitReplacement(t *testing.T, b portable.Binding, generation uint64, res *installruntime.PendingMutation) uint64 {
	t.Helper()
	if _, err := (Service{}).CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: generation, Reservation: res}); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	return snap.Ledger.Generation
}

func TestHistoricalBindingResolutionPreservesBothAgentsAndOverride(t *testing.T) {
	old, ledger := legacyFixture(t)
	target := filepath.Join(old.ScopeRoot, "client-target")
	old.BindingID = domain.ComputeClientBindingID(old.InstallationID, string(old.Integration), old.ScopeID, target)
	gen := commitLegacy(t, old, ledger.Generation)
	sibling := old
	sibling.Integration = portable.Claude
	sibling.BindingID = domain.ComputeClientBindingID(sibling.InstallationID, string(sibling.Integration), sibling.ScopeID, target)
	_ = commitLegacy(t, sibling, gen)
	if err := os.RemoveAll(filepath.Dir(old.GlobalConfig)); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, historical := range []portable.Binding{old, sibling} {
		id := Identity{
			InstallationID: historical.InstallationID, ComponentID: historical.ComponentID, Owner: historical.Owner,
			ScopeRoot: historical.ScopeRoot, ControlRoot: historical.ControlRoot, RuntimeRoot: historical.RuntimeRoot,
			GlobalConfig: filepath.Join(filepath.Dir(historical.RuntimeRoot), "explicit", "config.json"),
			Primary:      portable.PlatformPrimary(),
		}
		client := domain.ClientBinding{ClientBindingID: historical.BindingID, ClientID: string(historical.Integration), Scope: historical.ScopeID, TargetLocator: target, DataReceiptID: "receipt"}
		receipt := domain.DataReceipt{DataReceiptID: "receipt", Scope: historical.ScopeID, Locator: historical.DataRoot}
		got, err := resolveStoredBinding(id, historical.Integration, client, receipt, snap.Ledger)
		if err != nil || got != historical {
			t.Fatalf("%s historical bytes lost: %+v %v", historical.Integration, got, err)
		}
		client.ClientBindingID = "foreign-target"
		if _, err := resolveStoredBinding(id, historical.Integration, client, receipt, snap.Ledger); !errors.Is(err, ErrPreflight) {
			t.Fatalf("foreign UAP target accepted: %v", err)
		}
	}
}

func TestUninstallRetryRejectsConsumerDifferentFromFrozenIntent(t *testing.T) {
	old, ledger := legacyFixture(t)
	targetPath := filepath.Join(old.ScopeRoot, "client-target")
	old.BindingID = domain.ComputeClientBindingID(old.InstallationID, string(old.Integration), old.ScopeID, targetPath)
	gen := commitLegacy(t, old, ledger.Generation)
	oldKey, _, _, err := old.Registration()
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(filepath.Dir(old.ControlRoot), "profile")
	confirmed, reservation, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{
		ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner,
		Action: "uninstall", Targets: []IntentTarget{{
			Client: string(old.Integration), InstallationID: old.InstallationID,
			BindingID: old.BindingID, DataReceiptID: "receipt", Profile: profile,
			OldConsumerKey: oldKey, OldBinding: &old,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	gen = confirmed.Generation
	if _, err := installruntime.Commit(testCtx(t), installruntime.Request{
		ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner,
		ConsumerID: oldKey, RemoveConsumer: true, ExpectedGeneration: &gen, Reservation: reservation,
	}); err != nil {
		t.Fatal(err)
	}
	replacement := old
	replacement.GlobalConfig = filepath.Join(filepath.Dir(old.ControlRoot), "different", "config.json")
	replacementKey, consumer, _, err := replacement.Registration()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	gen = snap.Ledger.Generation
	if _, err := installruntime.Commit(testCtx(t), installruntime.Request{
		ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner,
		ConsumerID: replacementKey, Consumer: consumer, ExpectedGeneration: &gen, Reservation: reservation,
	}); err != nil {
		t.Fatal(err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{InstallationID: old.InstallationID, ComponentID: old.ComponentID, Owner: old.Owner,
		ScopeRoot: old.ScopeRoot, ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot,
		GlobalConfig: old.GlobalConfig, Primary: old.Primary}
	client := domain.ClientBinding{ClientBindingID: old.BindingID, ClientID: string(old.Integration),
		Scope: old.ScopeID, TargetLocator: targetPath, DataReceiptID: "receipt"}
	receipt := domain.DataReceipt{DataReceiptID: "receipt", Scope: old.ScopeID, Locator: old.DataRoot}
	if _, err := resolveRemovableBinding(id, old.Integration, client, receipt, profile, snap.Ledger); !errors.Is(err, ErrPreflight) {
		t.Fatalf("changed consumer bypassed frozen uninstall: %v", err)
	}
}

func TestRevokeHistoricalBindingWithoutOldConfigParentOrLogicalPrimary(t *testing.T) {
	old, ledger := legacyFixture(t)
	gen := commitLegacy(t, old, ledger.Generation)
	sibling := old
	sibling.Integration, sibling.BindingID = portable.Claude, "claude-binding"
	_ = commitLegacy(t, sibling, gen)
	if err := os.RemoveAll(filepath.Dir(old.GlobalConfig)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(old.RuntimeRoot, "primary")); !os.IsNotExist(err) {
		t.Fatalf("historical logical primary unexpectedly exists: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	svc := Service{}
	if err := svc.RevokeBinding(testCtx(t), Request{Binding: old, ExpectedGeneration: snap.Ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeBinding(testCtx(t), Request{Binding: old, ExpectedGeneration: snap.Ledger.Generation}); err != nil {
		t.Fatalf("retry after consumer and locator removal: %v", err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !portable.ExactCommittedBinding(snap.Ledger, sibling) {
		t.Fatal("sibling consumer was lost")
	}
	if present, err := portable.ExactLocator(sibling); err != nil || !present {
		t.Fatalf("sibling locator was lost: %v %v", present, err)
	}
}

func TestRevokeMissingManagedBinaryRetainsKernelInvariant(t *testing.T) {
	old, ledger := legacyFixture(t)
	gen := commitLegacy(t, old, ledger.Generation)
	managed := filepath.Join(old.RuntimeRoot, filepath.FromSlash(portable.PlatformPrimary()))
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	if err := (Service{}).RevokeBinding(testCtx(t), Request{Binding: old, ExpectedGeneration: gen}); err == nil {
		t.Fatal("kernel accepted a drifted managed binary")
	}
	if _, err := installruntime.ReadInstalledSnapshot(old.ControlRoot); err == nil {
		t.Fatal("drifted managed binary passed snapshot validation")
	}
	if present, err := portable.ExactLocator(old); err != nil || !present {
		t.Fatalf("locator changed despite refused revoke: %v %v", present, err)
	}
}

func TestReplaceCommittedBindingRequiresNewAndPreservesSiblingOnRetry(t *testing.T) {
	old, ledger := legacyFixture(t)
	gen := commitLegacy(t, old, ledger.Generation)
	sibling := old
	sibling.Integration, sibling.BindingID = portable.Claude, "claude-binding"
	commitLegacy(t, sibling, gen)
	replacement := old
	replacement.GlobalConfig = filepath.Join(filepath.Dir(old.RuntimeRoot), "explicit-config", "config.json")
	replacement.Primary = portable.PlatformPrimary()
	if err := os.MkdirAll(filepath.Dir(replacement.GlobalConfig), 0700); err != nil {
		t.Fatal(err)
	}
	svc := Service{}
	gen, res := migrationReservation(t, old, replacement)
	if err := svc.ReplaceCommittedBinding(testCtx(t), old, Request{Binding: replacement, ExpectedGeneration: gen, Reservation: res}); !errors.Is(err, ErrPreflight) {
		t.Fatalf("uncommitted replacement accepted: %v", err)
	}
	gen = commitReplacement(t, replacement, gen, res)
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := portable.ResolveCommittedBinding(snap.Ledger, replacement); !errors.Is(err, portable.ErrInvalid) {
		t.Fatalf("two committed variants were not treated as ambiguous: %v", err)
	}
	if err := os.RemoveAll(filepath.Dir(old.GlobalConfig)); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReplaceCommittedBinding(testCtx(t), old, Request{Binding: replacement, ExpectedGeneration: gen, Reservation: res}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReplaceCommittedBinding(testCtx(t), old, Request{Binding: replacement, ExpectedGeneration: gen, Reservation: res}); err != nil {
		t.Fatalf("replacement retry failed: %v", err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !portable.ExactCommittedBinding(snap.Ledger, replacement) || !portable.ExactCommittedBinding(snap.Ledger, sibling) {
		t.Fatal("replacement or sibling consumer missing")
	}
	if present, err := portable.ExactLocator(old); err != nil || present {
		t.Fatalf("old locator remains: %v %v", present, err)
	}
}

func TestReplaceCommittedBindingResumesAfterConsumerRemoval(t *testing.T) {
	old, ledger := legacyFixture(t)
	commitLegacy(t, old, ledger.Generation)
	replacement := old
	replacement.Primary = portable.PlatformPrimary()
	gen, res := migrationReservation(t, old, replacement)
	gen = commitReplacement(t, replacement, gen, res)
	oldKey, _, _, err := old.Registration()
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(testCtx(t), installruntime.Request{
		ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner,
		ConsumerID: oldKey, RemoveConsumer: true, ExpectedGeneration: &gen, Reservation: res,
	})
	if err != nil {
		t.Fatal(err)
	}
	if present, err := portable.ExactLocator(old); err != nil || !present {
		t.Fatalf("test did not retain old locator: %v %v", present, err)
	}
	name, err := old.Filename()
	if err != nil {
		t.Fatal(err)
	}
	locator := filepath.Join(old.DataRoot, name)
	if err := os.WriteFile(locator, []byte(`{"foreign":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (Service{}).ReplaceCommittedBinding(testCtx(t), old, Request{Binding: replacement, ExpectedGeneration: gen, Reservation: res}); err == nil {
		t.Fatal("retry removed a foreign old locator")
	}
	_, _, raw, err := old.Registration()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(locator, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := (Service{}).ReplaceCommittedBinding(testCtx(t), old, Request{Binding: replacement, ExpectedGeneration: gen, Reservation: res}); err != nil {
		t.Fatalf("recovery after consumer removal failed: %v", err)
	}
	if present, err := portable.ExactLocator(old); err != nil || present {
		t.Fatalf("old locator survived recovery: %v %v", present, err)
	}
}

func TestHistoricalResolverRejectsMalformedConsumerAndReceipt(t *testing.T) {
	old, ledger := legacyFixture(t)
	old.BindingID = domain.ComputeClientBindingID(old.InstallationID, string(old.Integration), old.ScopeID, old.ScopeRoot)
	_ = commitLegacy(t, old, ledger.Generation)
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	bad := snap.Ledger
	bad.Consumers = make(map[string]installruntime.Consumer, len(snap.Ledger.Consumers))
	for key, value := range snap.Ledger.Consumers {
		bad.Consumers[key] = value
	}
	key, _, _, err := old.Registration()
	if err != nil {
		t.Fatal(err)
	}
	consumer := bad.Consumers[key]
	consumer.Registration += " "
	bad.Consumers[key] = consumer
	if _, _, err := portable.ResolveCommittedBinding(bad, old); !errors.Is(err, portable.ErrInvalid) {
		t.Fatalf("noncanonical registration bytes accepted: %v", err)
	}
	id := Identity{InstallationID: old.InstallationID, ComponentID: old.ComponentID, Owner: old.Owner, ScopeRoot: old.ScopeRoot, ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, GlobalConfig: old.GlobalConfig, Primary: old.Primary}
	client := domain.ClientBinding{ClientBindingID: old.BindingID, ClientID: string(old.Integration), Scope: old.ScopeID, TargetLocator: old.ScopeRoot, DataReceiptID: "receipt"}
	receipt := domain.DataReceipt{DataReceiptID: "foreign", Scope: old.ScopeID, Locator: old.DataRoot}
	if _, err := resolveStoredBinding(id, old.Integration, client, receipt, snap.Ledger); !errors.Is(err, ErrPreflight) {
		t.Fatalf("foreign receipt accepted: %v", err)
	}
	receipt.DataReceiptID = "receipt"
	client.TargetLocator = filepath.Join(old.ScopeRoot, "other")
	if _, err := resolveStoredBinding(id, old.Integration, client, receipt, snap.Ledger); !errors.Is(err, ErrPreflight) {
		t.Fatalf("foreign target accepted: %v", err)
	}
}

func TestMaterializerRemoveGroupRetriesAfterBothRevokes(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		legacyIntent bool
		emptyReceipt bool
		revokes      int
	}{
		{name: "frozen-both-revoked", revokes: 2},
		{name: "legacy-both-revoked", legacyIntent: true, revokes: 2},
		{name: "legacy-one-revoked", legacyIntent: true, revokes: 1},
		{name: "legacy-empty-receipt", legacyIntent: true, emptyReceipt: true, revokes: 2},
		{name: "legacy-empty-receipt-one-revoked", legacyIntent: true, emptyReceipt: true, revokes: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testMaterializerRemoveGroupRetry(t, scenario.legacyIntent, scenario.emptyReceipt, scenario.revokes)
		})
	}
}

func testMaterializerRemoveGroupRetry(t *testing.T, legacyIntent, emptyReceipt bool, revokes int) {
	old, ledger := legacyFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(old.ControlRoot)
	pkg := filepath.Join(root, "package")
	writePackage(t, pkg, probe)
	claudeConfig := filepath.Join(root, "profiles", "claude")
	codexConfig := filepath.Join(root, "profiles", "codex")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000ab", ComponentID: old.ComponentID, Owner: old.Owner,
		ScopeRoot: old.ScopeRoot, ControlRoot: old.ControlRoot, GlobalConfig: old.GlobalConfig,
		RuntimeRoot: old.RuntimeRoot, Primary: old.Primary,
	}
	installed, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation, PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe, OperationID: "legacy-group-install"},
		{Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation, PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe, OperationID: "legacy-group-install"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(old.GlobalConfig)); err != nil {
		t.Fatal(err)
	}
	id.GlobalConfig = filepath.Join(root, "explicit-config", "config.json")
	id.Primary = portable.PlatformPrimary()
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok {
		t.Fatal("UAP installation missing before simulated crash")
	}
	profiles := map[portable.Integration]string{portable.Codex: codexConfig, portable.Claude: claudeConfig}
	var targets []IntentTarget
	for _, b := range installed {
		var receiptID string
		for _, client := range installation.Clients {
			if client.ClientID == string(b.Integration) {
				receiptID = client.DataReceiptID
			}
		}
		if receiptID == "" {
			t.Fatalf("%s data receipt missing", b.Integration)
		}
		target := IntentTarget{
			Client: string(b.Integration), InstallationID: b.InstallationID,
			BindingID: b.BindingID, DataReceiptID: receiptID, Profile: profiles[b.Integration],
			Units: []string{"direct-mcp"},
		}
		if !legacyIntent {
			key, _, _, err := b.Registration()
			if err != nil {
				t.Fatal(err)
			}
			copy := b
			target.OldConsumerKey, target.OldBinding = key, &copy
		}
		if emptyReceipt {
			target.DataReceiptID = ""
		}
		targets = append(targets, target)
	}
	confirmed, reservation, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{
		ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner,
		Action: "uninstall", Targets: targets,
	})
	if err != nil {
		t.Fatal(err)
	}
	second := installed[1]
	name, err := second.Filename()
	if err != nil {
		t.Fatal(err)
	}
	locatorPath := filepath.Join(second.DataRoot, name)
	_, _, original, err := second.Registration()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(locatorPath, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := mat.RemoveGroup(testCtx(t), []MaterializeRequest{
		{Identity: id, Integration: portable.Codex, ExpectedGeneration: confirmed.Generation, ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true, OperationID: "foreign-group-remove"},
		{Identity: id, Integration: portable.Claude, ExpectedGeneration: confirmed.Generation, ClientConfigRoot: claudeConfig, ClientExecutable: probe, OperationID: "foreign-group-remove"},
	}); !errors.Is(err, ErrPreflight) {
		t.Fatalf("foreign second locator did not block group preflight: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil || !portable.ExactCommittedBinding(snap.Ledger, installed[0]) {
		t.Fatalf("first consumer changed before second locator validation: %v", err)
	}
	if legacyIntent {
		intent, err := ReadIntent(old.ControlRoot)
		if err != nil || intent.Targets[0].OldBinding != nil {
			t.Fatalf("refused group mutated legacy intent: %+v %v", intent.Targets, err)
		}
	}
	if err := os.WriteFile(locatorPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	gen := confirmed.Generation
	for _, b := range installed[:revokes] {
		if err := (Service{}).RevokeBinding(testCtx(t), Request{
			Binding: b, ExpectedGeneration: gen, Reservation: reservation,
		}); err != nil {
			t.Fatal(err)
		}
		snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
		if err != nil {
			t.Fatal(err)
		}
		gen = snap.Ledger.Generation
	}
	// Simulate a crash before UAP Apply, then retry from its retained clients.
	removed, err := mat.RemoveGroup(testCtx(t), []MaterializeRequest{
		{Identity: id, Integration: portable.Codex, ExpectedGeneration: gen, ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true, OperationID: "legacy-group-remove"},
		{Identity: id, Integration: portable.Claude, ExpectedGeneration: gen, ClientConfigRoot: claudeConfig, ClientExecutable: probe, OperationID: "legacy-group-remove"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0].AlreadyAbsent || removed[1].AlreadyAbsent {
		t.Fatalf("unexpected group removal: %+v", removed)
	}
	for _, b := range installed {
		if present, err := portable.ExactLocator(b); err != nil || present {
			t.Fatalf("%s historical locator remains: %v %v", b.Integration, present, err)
		}
	}
}

func TestMaterializerRemoveRetriesAfterRevoke(t *testing.T) {
	old, ledger := legacyFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(old.ControlRoot)
	pkg := filepath.Join(root, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(root, "profiles", "claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: config},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000ac", ComponentID: old.ComponentID, Owner: old.Owner,
		ScopeRoot: old.ScopeRoot, ControlRoot: old.ControlRoot, GlobalConfig: old.GlobalConfig,
		RuntimeRoot: old.RuntimeRoot, Primary: old.Primary,
	}
	b, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe, OperationID: "legacy-single-install",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok {
		t.Fatal("UAP installation missing")
	}
	var client domain.ClientBinding
	for _, candidate := range installation.Clients {
		if candidate.ClientID == string(portable.Claude) {
			client = candidate
		}
	}
	key, _, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	confirmed, reservation, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, Action: "uninstall",
		Targets: []IntentTarget{{
			Client: string(portable.Claude), InstallationID: b.InstallationID, BindingID: b.BindingID,
			DataReceiptID: client.DataReceiptID, Profile: config, OldConsumerKey: key, OldBinding: &b,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (Service{}).RevokeBinding(testCtx(t), Request{
		Binding: b, ExpectedGeneration: confirmed.Generation, Reservation: reservation,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(old.GlobalConfig)); err != nil {
		t.Fatal(err)
	}
	id.GlobalConfig = filepath.Join(root, "explicit-config", "config.json")
	id.Primary = portable.PlatformPrimary()
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
		ClientConfigRoot: config, ClientExecutable: probe, OperationID: "legacy-single-remove",
	}); err != nil {
		t.Fatal(err)
	}
	if present, err := portable.ExactLocator(b); err != nil || present {
		t.Fatalf("old locator survived resumed remove: %v %v", present, err)
	}
}

func TestMaterializerRemoveRecoversLegacyUninstallWithoutFrozenBinding(t *testing.T) {
	for _, integration := range []portable.Integration{portable.Claude, portable.Codex} {
		for _, missingLocator := range []bool{false, true} {
			name := string(integration) + "/present"
			if missingLocator {
				name = string(integration) + "/absent"
			}
			t.Run(name, func(t *testing.T) {
				testMaterializerRemoveRecoversLegacyUninstall(t, integration, legacyUninstallOptions{missingLocator: missingLocator})
			})
		}
	}
}

func TestMaterializerRemoveRejectsMissingCustomLegacyLocator(t *testing.T) {
	testMaterializerRemoveRecoversLegacyUninstall(t, portable.Claude, legacyUninstallOptions{missingLocator: true, customIdentity: true})
}

func TestMaterializerRemoveRejectsModifiedLegacyProjection(t *testing.T) {
	for _, integration := range []portable.Integration{portable.Claude, portable.Codex} {
		for _, mutation := range []string{"trailing-data", "malformed-json", "duplicate-key"} {
			t.Run(string(integration)+"/"+mutation, func(t *testing.T) {
				testMaterializerRemoveRecoversLegacyUninstall(t, integration, legacyUninstallOptions{projectionMutation: mutation})
			})
		}
	}
}

func TestMaterializerRemoveRejectsForeignLegacyLocator(t *testing.T) {
	for _, integration := range []portable.Integration{portable.Claude, portable.Codex} {
		t.Run(string(integration), func(t *testing.T) {
			testMaterializerRemoveRecoversLegacyUninstall(t, integration, legacyUninstallOptions{foreignLocator: true})
		})
	}
}

func TestMaterializerRemoveRecoversLegacyIntentWithoutReceipt(t *testing.T) {
	for _, integration := range []portable.Integration{portable.Claude, portable.Codex} {
		for _, revoked := range []bool{false, true} {
			name := string(integration) + "/live"
			if revoked {
				name = string(integration) + "/revoked"
			}
			t.Run(name, func(t *testing.T) {
				testMaterializerRemoveRecoversLegacyUninstall(t, integration, legacyUninstallOptions{
					missingLocator: revoked, emptyReceipt: true, leaveConsumer: !revoked,
				})
			})
		}
	}
}

type legacyUninstallOptions struct {
	missingLocator     bool
	customIdentity     bool
	emptyReceipt       bool
	leaveConsumer      bool
	projectionMutation string
	foreignLocator     bool
}

func testMaterializerRemoveRecoversLegacyUninstall(t *testing.T, integration portable.Integration, opts legacyUninstallOptions) {
	old, ledger := legacyFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(old.ControlRoot)
	pkg := filepath.Join(root, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(root, "profiles", string(integration))
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: config},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000ad", ComponentID: old.ComponentID, Owner: old.Owner,
		ScopeRoot: old.ScopeRoot, ControlRoot: old.ControlRoot, GlobalConfig: old.GlobalConfig,
		RuntimeRoot: old.RuntimeRoot, Primary: old.Primary,
	}
	if opts.customIdentity {
		id.GlobalConfig = filepath.Join(root, "custom", "config.json")
		if err := os.MkdirAll(filepath.Dir(id.GlobalConfig), 0700); err != nil {
			t.Fatal(err)
		}
	}
	b, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: integration, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe, OperationID: "legacy-uninstall-install",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok {
		t.Fatal("UAP installation missing")
	}
	var client domain.ClientBinding
	for _, candidate := range installation.Clients {
		if candidate.ClientID == string(integration) {
			client = candidate
		}
	}
	if client.DataReceiptID == "" {
		t.Fatal("UAP client receipt missing")
	}
	intentReceipt := client.DataReceiptID
	if opts.emptyReceipt {
		intentReceipt = ""
	}
	confirmed, reservation, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, Action: "uninstall",
		Targets: []IntentTarget{{
			Client: string(integration), InstallationID: b.InstallationID, BindingID: b.BindingID,
			DataReceiptID: intentReceipt, Profile: config,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	key, _, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	if !opts.leaveConsumer {
		gen := confirmed.Generation
		if _, err := installruntime.Commit(testCtx(t), installruntime.Request{
			ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner,
			ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &gen, Reservation: reservation,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if opts.missingLocator {
		if err := portable.RevokeLocator(b); err != nil {
			t.Fatal(err)
		}
	}
	if opts.foreignLocator {
		name, err := b.Filename()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(b.DataRoot, name), []byte(`{"foreign":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if opts.projectionMutation != "" {
		path := filepath.Join(client.TargetLocator, ".mcp.json")
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		payload := map[string]string{
			"trailing-data": "\n", "malformed-json": "{", "duplicate-key": `,"mcpServers":{}}`,
		}[opts.projectionMutation]
		_, writeErr := file.Write([]byte(payload))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("tamper projection: %v %v", writeErr, closeErr)
		}
	}
	id.GlobalConfig = filepath.Join(root, "explicit-config", "config.json")
	id.Primary = portable.PlatformPrimary()
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	removeErr := mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: integration, ExpectedGeneration: snap.Ledger.Generation,
		ClientConfigRoot: config, ClientExecutable: probe, ExternalUninstalled: integration == portable.Codex,
		OperationID: "legacy-uninstall-retry",
	})
	if opts.customIdentity || opts.projectionMutation != "" || opts.foreignLocator {
		if removeErr == nil || (opts.customIdentity && !errors.Is(removeErr, ErrPreflight)) {
			t.Fatalf("unsafe legacy recovery accepted: %v", removeErr)
		}
		intent, err := ReadIntent(b.ControlRoot)
		if err != nil || intent.Targets[0].OldBinding != nil {
			t.Fatalf("refused custom recovery mutated intent: %+v %v", intent.Targets, err)
		}
		return
	}
	if removeErr != nil {
		t.Fatalf("legacy uninstall retry: %v", removeErr)
	}
	if present, err := portable.ExactLocator(b); err != nil || present {
		t.Fatalf("legacy locator remains after retry: %v %v", present, err)
	}
}

func TestRefreshHandoffAcceptsFrozenOldAndNewAfterCallbackRetry(t *testing.T) {
	old, ledger := legacyFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(old.ControlRoot)
	pkg := filepath.Join(root, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(root, "profiles", "claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe, ClaudeRunner: listingRunner{configRoot: config},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000ad", ComponentID: old.ComponentID, Owner: old.Owner,
		ScopeRoot: old.ScopeRoot, ControlRoot: old.ControlRoot, GlobalConfig: old.GlobalConfig,
		RuntimeRoot: old.RuntimeRoot, Primary: old.Primary,
	}
	oldBinding, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe, OperationID: "legacy-refresh-install",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok {
		t.Fatal("UAP installation missing")
	}
	var receiptID string
	for _, client := range installation.Clients {
		if client.ClientID == string(portable.Claude) {
			receiptID = client.DataReceiptID
		}
	}
	if receiptID == "" {
		t.Fatal("UAP receipt missing")
	}
	replacement := oldBinding
	replacement.GlobalConfig = filepath.Join(root, "explicit-config", "config.json")
	replacement.Primary = portable.PlatformPrimary()
	if err := os.MkdirAll(filepath.Dir(replacement.GlobalConfig), 0700); err != nil {
		t.Fatal(err)
	}
	oldKey, _, _, err := oldBinding.Registration()
	if err != nil {
		t.Fatal(err)
	}
	newKey, _, _, err := replacement.Registration()
	if err != nil {
		t.Fatal(err)
	}
	confirmed, res, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{
		ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner,
		Action: "update", Targets: []IntentTarget{{
			Client: string(portable.Claude), InstallationID: id.InstallationID,
			BindingID: oldBinding.BindingID, DataReceiptID: receiptID, Profile: config,
			OldConsumerKey: oldKey, NewConsumerKey: newKey,
			OldBinding: &oldBinding, NewBinding: &replacement,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = commitReplacement(t, replacement, confirmed.Generation, res)
	id.GlobalConfig, id.Primary = replacement.GlobalConfig, replacement.Primary
	req := MaterializeRequest{Identity: id, Integration: portable.Claude, ClientConfigRoot: config, HandoffAction: "update"}
	if err := mat.validateRefreshHandoff(req); err != nil {
		t.Fatalf("retry with old and new consumers refused: %v", err)
	}
	foreign := replacement
	foreign.GlobalConfig = filepath.Join(root, "third", "config.json")
	if err := os.MkdirAll(filepath.Dir(foreign.GlobalConfig), 0700); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	_ = commitReplacement(t, foreign, snap.Ledger.Generation, res)
	if err := mat.validateRefreshHandoff(req); !errors.Is(err, ErrPreflight) {
		t.Fatalf("third consumer accepted on retry: %v", err)
	}
}

func TestRestoreMigrationProjectionUsesFrozenOuterAction(t *testing.T) {
	for _, action := range []string{"update", "repair"} {
		t.Run(action, func(t *testing.T) {
			old, ledger := legacyFixture(t)
			probe := buildProbe(t)
			root := filepath.Dir(old.ControlRoot)
			pkg := filepath.Join(root, "package")
			writePackage(t, pkg, probe)
			config := filepath.Join(root, "profiles", "claude")
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			uapRoot := filepath.Join(root, "uap")
			mat, err := NewMaterializer(UAPRoots{
				StateFile: filepath.Join(uapRoot, "state", "state-v2.json"), LockFile: filepath.Join(uapRoot, "state", "mutation.lock"),
				OperationsDir: filepath.Join(uapRoot, "state", "operations"), PluginDataBase: filepath.Join(uapRoot, "plugin-data"),
				ManagedRoot: filepath.Join(uapRoot, "managed"), HelperExecutable: probe, ClaudeRunner: listingRunner{configRoot: config},
			})
			if err != nil {
				t.Fatal(err)
			}
			id := Identity{
				InstallationID: "00000000-0000-4000-8000-0000000000ae", ComponentID: old.ComponentID, Owner: old.Owner,
				ScopeRoot: old.ScopeRoot, ControlRoot: old.ControlRoot, GlobalConfig: old.GlobalConfig,
				RuntimeRoot: old.RuntimeRoot, Primary: old.Primary,
			}
			oldBinding, err := mat.Install(testCtx(t), MaterializeRequest{
				Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
				PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe, OperationID: "migration-restore-install",
			})
			if err != nil {
				t.Fatal(err)
			}
			state, err := mat.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			installation, ok := findInstallation(state, id.InstallationID)
			if !ok || len(installation.Clients) != 1 {
				t.Fatal("UAP client missing")
			}
			var client domain.ClientBinding
			for _, candidate := range installation.Clients {
				if candidate.ClientID == string(portable.Claude) {
					client = candidate
				}
			}
			if client.DataReceiptID == "" {
				t.Fatal("Claude UAP client missing")
			}
			replacement := oldBinding
			replacement.GlobalConfig = filepath.Join(root, "explicit-config", "config.json")
			replacement.Primary = portable.PlatformPrimary()
			if err := os.MkdirAll(filepath.Dir(replacement.GlobalConfig), 0700); err != nil {
				t.Fatal(err)
			}
			oldKey, _, _, err := oldBinding.Registration()
			if err != nil {
				t.Fatal(err)
			}
			newKey, _, _, err := replacement.Registration()
			if err != nil {
				t.Fatal(err)
			}
			confirmed, _, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{
				ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner, Action: action,
				Targets: []IntentTarget{{
					Client: string(portable.Claude), InstallationID: id.InstallationID, BindingID: oldBinding.BindingID,
					DataReceiptID: client.DataReceiptID, Profile: config,
					OldConsumerKey: oldKey, NewConsumerKey: newKey, OldBinding: &oldBinding, NewBinding: &replacement,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			req := MaterializeRequest{
				Identity: id, Integration: portable.Claude, ExpectedGeneration: confirmed.Generation,
				PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe,
				OperationID: "migration-restore-old", HandoffAction: action,
				Discovery: Discovery{ConfigPath: filepath.Join(root, "direct-mcp.json"), Command: filepath.Join(old.RuntimeRoot, filepath.FromSlash(portable.PlatformPrimary()))},
			}
			wrongAction := req
			if action == "update" {
				wrongAction.HandoffAction = "repair"
			} else {
				wrongAction.HandoffAction = "update"
			}
			if _, err := mat.RestoreMigrationProjection(testCtx(t), wrongAction); !errors.Is(err, ErrPreflight) {
				t.Fatalf("mismatched action accepted: %v", err)
			}
			wrongProfile := req
			wrongProfile.ClientConfigRoot = filepath.Join(root, "profiles", "other")
			if _, err := mat.RestoreMigrationProjection(testCtx(t), wrongProfile); !errors.Is(err, ErrPreflight) {
				t.Fatalf("mismatched profile accepted: %v", err)
			}
			got, err := mat.RestoreMigrationProjection(testCtx(t), req)
			if err != nil || got != oldBinding {
				t.Fatalf("old projection not restored: got=%+v err=%v", got, err)
			}
			snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
			if err != nil || snap.Ledger.PendingMutation == nil || !portable.ExactCommittedBinding(snap.Ledger, oldBinding) {
				t.Fatalf("old binding or migration reservation lost: %v", err)
			}
		})
	}
}

func TestRevokeAndReplaceRefuseForeignLocator(t *testing.T) {
	old, ledger := legacyFixture(t)
	gen := commitLegacy(t, old, ledger.Generation)
	name, err := old.Filename()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old.DataRoot, name), []byte(`{"foreign":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	svc := Service{}
	if err := svc.RevokeBinding(testCtx(t), Request{Binding: old, ExpectedGeneration: gen}); err == nil {
		t.Fatal("foreign locator was revoked")
	}
	replacement := old
	replacement.Primary = portable.PlatformPrimary()
	gen, res := migrationReservation(t, old, replacement)
	gen = commitReplacement(t, replacement, gen, res)
	if err := svc.ReplaceCommittedBinding(testCtx(t), old, Request{Binding: replacement, ExpectedGeneration: gen, Reservation: res}); err == nil {
		t.Fatal("foreign old locator was removed during replacement")
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !portable.ExactCommittedBinding(snap.Ledger, old) {
		t.Fatal("old consumer removed despite foreign locator")
	}
}
