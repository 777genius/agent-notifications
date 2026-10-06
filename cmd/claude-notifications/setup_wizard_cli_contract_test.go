package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/loader"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/packagedigest"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/profileauthority"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/specregistry"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/planner"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"
)

func TestSetupWizardHelpAndYesRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizard(ctx, []string{"--help"}, &out); code != 0 || !strings.Contains(out.String(), "setup-notifications wizard") || !strings.Contains(out.String(), "units") || !strings.Contains(out.String(), "stderr") || !strings.Contains(out.String(), "Omit on inspect to report both clients") || !strings.Contains(out.String(), "invalid for mutation") || !strings.Contains(out.String(), "matching pending intent") || !strings.Contains(out.String(), "update, or repair") || !strings.Contains(out.String(), "omit on update/repair to keep live units") || !strings.Contains(out.String(), "Inspect exit 0") || !strings.Contains(out.String(), "one group apply") || !strings.Contains(out.String(), "mixed live revisions") || !strings.Contains(out.String(), "then Add of the missing") || !strings.Contains(out.String(), "existing config.toml") || !strings.Contains(out.String(), "existing .claude.json") {
		t.Fatalf("help: %d %s", code, out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex"}, &out, io.Discard, strings.NewReader(""), false); code != 2 {
		t.Fatalf("missing yes: %d %s", code, out.String())
	}
	out.Reset()
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex", "--yes", "--control-root", root, "--json"}, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("missing runtime: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "managed_runtime_required") {
		t.Fatalf("runtime reason: %s", out.String())
	}
}

