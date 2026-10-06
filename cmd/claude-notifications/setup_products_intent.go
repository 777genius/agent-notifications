package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// One immutable host handoff, not a phase journal or an authorization token.
// Existing verified-helper acquisition and writers retain their authority.
type confirmedBootstrapIntent struct {
	Schema     int                `json:"schema"`
	Provenance selectorProvenance `json:"provenance"`
	Request    setupProductsArgs  `json:"request"`
	// Byte values preserve Unix path bytes through JSON without lossy UTF8 repair.
	Scopes  map[string][]byte                 `json:"scopes"`
	Units   []bootstrapProductUnits           `json:"units"`
	Initial bootstrapInitialObservation       `json:"initial"`
	MCP     setupwizard.BootstrapMCPSelection `json:"mcp"`
}
type bootstrapProductUnits struct {
	Product                                                   string
	Hooks, Native, MCP, Skill, Desktop, Webhook, PreservedOff bool
}
type bootstrapInitialObservation struct {
	LedgerID, Owner string
	Generation      uint64
	Policy          installruntime.Identity
}

var intentScalarKeys = []string{"home", "claude-config", "claude-mcp-config", "codex-home", "codex-mcp-config", "opencode-config-dir", "gemini-config-root", "control-root", "runtime-root", "global-config", "claude-executable", "codex-executable", "opencode-executable", "gemini-executable", "scope-root", "client-executable"}

func intentWizardRequest(i confirmedBootstrapIntent) setupwizard.Request {
	scopes := i.Scopes
	ids := []string{}
	for _, id := range i.Request.Products {
		if id == "claude" || id == "codex" || id == "cursor" {
			ids = append(ids, id)
		}
	}
	on, off := true, false
	return setupwizard.Request{Action: setupwizard.ActionInstall, Agents: ids, Hooks: &off, AgentNotify: &on, Yes: true,
		ControlRoot: string(scopes["control-root"]), RuntimeRoot: string(scopes["runtime-root"]), GlobalConfig: string(scopes["global-config"]),
		ClaudeConfig: string(scopes["claude-config"]), CodexHome: string(scopes["codex-home"]),
		CursorConfig: string(scopes["scope-root"]), ScopeRoot: string(scopes["scope-root"]),
		MCPConfig:         map[string]string{"claude": string(scopes["claude-mcp-config"]), "codex": string(scopes["codex-mcp-config"])},
		ClientExecutables: map[string]string{"claude": string(scopes["claude-executable"]), "codex": string(scopes["codex-executable"]), "cursor": string(scopes["client-executable"])},
	}
}

