package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/desktopinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/cursor"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

func admitBootstrapWizardRequest(path string, r setupwizard.Request) (setupwizard.Request, error) {
	r, _, err := admitBootstrapWizardIntent(path, r)
	return r, err
}
func admitBootstrapWizardIntent(path string, r setupwizard.Request) (setupwizard.Request, *confirmedBootstrapIntent, error) {
	provenance, err := currentSelectorProvenance()
	if err != nil {
		return r, nil, err
	}
	provenance.Stage = []byte(filepath.Dir(path))
	intent, err := loadBootstrapIntent(path, provenance)
	if err != nil {
		return r, nil, err
	}
	parent := intent
	intent, err = bootstrapWizardChild(parent, r.Agents)
	if err != nil {
		return r, nil, err
	}
	expected := intentWizardRequest(intent)
	if !sameWizardBootstrapScope(r, expected) || intent.Request.SkipAgentNotify && !containsProduct(r.Agents, "pi") {
		return r, nil, errors.New("bootstrap request differs from confirmed intent")
	}
	if containsProduct(r.Agents, "cursor") {
		checkpoint, cancel := context.WithTimeout(context.Background(), bootstrapInspectionTimeout)
		defer cancel()
		intent, err = admitCursorBootstrapChild(checkpoint, parent, intent, r)
		if err != nil {
			return r, nil, err
		}
	}
	r.BootstrapMCP = &intent.MCP
	r.BootstrapStateMigrationOwned = true
	if containsProduct(intent.Request.Products, "cursor") || containsProduct(intent.Request.Products, "copilot-vscode") || containsProduct(intent.Request.Products, "pi") {
		generation, policy := intent.Initial.Generation, intent.Initial.Policy
		r.BootstrapExpectedGeneration, r.BootstrapExpectedPolicy = &generation, &policy
	}
	r.EnvClaudeConfig, r.EnvCodexHome, r.EnvPiAgentDir = "", "", ""
	return r, &intent, nil
}

// Keep exact registration spelling unless Windows confirms a case-only variant
// names the same directory. An alias or unresolved path grants no authority.
func sameCursorScopeRoot(recorded, selected string) bool {
	if recorded == selected {
		return true
	}
	if runtime.GOOS != "windows" || !strings.EqualFold(recorded, selected) {
		return false
	}
	a, err := os.Stat(recorded)
	if err != nil || !a.IsDir() {
		return false
	}
	b, err := os.Stat(selected)
	return err == nil && b.IsDir() && os.SameFile(a, b)
}

// Record selection uses immutable registration bytes, even when physical assets
// are replaced. It neither allocates a binding nor chooses a sorted consumer.
func recordedCursorRequest(r setupwizard.Request) (portable.Binding, bool, error) {
	ledger, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery || ledger.PendingMutation != nil {
		return portable.Binding{}, false, setupwizard.ErrRefused
	}
	var selected portable.Binding
	found := false
	for key, consumer := range ledger.Consumers {
		if !strings.HasPrefix(key, "portable:") {
			continue
		}
		var b portable.Binding
		if json.Unmarshal([]byte(consumer.Registration), &b) != nil {
			return selected, false, portable.ErrInvalid
		}
		if b.Integration != portable.Cursor || !sameCursorScopeRoot(b.ScopeRoot, r.ScopeRoot) || r.InstallationID != "" && b.InstallationID != r.InstallationID {
			continue
		}
		if found || b.ControlRoot != r.ControlRoot || b.ComponentID != ledger.ID || b.Owner != ledger.Owner || b.RuntimeRoot != ledger.RuntimeRoot || !portable.ExactCommittedBinding(ledger, b) || r.BindingIDs["cursor"] != "" && b.BindingID != r.BindingIDs["cursor"] {
			return selected, false, portable.ErrInvalid
		}
		selected, found = b, true
	}
	return selected, found, nil
}

func fenceCursorIntent(ctx context.Context, r setupwizard.Request, i confirmedBootstrapIntent) error {
	policy, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return err
	}
	l := policy.Installation.Ledger
	if policy.Installation.Recovery || l.PendingMutation != nil || l.ID != i.Initial.LedgerID || l.Owner != i.Initial.Owner || l.Generation != i.Initial.Generation || policy.Preimage != i.Initial.Policy {
		return portablesetup.ErrConcurrentChange
	}
	observation := r
	off := false
	observation.CursorAgentNotify = &off
	current, generation, err := setupwizard.ObserveBootstrapMCP(ctx, observation)
	if err != nil {
		return err
	}
	if generation != i.Initial.Generation {
		return portablesetup.ErrConcurrentChange
	}
	return setupwizard.CheckBootstrapMCP(i.MCP, current)
}