// Regression: existing caller uses historical Cursor to verify a valid original
// token or refuses a matching retry. Real public token/validated TEST state;
// installed/native proof is NOT_RUN.
func TestCursorCallerAcknowledgedTokenAndMatchingPendingRetry(t *testing.T) {
	ctx := setupCommandContext(t)
	root := setupCommandRoot(t)
	profile := filepath.Join(root, ".cursor")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := cursorQualifiedNamespace(profile); err != nil {
		t.Logf("NOT_RUN positive caller/P1/resume: TEST full ancestry outside fixed qualified tuple: %v", err)
		return
	}
	token, err := profileauthority.Capture(ctx, profile)
	if err != nil || token.IsZero() {
		t.Fatalf("qualified TEST Capture: %v", err)
	}
	control, runtimeRoot := filepath.Join(root, "control"), filepath.Join(root, "runtime")
	observer := filepath.Join(runtimeRoot, filepath.FromSlash(portable.PlatformPrimary()))
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "TEST-owner", Files: []installruntime.File{{Path: observer, Data: []byte("TEST-inert" + installruntime.WriterProtocolMarker), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(root, "TEST-version-agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nprintf '%s\\n' '"+selectedCursorVersion+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "TEST-package")
	writeWizardPackage(t, pkg, agent)
	on, off := true, false
	r := setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"cursor"}, Hooks: &off, AgentNotify: &on, Yes: true, CursorConfig: profile, ScopeRoot: profile, ControlRoot: control, GlobalConfig: filepath.Join(root, "TEST-global.json"), ClientExecutable: agent, PackageRoot: pkg}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	r, cfg, _, _, err := reserveCursorCaller(ctx, r, snap)
	if err != nil {
		t.Fatal(err)
	}
	b, err := portablesetup.Complete(portablesetup.Identity{InstallationID: r.InstallationID, ComponentID: snap.Ledger.ID, Owner: snap.Ledger.Owner, ScopeRoot: profile, ControlRoot: control, GlobalConfig: r.GlobalConfig, RuntimeRoot: runtimeRoot, Primary: r.Primary}, portable.Cursor, "cursor", "user", filepath.Join(profile, "plugins", "local", domain.ComputePhysicalArtifactID("agent-notify", r.InstallationID)), filepath.Dir(r.CursorAuthority.Selector))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	r.CursorAuthority.ObjectID = "TEST-retained-stop"
	adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, r.CursorAuthority)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.SourceIdentity{RequestedSource: pkg, CanonicalSource: pkg, SourceBindingHint: "direct-local"}
	packageSnapshot, err := (packagedigest.Builder{TempRoot: filepath.Join(root, "TEST-snapshot")}).SnapshotWithExecutables(ctx, pkg, source, []string{"bin/probe"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = packagedigest.Remove(packageSnapshot) }()
	specs, err := specregistry.New()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := (loader.Loader{Registry: specs}).Load(ctx, domain.LoadInput{SnapshotRoot: packageSnapshot.Root, TreeDigest: packageSnapshot.TreeDigest, ExecutableFiles: packageSnapshot.ExecutableFiles, Source: packageSnapshot.Source})
	if err != nil {
		t.Fatal(err)
	}
	digest := envelope.TreeDigest
	selectedRegistry, err := clients.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := (planner.Planner{ManagedRoot: cfg.ManagedRoot, Paths: pathpolicy.Policy{}, Registry: selectedRegistry}).Plan(ctx, domain.PlanRequest{Envelope: envelope, Client: domain.DetectedClient{ClientID: domain.ClientCursor, Status: domain.DetectionDetected, ConfigRoot: profile, ProfileAuthority: &token}, Scope: domain.ScopeUser, PhysicalArtifactID: domain.ComputePhysicalArtifactID("agent-notify", r.InstallationID)})
	if err != nil {
		t.Fatal(err)
	}
	dataReceipt, _, err := (providers.PluginDataManager{Base: cfg.PluginDataBase}).EnsureData(ctx, r.InstallationID, delivery.PhysicalArtifactID, "user")
	if err != nil {
		t.Fatal(err)
	}
	stagedDelivery, err := (providers.Stager{Registry: selectedRegistry, Paths: pathpolicy.Policy{}, SnapshotBuilder: packagesnapshot.Builder{TempRoot: filepath.Join(root, "TEST-stager")}}).StageWithPluginData(ctx, envelope, delivery, "TEST-public-pre-native", domain.CompatibilityHints{}, dataReceipt.Locator)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stagedDelivery.StagingPath, stagedDelivery.ActivePath); err != nil {
		t.Fatal(err)
	}
	selected, err := delivery.SelectedDelivery.WithProjectionDigest(stagedDelivery.ArtifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	facts, _ := selected.CursorFacts()
	client := domain.ClientBinding{ClientBindingID: b.BindingID, ClientID: "cursor", Scope: "user", TargetLocator: filepath.Join(profile, "plugins", "local", domain.ComputePhysicalArtifactID("agent-notify", r.InstallationID)), PhysicalArtifact: domain.ComputePhysicalArtifactID("agent-notify", r.InstallationID),
		Materialization: domain.MaterializationMaterialized, Activation: domain.ActivationActive, Authentication: domain.AuthenticationNotRequired, Policy: domain.PolicyAllowed, Verification: domain.VerificationInstalled,
		DataReceiptID: dataReceipt.DataReceiptID, PackageRevision: &domain.ClientPackageRevision{Version: envelope.Manifest.Version, TreeDigest: envelope.TreeDigest, ManifestDigest: envelope.ManifestDigest}, NativeProfileRoot: profile, ProfileNamespace: cfg.StateRoot, ProfileAuthority: &token,
		SelectedDelivery: selected, NativeObjects: append(stagedDelivery.NativeObjects, selected.CursorOwnership(facts.PlannedReceipt))}
	store := statev2.Store{Path: filepath.Join(cfg.StateRoot, "state-v2.json")}
	if err := store.Save(domain.StateFileV2{SchemaVersion: domain.StateSchemaVersion, Installations: []domain.Installation{{InstallationID: r.InstallationID, DeclaredName: "agent-notify",
		Source: domain.SourceBinding{SourceBindingID: domain.ComputeSourceBindingID(packageSnapshot.Source), RequestedSource: pkg, CanonicalSource: pkg, TreeDigest: digest}, Package: domain.PackageBinding{DeclaredName: "agent-notify", LoaderKind: envelope.LoaderKind, FormatID: envelope.FormatID, Version: envelope.Manifest.Version, ManifestDigest: envelope.ManifestDigest},
		Clients:      map[string]domain.ClientBinding{b.BindingID: client},
		DataReceipts: map[string]domain.DataReceipt{dataReceipt.DataReceiptID: dataReceipt}}}}); err != nil {
		t.Fatal(err)
	}
	// The public durable state exists, but the AN consumer and all native
	// attempts/objects are absent. No in-memory authority survives the parser.
	firstState, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	firstClient := client
	firstClient.Activation, firstClient.Verification, firstClient.NativeObjects = domain.ActivationPrepared, domain.VerificationPackageValid, stagedDelivery.NativeObjects
	firstState.Installations[0].Clients[b.BindingID] = firstClient
	if err := store.Save(firstState); err != nil {
		t.Fatal(err)
	}
	intentBody := portablesetup.ConfirmedIntent{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: snap.Ledger.Owner, ExpectedGeneration: snap.Ledger.Generation, Action: "install", Stage: "confirmed", SourceRevision: "1.0.0", TreeDigest: digest, HelperDigest: snap.Ledger.Files[observer].SHA256, HelperVersion: "agent-notify-portable-v1", Primary: r.Primary, GlobalConfig: r.GlobalConfig, Targets: []portablesetup.IntentTarget{{Client: "cursor", InstallationID: b.InstallationID, BindingID: b.BindingID, Profile: profile, Units: []string{"agent-notify"}}}}
	published, reservation, err := (portablesetup.Service{}).PublishConfirmedIntent(ctx, intentBody)
	if err != nil {
		t.Fatal(err)
	}
	retryArgs := []string{"--action", "install", "--agents", "cursor", "--hooks", "false", "--agent-notify", "true", "--control-root", control, "--scope-root", profile, "--global-config", r.GlobalConfig, "--client-executable", agent}
	fresh, _, err := parseSetupWizard(retryArgs)
	if err != nil || fresh.CursorAuthority != nil {
		t.Fatal("fresh parser", err)
	}
	noToken := firstClient
	noToken.ProfileAuthority = nil
	uncertain := firstClient
	uncertain.NativeActivationAttempt = "TEST-uncertain"
	uncertain.PendingNativeIntent = &domain.PendingNativeIntent{ProfileAuthority: &token, ProfileNamespace: cfg.StateRoot, AttemptID: uncertain.NativeActivationAttempt, Direction: domain.NativeIntentRegister, Delivery: selected}
	failed := firstClient
	failed.Activation, failed.Verification = domain.ActivationFailed, domain.VerificationFailed
	for _, refusal := range []domain.ClientBinding{noToken, uncertain, failed} {
		firstState.Installations[0].Clients[b.BindingID] = refusal
		if err := store.Save(firstState); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(store.Path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := composeCursorWizard(ctx, fresh); err == nil {
			t.Fatal("first handoff without original token or with native uncertainty admitted")
		}
		after, err := os.ReadFile(store.Path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("first handoff refusal rewrote state", err)
		}
	}
	firstState.Installations[0].Clients[b.BindingID] = firstClient
	if err := store.Save(firstState); err != nil {
		t.Fatal(err)
	}
	resumed, err := composeCursorWizard(ctx, fresh)
	if err != nil || resumed.CursorAuthority.ObjectID != facts.ObjectID || portable.ExactCommittedBinding(published, b) {
		t.Fatalf("unregistered first handoff: %+v %v", resumed, err)
	}
	pendingSnapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cursorInstalledInputs(ctx, b, pendingSnapshot, cfg.StateRoot); err == nil {
		t.Fatal("unregistered first handoff became eligible for installed delivery")
	}
	adapter, err = cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, resumed.CursorAuthority)
	if err != nil {
		t.Fatal(err)
	}
	base, err := portablesetup.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Registry, err = clients.NewRegistry(append(base.All(), adapter)...)
	if err != nil {
		t.Fatal(err)
	}
	handoffs := 0
	interrupted := errors.New("TEST-interrupt-after-AN-handoff-before-native")
	cfg.TrustedLocalPackages, cfg.HelperExecutable, cfg.HelperVersion = true, observer, "agent-notify-portable-v1"
	cfg.OnCommittedBinding = func(ctx context.Context, f uapinstaller.BindingFacts) error {
		if f.BindingID != b.BindingID || f.ProfileAuthority == nil || !reflect.DeepEqual(*f.ProfileAuthority, token) {
			t.Fatal("public handoff lost original authority")
		}
		handoffs++
		if _, err := (portablesetup.Service{}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: published.Generation, Reservation: reservation}); err != nil {
			return err
		}
		return interrupted
	}
	handoffEngine, err := uapinstaller.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := handoffEngine.Prepare(ctx, uapinstaller.Request{Operation: uapinstaller.OpInstall, ClientID: "cursor", InstallationID: b.InstallationID, ClientConfigRoot: profile, ClientExecutable: agent, PackageRoot: pkg})
	if err != nil {
		t.Fatal(err)
	}
	_, err = handoffEngine.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
	_ = prepared.Close()
	if !errors.Is(err, interrupted) || handoffs != 1 {
		t.Fatalf("actual public retry handoff: calls=%d %v", handoffs, err)
	}
	afterHandoff, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || !portable.ExactCommittedBinding(afterHandoff.Ledger, b) {
		t.Fatal("AN handoff not committed", err)
	}
	acknowledgedRetry, err := composeCursorWizard(ctx, fresh)
	if err != nil || acknowledgedRetry.PackageRoot != pkg || acknowledgedRetry.CursorAuthority.ObjectID != facts.ObjectID {
		t.Fatalf("same-source acknowledged handoff retry: %+v %v", acknowledgedRetry, err)
	}
	if _, _, err := (portablesetup.Service{}).FinishConfirmedIntent(ctx, intentBody, reservation); err != nil {
		t.Fatal(err)
	}
	firstState.Installations[0].Clients[b.BindingID] = client
	if err := store.Save(firstState); err != nil {
		t.Fatal(err)
	}
	// Acknowledged fixture remains a read-only caller boundary, not native proof.
	r.BootstrapExpectedGeneration = nil
	got, err := composeCursorWizard(ctx, r)
	if err != nil || got.CursorAuthority.ObjectID != "TEST-retained-stop" || got.CursorAuthority.Selector != facts.Selector {
		t.Fatalf("existing token caller: %+v %v", got, err)
	}
	// A is acknowledged. Acquire candidate B, then interrupt immediately
	// after the same confirmed publisher used by the wizard, before UAP B commit.
	archive := filepath.Join(root, "TEST-B.zip")
	built, err := portableasset.Build(portableasset.BuildRequest{Version: "1.1.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: agent, OutputRoot: filepath.Join(root, "TEST-B"), Archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	archiveBody, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	update := r
	update.Action, update.PackageRoot, update.ReleaseVersion, update.CursorAuthority = setupwizard.ActionUpdate, "", "1.1.0", nil
	update.PackageFetcher = func(_ context.Context, url string) ([]byte, error) {
		fetches++
		if strings.HasSuffix(url, "/checksums.txt") {
			return []byte(built.ArchiveSHA256 + "  " + portableasset.AssetName(runtime.GOOS, runtime.GOARCH) + "\n"), nil
		}
		return archiveBody, nil
	}
	candidate, err := composeCursorWizard(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	intentBody.Action, intentBody.ExpectedGeneration, intentBody.SourceRevision, intentBody.TreeDigest = "update", *candidate.BootstrapExpectedGeneration, candidate.ReleaseVersion, candidate.TreeDigest
	intentBody.Targets[0].DataReceiptID = dataReceipt.DataReceiptID
	published, reservation, err = (portablesetup.Service{}).PublishConfirmedIntent(ctx, intentBody)
	if err != nil {
		t.Fatal(err)
	}
	pending := *reservation
	retryArgs[1] = "update"
	r, _, err = parseSetupWizard(retryArgs)
	if err != nil {
		t.Fatal(err)
	}
	r.PackageFetcher = update.PackageFetcher
	before, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	got, err = composeCursorWizard(ctx, r)
	if err != nil || got.InstallationID != b.InstallationID || got.BindingIDs["cursor"] != b.BindingID || got.PackageRoot != candidate.PackageRoot || got.CursorAuthority.ObjectID != facts.ObjectID {
		t.Fatalf("matching caller retry: %+v %v", got, err)
	}
	explicit := r
	explicit.PackageRoot = candidate.PackageRoot
	if _, err := composeCursorWizard(ctx, explicit); err != nil {
		t.Fatal("exact explicit B refused", err)
	}
	wrong := r
	wrong.PackageRoot = pkg
	if _, err := composeCursorWizard(ctx, wrong); err == nil {
		t.Fatal("predecessor A substituted for pending B")
	}
	wrong.PackageRoot = built.Root
	if err := os.WriteFile(filepath.Join(built.Root, "skills", "agent-notifications", "SKILL.md"), []byte("TEST-conflicting-B"), 0600); err != nil {
		t.Fatal(err)
	}
	assertCursorPackageRefusal(t, control, []string{pkg, candidate.PackageRoot, built.Root}, func() {
		if _, err := composeCursorWizard(ctx, wrong); err == nil {
			t.Fatal("wrong same-release B admitted")
		}
	})
	otherExe := filepath.Join(root, "TEST-other-executable")
	if err := os.WriteFile(otherExe, []byte("TEST-different-B"), 0700); err != nil {
		t.Fatal(err)
	}
	wrongB, err := portableasset.Build(portableasset.BuildRequest{Version: "1.1.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: otherExe, OutputRoot: filepath.Join(root, "TEST-wrong-B"), Archive: filepath.Join(root, "TEST-wrong-B.zip")})
	if err != nil {
		t.Fatal(err)
	}
	wrong.PackageRoot = wrongB.Archive
	assertCursorPackageRefusal(t, control, []string{pkg, candidate.PackageRoot, wrongB.Archive}, func() {
		if _, err := composeCursorWizard(ctx, wrong); err == nil {
			t.Fatal("wrong same-release archive B admitted")
		}
	})
	if raw, err := os.ReadFile(filepath.Join(built.Root, "skills", "agent-notifications", "SKILL.md")); err != nil || string(raw) != "TEST-conflicting-B" {
		t.Fatal("conflicting B cleaned", err)
	}
	if fetches != 2 {
		t.Fatal("retry fetched", fetches)
	}
	for _, source := range []string{pkg, candidate.PackageRoot} {
		if _, err := os.Stat(source); err != nil {
			t.Fatal("retry cleaned source", err)
		}
	}
	plan, err := setupwizard.Plan(ctx, got)
	if err == nil || !strings.Contains(err.Error(), "selected Cursor portable ProjectArgs composition is unavailable") || plan.Request.DataReceiptIDs["cursor"] != dataReceipt.DataReceiptID {
		t.Fatalf("owner did not resume through unchanged materializer denial: %+v %v", plan, err)
	}
	bad := r
	bad.Action = setupwizard.ActionRepair
	if _, err := composeCursorWizard(ctx, bad); err == nil {
		t.Fatal("conflicting pending action admitted")
	}
	if err := os.Rename(profile, profile+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"", archive} {
		replaced := r
		replaced.PackageRoot = source
		assertCursorPackageRefusal(t, control, []string{pkg, candidate.PackageRoot, archive}, func() {
			if _, err := composeCursorWizard(ctx, replaced); err == nil {
				t.Fatal("same-path replacement repaired original token", source)
			}
		})
	}
	if fetches != 2 {
		t.Fatal("token refusal fetched", fetches)
	}
	after, err := os.ReadFile(store.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("caller replaced token/selected packet: %v", err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snapshot.Ledger.PendingMutation == nil || *snapshot.Ledger.PendingMutation != pending || snapshot.Ledger.Generation != *got.BootstrapExpectedGeneration {
		t.Fatalf("retry replaced pending intent: %+v %v", snapshot, err)
	}
	t.Log("QUALIFIED_TEST_FIRST_HANDOFF=true; QUALIFIED_TEST_PENDING_A_TO_B=true; QUALIFIED_TEST_CALLER_ORIGINAL_TOKEN_AND_MATCHING_RESUME=true; installed/native NOT_RUN")
}

// Missing physical authority must refuse even at the public pre-native handoff.
func TestCursorCallerFirstHandoffMissingToken(t *testing.T) {
	ctx := setupCommandContext(t)
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	r := setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"cursor"}, ControlRoot: env.control, CursorConfig: env.scope, ScopeRoot: env.scope, GlobalConfig: env.global, PackageRoot: env.pkg}
	reserved, cfg, _, _, err := reserveCursorCaller(ctx, r, snap)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(reserved.CursorAuthority.Selector)
	state := domain.StateFileV2{SchemaVersion: domain.StateSchemaVersion, Installations: []domain.Installation{{InstallationID: reserved.InstallationID, DeclaredName: "agent-notify", Source: domain.SourceBinding{SourceBindingID: "TEST-source", CanonicalSource: env.pkg}, Package: domain.PackageBinding{DeclaredName: "agent-notify"}, Clients: map[string]domain.ClientBinding{reserved.BindingIDs["cursor"]: {ClientBindingID: reserved.BindingIDs["cursor"], ClientID: "cursor", Scope: "user", TargetLocator: filepath.Join(env.scope, "plugins", "local", domain.ComputePhysicalArtifactID("agent-notify", reserved.InstallationID)), PhysicalArtifact: domain.ComputePhysicalArtifactID("agent-notify", reserved.InstallationID), Materialization: domain.MaterializationMaterialized, Activation: domain.ActivationPrepared, Verification: domain.VerificationPackageValid, Authentication: domain.AuthenticationNotRequired, Policy: domain.PolicyAllowed, DataReceiptID: "TEST-data", NativeProfileRoot: env.scope, ProfileNamespace: cfg.StateRoot}}, DataReceipts: map[string]domain.DataReceipt{"TEST-data": {DataReceiptID: "TEST-data", PhysicalBackend: "TEST-backend", Scope: "user", Locator: root, OwnershipDigest: "sha256:" + strings.Repeat("a", 64), State: domain.DataReceiptOwned}}}}}
	store := statev2.Store{Path: filepath.Join(cfg.StateRoot, "state-v2.json")}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	_, pending, err := (portablesetup.Service{}).PublishConfirmedIntent(ctx, portablesetup.ConfirmedIntent{ControlRoot: env.control, RuntimeRoot: env.runtime, Owner: snap.Ledger.Owner, ExpectedGeneration: snap.Ledger.Generation, Action: "install", Stage: "confirmed", Primary: reserved.Primary, GlobalConfig: reserved.GlobalConfig, Targets: []portablesetup.IntentTarget{{Client: "cursor", InstallationID: reserved.InstallationID, BindingID: reserved.BindingIDs["cursor"], Profile: env.scope, Units: []string{"agent-notify"}}}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	r.PackageRoot = ""
	r.PackageFetcher = func(context.Context, string) ([]byte, error) {
		fetches++
		return nil, errors.New("TEST-forbidden-fetch")
	}
	if _, _, _, _, err := reserveCursorCaller(ctx, r, snap); err == nil {
		t.Fatal("missing token admitted")
	}
	after, err := os.ReadFile(store.Path)
	current, e := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil || e != nil || !bytes.Equal(before, after) || !reflect.DeepEqual(snap.Ledger, current.Ledger) || current.Ledger.PendingMutation == nil || *current.Ledger.PendingMutation != *pending || fetches != 0 {
		t.Fatal("missing token refusal allocated, fetched, or changed owners", err, e)
	}
}

// Regression: a missing-package caller fetches a release before helper or
// generation refusal, or leaves its new acquisition after binding conflict.
func TestCursorCallerMissingPackageRefusalAndOwnedCleanup(t *testing.T) {
	ctx := setupCommandContext(t)
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(env.root, "TEST-release.zip")
	built, err := portableasset.Build(portableasset.BuildRequest{Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: env.probe, OutputRoot: filepath.Join(env.root, "TEST-release"), Archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	r := setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"cursor"}, ControlRoot: env.control, CursorConfig: env.scope, ScopeRoot: env.scope, GlobalConfig: env.global, ReleaseVersion: "1.43.0", PackageFetcher: func(_ context.Context, url string) ([]byte, error) {
		fetches++
		if strings.HasSuffix(url, "/checksums.txt") {
			return []byte(built.ArchiveSHA256 + "  " + portableasset.AssetName(runtime.GOOS, runtime.GOARCH) + "\n"), nil
		}
		return data, nil
	}}
	wrongGeneration := snap.Ledger.Generation + 1
	for _, bad := range []setupwizard.Request{func() setupwizard.Request { b := r; b.Helper = env.probe; return b }(), func() setupwizard.Request { b := r; b.BootstrapExpectedGeneration = &wrongGeneration; return b }()} {
		if _, _, _, _, err := reserveCursorCaller(ctx, bad, snap); err == nil {
			t.Fatal("refusal admitted")
		}
		if fetches != 0 {
			t.Fatal("release fetched before helper/generation refusal", fetches)
		}
	}
	r.ClientExecutable = env.probe // Existing inert version fixture, never a real agent.
	if _, err := composeCursorWizard(ctx, r); err == nil || !strings.Contains(err.Error(), "version is outside") {
		t.Fatalf("wrong version: %v", err)
	}
	if fetches != 0 {
		t.Fatal("release fetched before refusal", fetches)
	}
	uap := filepath.Join(env.root, "uap")
	if _, err := os.Stat(uap); !os.IsNotExist(err) {
		t.Fatalf("refusal retained directories/files/locks: %v", err)
	}
	r.BindingIDs = map[string]string{"cursor": "TEST-conflicting-binding"}
	if _, _, _, _, err := reserveCursorCaller(ctx, r, snap); err == nil || fetches != 2 {
		t.Fatalf("post-acquisition conflict: fetches=%d %v", fetches, err)
	}
	if _, err := os.Stat(uap); !os.IsNotExist(err) {
		t.Fatalf("composition retained owned staging: %v", err)
	}
	after, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil || after.Ledger.Generation != snap.Ledger.Generation || after.Ledger.PendingMutation != nil {
		t.Fatalf("refusal saved intent: %+v %v", after, err)
	}
	// A conflicting durable action is refused by the real caller even here,
	// where positive physical admission is NOT_RUN.
	path := portablesetup.IntentPath(env.control)
	intent := portablesetup.Intent{Version: 1, SetupIntentID: "TEST-conflicting-intent", Action: "install", Targets: []portablesetup.IntentTarget{{Client: "cursor", InstallationID: "TEST-existing", BindingID: "TEST-binding", Profile: env.scope, Units: []string{"agent-notify"}}}}
	payload, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	pending := installruntime.PendingMutation{ID: intent.SetupIntentID, Owner: "existing-installer", IntentRef: path}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: env.control, RuntimeRoot: env.runtime, Owner: "existing-installer", ConsumerID: "existing", RefreshOnly: true, ExpectedGeneration: &snap.Ledger.Generation, Reservation: &pending, Files: []installruntime.File{{Path: path, Data: payload, Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	r.Action = setupwizard.ActionUninstall
	if _, err := composeCursorWizard(ctx, r); err == nil {
		t.Fatal("conflicting pending action admitted")
	}
	retained, err := os.ReadFile(path)
	after, readErr := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil || readErr != nil || !bytes.Equal(payload, retained) || after.Ledger.Generation != ledger.Generation || after.Ledger.PendingMutation == nil || *after.Ledger.PendingMutation != pending || fetches != 2 {
		t.Fatalf("pending conflict had effects: %+v %v %v", after, err, readErr)
	}
	if _, err := os.Stat(uap); !os.IsNotExist(err) {
		t.Fatalf("pending conflict created state: %v", err)
	}
}

func TestSetupWizardTTYSelectsAndCancels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, []string{"--action", "install"}, &out, io.Discard, strings.NewReader("\n"), true); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("tty cancel: %d %s", code, out.String())
	}
	out.Reset()
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--control-root", root}, &out, io.Discard, strings.NewReader("2\n3\ny\n"), true); code != 1 {
		t.Fatalf("tty confirm still needs runtime: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "managed_runtime_required") || !strings.Contains(out.String(), "retry:") {
		t.Fatalf("tty retry: %s", out.String())
	}
}

func TestSetupWizardTTYOmitsActionDefaultsInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--control-root", root}, &out, io.Discard, strings.NewReader("2\n3\ny\n"), true); code != 1 {
		t.Fatalf("omitted action: %d %s", code, out.String())
	}
	if strings.Contains(out.String(), "Existing agent-notify") {
		t.Fatalf("new machine prompted existing action: %s", out.String())
	}
	if !strings.Contains(out.String(), "Units:") || !strings.Contains(out.String(), "Plan: action=install") || !strings.Contains(out.String(), "managed_runtime_required") {
		t.Fatalf("omitted action flow: %s", out.String())
	}
}

func TestSetupWizardJSONInspectIsOneObject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: []byte("inert"), Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := executeSetupWizardWith(ctx, []string{"--action", "inspect", "--agents", "codex", "--control-root", control, "--json"}, &out, &stderr, strings.NewReader("y\n"), true)
	if code != 0 {
		t.Fatalf("inspect json: %d stdout=%s stderr=%s", code, out.String(), stderr.String())
	}
	if strings.Contains(out.String(), "phase ") {
		t.Fatalf("json stdout included progress: %s", out.String())
	}
	var result setupwizard.Result
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if err := dec.Decode(&result); err != nil {
		t.Fatalf("stdout json: %v %s", err, out.String())
	}
	if result.Action != "inspect" || result.Outcome == "" {
		t.Fatalf("inspect result: %+v", result)
	}
	if dec.More() {
		t.Fatalf("stdout contained more than one JSON value: %s", out.String())
	}
}

func TestSetupWizardJSONInspectOmitsAgents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: []byte("inert"), Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := executeSetupWizardWith(ctx, []string{"--action", "inspect", "--control-root", control, "--json"}, &out, &stderr, strings.NewReader(""), false)
	if code != 0 {
		t.Fatalf("omitted-agents inspect: %d stdout=%s stderr=%s", code, out.String(), stderr.String())
	}
	var result setupwizard.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout json: %v %s", err, out.String())
	}
	if result.Action != "inspect" || result.Outcome != "completed" || result.Reason == "empty_selection" {
		t.Fatalf("omitted-agents inspect result: %+v", result)
	}
	saw := map[string]bool{}
	for _, target := range result.Targets {
		if target.Unit == "agent-notify" {
			saw[target.Client] = true
		}
	}
	if !saw["claude"] || !saw["codex"] {
		t.Fatalf("omitted-agents inspect missed a client: %+v", result.Targets)
	}
}

