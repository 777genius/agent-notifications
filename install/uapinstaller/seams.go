package uapinstaller

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
)

type seamStager struct {
	providers.Stager
	serverName  string
	projectArgs func(BindingFacts) ([]string, error)
	facts       BindingFacts
}

func (s seamStager) Stage(ctx context.Context, envelope domain.PackageEnvelope, plan domain.DeliveryPlan, operationID string, hints domain.CompatibilityHints) (domain.StagedDelivery, error) {
	if s.serverName != "" && s.projectArgs != nil {
		return domain.StagedDelivery{}, fmt.Errorf("host projection requires owned plugin data")
	}
	return s.Stager.Stage(ctx, envelope, plan, operationID, hints)
}

func (s seamStager) StageWithPluginData(ctx context.Context, envelope domain.PackageEnvelope, plan domain.DeliveryPlan, operationID string, hints domain.CompatibilityHints, data string) (domain.StagedDelivery, error) {
	facts := s.facts
	facts.TargetPath = plan.ActivePath
	facts.DataRoot = data
	facts.ClientID = string(plan.ClientID)
	facts.Scope = string(plan.Scope)
	if facts.InstallationID != "" {
		facts.BindingID = domain.ComputeClientBindingID(facts.InstallationID, facts.ClientID, facts.Scope, plan.ActivePath)
	}
	projected, err := projectArgs(envelope, s.serverName, s.projectArgs, facts)
	if err != nil {
		return domain.StagedDelivery{}, err
	}
	return s.Stager.StageWithPluginData(ctx, projected, plan, operationID, hints, data)
}

func projectArgs(envelope domain.PackageEnvelope, serverName string, args func(BindingFacts) ([]string, error), facts BindingFacts) (domain.PackageEnvelope, error) {
	if serverName == "" || args == nil {
		return envelope, nil
	}
	raw, err := json.Marshal(envelope.MCP.Servers)
	if err != nil {
		return domain.PackageEnvelope{}, err
	}
	var servers map[string]domain.MCPServer
	if err := json.Unmarshal(raw, &servers); err != nil {
		return domain.PackageEnvelope{}, err
	}
	server, ok := servers[serverName]
	if !ok {
		return domain.PackageEnvelope{}, fmt.Errorf("package is missing declared MCP server %s", serverName)
	}
	if server.Decoded == nil {
		server.Decoded = map[string]any{}
	}
	replacement, err := args(facts)
	if err != nil {
		return domain.PackageEnvelope{}, err
	}
	copied := make([]any, len(replacement))
	for i, item := range replacement {
		copied[i] = item
	}
	server.Decoded["args"] = copied
	server.Raw, err = json.Marshal(server.Decoded)
	if err != nil {
		return domain.PackageEnvelope{}, err
	}
	servers[serverName] = server
	envelope.MCP.Servers = servers
	return envelope, nil
}

type seamActivator struct {
	inner       providers.Activator
	onCommitted func(context.Context, BindingFacts) error
	store       interface {
		Load() (domain.StateFileV2, error)
	}
	facts BindingFacts
}

func (a seamActivator) Activate(ctx context.Context, request domain.ActivationRequest) (domain.ActivationOutcome, error) {
	if a.onCommitted != nil && !request.VerifyOnly {
		facts, err := a.committedFacts(request)
		if err != nil {
			return domain.ActivationOutcome{}, err
		}
		if err := a.onCommitted(ctx, facts); err != nil {
			return domain.ActivationOutcome{}, err
		}
	}
	return a.inner.Activate(ctx, request)
}

func (a seamActivator) AutomaticallyActivates(request domain.ActivationRequest) bool {
	return a.inner.AutomaticallyActivates(request)
}

func (a seamActivator) PreflightActivation(request domain.ActivationRequest) error {
	return a.inner.PreflightActivation(request)
}

func (a seamActivator) Deactivate(ctx context.Context, request domain.DeactivationRequest) (domain.DeactivationOutcome, error) {
	return a.inner.Deactivate(ctx, request)
}

func (a seamActivator) committedFacts(request domain.ActivationRequest) (BindingFacts, error) {
	facts := a.facts
	if a.store == nil {
		return facts, nil
	}
	state, err := a.store.Load()
	if err != nil {
		return BindingFacts{}, err
	}
	installation, ok := findInstall(state, facts.InstallationID)
	if !ok {
		return facts, nil
	}
	for _, binding := range installation.Clients {
		if binding.TargetLocator != request.Plan.ActivePath || binding.ClientID != string(request.Plan.ClientID) {
			continue
		}
		receipt := installation.DataReceipts[binding.DataReceiptID]
		facts.BindingID = binding.ClientBindingID
		facts.Scope = binding.Scope
		facts.TargetPath = binding.TargetLocator
		facts.DataRoot = receipt.Locator
		facts.DataReceiptID = binding.DataReceiptID
		facts.ClientID = binding.ClientID
		return facts, nil
	}
	return facts, nil
}
