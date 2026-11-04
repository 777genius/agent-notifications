package setupwizard

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func liveBindingTreeDigest(mat portablesetup.Materializer, installationID, clientID string) string {
	clientID = portablesetup.ClientID(portablesetup.Integration(clientID))
	if installationID == "" || clientID == "" {
		return ""
	}
	state, err := mat.Store.Load()
	if err != nil {
		return ""
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == clientID && binding.PackageRevision != nil {
				return binding.PackageRevision.TreeDigest
			}
		}
	}
	return ""
}

func completedNotifyTarget(mat portablesetup.Materializer, installationID, client, bindingID string) TargetResult {
	return TargetResult{
		Client:     client,
		Unit:       "agent-notify",
		Outcome:    "completed",
		Reason:     bindingID,
		TreeDigest: liveBindingTreeDigest(mat, installationID, client),
	}
}

func liveNotifyClient(mat portablesetup.Materializer, installationID, clientID string) bool {
	clientID = portablesetup.ClientID(portablesetup.Integration(clientID))
	if installationID == "" || clientID == "" {
		return false
	}
	state, err := mat.Store.Load()
	if err != nil {
		return false
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == clientID {
				return true
			}
		}
	}
	return false
}

// liveManagedTargetsPresent is true when every selected live binding still has
// its managed artifact. Missing targets skip ApplyGroup Repair so sequential
// Repair can rematerialize without the unset NativeObserver sibling check.
func liveManagedTargetsPresent(mat portablesetup.Materializer, installationID string, agents []portable.Integration) bool {
	if installationID == "" || len(agents) == 0 {
		return false
	}
	state, err := mat.Store.Load()
	if err != nil {
		return false
	}
	for _, agent := range agents {
		found := false
		for _, installation := range state.Installations {
			if installation.InstallationID != installationID {
				continue
			}
			for _, binding := range installation.Clients {
				if binding.ClientID != portablesetup.ClientID(agent) {
					continue
				}
				if binding.TargetLocator == "" {
					return false
				}
				if _, err := os.Lstat(binding.TargetLocator); err != nil {
					return false
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func inspectUAPState(ctx context.Context, req Request) (uapinstaller.Inspection, error) {
	if !explicitAbs(req.ControlRoot) {
		return uapinstaller.Inspection{}, nil
	}
	stateRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state")
	registry, err := portablesetup.NewRegistry()
	if err != nil {
		return uapinstaller.Inspection{}, err
	}
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: stateRoot, Registry: registry})
	if err != nil {
		return uapinstaller.Inspection{}, err
	}
	return eng.Inspect(ctx)
}

// DiscoverAgents reports Claude/Codex user-scope metadata and executable
// presence without creating UAP state or executing found files.
func DiscoverAgents(req Request) []AgentCapability {
	stateRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state")
	if !explicitAbs(req.ControlRoot) {
		stateRoot = filepath.Join(os.TempDir(), "uapinstaller-discover-absent")
	}
	registry, err := portablesetup.NewRegistry()
	if err != nil {
		return nil
	}
	eng, err := uapinstaller.New(uapinstaller.Config{
		StateRoot: stateRoot, ClientExecutables: req.ClientExecutables, Registry: registry,
	})
	if err != nil {
		return nil
	}
	found := eng.Discover()
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].ClientID == found[j].ClientID {
			return false
		}
		return found[i].ClientID == "claude"
	})
	out := make([]AgentCapability, 0, len(found))
	for _, item := range found {
		out = append(out, AgentCapability{
			ID: item.ClientID, Present: item.ExecutablePresent, Path: item.ExecutablePath,
			Bound: len(item.Bindings) > 0, Profile: firstBindingProfile(item.Bindings),
		})
	}
	return out
}

func firstBindingProfile(bindings []uapinstaller.InspectedBinding) string {
	for _, binding := range bindings {
		if profile, err := recordedLiveProfile(binding.DataRoot, binding.ClientID); err == nil && profile != "" {
			return profile
		}
	}
	return ""
}