// Use policy-only observation before physical cleanup, retaining any admitted
// original CAS. The predicted output is meaningful only after successful Commit.
func revokeRecordedCursor(ctx context.Context, b portable.Binding, generation *uint64, policy *installruntime.Identity) (installruntime.Ledger, installruntime.Identity, error) {
	s, err := installruntime.ReadRevocationSnapshot(ctx, b.ControlRoot)
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	if generation != nil && *generation != s.Generation || policy != nil && *policy != s.Preimage {
		return installruntime.Ledger{}, installruntime.Identity{}, portablesetup.ErrConcurrentChange
	}
	off := false
	patch, err := copilotvscodeinstall.CursorPolicyPatch(b, copilotvscodeinstall.CursorChoices{Desktop: &off, Webhook: &off}, nil)
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	after, err := installruntime.PredictPolicyIdentity(b.ControlRoot, s.Preimage, patch)
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, RevokeCursor: true, ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: patch})
	if err != nil {
		return ledger, installruntime.Identity{}, err
	}
	return ledger, after, nil
}

func commitConfirmedCursorConsent(ctx context.Context, r setupwizard.Request, result setupwizard.Result, i confirmedBootstrapIntent) (setupwizard.Result, error) {
	deny := func(err error) (setupwizard.Result, error) {
		result.Outcome, result.Reason = "incomplete", "cursor_consent_unavailable"
		return result, err
	}
	if result.Outcome == "cancelled" || result.InstallationID == "" {
		return deny(setupwizard.ErrRefused)
	}
	for _, next := range result.NextActions {
		switch next.Kind {
		case "restart-client", "request-permission", "test-notification":
		default:
			return deny(setupwizard.ErrRefused)
		}
	}
	r.InstallationID = result.InstallationID
	b, found, err := recordedCursorRequest(r)
	if err != nil || !found {
		return deny(setupwizard.ErrRefused)
	}
	acknowledged := false
	for _, target := range result.Targets {
		if target.Client == "cursor" && target.Unit == "agent-notify" && target.Outcome == "completed" && target.Reason == b.BindingID {
			acknowledged = true
		}
	}
	if !acknowledged {
		return deny(setupwizard.ErrRefused)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return deny(err)
	}
	policy := i.Initial.Policy
	if r.BootstrapExpectedPolicy != nil {
		policy = *r.BootstrapExpectedPolicy
	}
	if s.Installation.Recovery || s.Installation.Ledger.PendingMutation != nil || s.Installation.Ledger.ID != i.Initial.LedgerID || s.Installation.Ledger.Owner != i.Initial.Owner || s.Installation.Ledger.Generation != result.Generation || s.Preimage != policy {
		return deny(portablesetup.ErrConcurrentChange)
	}
	// The fresh Cursor path has no legacy configure phase. Initialize only the
	// bound canonical document before physical qualification; the shared writer
	// preserves an existing document and grants no channel consent.
	env := config.SnapshotEnv()
	env.Vars = map[string]string{config.OverrideEnv: b.GlobalConfig}
	initialized, err := config.EnsureInitialized(ctx, config.InitRequest{Env: env, Assets: config.AssetContext{Agent: config.AgentCursor, PluginRoot: b.RuntimeRoot}})
	if err != nil || initialized.Selection.Path != b.GlobalConfig {
		return deny(setupwizard.ErrRefused)
	}
	cfg, fixed, err := cursorInstalledInputs(ctx, b, s.Installation, filepath.Join(filepath.Dir(b.ControlRoot), "uap", "state"))
	if err != nil {
		return deny(err)
	}
	gate, err := copilotvscodeinstall.NewCursorGate(b, cfg, fixed)
	if err != nil {
		return deny(err)
	}
	binding, err := gate.ConsumerBinding(ctx)
	if err != nil || binding.Generation != result.Generation {
		return deny(setupwizard.ErrRefused)
	}
	previous, err := copilotvscodeinstall.ReadCursorConsent(s, b)
	if err != nil {
		return deny(err)
	}
	choices := bootstrapProductUnits{}
	for _, unit := range i.Units {
		if unit.Product == "cursor" {
			choices = unit
		}
	}
	if runtime.GOOS == "darwin" && choices.Desktop {
		if _, err := copilotvscodeinstall.PrepareCursorNativeSpool(ctx, b.ControlRoot); err != nil {
			return deny(err)
		}
	}
	patch, err := copilotvscodeinstall.CursorPolicyPatch(b, copilotvscodeinstall.CursorChoices{Desktop: &choices.Desktop, Webhook: &choices.Webhook}, &previous)
	if err != nil {
		return deny(err)
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		return deny(err)
	}
	if runtime.GOOS == "windows" && choices.Desktop {
		home, err := os.UserHomeDir()
		if err != nil {
			return deny(err)
		}
		file, err := desktopinstall.StageCursorWindowsShortcut(key, home, fixed.Executable, true, s.Installation.Ledger)
		if err != nil {
			return deny(err)
		}
		if file != nil {
			staged, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, RefreshOnly: true, ExpectedGeneration: &result.Generation, ExpectedPolicy: &policy, Files: []installruntime.File{*file}})
			if err != nil {
				return deny(err)
			}
			result.Generation = staged.Generation
		}
	}
	var seedEnabled *bool
	if i.MCP.AllowPolicySeed && !i.Initial.Policy.Exists {
		// Apply the absent-policy seed shown in the immutable confirmation, after
		// physical qualification. Existing policy and intervening edits stay fenced.
		seedEnabled = &i.MCP.SeedEnabled
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &result.Generation, ExpectedPolicy: &policy, PolicyEnabled: seedEnabled, PolicyFields: patch})
	if err != nil {
		return deny(err)
	}
	result.Generation = ledger.Generation
	for n := range result.NextActions {
		if result.NextActions[n].Kind == "request-permission" {
			result.NextActions[n].Command = []string{"setup-notifications", "request-permission", "--control-root", r.ControlRoot, "--expected-generation", fmt.Sprintf("%d", ledger.Generation)}
		}
	}
	return result, nil
}
func sameWizardBootstrapScope(actual, expected setupwizard.Request) bool {
	if containsProduct(expected.Agents, "pi") {
		if actual.PiAgentDir != expected.PiAgentDir || !samePiChoice(actual.PiDesktop, expected.PiDesktop) || !samePiChoice(actual.PiWebhook, expected.PiWebhook) || actual.PiChannelPreimage != expected.PiChannelPreimage {
			return false
		}
	}
	if containsProduct(expected.Agents, "copilot-vscode") && !sameLocalWizardSelection(actual, expected) {
		return false
	}
	if strings.Join(actual.Agents, ",") != strings.Join(expected.Agents, ",") || actual.ControlRoot != expected.ControlRoot || (!containsProduct(expected.Agents, "cursor") && !containsProduct(expected.Agents, "copilot-vscode") && (actual.ClaudeConfig != expected.ClaudeConfig || actual.CodexHome != expected.CodexHome || actual.PiAgentDir != expected.PiAgentDir)) || containsProduct(expected.Agents, "cursor") && actual.CursorConfig != expected.CursorConfig || (containsProduct(expected.Agents, "cursor") && actual.ScopeRoot != expected.ScopeRoot) || actual.GlobalConfig != expected.GlobalConfig {
		return false
	}
	// A fresh legacy phase chooses its existing managed runtime during hooks
	// preparation. A known runtime authority must remain exact; its own creation
	// is outside the relevant portable binding projection.
	if expected.RuntimeRoot != "" && actual.RuntimeRoot != "" && actual.RuntimeRoot != expected.RuntimeRoot {
		return false
	}
	for _, id := range expected.Agents {
		path := actual.ClientExecutables[id]
		if path == "" {
			path = actual.ClientExecutable
		}
		if path != expected.ClientExecutables[id] || actual.MCPConfig[id] != expected.MCPConfig[id] {
			return false
		}
	}
	return true
}

