package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

func sameLiveRepairRevision(mat portablesetup.Materializer, installationID string, agents []portable.Integration) bool {
	digest := ""
	for i, agent := range agents {
		got := liveBindingTreeDigest(mat, installationID, string(agent))
		if got == "" {
			return false
		}
		if i == 0 {
			digest = got
			continue
		}
		if got != digest {
			return false
		}
	}
	return digest != ""
}

func exactRepairRevision(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "repair must use the exact applied package revision") ||
		strings.Contains(msg, "resolved repair package differs from the installed revision")
}

func copyBindingIDs(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(src))
	for client, id := range src {
		out[client] = id
	}
	return out
}

func copyPackageRoots(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for client, root := range src {
		out[client] = root
	}
	return out
}

func clientPackageRoot(req Request, agent portable.Integration) string {
	if req.PackageRoots != nil {
		if root := req.PackageRoots[string(agent)]; root != "" {
			return root
		}
	}
	return req.PackageRoot
}

func bindRepairPackages(ctx context.Context, req *Request, snap installruntime.InstalledSnapshot, runtimeRoot string, agents []portable.Integration) {
	if req == nil || req.Action != ActionRepair || len(agents) == 0 {
		return
	}
	recorded, _ := desiredPackage(*req)
	seen := map[string]bool{}
	var candidates []string
	for _, root := range []string{req.PackageRoot, recorded} {
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		candidates = append(candidates, root)
	}
	if len(candidates) == 0 {
		return
	}
	req.PackageRoots = copyPackageRoots(req.PackageRoots)
	if req.PackageRoots == nil {
		req.PackageRoots = map[string]string{}
	}
	for _, agent := range agents {
		if req.PackageRoots[string(agent)] != "" {
			continue
		}
		for _, root := range candidates {
			try := *req
			try.PackageRoot = root
			try.PackageRoots = nil
			if _, err := previewNotifyPlan(ctx, try, snap, runtimeRoot, agent); err == nil {
				req.PackageRoots[string(agent)] = root
				break
			}
		}
	}
}

func reserveClientBindings(req *Request, mat portablesetup.Materializer, agents []portable.Integration) error {
	if req == nil || req.InstallationID == "" || len(agents) == 0 {
		return nil
	}
	eng, err := installerEngine(mat)
	if err != nil {
		return err
	}
	bindings := copyBindingIDs(req.BindingIDs)
	for _, agent := range agents {
		client := string(agent)
		if bindings[client] != "" {
			continue
		}
		reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{
			ClientID:         portablesetup.ClientID(agent),
			InstallationID:   req.InstallationID,
			DeclaredName:     packageDeclaredName(req.PackageRoot),
			ClientConfigRoot: clientConfig(*req, agent),
		})
		if err != nil {
			if errors.Is(err, uapinstaller.ErrAmbiguousInstallations) {
				return fmt.Errorf("%w: %w", ErrAmbiguousInstallation, err)
			}
			return err
		}
		if reserved.BindingID != "" {
			bindings[client] = reserved.BindingID
		}
	}
	req.BindingIDs = bindings
	return nil
}

func canGroupNotify(mat portablesetup.Materializer, id portablesetup.Identity, req Request, agents []portable.Integration) bool {
	if len(agents) != 2 {
		return false
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return false
	}
	if _, _, err := portable.InstalledGlobalConfig(snap.Ledger, id.InstallationID, req.ControlRoot); err != nil {
		return false
	}
	if _, _, err := portable.InstalledPrimary(snap.Ledger, id.InstallationID, req.ControlRoot); err != nil {
		return false
	}
	first := liveNotifyClient(mat, id.InstallationID, string(agents[0]))
	second := liveNotifyClient(mat, id.InstallationID, string(agents[1]))
	switch req.Action {
	case ActionInstall:
		if first != second {
			return false
		}
		if !first {
			return true
		}
		return sameLiveRepairRevision(mat, id.InstallationID, agents)
	case ActionUpdate:
		return first && second
	case ActionRepair:
		if !first || !second {
			return false
		}
		if !liveManagedTargetsPresent(mat, id.InstallationID, agents) {
			return false
		}
		if sameLiveRepairRevision(mat, id.InstallationID, agents) {
			return true
		}
		if len(req.PackageRoots) != 2 {
			return false
		}
		for _, agent := range agents {
			if req.PackageRoots[string(agent)] == "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func canGroupRemove(mat portablesetup.Materializer, id portablesetup.Identity, req Request, agents []portable.Integration) bool {
	if req.Action != ActionUninstall || len(agents) != 2 {
		return false
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return false
	}
	for _, agent := range agents {
		if removal, ok := req.RemovalBindings[string(agent)]; ok && !portable.ExactCommittedBinding(snap.Ledger, removal.Old) {
			return false
		}
	}
	if _, _, err := portable.InstalledGlobalConfig(snap.Ledger, id.InstallationID, req.ControlRoot); err != nil {
		return false
	}
	if _, _, err := portable.InstalledPrimary(snap.Ledger, id.InstallationID, req.ControlRoot); err != nil {
		return false
	}
	for _, agent := range agents {
		if agent == portable.Codex && !req.ExternalUninstalled && liveNotifyClient(mat, id.InstallationID, string(agent)) {
			return false
		}
	}
	return true
}

func retainedMetadataUpdate(mat portablesetup.Materializer, id portablesetup.Identity, action Action) bool {
	if action != ActionUpdate || id.InstallationID == "" {
		return false
	}
	empty, err := mat.RetainedEmpty(id.InstallationID)
	return err == nil && empty
}

func requireLiveNotifyBindings(req Request, mat portablesetup.Materializer, id portablesetup.Identity, agents []portable.Integration, out Result) (Result, error) {
	if retained, err := mat.RetainedEmpty(id.InstallationID); err == nil && retained && req.Action == ActionUpdate {
		return out, nil
	}
	var missing, live []string
	for _, agent := range agents {
		if liveNotifyClient(mat, id.InstallationID, string(agent)) {
			live = append(live, string(agent))
			continue
		}
		missing = append(missing, string(agent))
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "not_installed"})
	}
	if len(missing) == 0 {
		return out, nil
	}
	out.Outcome, out.Reason = "incomplete", "not_installed"
	if len(live) > 0 {
		installReq := req
		installReq.Action = ActionInstall
		installReq.Agents = append([]string(nil), missing...)
		subset := req
		subset.Agents = append([]string(nil), live...)
		out.NextActions = []NextAction{
			{Kind: "install", Agents: missing, Command: localRetryCommand(installReq), Reason: "install_missing"},
			{Kind: string(req.Action), Agents: live, Command: localRetryCommand(subset), Reason: "continue_installed_only"},
		}
	}
	return out, uapinstaller.ErrNotInstalled
}

