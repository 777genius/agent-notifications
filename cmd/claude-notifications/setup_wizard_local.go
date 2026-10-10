package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

func localWizardConfig(settings string) *vscode.LocalConfig {
	tuple := vscode.QualifiedDarwinTESTTuple()
	return &vscode.LocalConfig{ProfileSettingsPath: settings, QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)}}
}

func sameLocalWizardSelection(a, b setupwizard.Request) bool {
	if a.ScopeRoot != b.ScopeRoot || a.LocalConfig == nil || b.LocalConfig == nil {
		return false
	}
	x, y := *a.LocalConfig, *b.LocalConfig
	x.HookSpecs, y.HookSpecs = nil, nil
	x.DeclaredHookDigest, y.DeclaredHookDigest = "", ""
	return reflect.DeepEqual(x, y)
}

// Select registration before any physical profile/helper lookup. An ambiguous
// historical consumer never resolves by sorting or by current filesystem state.
func recordedLocalRequest(r setupwizard.Request) (portable.Binding, bool, error) {
	ledger, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery {
		return portable.Binding{}, false, setupwizard.ErrRefused
	}
	var selected portable.Binding
	found := false
	for key, consumer := range ledger.Consumers {
		var b portable.Binding
		if len(key) < 9 || key[:9] != "portable:" {
			continue
		}
		if json.Unmarshal([]byte(consumer.Registration), &b) != nil {
			return selected, false, portable.ErrInvalid
		}
		if b.Integration != portable.CopilotVSCode || b.ScopeRoot != r.ScopeRoot || r.InstallationID != "" && b.InstallationID != r.InstallationID {
			continue
		}
		if found || b.ControlRoot != r.ControlRoot || b.ComponentID != ledger.ID || b.Owner != ledger.Owner || b.RuntimeRoot != ledger.RuntimeRoot || !portable.ExactCommittedBinding(ledger, b) || r.BindingIDs["copilot-vscode"] != "" && b.BindingID != r.BindingIDs["copilot-vscode"] {
			return selected, false, portable.ErrInvalid
		}
		selected, found = b, true
	}
	return selected, found, nil
}

// Frozen callers use the original generation AND policy bytes. The public
// convenience revoker cannot adopt a newer decision on their behalf.
func revokeRecordedLocal(ctx context.Context, b portable.Binding, selection copilotvscodeinstall.RevokeSelection, generation *uint64, policy *installruntime.Identity) (installruntime.Ledger, installruntime.Identity, error) {
	s, err := installruntime.ReadRevocationSnapshot(ctx, b.ControlRoot)
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	if generation != nil && *generation != s.Generation || policy != nil && *policy != s.Preimage {
		return installruntime.Ledger{}, installruntime.Identity{}, portablesetup.ErrConcurrentChange
	}
	var patch string
	switch selection {
	case copilotvscodeinstall.RevokeAll:
		patch = `{"copilotVSCodeNotifications":{"desktop":false,"webhook":false,"manual":{"enabled":false}}}`
	case copilotvscodeinstall.RevokeNative:
		patch = `{"copilotVSCodeNotifications":{"desktop":false,"webhook":false}}`
	case copilotvscodeinstall.RevokeManual:
		patch = `{"copilotVSCodeNotifications":{"manual":{"enabled":false}}}`
	default:
		return installruntime.Ledger{}, installruntime.Identity{}, copilotvscodeinstall.ErrDenied
	}
	fields := map[string]json.RawMessage{"route": json.RawMessage(patch)}
	after, err := installruntime.PredictPolicyIdentity(b.ControlRoot, s.Preimage, fields)
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, RevokeCopilotVSCode: true, ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: fields})
	return ledger, after, err
}

func localEngineConfig(control string) uapinstaller.Config {
	root := filepath.Join(filepath.Dir(control), "uap")
	state := filepath.Join(root, "state")
	return uapinstaller.Config{StateRoot: state, StateFile: filepath.Join(state, "state-v2.json"), LockFile: filepath.Join(state, "mutation.lock"), OperationsDir: filepath.Join(state, "operations"), PluginDataBase: filepath.Join(root, "plugin-data"), ManagedRoot: filepath.Join(root, "managed"), TempRoot: filepath.Join(state, "tmp"), TrustedLocalPackages: true}
}