// Immutable accepted source proof: public selected projection/ack/data, eight
// refusals and sibling/recovery on Ubuntu24.04, 6.17.0-1022-azure, ext4. This
// qualifies no other host/profile and supplies no installed native E or consent.
const cursorP1Qualification = "uap406:run37381034498:job112002773007:public098:commit0983418ab0c2724b5cf648512e0a26baafaad546:sha256:7d305d6cabe775e12083c5c4f49fe9246eb1cb8244d596d03c50aa1e7de670b5"
const selectedCursorVersion = "2026.09.28-64d2043"

// Mac evidence qualifies only the fixed native shell tuple; installed receipt,
// physical profile authority and explicit consent remain mandatory.
func cursorHostQualification() string {
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		return "cursor-windows-amd64-20261001:run38220319890:sha84eda2404918:archive31896ce9a43f63c8ae7ff3c4bee868213d6d02d60fb49908bef6dc91d582f6ee"
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return "cursor-darwin-arm64-20260918:stop-773b7499ccd8afdd5a087be4fe764168455cb80c24caa6b2888cc4608fc472b5:uap-eec70fb9bd5f"
	}
	version, _ := cursorinstall.CurrentTuple()
	if version == selectedCursorVersion {
		kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
		if err == nil && strings.TrimSpace(string(kernel)) == "7.0.0-1017-azure" {
			// Supplemental source qualification; the historical P1 receipt above
			// does not qualify this kernel. See docs/CI_RUNNERS.md.
			return "cursor-linux-amd64-ubuntu24.04.5-kernel7.0.0-1017-azure-ext4:source-ci-pr497"
		}
		return cursorP1Qualification
	}
	return ""
}