func inspect(ctx context.Context, req Request, agents []portable.Integration, snap installruntime.InstalledSnapshot, runtimeRoot string, out Result) (Result, error) {
	out.InstallationID = req.InstallationID
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	state, err := mat.Store.Load()
	if err != nil {
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	out.Outcome = "completed"
	for _, agent := range agents {
		found := false
		for _, installation := range state.Installations {
			if req.InstallationID != "" && installation.InstallationID != req.InstallationID {
				continue
			}
			for _, binding := range installation.Clients {
				if binding.ClientID != portablesetup.ClientID(agent) {
					continue
				}
				found = true
				profile, _ := recordedLiveProfile(installation.DataReceipts[binding.DataReceiptID].Locator, string(agent))
				digest := installation.Source.TreeDigest
				if binding.PackageRevision != nil && binding.PackageRevision.TreeDigest != "" {
					digest = binding.PackageRevision.TreeDigest
				}
				out.Targets = append(out.Targets, TargetResult{
					Client: string(agent), Unit: "agent-notify", Outcome: "installed",
					Reason: binding.ClientBindingID, Profile: profile,
					TreeDigest: digest,
				})
			}
		}
		if !found {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "absent"})
		}
		out = inspectHooks(req, agent, out)
		mcpPath := discoveryConfigPath(req, agent)
		if mcpPath == "" {
			continue
		}
		command := discovery(req, agent, runtimeRoot, snap).Command
		facts, err := clientsetup.Inspect(ctx, clientsetup.Request{
			ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, Command: command, ConfigPath: mcpPath,
			Provider: discoveryProvider(agent), Mode: clientsetup.Managed, ExpectedGeneration: snap.Ledger.Generation,
		})
		if err != nil {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "direct-mcp", Outcome: "unknown", Reason: err.Error(), ConfigPath: mcpPath})
			continue
		}
		outcome := "absent"
		if facts.Registered {
			outcome = "installed"
		}
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "direct-mcp", Outcome: outcome, ConfigPath: mcpPath})
	}
	if snap.Recovery {
		out.Outcome, out.Reason = "incomplete", "recovery_required"
		out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: "kernel-journal"})
	}
	view, err := inspectUAPState(ctx, req)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", "recovery_required"
		out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: err.Error()})
		return out, err
	}
	if view.Recovery.Required {
		out.Outcome, out.Reason = "incomplete", "recovery_required"
		out.NextActions = append(out.NextActions, NextAction{
			Kind: "recover", Reason: strings.Join(recoveryIDs(view), ","),
		})
	}
	out = markInspectedIdentity(view, out)
	return reportPendingWizardIntent(req, snap, out), nil
}

func markInspectedIdentity(view uapinstaller.Inspection, out Result) Result {
	if out.InstallationID == "" && len(view.Installations) == 1 {
		out.InstallationID = view.Installations[0].InstallationID
	}
	return markInspectedDataRetained(view, out)
}

func markInspectedDataRetained(view uapinstaller.Inspection, out Result) Result {
	for _, installation := range view.Installations {
		if out.InstallationID != "" && installation.InstallationID != out.InstallationID {
			continue
		}
		if installation.DataRetained {
			out.DataRetained = true
			return out
		}
	}
	return out
}

func markRetainedEmpty(mat portablesetup.Materializer, installationID string, out Result) Result {
	if installationID == "" {
		return out
	}
	retained, err := mat.RetainedEmpty(installationID)
	if err == nil && retained {
		out.DataRetained = true
	}
	return out
}

// reportPendingWizardIntent lists the persisted resume command without
// restoring omitted flags into this inspect request or running Recover.
func reportPendingWizardIntent(req Request, snap installruntime.InstalledSnapshot, out Result) Result {
	pending := snap.Ledger.PendingMutation
	if pending == nil || pending.Owner != "existing-installer" {
		return out
	}
	intent, err := portablesetup.ReadIntent(req.ControlRoot)
	if err != nil || intent.SetupIntentID != pending.ID {
		out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: "pending_intent_unreadable"})
		return out
	}
	clients := intentClients(intent)
	retry := retryRequestFromIntent(req, intent)
	if intent.Action == string(ActionUninstall) && !intent.ExternalUninstalled {
		for _, client := range clients {
			if client == string(portable.Codex) {
				retry.ExternalUninstalled = true
				out.NextActions = append(out.NextActions, NextAction{
					Kind: "external-uninstall", Agents: clients,
					Command: localRetryCommand(retry), Reason: "attest_codex_plugin_removed",
				})
				return out
			}
		}
	}
	out.NextActions = append(out.NextActions, NextAction{
		Kind: "resume", Agents: clients, Command: localRetryCommand(retry),
		Reason: "pending_" + intent.Action,
	})
	return out
}