func restoreRecordedLocal(r setupwizard.Request, b portable.Binding) (setupwizard.Request, error) {
	state, err := (statev2.Store{Path: localEngineConfig(b.ControlRoot).StateFile}).Load()
	if err != nil {
		return r, err
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != b.InstallationID {
			continue
		}
		record, ok := installation.Clients[b.BindingID]
		facts, local := record.SelectedDelivery.LocalFacts()
		if !ok || !local || record.ClientID != string(domain.ClientVSCode) || record.ClientBindingID != b.BindingID || facts.ProfileRoot != b.ScopeRoot || facts.SettingsPath != filepath.Join(b.ScopeRoot, "settings.json") {
			return r, setupwizard.ErrRefused
		}
		recorded := vscode.LocalConfig{ProfileSettingsPath: facts.SettingsPath, QualifiedTuple: facts.Tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(facts.Tuple.TargetShell)}, NativeStop: facts.NativeStop, MCPServers: facts.MCPServers, Skills: facts.Skills}
		if r.LocalConfig == nil {
			r.LocalConfig = &recorded
		}
		r.InstallationID = b.InstallationID
		if r.BindingIDs == nil {
			r.BindingIDs = map[string]string{}
		}
		r.BindingIDs["copilot-vscode"] = b.BindingID
		return r, nil
	}
	return r, setupwizard.ErrRefused
}