func composeCursorWizard(ctx context.Context, req setupwizard.Request) (_ setupwizard.Request, err error) {
	if !containsProduct(req.Agents, "cursor") {
		return req, nil
	}
	if err := ctx.Err(); err != nil {
		return req, err
	}
	if len(req.Agents) != 1 || req.CursorConfig != req.ScopeRoot || !validProductPath(req.ScopeRoot) {
		return req, portable.ErrInvalid
	}
	profile, err := cursor.New().ResolveProfileRoot(req.CursorConfig)
	if err != nil {
		return req, err
	}
	req.ScopeRoot, req.CursorConfig = profile, profile
	if req.BootstrapMCP != nil && !containsProduct(req.BootstrapMCP.Selected, "cursor") {
		off := false
		req.CursorAgentNotify = &off
	}
	if req.CursorAgentNotify != nil && !*req.CursorAgentNotify || req.AgentNotify != nil && !*req.AgentNotify || req.Action == setupwizard.ActionInspect && req.AgentNotify == nil {
		return req, nil
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return req, err
	}
	if snapshot.Recovery || snapshot.Ledger.ID == "" || snapshot.Ledger.Owner != "existing-installer" {
		return req, setupwizard.ErrRefused
	}
	if req.Action == setupwizard.ActionInspect && snapshot.Ledger.PendingMutation == nil {
		observation, cfg, _, _, absentErr := reserveCursorCaller(ctx, req, snapshot)
		if absentErr != nil {
			if !errors.Is(absentErr, uapinstaller.ErrNotInstalled) || req.BindingIDs["cursor"] != "" {
				return req, absentErr
			}
			eng, e := uapinstaller.New(cfg)
			if e != nil {
				return req, e
			}
			view, e := eng.Inspect(ctx)
			if e != nil || view.Recovery.Required {
				return req, setupwizard.ErrRefused
			}
			for _, installation := range view.Installations {
				if req.InstallationID != "" && installation.InstallationID != req.InstallationID {
					return req, absentErr
				}
				for _, binding := range installation.Bindings {
					if binding.ClientID == "cursor" {
						return req, absentErr
					}
				}
			}
			for key, consumer := range snapshot.Ledger.Consumers {
				if !strings.HasPrefix(key, "portable:") {
					continue
				}
				var binding portable.Binding
				if json.Unmarshal([]byte(consumer.Registration), &binding) != nil || binding.Integration == portable.Cursor {
					return req, portable.ErrInvalid
				}
			}
			// The owner reports absence without constructing installed authority.
			// Keep the retained identity and let the wizard report retained data.
			off := false
			observation.CursorAgentNotify = &off
			return observation, nil
		}
	}
	if req.AgentNotify == nil && (req.Action == setupwizard.ActionUpdate || req.Action == setupwizard.ActionRepair) {
		state, e := (statev2.Store{Path: filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state", "state-v2.json")}).Load()
		if e != nil {
			return req, e
		}
		live := false
		for _, installation := range state.Installations {
			for _, binding := range installation.Clients {
				live = live || binding.ClientID == "cursor"
			}
		}
		if !live {
			off := false
			req.CursorAgentNotify = &off
			return req, nil
		}
	}
	if req.Action != setupwizard.ActionUninstall {
		// Fresh Capture belongs to public Prepare. Its frozen original token is
		// retained/revalidated by Apply and lifecycle, never recaptured here.
		agent := req.ClientExecutables["cursor"]
		if agent == "" {
			agent = req.ClientExecutable
		}
		// Do not gate the installed Cursor CLI on a test/qualification version.
		// Admit by executable presence and the profile/capability contract below.
		agent, err = normalizeProductExecutable(agent)
		if err != nil {
			return req, err
		}
		req.ClientExecutables = map[string]string{"cursor": agent}
		if filepath.Base(req.CursorConfig) != ".cursor" {
			return req, setupwizard.ErrRefused
		}
		if err := cursorQualifiedNamespace(req.CursorConfig); err != nil {
			return req, err
		}
	}
	acquiring := req.PackageRoot == "" && snapshot.Ledger.PendingMutation == nil
	req, cfg, previous, cleanup, err := reserveCursorCaller(ctx, req, snapshot)
	if err != nil {
		return req, err
	}
	defer func() {
		if err != nil {
			cleanup() // Composition is still read-only; Plan/Run has not begun.
		}
	}()
	adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, req.CursorAuthority)
	if err != nil {
		return req, err
	}
	base, err := portablesetup.NewRegistry()
	if err != nil {
		return req, err
	}
	cfg.Registry, err = clients.NewRegistry(append(base.All(), adapter)...)
	if err != nil {
		return req, err
	}
	eng, err := uapinstaller.New(cfg)
	if err != nil {
		return req, err
	}
	// The selected registry, constructed from acknowledged facts, verifies the
	// original token. Historical Cursor is used only for inert reservation.
	if previous && req.Action != setupwizard.ActionUninstall {
		if err := eng.VerifyProfileAuthority(ctx, req.InstallationID, req.BindingIDs["cursor"]); err != nil {
			return req, err
		}
	}
	// Retained identity and Authority above are inert. Package admission starts
	// only after the selected registry verifies the original physical token.
	if previous && req.Action != setupwizard.ActionUninstall {
		if snapshot.Ledger.PendingMutation != nil {
			intent, e := portablesetup.ReadIntent(req.ControlRoot)
			if e != nil {
				return req, e
			}
			req, err = setupwizard.ResolvePendingPackage(ctx, req, intent)
		} else if req.Action == setupwizard.ActionUpdate && req.PackageRoot == "" {
			req, cleanup, err = setupwizard.StageCurrentReleasePackage(ctx, req)
		}
		if err != nil {
			return req, err
		}
	}
	reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{ClientID: "cursor", InstallationID: req.InstallationID, ClientConfigRoot: req.CursorConfig, DeclaredName: cursorCallerDeclaredName(req.PackageRoot)})
	if err != nil || reserved.InstallationID != req.InstallationID || reserved.BindingID != req.BindingIDs["cursor"] {
		return req, portable.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return req, err
	}
	if acquiring {
		req, err = setupwizard.RetainCurrentReleasePackage(ctx, req)
	}
	return req, err
}