func mapAmbiguous(err error, out Result) (Result, bool) {
	reason := ""
	switch {
	case errors.Is(err, ErrAmbiguousInstallation):
		reason = "ambiguous_installation"
	case errors.Is(err, ErrAmbiguousBinding):
		reason = "ambiguous_binding"
	case errors.Is(err, ErrLiveProfileConflict):
		reason = "live_profile_conflict"
	default:
		return out, false
	}
	out.Outcome, out.Reason = "conflict", reason
	out.NextActions = append(out.NextActions, NextAction{
		Kind: "inspect", Reason: err.Error(),
		Command: []string{"setup-notifications", "wizard", "--action", "inspect", "--json"},
	})
	return out, true
}

func liveClientBindings(mat portablesetup.Materializer, installationID, clientID string) ([]domain.ClientBinding, error) {
	clientID = portablesetup.ClientID(portablesetup.Integration(clientID))
	if installationID == "" || clientID == "" {
		return nil, nil
	}
	state, err := mat.Store.Load()
	if err != nil {
		return nil, err
	}
	var out []domain.ClientBinding
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == clientID {
				out = append(out, binding)
			}
		}
	}
	return out, nil
}

func guardLiveProfile(mat portablesetup.Materializer, installationID, clientID, profile string) error {
	bindings, err := liveClientBindings(mat, installationID, clientID)
	if err != nil {
		return err
	}
	if len(bindings) > 1 {
		return fmt.Errorf("%w: client %s has %d bindings", ErrAmbiguousBinding, clientID, len(bindings))
	}
	if len(bindings) == 0 || profile == "" {
		return nil
	}
	live := bindings[0]
	if profileOwnsLive(profile, live) {
		return nil
	}
	if clientConfigAnchored(live) {
		return fmt.Errorf("%w: live target %s is not under %s", ErrLiveProfileConflict, live.TargetLocator, profile)
	}
	// Codex TargetLocator lives under managed/clients. The live CodexHome is
	// recorded in PLUGIN_DATA, not locator JSON, so Registration() bytes stay
	// stable.
	dataRoot, err := bindingDataRoot(mat, installationID, live)
	if err != nil {
		return err
	}
	recorded, err := recordedLiveProfile(dataRoot, clientID)
	if err != nil {
		return err
	}
	if recorded == "" || sameLiveProfile(profile, recorded) {
		return nil
	}
	return fmt.Errorf("%w: live profile %s is not %s", ErrLiveProfileConflict, recorded, profile)
}

func profileOwnsLive(profile string, binding domain.ClientBinding) bool {
	if profileMatchesLive(profile, binding.TargetLocator) {
		return true
	}
	for _, obj := range binding.NativeObjects {
		if profileMatchesLive(profile, obj.Path) {
			return true
		}
	}
	return false
}

func clientConfigAnchored(binding domain.ClientBinding) bool {
	if binding.TargetLocator != "" && !managedClientPath(binding.TargetLocator) {
		return true
	}
	for _, obj := range binding.NativeObjects {
		if obj.Path != "" && !managedClientPath(obj.Path) {
			return true
		}
	}
	return false
}

func managedClientPath(p string) bool {
	return strings.Contains(filepath.ToSlash(p), "/managed/clients/")
}

func profileMatchesLive(profile, target string) bool {
	if profile == "" || target == "" {
		return false
	}
	root := filepath.Clean(profile)
	path := filepath.Clean(target)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

// RestoreLocalIntentScope restores the existing immutable handoff before the
// composition root touches the recorded profile or helper. It grants nothing.
func RestoreLocalIntentScope(req Request, intent portablesetup.Intent) (Request, error) {
	if len(intent.Targets) != 1 || intent.Targets[0].Client != string(portable.CopilotVSCode) || intent.Action != string(req.Action) {
		return req, portablesetup.ErrIntentConflict
	}
	agents, err := normalizeAgents(req.Agents)
	if err != nil {
		return req, err
	}
	req, _, err = restoreOmittedFromIntent(req, agents, intent)
	return req, err
}

func copyLocalConfig(source *vscode.LocalConfig) *vscode.LocalConfig {
	if source == nil {
		return nil
	}
	copy := *source
	copy.MCPServers = append([]string(nil), source.MCPServers...)
	copy.Skills = append([]string(nil), source.Skills...)
	copy.HookSpecs = append([]vscodelocalhooks.Spec(nil), source.HookSpecs...)
	for i := range copy.HookSpecs {
		copy.HookSpecs[i].Args = append([]string(nil), source.HookSpecs[i].Args...)
	}
	return &copy
}