func prepareLocalWizard(ctx context.Context, r setupwizard.Request, confirmed *confirmedBootstrapIntent) (setupwizard.Request, *setupwizard.Result, error) {
	if r.LocalConfig != nil {
		cp := *r.LocalConfig
		r.LocalConfig = &cp
	}
	fail := func(reason string, err error) (setupwizard.Request, *setupwizard.Result, error) {
		return r, &setupwizard.Result{Action: string(r.Action), Outcome: "incomplete", Reason: reason}, err
	}
	b, found, err := recordedLocalRequest(r)
	if err != nil {
		return fail("local_identity_unavailable", err)
	}
	ledger, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery {
		return fail("pending_setup_required", setupwizard.ErrRefused)
	}
	if ledger.PendingMutation != nil {
		intent, e := portablesetup.ReadIntent(r.ControlRoot)
		if e != nil || ledger.PendingMutation.Owner != "existing-installer" || ledger.PendingMutation.IntentRef != portablesetup.IntentPath(r.ControlRoot) || ledger.PendingMutation.ID != intent.SetupIntentID {
			return fail("pending_setup_required", setupwizard.ErrRefused)
		}
		r, e = setupwizard.RestoreLocalIntentScope(r, intent)
		if e != nil {
			return fail("local_intent_changed", e)
		}
		if r.ScopeRoot != "" && r.ScopeRoot != intent.Targets[0].Profile {
			return fail("local_intent_changed", portablesetup.ErrIntentConflict)
		}
		r.ScopeRoot = intent.Targets[0].Profile
	}
	manualOff := r.AgentNotify != nil && !*r.AgentNotify
	nativeOff := r.LocalConfig != nil && !r.LocalConfig.NativeStop
	if ledger.PendingMutation != nil && (confirmed != nil || manualOff && r.LocalConfig == nil && r.Action != setupwizard.ActionUninstall) {
		return fail("pending_setup_required", setupwizard.ErrRefused)
	}
	if confirmed != nil {
		s, e := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
		if e != nil || s.Installation.Ledger.Generation != confirmed.Initial.Generation || s.Preimage != confirmed.Initial.Policy {
			return fail("concurrent_change", portablesetup.ErrConcurrentChange)
		}
	}
	if ledger.PendingMutation == nil && found && r.Action != setupwizard.ActionInspect && (r.Action == setupwizard.ActionUninstall || manualOff || nativeOff) {
		if !r.Yes {
			return fail("confirmation_required", setupwizard.ErrRefused)
		}
		selection := copilotvscodeinstall.RevokeNative
		if r.Action == setupwizard.ActionUninstall || manualOff && nativeOff {
			selection = copilotvscodeinstall.RevokeAll
		} else if manualOff {
			selection = copilotvscodeinstall.RevokeManual
		}
		next, policy, e := revokeRecordedLocal(ctx, b, selection, r.BootstrapExpectedGeneration, r.BootstrapExpectedPolicy)
		if e != nil {
			return fail("local_revocation_failed", e)
		}
		generation := next.Generation
		r.BootstrapExpectedGeneration, r.BootstrapExpectedPolicy = &generation, &policy
		if manualOff && r.LocalConfig == nil && r.Action != setupwizard.ActionUninstall {
			return r, &setupwizard.Result{Action: string(r.Action), Outcome: "completed", InstallationID: b.InstallationID, Generation: generation}, nil
		}
	}
	if ledger.PendingMutation != nil && r.Action == setupwizard.ActionUninstall {
		frozen, e := installruntime.ReadRevocationSnapshot(ctx, r.ControlRoot)
		if e != nil {
			return fail("local_revocation_unavailable", e)
		}
		body, e := os.ReadFile(filepath.Join(r.ControlRoot, "agent-notifications.json"))
		var doc struct {
			Route struct {
				Local struct {
					Desktop, Webhook *bool
					Manual           struct{ Enabled *bool }
				} `json:"copilotVSCodeNotifications"`
			}
		}
		if e != nil || json.Unmarshal(body, &doc) != nil || doc.Route.Local.Desktop == nil || *doc.Route.Local.Desktop || doc.Route.Local.Webhook == nil || *doc.Route.Local.Webhook || doc.Route.Local.Manual.Enabled == nil || *doc.Route.Local.Manual.Enabled {
			return fail("local_revocation_required", setupwizard.ErrRefused)
		}
		current, e := installruntime.ReadRevocationSnapshot(ctx, r.ControlRoot)
		if e != nil || current != frozen {
			return fail("concurrent_change", portablesetup.ErrConcurrentChange)
		}
		if r.BootstrapExpectedGeneration != nil && *r.BootstrapExpectedGeneration != frozen.Generation || r.BootstrapExpectedPolicy != nil && *r.BootstrapExpectedPolicy != frozen.Preimage {
			return fail("concurrent_change", portablesetup.ErrConcurrentChange)
		}
		r.BootstrapExpectedGeneration, r.BootstrapExpectedPolicy = &frozen.Generation, &frozen.Preimage
	}
	if r.Action == setupwizard.ActionInspect {
		return r, nil, nil
	}
	if found {
		r, err = restoreRecordedLocal(r, b)
		if err != nil {
			return fail("local_record_unavailable", err)
		}
	}
	if r.LocalConfig == nil || r.LocalConfig.ProfileSettingsPath != filepath.Join(r.ScopeRoot, "settings.json") || r.LocalConfig.QualifiedTuple != vscode.QualifiedDarwinTESTTuple() {
		return fail("local_selection_required", setupwizard.ErrRefused)
	}
	policySnapshot, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	snap := policySnapshot.Installation
	if err == nil && (r.BootstrapExpectedGeneration != nil && *r.BootstrapExpectedGeneration != snap.Ledger.Generation || r.BootstrapExpectedPolicy != nil && *r.BootstrapExpectedPolicy != policySnapshot.Preimage) {
		return fail("concurrent_change", portablesetup.ErrConcurrentChange)
	}
	if err == nil {
		generation, preimage := snap.Ledger.Generation, policySnapshot.Preimage
		r.BootstrapExpectedGeneration, r.BootstrapExpectedPolicy = &generation, &preimage
	}
	if err != nil || snap.Recovery {
		return fail("local_runtime_unavailable", setupwizard.ErrRefused)
	}
	primary := r.Primary
	if primary == "" {
		primary = portable.PlatformPrimary()
	}
	helper, err := portable.ResolvePrimaryExecutable(snap.Ledger, primary)
	if err != nil || r.Helper != "" && r.Helper != helper {
		return fail("local_helper_unavailable", setupwizard.ErrRefused)
	}
	r.Helper, r.Primary, r.RuntimeRoot = helper, primary, snap.Ledger.RuntimeRoot
	if found && (b.Primary != primary || b.RuntimeRoot != r.RuntimeRoot || r.GlobalConfig != "" && b.GlobalConfig != r.GlobalConfig) {
		return fail("local_rebind_required", setupwizard.ErrRefused)
	}
	if !r.LocalConfig.NativeStop {
		r.LocalConfig.HookSpecs = nil
		r.LocalConfig.DeclaredHookDigest = ""
		if r.PackageRoot != "" {
			body, e := os.ReadFile(filepath.Join(r.PackageRoot, filepath.FromSlash(vscodelocalhooks.PluginPath)))
			if e == nil {
				r.LocalConfig.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
			} else if !os.IsNotExist(e) {
				return fail("local_canonical_hook_unavailable", e)
			}
		}
	}
	if r.LocalConfig.NativeStop {
		r.LocalConfig.HookSpecs = copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: r.ControlRoot, BindingID: "reserved"}, helper)
		body, e := vscodelocalhooks.Render(r.LocalConfig.TargetShell, r.LocalConfig.HookSpecs)
		if e != nil {
			return fail("local_hook_unavailable", e)
		}
		r.LocalConfig.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	}
	adapter, err := vscode.NewLocal(*r.LocalConfig)
	if err != nil {
		return fail("local_adapter_unavailable", err)
	}
	registry, err := clients.NewRegistry(adapter)
	if err != nil {
		return fail("local_adapter_unavailable", err)
	}
	cfg := localEngineConfig(r.ControlRoot)
	cfg.Registry = registry
	cfg.HelperExecutable = helper
	engine, err := uapinstaller.New(cfg)
	if err != nil {
		return fail("local_engine_unavailable", err)
	}
	reserved, err := engine.ReserveIdentity(uapinstaller.IdentityRequest{ClientID: string(domain.ClientVSCode), ClientConfigRoot: r.ScopeRoot, InstallationID: r.InstallationID, Allocate: r.Action == setupwizard.ActionInstall && ledger.PendingMutation == nil, DeclaredName: cursorCallerDeclaredName(r.PackageRoot)})
	if err != nil {
		return fail("local_identity_unavailable", err)
	}
	if found && r.Action != setupwizard.ActionUninstall {
		if err := engine.VerifyProfileAuthority(ctx, b.InstallationID, b.BindingID); err != nil {
			return fail("local_original_authority_unavailable", err)
		}
	}
	r.InstallationID = reserved.InstallationID
	if r.BindingIDs == nil {
		r.BindingIDs = map[string]string{}
	}
	if r.BindingIDs["copilot-vscode"] != "" && r.BindingIDs["copilot-vscode"] != reserved.BindingID {
		return fail("local_identity_changed", portablesetup.ErrConcurrentChange)
	}
	r.BindingIDs["copilot-vscode"] = reserved.BindingID
	if r.LocalConfig.NativeStop {
		r.LocalConfig.HookSpecs = copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: r.ControlRoot, BindingID: reserved.BindingID}, helper)
		body, e := vscodelocalhooks.Render(r.LocalConfig.TargetShell, r.LocalConfig.HookSpecs)
		if e != nil {
			return fail("local_hook_unavailable", e)
		}
		r.LocalConfig.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	}
	return r, nil, nil
}