func TestSetupWizardInspectReportsDiscoveredMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: []byte("inert"), Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(root, "codex")
	t.Setenv("CODEX_HOME", filepath.Join(root, "env-codex")+string(filepath.Separator))
	t.Setenv("CLAUDE_CONFIG_DIR", "relative-claude")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	mcp := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(mcp, []byte("title = 'keep'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--action", "inspect", "--agents", "codex", "--control-root", control, "--codex-home", codexHome}
	var out bytes.Buffer
	code := executeSetupWizardWith(ctx, append(append([]string{}, flags...), "--json"), &out, io.Discard, strings.NewReader(""), false)
	if code != 0 {
		t.Fatalf("inspect json: %d %s", code, out.String())
	}
	var result setupwizard.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("inspect json: %v %s", err, out.String())
	}
	var mcpFile string
	for _, target := range result.Targets {
		if target.Unit == "direct-mcp" && target.Client == "codex" {
			mcpFile = target.ConfigPath
		}
	}
	if mcpFile != mcp {
		t.Fatalf("inspect json mcp: %s targets=%+v", mcpFile, result.Targets)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect text: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "MCP config: "+mcp) {
		t.Fatalf("inspect text omitted mcp: %s", out.String())
	}
}

func TestSetupWizardReportsDataRetained(t *testing.T) {
	var out bytes.Buffer
	result := setupwizard.Result{
		Action: "inspect", Outcome: "completed", Generation: 3, DataRetained: true,
		InstallationID: "00000000-0000-4000-8000-000000000099",
		Targets:        []setupwizard.TargetResult{{Client: "codex", Unit: "agent-notify", Outcome: "absent"}},
	}
	if code := writeSetupWizardResult(&out, false, result, nil); code != 0 || !strings.Contains(out.String(), "Settings and notification data were retained.") || strings.Contains(out.String(), "installation-id=") {
		t.Fatalf("text: %d %s", code, out.String())
	}
	out.Reset()
	if code := writeSetupWizardResult(&out, true, result, nil); code != 0 {
		t.Fatalf("json code: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"dataRetained":true`) || !strings.Contains(out.String(), `"installationID":"00000000-0000-4000-8000-000000000099"`) || strings.Contains(out.String(), `"DataRetained"`) {
		t.Fatalf("json: %s", out.String())
	}
	out.Reset()
	live := setupwizard.Result{Action: "inspect", Outcome: "completed", Generation: 2}
	if code := writeSetupWizardResult(&out, false, live, nil); code != 0 || strings.Contains(out.String(), "data_retained") {
		t.Fatalf("live text leaked retained: %d %s", code, out.String())
	}
}

func TestSetupWizardJSONDoesNotPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex", "--json"}, &out, io.Discard, strings.NewReader("y\n"), true); code != 2 {
		t.Fatalf("json prompt: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "noninteractive_requires_yes") {
		t.Fatalf("json must not consume TTY confirm: %s", out.String())
	}
	if strings.Contains(out.String(), "phase ") {
		t.Fatalf("json stdout included progress: %s", out.String())
	}
}