// Inert reservation is independent of physical identity and channel consent.
// It binds the real managed observer, never a future helper or the vendor agent.
func reserveCursorCaller(ctx context.Context, req setupwizard.Request, snapshot installruntime.InstalledSnapshot) (_ setupwizard.Request, _ uapinstaller.Config, _ bool, cleanup func(), err error) {
	cleanup = func() {}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	uapRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap")
	cfg := uapinstaller.Config{StateRoot: filepath.Join(uapRoot, "state"), PluginDataBase: filepath.Join(uapRoot, "plugin-data"), ManagedRoot: filepath.Join(uapRoot, "managed")}
	deny := func(err error) (setupwizard.Request, uapinstaller.Config, bool, func(), error) {
		return req, cfg, false, cleanup, err
	}
	if snapshot.Recovery || snapshot.Ledger.ID == "" || snapshot.Ledger.Owner != "existing-installer" || req.RuntimeRoot != "" && req.RuntimeRoot != snapshot.Ledger.RuntimeRoot {
		return deny(setupwizard.ErrRefused)
	}
	// Pending identity is selected before reservation; the wizard remains the
	// owner of retry unit/digest validation and uncertain recovery.
	var pendingIntent *portablesetup.Intent
	if pending := snapshot.Ledger.PendingMutation; pending != nil {
		intent, e := portablesetup.ReadIntent(req.ControlRoot)
		if e != nil || pending.Owner != "existing-installer" || pending.IntentRef != portablesetup.IntentPath(req.ControlRoot) || intent.SetupIntentID != pending.ID || intent.Action != string(req.Action) || len(intent.Targets) != 1 {
			return deny(setupwizard.ErrRefused)
		}
		target := intent.Targets[0]
		if target.Client != "cursor" || target.InstallationID == "" || target.BindingID == "" || target.Profile != req.CursorConfig || req.InstallationID != "" && req.InstallationID != target.InstallationID || req.BindingIDs["cursor"] != "" && req.BindingIDs["cursor"] != target.BindingID {
			return deny(portablesetup.ErrIntentConflict)
		}
		req.InstallationID = target.InstallationID
		req.BindingIDs = map[string]string{"cursor": target.BindingID}
		pendingIntent = &intent
	}
	req.RuntimeRoot = snapshot.Ledger.RuntimeRoot
	generation := snapshot.Ledger.Generation
	if req.BootstrapExpectedGeneration != nil && *req.BootstrapExpectedGeneration != generation {
		return deny(portablesetup.ErrConcurrentChange)
	}
	req.BootstrapExpectedGeneration = &generation
	base, err := portablesetup.NewRegistry()
	if err != nil {
		return deny(err)
	}
	cfg.Registry, err = clients.NewRegistry(append(base.All(), cursor.New())...)
	if err != nil {
		return deny(err)
	}
	eng, err := uapinstaller.New(cfg)
	if err != nil {
		return deny(err)
	}
	reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{ClientID: "cursor", InstallationID: req.InstallationID, Allocate: req.Action == setupwizard.ActionInstall && snapshot.Ledger.PendingMutation == nil, ClientConfigRoot: req.CursorConfig})
	if err != nil {
		return deny(err)
	}
	req.InstallationID = reserved.InstallationID
	primary, found, err := portable.InstalledPrimary(snapshot.Ledger, req.InstallationID, req.ControlRoot)
	if err != nil {
		return deny(err)
	}
	if !found {
		primary = portable.PlatformPrimary()
	}
	if req.Primary != "" && req.Primary != primary {
		return deny(portable.ErrInvalid)
	}
	req.Primary = primary
	observer, err := portable.ResolvePrimaryExecutable(snapshot.Ledger, primary)
	if err != nil || req.Helper != "" && req.Helper != observer {
		return deny(portable.ErrInvalid)
	}
	req.Helper = observer
	global, found, err := portable.InstalledGlobalConfig(snapshot.Ledger, req.InstallationID, req.ControlRoot)
	if err != nil {
		return deny(err)
	}
	if found && req.GlobalConfig != "" && req.GlobalConfig != global {
		return deny(portable.ErrInvalid)
	}
	if found {
		req.GlobalConfig = global
	}
	if req.GlobalConfig == "" {
		selection, e := config.Resolve(config.SnapshotEnv())
		if e != nil {
			return deny(e)
		}
		req.GlobalConfig = selection.Path
	}
	if pendingIntent != nil {
		if pendingIntent.Primary != "" && pendingIntent.Primary != req.Primary || pendingIntent.GlobalConfig != "" && pendingIntent.GlobalConfig != req.GlobalConfig {
			return deny(portablesetup.ErrIntentConflict)
		}
		view, e := eng.Inspect(ctx)
		if e != nil || view.Recovery.Required {
			return deny(setupwizard.ErrRefused)
		}
	}
	if reserved.BindingID == "" {
		if snapshot.Ledger.PendingMutation != nil || req.Action != setupwizard.ActionInstall {
			return deny(uapinstaller.ErrNotInstalled)
		}
		if req.PackageRoot == "" {
			req, cleanup, err = setupwizard.StageCurrentReleasePackage(ctx, req)
			if err != nil {
				return deny(err)
			}
		}
		name := cursorCallerDeclaredName(req.PackageRoot)
		if name == "" {
			return deny(portable.ErrInvalid)
		}
		reserved, err = eng.ReserveIdentity(uapinstaller.IdentityRequest{ClientID: "cursor", InstallationID: reserved.InstallationID, DeclaredName: name, ClientConfigRoot: req.CursorConfig})
		if err != nil || reserved.BindingID == "" || reserved.TargetPath == "" {
			return deny(portable.ErrInvalid)
		}
		version, _ := cursorinstall.CurrentTuple()
		req.CursorAuthority = &cursorinstall.Authority{ObjectID: "cursor-user-stop-v1", CursorVersion: version, QualificationID: cursorHostQualification()}
		req.CursorAuthority.Selector = filepath.Join(cfg.PluginDataBase, domain.ComputePhysicalArtifactID(name, reserved.InstallationID)) // completed below
	}
	if reserved.InstallationID == "" || reserved.Scope != string(domain.ScopeUser) || reserved.BindingID != domain.ComputeClientBindingID(reserved.InstallationID, "cursor", reserved.Scope, reserved.TargetPath) {
		return deny(portable.ErrInvalid)
	}
	req.InstallationID = reserved.InstallationID
	if req.BindingIDs != nil && req.BindingIDs["cursor"] != "" && req.BindingIDs["cursor"] != reserved.BindingID {
		return deny(portable.ErrInvalid)
	}
	req.BindingIDs = map[string]string{"cursor": reserved.BindingID}
	state, err := (statev2.Store{Path: filepath.Join(cfg.StateRoot, "state-v2.json")}).Load()
	if err != nil {
		return deny(err)
	}
	dataRoot := ""
	previous := false
	var installed domain.Installation
	var client domain.ClientBinding
	for _, installation := range state.Installations {
		if installation.InstallationID != req.InstallationID {
			continue
		}
		if c, ok := installation.Clients[reserved.BindingID]; ok {
			data, ok := installation.DataReceipts[c.DataReceiptID]
			if !ok || c.PendingNativeIntent != nil || c.NativeActivationAttempt != "" {
				return deny(portable.ErrInvalid)
			}
			dataRoot, previous = data.Locator, true
			installed, client = installation, c
		}
	}
	if !previous {
		if req.CursorAuthority == nil {
			return deny(portable.ErrInvalid)
		}
		dataRoot = req.CursorAuthority.Selector
	}
	id := portablesetup.Identity{InstallationID: req.InstallationID, ComponentID: snapshot.Ledger.ID, Owner: snapshot.Ledger.Owner, ScopeRoot: req.CursorConfig, ControlRoot: req.ControlRoot, GlobalConfig: req.GlobalConfig, RuntimeRoot: req.RuntimeRoot, Primary: primary}
	binding, err := portablesetup.Complete(id, portable.Cursor, "cursor", reserved.Scope, reserved.TargetPath, dataRoot)
	if err != nil || binding.BindingID != reserved.BindingID {
		return deny(portable.ErrInvalid)
	}
	if previous {
		committed, found, e := portable.ResolveCommittedBinding(snapshot.Ledger, binding)
		if e != nil {
			return deny(portable.ErrInvalid)
		}
		var fixed cursorinstall.Authority
		if found {
			_, fixed, e = cursorInstalledInputs(ctx, committed, snapshot, cfg.StateRoot)
		} else {
			fixed, e = cursorPendingHandoff(binding, snapshot, cfg, installed, client, pendingIntent)
		}
		if e != nil || (req.Action != setupwizard.ActionUninstall && fixed.QualificationID != cursorHostQualification()) {
			return deny(portable.ErrInvalid)
		}
		req.CursorAuthority = &fixed
		if pendingIntent != nil && req.Action != setupwizard.ActionUninstall {
			// The committed source is usable only if it is this exact candidate;
			// an acknowledged A cannot stand in for a frozen update B.
			if req.PackageRoot == "" && pendingIntent.TreeDigest != "" && installed.Source.TreeDigest == pendingIntent.TreeDigest {
				req.PackageRoot = installed.Source.CanonicalSource
			}
		}
	} else {
		name, e := binding.Filename()
		if e != nil {
			return deny(e)
		}
		req.CursorAuthority.ProfileRoot, req.CursorAuthority.Executable = req.CursorConfig, observer
		req.CursorAuthority.ExecutableDigest = "sha256:" + snapshot.Ledger.Files[observer].SHA256
		req.CursorAuthority.Selector = filepath.Join(binding.DataRoot, name)
	}
	return req, cfg, previous, cleanup, nil
}