func commitConfirmedLocalConsent(ctx context.Context, r setupwizard.Request, result setupwizard.Result, i confirmedBootstrapIntent) (setupwizard.Result, error) {
	deny := func(err error) (setupwizard.Result, error) {
		result.Outcome, result.Reason = "incomplete", "local_consent_unavailable"
		return result, err
	}
	if result.Outcome != "completed" && result.Outcome != "unchanged" || result.InstallationID == "" {
		return deny(setupwizard.ErrRefused)
	}
	if !i.Request.DesktopSet && !i.Request.WebhookSet && i.Request.Manual == nil {
		return result, nil
	}
	r.InstallationID = result.InstallationID
	b, found, err := recordedLocalRequest(r)
	if err != nil || !found {
		return deny(setupwizard.ErrRefused)
	}
	acknowledged := false
	for _, target := range result.Targets {
		if target.Client == "copilot-vscode" && target.Unit == "agent-notify" && target.Outcome == "completed" && target.Reason == b.BindingID {
			acknowledged = true
		}
	}
	if !acknowledged {
		return deny(setupwizard.ErrRefused)
	}
	expectedGeneration := result.Generation
	expectedPolicy := i.Initial.Policy
	if r.BootstrapExpectedPolicy != nil {
		expectedPolicy = *r.BootstrapExpectedPolicy
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
	if err != nil {
		return deny(err)
	}
	if s.Installation.Recovery || s.Installation.Ledger.PendingMutation != nil || s.Installation.Ledger.ID != i.Initial.LedgerID || s.Installation.Ledger.Owner != i.Initial.Owner || s.Installation.Ledger.Generation != expectedGeneration || s.Preimage != expectedPolicy {
		return deny(portablesetup.ErrConcurrentChange)
	}
	pinned, release, err := installruntime.AcquirePolicyLease(ctx, b.ControlRoot, s)
	if err != nil {
		return deny(err)
	}
	nativeEnable := i.Request.DesktopSet && i.Request.Desktop || i.Request.WebhookSet && i.Request.Webhook
	if nativeEnable {
		var gate copilotvscodeinstall.Gate
		gate, _, _, err = copilotvscodeinstall.NewLocalGateFromSnapshot(ctx, b, localEngineConfig(b.ControlRoot), pinned)
		if err == nil {
			binding, e := gate.ConsumerBindingFromSnapshot(ctx, pinned)
			err = e
			if e == nil && binding.Generation != expectedGeneration {
				err = portablesetup.ErrConcurrentChange
			}
		}
	} else {
		err = copilotvscodeinstall.QualifyLocalConsentFromSnapshot(ctx, b, localEngineConfig(b.ControlRoot), pinned, i.Request.Manual != nil && *i.Request.Manual)
	}
	previous, consentErr := copilotvscodeinstall.ReadConsent(pinned, b)
	release()
	if err != nil {
		return deny(err)
	}
	if consentErr != nil {
		return deny(consentErr)
	}
	choices := copilotvscodeinstall.Choices{Manual: i.Request.Manual}
	if i.Request.DesktopSet {
		choices.Desktop = &i.Request.Desktop
	}
	if i.Request.WebhookSet {
		choices.Webhook = &i.Request.Webhook
	}
	patch, err := copilotvscodeinstall.PolicyPatch(b, choices, &previous)
	if err != nil {
		return deny(err)
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		return deny(err)
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &expectedGeneration, ExpectedPolicy: &expectedPolicy, PolicyFields: patch})
	if err != nil {
		return deny(err)
	}
	result.Generation = ledger.Generation
	return result, nil
}