func TestSetupWizardJSONMutationRequiresYes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, action := range []string{"update", "repair", "uninstall"} {
		var out bytes.Buffer
		code := executeSetupWizardWith(ctx, []string{"--action", action, "--agents", "codex", "--json"}, &out, io.Discard, strings.NewReader("y\n"), true)
		if code != 2 {
			t.Fatalf("%s json prompt: %d %s", action, code, out.String())
		}
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s json: %v %s", action, err, out.String())
		}
		if result.Action != action || result.Outcome != "invalid" || result.Reason != "noninteractive_requires_yes" {
			t.Fatalf("%s result: %+v", action, result)
		}
	}
}

func TestSetupWizardTTYShowsDiscoverCapabilities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binDir := t.TempDir()
	name := "codex"
	body := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		name = "codex.bat"
		body = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".BAT;.COM;.EXE")
	}
	var out bytes.Buffer
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--control-root", root}, &out, io.Discard, strings.NewReader("\n"), true); code != 0 {
		t.Fatalf("tty discover cancel: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Claude (executable not found)") || !strings.Contains(out.String(), "Codex (executable present)") {
		t.Fatalf("discover labels: %s", out.String())
	}
}

func TestSetupWizardUpdateAndRepairRequireManagedRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	for _, action := range []string{"update", "repair"} {
		var out bytes.Buffer
		code := executeSetupWizardWith(ctx, []string{"--action", action, "--agents", "codex", "--yes", "--control-root", root, "--json"}, &out, io.Discard, strings.NewReader(""), false)
		if code != 1 {
			t.Fatalf("%s exit: %d %s", action, code, out.String())
		}
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s json: %v %s", action, err, out.String())
		}
		if result.Action != action || result.Outcome != "incomplete" || result.Reason != "managed_runtime_required" {
			t.Fatalf("%s result: %+v", action, result)
		}
	}
}