// This admits only the public pre-native first handoff. It does not grant
// installed delivery eligibility or synthesize a runtime consumer.
func cursorPendingHandoff(b portable.Binding, snap installruntime.InstalledSnapshot, cfg uapinstaller.Config, installation domain.Installation, c domain.ClientBinding, intent *portablesetup.Intent) (cursorinstall.Authority, error) {
	deny := func() (cursorinstall.Authority, error) { return cursorinstall.Authority{}, portable.ErrInvalid }
	if intent == nil || intent.Action != "install" || intent.Stage != "confirmed" || intent.ExpectedGeneration+1 != snap.Ledger.Generation || len(intent.Targets) != 1 || b.CheckPrimaryFile(snap) != nil || installation.NeedsRebind ||
		c.ClientBindingID != b.BindingID || c.ClientBindingID != domain.ComputeClientBindingID(b.InstallationID, c.ClientID, c.Scope, c.TargetLocator) || c.ClientID != "cursor" || c.Scope != b.ScopeID || c.NativeProfileRoot != b.ScopeRoot || c.ProfileNamespace != cfg.StateRoot || c.ProfileAuthority == nil || c.ProfileAuthority.IsZero() || c.ProfileAuthority.Facts().CanonicalRoot != b.ScopeRoot ||
		c.PendingNativeIntent != nil || c.NativeActivationAttempt != "" || c.Activation != domain.ActivationPrepared || c.Verification != domain.VerificationPackageValid || c.Materialization != domain.MaterializationMaterialized || c.SelectedDelivery.ValidateClient(domain.ClientCursor) != nil {
		return deny()
	}
	target, data := intent.Targets[0], installation.DataReceipts[c.DataReceiptID]
	if target.OldBinding != nil || target.NewBinding != nil || target.OldConsumerKey != "" || target.NewConsumerKey != "" || target.DataReceiptID != "" && target.DataReceiptID != c.DataReceiptID || data.DataReceiptID != c.DataReceiptID || data.PhysicalBackend != c.PhysicalArtifact || data.State != domain.DataReceiptOwned || data.Locator != b.DataRoot || intent.Primary != b.Primary || intent.GlobalConfig != b.GlobalConfig || intent.TreeDigest != installation.Source.TreeDigest {
		return deny()
	}
	for _, object := range c.NativeObjects {
		if object.Kind != "managed_package_directory" || object.CursorReceipt != (domain.CursorHookReceipt{}) {
			return deny()
		}
	}
	facts, ok := c.SelectedDelivery.CursorFacts()
	name, err := b.Filename()
	observer, e := portable.ResolvePrimaryExecutable(snap.Ledger, b.Primary)
	if !ok || err != nil || e != nil || c.SelectedDelivery.Validate() != nil || facts.CanonicalDigest != intent.TreeDigest || facts.ProfileRoot != b.ScopeRoot || facts.Selector != filepath.Join(b.DataRoot, name) || facts.Executable != observer {
		return deny()
	}
	return cursorinstall.Authority{ProfileRoot: facts.ProfileRoot, CursorVersion: facts.CursorVersion, QualificationID: facts.QualificationID, Executable: observer, ExecutableDigest: "sha256:" + snap.Ledger.Files[observer].SHA256, Selector: facts.Selector, ObjectID: facts.ObjectID}, nil
}

