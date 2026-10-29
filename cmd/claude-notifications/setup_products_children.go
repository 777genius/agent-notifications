package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// The parent stays immutable. Its only portable child operations are the
// selected Claude/Codex group and Cursor alone; arbitrary subsets cannot expand
// or replace a confirmed scope.
func bootstrapWizardChild(parent confirmedBootstrapIntent, agents []string) (confirmedBootstrapIntent, error) {
	legacy := []string{}
	for _, id := range parent.Request.Products {
		if id == "claude" || id == "codex" {
			legacy = append(legacy, id)
		}
	}
	cursor := len(agents) == 1 && agents[0] == "cursor" && containsProduct(parent.Request.Products, "cursor")
	local := len(agents) == 1 && agents[0] == "copilot-vscode" && reflect.DeepEqual(parent.Request.Products, agents)
	if !cursor && !local && (len(legacy) == 0 || !reflect.DeepEqual(legacy, agents)) {
		return parent, errors.New("bootstrap request differs from confirmed child scope")
	}
	child := parent
	child.Request.Products = append([]string(nil), agents...)
	child.Units = nil
	for _, unit := range parent.Units {
		if containsProduct(agents, unit.Product) {
			child.Units = append(child.Units, unit)
		}
	}
	selection := parent.MCP
	selection.Selected, selection.Skipped = nil, nil
	selection.Projection.Bindings, selection.Projection.Direct = nil, nil
	selection.Projection.Profiles = map[string][]byte{}
	for _, id := range parent.MCP.Selected {
		if containsProduct(agents, id) {
			selection.Selected = append(selection.Selected, id)
		}
	}
	for _, id := range parent.MCP.Skipped {
		if containsProduct(agents, id) {
			selection.Skipped = append(selection.Skipped, id)
		}
	}
	for id, profile := range parent.MCP.Projection.Profiles {
		if containsProduct(agents, id) {
			selection.Projection.Profiles[id] = append([]byte(nil), profile...)
		}
	}
	for _, binding := range parent.MCP.Projection.Bindings {
		if containsProduct(agents, binding.Client) {
			selection.Projection.Bindings = append(selection.Projection.Bindings, binding)
		}
	}
	for _, direct := range parent.MCP.Projection.Direct {
		for _, id := range agents {
			key := id + "-mcp-config"
			if string(direct.Config) == string(parent.Scopes[key]) {
				selection.Projection.Direct = append(selection.Projection.Direct, direct)
				break
			}
		}
	}
	child.MCP = selection
	return child, nil
}

// A preceding sibling may create the shared portable installation after the
// confirmation observed absence. Only exact completed bindings of authorized
// selected siblings can establish that new identity; no sorted/default adoption.
func bootstrapSiblingInstallation(parent confirmedBootstrapIntent, installationID string, snapshot installruntime.PolicySnapshot) error {
	state, err := (statev2.Store{Path: filepath.Join(filepath.Dir(string(parent.Scopes["control-root"])), "uap", "state", "state-v2.json")}).Load()
	if err != nil || len(state.Installations) != 1 || state.Installations[0].InstallationID != installationID {
		return portablesetup.ErrConcurrentChange
	}
	installation := state.Installations[0]
	if len(installation.Clients) == 0 || installation.NeedsRebind {
		return portablesetup.ErrConcurrentChange
	}
	for _, client := range installation.Clients {
		if client.ClientID != "claude" && client.ClientID != "codex" || !containsProduct(parent.MCP.Selected, client.ClientID) || client.PendingNativeIntent != nil || client.NativeActivationAttempt != "" || client.Materialization != domain.MaterializationMaterialized || client.Scope != string(domain.ScopeUser) || client.Activation != domain.ActivationActive || client.Verification != domain.VerificationInstalled {
			return portablesetup.ErrConcurrentChange
		}
		data, owned := installation.DataReceipts[client.DataReceiptID]
		if !owned || data.State != domain.DataReceiptOwned {
			return portablesetup.ErrConcurrentChange
		}
		matched := false
		for key, consumer := range snapshot.Installation.Ledger.Consumers {
			if len(key) < 9 || key[:9] != "portable:" {
				continue
			}
			var binding portable.Binding
			if json.Unmarshal([]byte(consumer.Registration), &binding) != nil {
				return portablesetup.ErrConcurrentChange
			}
			profileKey := "claude-config"
			if client.ClientID == "codex" {
				profileKey = "codex-home"
			}
			if string(binding.Integration) == client.ClientID && binding.InstallationID == installationID && binding.BindingID == client.ClientBindingID && binding.BindingID == domain.ComputeClientBindingID(installationID, client.ClientID, client.Scope, client.TargetLocator) && binding.ScopeRoot == string(parent.Scopes[profileKey]) && (binding.ScopeRoot == client.NativeProfileRoot || client.ClientID == "claude" && client.NativeProfileRoot == "") && binding.ScopeID == client.Scope && binding.DataRoot == data.Locator && binding.GlobalConfig == string(parent.Scopes["global-config"]) && binding.ControlRoot == string(parent.Scopes["control-root"]) && portable.ExactCommittedBinding(snapshot.Installation.Ledger, binding) {
				matched = true
			}
		}
		if !matched {
			return portablesetup.ErrConcurrentChange
		}
	}
	return nil
}

// Rebase only the CAS observation after original relevant decisions are proven
// unchanged. Discovery, profiles, channel choices and binding projection all
// remain the parent's facts; the fresh observation grants no new choice.
func admitCursorBootstrapChild(ctx context.Context, parent, child confirmedBootstrapIntent, request setupwizard.Request) (confirmedBootstrapIntent, error) {
	observation := request
	off := false
	observation.CursorAgentNotify = &off
	current, generation, policy, err := setupwizard.ObserveBootstrapMCPWithPolicy(ctx, observation)
	if err != nil {
		return child, err
	}
	snapshot, err := installruntime.ReadPolicySnapshot(ctx, request.ControlRoot)
	if err != nil || snapshot.Installation.Recovery || snapshot.Installation.Ledger.PendingMutation != nil || snapshot.Installation.Ledger.Generation != generation || snapshot.Preimage != policy {
		return child, portablesetup.ErrConcurrentChange
	}
	ledger := snapshot.Installation.Ledger
	if parent.Initial.LedgerID != "" && (ledger.ID != parent.Initial.LedgerID || ledger.Owner != parent.Initial.Owner) || ledger.Owner != "existing-installer" {
		return child, portablesetup.ErrConcurrentChange
	}
	if runtime := string(parent.Scopes["runtime-root"]); runtime != "" && runtime != ledger.RuntimeRoot {
		return child, portablesetup.ErrConcurrentChange
	}
	preimage, err := installruntime.ObserverChannelPreimage(parent.Initial.ChannelPolicy, "cursorNotifications")
	if err != nil {
		return child, err
	}
	if err := installruntime.CheckObserverChannelPreimage(snapshot.Fields["route"], "cursorNotifications", preimage); err != nil {
		return child, err
	}
	expected := child.MCP
	if expected.Projection.InstallationID == "" && current.InstallationID != "" {
		if err := bootstrapSiblingInstallation(parent, current.InstallationID, snapshot); err != nil {
			return child, err
		}
		expected.Projection.InstallationID = current.InstallationID
	}
	if err := setupwizard.CheckBootstrapMCP(expected, current); err != nil {
		return child, err
	}
	child.MCP = expected
	child.Initial.LedgerID, child.Initial.Owner, child.Initial.Generation, child.Initial.Policy = ledger.ID, ledger.Owner, generation, policy
	return child, nil
}