func TestSetupWizardJSONOmittedAgentsIsInvalid(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: []byte("inert"), Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"install", "update", "repair", "uninstall"} {
		var out bytes.Buffer
		code := executeSetupWizardWith(ctx, []string{"--action", action, "--yes", "--control-root", control, "--json"}, &out, io.Discard, strings.NewReader(""), false)
		if code != 2 {
			t.Fatalf("%s omitted agents: %d %s", action, code, out.String())
		}
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s json: %v %s", action, err, out.String())
		}
		if result.Action != action || result.Outcome != "invalid" || result.Reason != "agents_required" {
			t.Fatalf("%s omitted agents result: %+v", action, result)
		}
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, []string{"--action", "inspect", "--control-root", control, "--json"}, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("omitted inspect: %d %s", code, out.String())
	}
	var result setupwizard.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("inspect json: %v %s", err, out.String())
	}
	if result.Action != "inspect" || result.Outcome != "completed" || result.Reason == "agents_required" {
		t.Fatalf("omitted inspect result: %+v", result)
	}
}

func TestParseSetupWizardResolvesEnvOnce(t *testing.T) {
	envCodex := filepath.Join(t.TempDir(), "env-codex")
	envClaude := filepath.Join(t.TempDir(), "env-claude")
	explicit := filepath.Join(t.TempDir(), "explicit")
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("CODEX_HOME", envCodex)
	t.Setenv("CLAUDE_CONFIG_DIR", envClaude)
	t.Setenv("HOME", home)
	req, _, err := parseSetupWizard([]string{"--action", "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if req.CodexHome != "" || req.ClaudeConfig != "" {
		t.Fatalf("parse treated env as explicit: %+v", req)
	}
	if req.EnvCodexHome != envCodex || req.EnvClaudeConfig != envClaude {
		t.Fatalf("env snapshot: %+v", req)
	}
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "later"))
	if req.EnvCodexHome != envCodex {
		t.Fatal("parsed request reread env")
	}
	flagged, _, err := parseSetupWizard([]string{"--action", "inspect", "--codex-home", explicit})
	if err != nil {
		t.Fatal(err)
	}
	if flagged.CodexHome != explicit {
		t.Fatalf("explicit lost: %+v", flagged)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	empty, _, err := parseSetupWizard([]string{"--action", "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.CodexHome != "" || empty.ClaudeConfig != "" || empty.EnvCodexHome != "" || empty.EnvClaudeConfig != "" {
		t.Fatalf("HOME used as profile fallback: %+v", empty)
	}
}

func TestParseSetupWizardIgnoresRelativeEnvProfiles(t *testing.T) {
	t.Setenv("CODEX_HOME", "relative-codex")
	t.Setenv("CLAUDE_CONFIG_DIR", "relative-claude")
	t.Setenv("HOME", t.TempDir())
	explicit := filepath.Join(t.TempDir(), "explicit")
	for _, args := range [][]string{
		{"--action", "inspect"},
		{"--action", "inspect", "--codex-home", explicit, "--claude-config", explicit},
	} {
		req, _, err := parseSetupWizard(args)
		if err != nil {
			t.Fatalf("relative snapshots blocked %v: %v", args, err)
		}
		if req.EnvCodexHome != "" || req.EnvClaudeConfig != "" {
			t.Fatalf("relative snapshot or HOME fallback retained: %+v", req)
		}
		if len(args) > 2 && (req.CodexHome != explicit || req.ClaudeConfig != explicit) {
			t.Fatalf("explicit profiles lost: %+v", req)
		}
	}
}

func TestParseSetupWizardCleansEnvSnapshotsKeepsExplicitPathsStrict(t *testing.T) {
	envCodex := filepath.Join(t.TempDir(), "env-codex")
	envClaude := filepath.Join(t.TempDir(), "env-claude")
	t.Setenv("CODEX_HOME", envCodex+string(filepath.Separator))
	t.Setenv("CLAUDE_CONFIG_DIR", envClaude+string(filepath.Separator))
	req, _, err := parseSetupWizard([]string{"--action", "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if req.EnvCodexHome != envCodex || req.EnvClaudeConfig != envClaude || req.CodexHome != "" || req.ClaudeConfig != "" {
		t.Fatalf("trailing separator snapshots: %+v", req)
	}
	for _, flag := range []string{"package", "plugin-root", "control-root", "runtime-root", "global-config", "codex-home", "claude-config", "client-executable", "helper", "scope-root", "mcp-config", "claude-mcp-config", "claude-executable", "codex-executable"} {
		for _, bad := range []string{"relative", envCodex + string(filepath.Separator), envCodex + string(filepath.Separator) + ".." + string(filepath.Separator) + "other"} {
			if _, _, err := parseSetupWizard([]string{"--action", "inspect", "--" + flag, bad}); err == nil {
				t.Fatalf("accepted noncanonical explicit --%s %q", flag, bad)
			}
		}
	}
}

func TestQuoteWizardArgsQuotesPathsWithSpaces(t *testing.T) {
	got := quoteWizardArgs([]string{"setup-notifications", "wizard", "--codex-home", `/tmp/codex home`, "--yes"})
	if len(got) != 5 || got[3] != `'/tmp/codex home'` || got[4] != "'--yes'" {
		t.Fatalf("quoted: %#v", got)
	}
}

func TestReportAgentNotifySetupFailureQuotesCodexHome(t *testing.T) {
	var buf bytes.Buffer
	reportAgentNotifySetupFailure(&buf, "codex", []string{"--codex-home", `/tmp/codex home`, "--navigation", "none"})
	got := buf.String()
	if !strings.Contains(got, `Retry:`) || !strings.Contains(got, `'/tmp/codex home'`) {
		t.Fatalf("unquoted retry: %s", got)
	}
	if strings.Contains(got, "configure --provider codex --codex-home /tmp/codex home --navigation") {
		t.Fatal("space path split in retry", got)
	}
}

func TestQuoteWizardArgsBashRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows retry commands use PowerShell quoting")
	}
	want := []string{"plain", "spaces here", "dollar$sign", "apostrophe's", `back\\slash`, "$(touch SHOULD_NOT_EXIST) ; & |"}
	quoted := strings.Join(quoteWizardArgs(want), " ")
	cmd := exec.Command("bash")
	cmd.Stdin = strings.NewReader("set -- " + quoted + `; printf '%s\0' "$@"`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestQuotePowerShellArgsPreservesMetacharacters(t *testing.T) {
	want := []string{`C:\Program Files\Claude\claude.exe`, `dollar$sign`, "apostrophe's", "back`tick", `$(no-expand)`}
	got := quotePowerShellArgs(want)
	wantQuoted := []string{`& 'C:\Program Files\Claude\claude.exe'`, `'dollar$sign'`, `'apostrophe''s'`, "'back`tick'", "'$(no-expand)'"}
	if !reflect.DeepEqual(got, wantQuoted) {
		t.Fatalf("PowerShell quoting = %#v, want %#v", got, wantQuoted)
	}
}

func TestWizardPrintableCommandIncludesExecutable(t *testing.T) {
	got := wizardPrintableCommand([]string{"setup-notifications", "wizard", "--yes"})
	if len(got) != 4 || got[0] == "setup-notifications" || got[1] != "setup-notifications" {
		t.Fatalf("printable command = %#v", got)
	}
}

// Regression: a fresh selected caller requires predecessor ownership, computes
// a different selector than public Complete/Filename, or binds the vendor/future
// executable in place of the live ledger-owned observer. No agent is executed.
func TestCursorCallerInertReservation(t *testing.T) {
	ctx := setupCommandContext(t)
	root := setupCommandRoot(t)
	control, runtimeRoot := filepath.Join(root, "control"), filepath.Join(root, "runtime")
	observer := filepath.Join(runtimeRoot, filepath.FromSlash(portable.PlatformPrimary()))
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "TEST-existing", Files: []installruntime.File{{Path: observer, Data: []byte("TEST-inert-observer" + installruntime.WriterProtocolMarker), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	pkg, profile := filepath.Join(root, "TEST-package"), filepath.Join(root, "TEST-profile")
	for _, path := range []string{pkg, profile} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"name":"agent-notify"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req, _, err := parseSetupWizard([]string{"--action", "install", "--agents", "cursor", "--scope-root", profile, "--client-executable", filepath.Join(root, "TEST-vendor"), "--package", pkg, "--control-root", control, "--global-config", filepath.Join(root, "TEST-config.json"), "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	got, cfg, previous, _, err := reserveCursorCaller(ctx, req, snapshot)
	if err != nil || previous || got.CursorAuthority == nil {
		t.Fatalf("fresh: %+v %t %v", got, previous, err)
	}
	fixed := got.CursorAuthority
	physical := domain.ComputePhysicalArtifactID("agent-notify", got.InstallationID)
	binding, err := portablesetup.Complete(portablesetup.Identity{InstallationID: got.InstallationID, ComponentID: snapshot.Ledger.ID, Owner: snapshot.Ledger.Owner, ScopeRoot: profile, ControlRoot: control, GlobalConfig: got.GlobalConfig, RuntimeRoot: runtimeRoot, Primary: got.Primary}, portable.Cursor, "cursor", string(domain.ScopeUser), filepath.Join(profile, "plugins", "local", physical), filepath.Join(cfg.PluginDataBase, physical))
	if err != nil {
		t.Fatal(err)
	}
	name, err := binding.Filename()
	if err != nil || fixed.Selector != filepath.Join(binding.DataRoot, name) || got.BindingIDs["cursor"] != binding.BindingID || fixed.ObjectID != "cursor-user-stop-v1" || fixed.Executable != observer || fixed.ExecutableDigest != "sha256:"+snapshot.Ledger.Files[observer].SHA256 || got.ClientExecutables["cursor"] == observer {
		t.Fatalf("reservation: %+v %+v %v", got, binding, err)
	}
	for _, helper := range []string{req.ClientExecutable, filepath.Join(runtimeRoot, "future-helper")} {
		bad := got
		bad.Helper = helper
		if _, _, _, _, err := reserveCursorCaller(ctx, bad, snapshot); err == nil {
			t.Fatal("unowned observer admitted", helper)
		}
	}
	if err := os.WriteFile(observer, []byte("TEST-replaced-observer"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := reserveCursorCaller(ctx, got, snapshot); err == nil {
		t.Fatal("changed observer admitted")
	}
	if _, err := os.Stat(filepath.Dir(cfg.StateRoot)); !os.IsNotExist(err) {
		t.Fatalf("inert reservation wrote UAP state: %v", err)
	}
}

// Regression: a direct Cursor request reaches installer mutation using only a
// vendor path, or omission/explicit opt-out tries to allocate selected authority.
func TestCursorCallerRefusalAndOptOut(t *testing.T) {
	root := setupCommandRoot(t)
	flags := []string{"--action", "install", "--agents", "cursor", "--scope-root", root, "--client-executable", filepath.Join(root, "TEST-vendor"), "--control-root", filepath.Join(root, "control"), "--yes", "--json"}
	var out bytes.Buffer
	if code := executeSetupWizardWith(setupCommandContext(t), flags, &out, io.Discard, strings.NewReader(""), false); code != 1 || !strings.Contains(out.String(), "cursor_composition_required") {
		t.Fatalf("refusal: %d %s", code, out.String())
	}
	req, _, err := parseSetupWizard(flags)
	if err != nil {
		t.Fatal(err)
	}
	off := false
	req.AgentNotify = &off
	got, err := composeCursorWizard(setupCommandContext(t), req)
	if err != nil || got.CursorAuthority != nil {
		t.Fatalf("opt-out: %+v %v", got, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("refusal wrote files: %v %v", entries, err)
	}
}

// Directory timestamps observe even a transient staging/snapshot creation and
// removal. Durable bytes alone would miss that rejected admission performed work.
func assertCursorPackageRefusal(t *testing.T, control string, sources []string, refuse func()) {
	t.Helper()
	uap := filepath.Join(filepath.Dir(control), "uap")
	for _, dir := range []string{filepath.Join(uap, "state", "tmp"), filepath.Join(uap, "acquired-source")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(1234567890, 0)
		if err := os.Chtimes(dir, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	capture := func() map[string]string {
		result := map[string]string{}
		for _, root := range append([]string{control, uap}, sources...) {
			err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				value := fmt.Sprint(info.Mode())
				if info.IsDir() {
					value += fmt.Sprint(info.ModTime().UnixNano())
				} else if info.Mode().IsRegular() {
					body, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					value += string(body)
				} else if info.Mode()&os.ModeSymlink != 0 {
					target, err := os.Readlink(path)
					if err != nil {
						return err
					}
					value += target
				}
				result[path] = value
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	before := capture()
	refuse()
	after := capture()
	if !reflect.DeepEqual(before, after) {
		for path, value := range after {
			if before[path] != value {
				t.Errorf("refusal mutated package/state or transient staging directory: %s", path)
			}
		}
		for path := range before {
			if _, ok := after[path]; !ok {
				t.Errorf("refusal cleaned source/state: %s", path)
			}
		}
	}
}