func buildConfirmedBootstrapIntent(ctx context.Context, a setupProductsArgs, e productEnvironment, provenance selectorProvenance) (confirmedBootstrapIntent, []string, error) {
	i := confirmedBootstrapIntent{Schema: 1, Provenance: provenance, Request: a, Scopes: map[string][]byte{}}
	facts, scopes, err := discoverProducts(ctx, a, e)
	if err != nil {
		return i, nil, err
	}
	for _, f := range facts {
		if containsProduct(a.Products, f.ID) && (!f.Selectable || !f.Present) {
			return i, nil, fmt.Errorf("%s prerequisite unavailable: %s", f.ID, f.Reason)
		}
	}
	snapshot, err := installruntime.ReadPolicySnapshot(ctx, scopes["control-root"])
	if err != nil {
		return i, nil, err
	}
	ledger := snapshot.Installation.Ledger
	if snapshot.Installation.Recovery || ledger.PendingMutation != nil {
		return i, nil, errors.New("pending recovery or setup intent requires inspection")
	}
	i.Initial = bootstrapInitialObservation{ledger.ID, ledger.Owner, ledger.Generation, snapshot.Preimage}
	if ledger.ID != "" && ledger.Owner != "existing-installer" {
		return i, nil, errors.New("foreign installation owner")
	}
	if ledger.RuntimeRoot != "" {
		scopes["runtime-root"] = ledger.RuntimeRoot
	} else if !containsProduct(a.Products, "claude") && !containsProduct(a.Products, "codex") {
		scopes["runtime-root"] = filepath.Join(scopes["control-root"], "runtime")
	}
	selection, err := config.Resolve(e.Config)
	if err != nil {
		return i, nil, err
	}
	scopes["global-config"] = selection.Path
	for _, key := range intentScalarKeys {
		if value := scopes[key]; value != "" {
			i.Scopes[key] = []byte(value)
		}
	}
	// Input/UI are presentation only; frozen normalized effects compare without
	// re-reading env or including the staging filename in product scope.
	i.Request.Scopes = nil
	i.Request.IntentFile = ""
	i.Request.Mode = ""
	i.Request.ConfigureArgs = nil
	r := intentWizardRequest(i)
	// Confirmation observes stored units without granting the selected route.
	// The direct caller constructs fresh authority before Plan/Run, after the
	// frozen selection has preserved absent siblings and explicit opt-outs.
	if containsProduct(r.Agents, "cursor") {
		r.CursorAgentNotify = r.Hooks
	}

	if len(r.Agents) > 0 && !a.SkipAgentNotify {
		projection, generation, err := setupwizard.ObserveBootstrapMCP(ctx, r)
		if err != nil {
			return i, nil, err
		}
		if generation != ledger.Generation {
			return i, nil, errors.New("concurrent_change")
		}
		i.MCP.Projection = projection
		if a.AgentNotify {
			i.MCP.Selected = append([]string(nil), r.Agents...)
		} else {
			inspect := r
			inspect.Action = setupwizard.ActionInspect
			before, err := setupwizard.Run(ctx, inspect)
			if err != nil {
				return i, nil, err
			}
			// Fresh absence has no managed runtime yet. Existing unknown state remains
			// an error; BootstrapAutoTargets owns the actual absent-sibling policy.
			if ledger.ID == "" {
				before = setupwizard.Result{}
			}
			i.MCP.Selected, i.MCP.Skipped, err = setupwizard.BootstrapAutoTargets(ctx, r, before)
			if err != nil {
				return i, nil, err
			}
		}
		i.MCP.AllowPolicySeed = !projection.PolicyPresent
		// Portable hooks/runtime preparation and normalized policy configuration
		// precede observer installs, even on a fresh machine. Configuration seeds
		// on only when preservation finds no existing enabled/route decision.
		i.MCP.SeedEnabled = true
		if a.Configure.PreserveEnabled || a.Configure.PreservePolicy {
			_, enabledConfigured := snapshot.Fields["enabled"]
			_, routeConfigured := snapshot.Fields["route"]
			if enabledConfigured || routeConfigured {
				i.MCP.SeedEnabled = snapshot.Policy.Enabled
			}
		}
	}
	for _, id := range a.Products {
		portable := id == "claude" || id == "codex" || id == "cursor"
		u := bootstrapProductUnits{Product: id, Hooks: portable && id != "cursor", Native: !portable, MCP: containsProduct(i.MCP.Selected, id), Skill: containsProduct(i.MCP.Selected, id), PreservedOff: containsProduct(i.MCP.Skipped, id)}
		if !portable {
			u.Desktop = a.Desktop
			u.Webhook = a.Webhook
		}
		i.Units = append(i.Units, u)
	}
	rows, err := bootstrapIntentSummary(i, snapshot.Fields)
	if err != nil {
		return i, nil, err
	}
	// Verify the observation still represents the confirmation baseline after
	// composing portable and policy facts. This does not advance the record.
	current, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery || current.ID != ledger.ID || current.Owner != ledger.Owner || current.Generation != ledger.Generation {
		return i, nil, errors.New("concurrent_change")
	}
	return i, rows, nil
}