// A narrow filter for the Ubuntu24.04.5/ext4 source qualification kernels.
// Each kernel requires the physical Cursor/materializer/caller CI contract.
// Mount metadata only limits evidence scope; UUID/inode authority
// comes exclusively from public complete-ancestry Capture/Revalidate.
func cursorQualifiedNamespace(profile string) error {
	// Public Prepare captures the NTFS volume/file-ID complete ancestry; this
	// host check neither captures a second token nor substitutes Linux evidence.
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		return nil
	}
	// The existing public Darwin complete-ancestry owner subsequently captures
	// and revalidates the physical token. No Linux mount evidence is substituted.
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return nil
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return setupwizard.ErrRefused
	}
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil || !cursorinstall.SupportsLinuxSourceKernel(strings.TrimSpace(string(kernel))) {
		return setupwizard.ErrRefused
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil || !strings.Contains("\n"+string(osRelease), "\nID=ubuntu\n") || !strings.Contains(string(osRelease), `VERSION="24.04.5 LTS`) {
		return setupwizard.ErrRefused
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil || len(mounts) > 1<<20 {
		return setupwizard.ErrRefused
	}
	return cursorQualifiedMounts(profile, string(mounts))
}

func cursorQualifiedMounts(profile, mounts string) error {
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for path := profile; ; path = filepath.Dir(path) {
		longest, fs := -1, ""
		ambiguous := false
		for _, row := range strings.Split(mounts, "\n") {
			left, right, ok := strings.Cut(row, " - ")
			fields, kind := strings.Fields(left), strings.Fields(right)
			if !ok || len(fields) < 6 || len(kind) < 3 {
				continue
			}
			mount := decode.Replace(fields[4])
			if mount != "/" && path != mount && !strings.HasPrefix(path, mount+"/") {
				continue
			}
			if len(mount) > longest {
				longest, fs = len(mount), kind[0]
				ambiguous = false
			} else if len(mount) == longest {
				// Row order does not establish which stacked mount is visible.
				ambiguous = true
			}
		}
		if ambiguous || fs != "ext4" {
			return setupwizard.ErrRefused
		}
		if path == "/" {
			return nil
		}
	}
}

func cursorCallerDeclaredName(root string) string {
	var body []byte
	var err error
	if strings.EqualFold(filepath.Ext(root), ".zip") {
		archive, e := zip.OpenReader(root)
		if e != nil {
			return ""
		}
		defer func() { _ = archive.Close() }()
		file, e := archive.Open("plugin.json")
		if e != nil {
			return ""
		}
		defer func() { _ = file.Close() }()
		body, err = io.ReadAll(io.LimitReader(file, (1<<20)+1))
	} else {
		path := filepath.Join(root, "plugin.json")
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return ""
		}
		file, e := os.Open(path)
		if e != nil {
			return ""
		}
		defer func() { _ = file.Close() }()
		opened, e := file.Stat()
		if e != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return ""
		}
		body, err = io.ReadAll(io.LimitReader(file, (1<<20)+1))
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err != nil || len(body) > 1<<20 || json.Unmarshal(body, &manifest) != nil {
		return ""
	}
	return manifest.Name
}

func samePiChoice(a, b *bool) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