func preflightBootstrapIntent(ctx context.Context, i confirmedBootstrapIntent, a setupProductsArgs) error {
	comparable := a
	comparable.IntentFile = ""
	comparable.Mode = ""
	comparable.Scopes = nil
	comparable.Operation = "confirm"
	comparable.ConfigureArgs = nil
	if !reflect.DeepEqual(i.Request, comparable) {
		return errors.New("intent arguments differ from confirmed request")
	}
	root := string(i.Scopes["control-root"])
	policy, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return err
	}
	l := policy.Installation.Ledger
	if policy.Installation.Recovery || l.PendingMutation != nil || l.ID != i.Initial.LedgerID || l.Owner != i.Initial.Owner || l.Generation != i.Initial.Generation || policy.Preimage != i.Initial.Policy {
		return errors.New("concurrent_change: confirmed ownership/policy changed")
	}
	for _, id := range i.Request.Products {
		key := id + "-executable"
		if id == "cursor" {
			key = "client-executable"
		}
		path := string(i.Scopes[key])
		got, err := normalizeProductExecutable(path)
		if err != nil || got != path {
			return fmt.Errorf("%s selected executable changed", id)
		}
	}
	for _, key := range intentScalarKeys {
		if key == "home" || key == "global-config" || key == "claude-mcp-config" || key == "codex-mcp-config" || containsProduct([]string{"claude-executable", "codex-executable", "opencode-executable", "gemini-executable", "client-executable"}, key) {
			continue
		}
		if path := string(i.Scopes[key]); path != "" {
			got, err := installruntime.CanonicalPath(path)
			if err != nil || got != path {
				return fmt.Errorf("confirmed %s authority changed", key)
			}
		}
	}
	if len(i.MCP.Selected)+len(i.MCP.Skipped) > 0 {
		r := intentWizardRequest(i)
		if containsProduct(r.Agents, "cursor") {
			r.CursorAgentNotify = r.Hooks
		}
		current, _, err := setupwizard.ObserveBootstrapMCP(ctx, r)
		if err != nil {
			return err
		}
		if err := setupwizard.CheckBootstrapMCP(i.MCP, current); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func bootstrapIntentSummary(i confirmedBootstrapIntent, policy map[string]json.RawMessage) ([]string, error) {
	raw := []string{"Notifications installation plan; product selection alone does not authorize effects."}
	for _, u := range i.Units {
		raw = append(raw, fmt.Sprintf("%s: hooks=%t native-plugin/hooks=%t MCP=%t skill=%t preserved-off=%t desktop=%t webhook=%t", u.Product, u.Hooks, u.Native, u.MCP, u.Skill, u.PreservedOff, u.Desktop, u.Webhook))
	}
	for _, key := range intentScalarKeys {
		if p, ok := i.Scopes[key]; ok {
			raw = append(raw, key+"="+string(p))
		}
	}
	c := i.Request.Configure
	if c.Route != nil {
		raw = append(raw, fmt.Sprintf("requested route: local=%t app=%s team=%s allow-unknown-caller=%t allow-caller-asserted=%t", c.Route.LocalRouting, c.Route.ApplicationPath, c.Route.TeamID, c.Route.AllowUnknownCaller, c.Route.AllowCallerAsserted))
		raw = append(raw, fmt.Sprintf("preserve-policy=%t preserve-enabled=%t policy-only=%t request-permission=%t", c.PreservePolicy, c.PreserveEnabled, c.PolicyOnly, c.RequestPermission))
		if c.PreservePolicy {
			preserved, err := setupwizard.BootstrapPolicyRows(policy)
			if err != nil {
				return nil, err
			}
			raw = append(raw, preserved...)
		} else if value, ok := policy["enabled"]; ok {
			raw = append(raw, "preserved enabled="+string(value))
		}
	}
	if i.MCP.AllowPolicySeed {
		raw = append(raw, fmt.Sprintf("absent shared policy: shown initial enabled seed=%t; preserve-enabled keeps the resulting decision", i.MCP.SeedEnabled))
	}
	raw = append(raw, "helper release="+i.Provenance.Version, "helper source="+i.Provenance.SourceCommit, "helper SHA256="+i.Provenance.SHA256)
	for _, b := range i.MCP.Projection.Bindings {
		raw = append(raw, fmt.Sprintf("%s portable installation=%s binding=%s target=%s", b.Client, i.MCP.Projection.InstallationID, b.ID, string(b.Target)))
	}
	for _, d := range i.MCP.Projection.Direct {
		raw = append(raw, "owned direct MCP="+d.ID+" config="+string(d.Config))
	}
	raw = append(raw, "Restart/trust may be required. Gemini hook-enable/security settings remain user controlled. Webhook delivery requires an enabled destination.", "Products use separate existing transactions. A later failure may leave partial installation; activation/authentication/delivery are not attested.")
	return setupwizard.EscapeConfirmationRows(raw)
}
