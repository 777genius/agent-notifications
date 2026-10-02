package setupwizard

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// BootstrapMCPSelection freezes only portable identity, profile and keep/off
// decisions. Hooks/native runtime generations are intentionally not identity.
type BootstrapMCPSelection struct {
	Selected   []string               `json:"selected"`
	Skipped    []string               `json:"skipped"`
	Projection BootstrapMCPProjection `json:"projection"`
	// A fresh absent policy may become the exact seed shown at confirmation.
	AllowPolicySeed bool `json:"allowPolicySeed"`
	SeedEnabled     bool `json:"seedEnabled"`
}
type BootstrapMCPProjection struct {
	InstallationID string                `json:"installationID"`
	Bindings       []BootstrapMCPBinding `json:"bindings"`
	Direct         []BootstrapDirectMCP  `json:"direct"`
	Profiles       map[string][]byte     `json:"profiles"`
	PolicyPresent  bool                  `json:"policyPresent"`
	Enabled        bool                  `json:"enabled"`
}
type BootstrapMCPBinding struct {
	Client, ID, Scope         string
	Target, DataRoot, Profile []byte
}
type BootstrapDirectMCP struct {
	ID, OwnerID         string
	Registered          bool
	Config, RuntimeRoot []byte
	Commands            [][]byte
}

// ObserveBootstrapMCP returns the generation from the same verified ownership
// window as the relevant projection. Read-only observers never create state or
// run agents. Caller uses a deadline context for existing lock-based readers.
func ObserveBootstrapMCP(ctx context.Context, r Request) (BootstrapMCPProjection, uint64, error) {
	return observeBootstrapMCP(ctx, r, nil)
}

// ObserveBootstrapMCPWithPolicy carries the policy bytes verified with this
// projection into protected admission. It never reuses the confirmation preimage.
func ObserveBootstrapMCPWithPolicy(ctx context.Context, r Request) (BootstrapMCPProjection, uint64, installruntime.Identity, error) {
	var preimage installruntime.Identity
	projection, generation, err := observeBootstrapMCP(ctx, r, &preimage)
	return projection, generation, preimage, err
}

func observeBootstrapMCP(ctx context.Context, r Request, preimage *installruntime.Identity) (BootstrapMCPProjection, uint64, error) {
	p := BootstrapMCPProjection{Profiles: map[string][]byte{}}
	if ctx == nil {
		return p, 0, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return p, 0, err
	}
	initial, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery || initial.PendingMutation != nil {
		return p, 0, fmt.Errorf("MCP ownership unavailable or recovery pending: %w", errOrRefused(err))
	}
	policy, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return p, 0, err
	}
	if policy.Installation.Recovery || policy.Installation.Ledger.Generation != initial.Generation || policy.Installation.Ledger.ID != initial.ID {
		return p, 0, ErrRefused
	}
	_, p.PolicyPresent = policy.Fields["enabled"]
	p.Enabled = policy.Policy.Enabled
	view, err := inspectUAPState(ctx, r)
	if err != nil {
		return p, 0, err
	}
	if view.Recovery.Required || len(view.Installations) > 1 {
		return p, 0, ErrAmbiguousInstallation
	}
	if len(view.Installations) > 0 && initial.ID == "" {
		return p, 0, ErrRefused
	}
	if len(view.Installations) == 1 {
		p.InstallationID = view.Installations[0].InstallationID
		for _, b := range view.Installations[0].Bindings {
			if !containsBootstrapClient(r.Agents, b.ClientID) {
				continue
			}
			profile, err := recordedLiveProfile(b.DataRoot, b.ClientID)
			if err != nil {
				return p, 0, err
			}
			if profile != "" && profile != clientConfig(r, portableAgent(b.ClientID)) {
				return p, 0, ErrLiveProfileConflict
			}
			p.Bindings = append(p.Bindings, BootstrapMCPBinding{Client: b.ClientID, ID: b.BindingID, Scope: b.Scope, Target: []byte(b.TargetPath), DataRoot: []byte(b.DataRoot), Profile: []byte(profile)})
		}
	}
	for _, id := range r.Agents {
		p.Profiles[id] = []byte(clientConfig(r, portableAgent(id)))
	}
	// Actual inspect validates portable/direct ownership, not just a bool derived
	// from directory existence. The native ledger's fresh absence is explicit.
	if initial.ID != "" {
		inspect := r
		inspect.Action = ActionInspect
		report, err := Run(ctx, inspect)
		if err != nil {
			return p, 0, err
		}
		for _, next := range report.NextActions {
			if next.Kind == "recover" || next.Kind == "resume" {
				return p, 0, ErrRefused
			}
		}
		for _, target := range report.Targets {
			if (target.Unit == "agent-notify" || target.Unit == "direct-mcp") && (target.Outcome != "installed" && target.Outcome != "absent") {
				return p, 0, ErrRefused
			}
		}
	}
	for id, c := range initial.Consumers {
		if !strings.HasPrefix(id, "agentnotify-mcp:") {
			continue
		}
		for _, agent := range r.Agents {
			if c.Registration == discoveryConfigPath(r, portableAgent(agent)) {
				if len(c.Commands) != 1 {
					return p, 0, ErrRefused
				}
				facts, err := clientsetup.Inspect(ctx, clientsetup.Request{ControlRoot: r.ControlRoot, RuntimeRoot: c.RuntimeRoot, ConfigPath: c.Registration, Command: c.Commands[0], Provider: discoveryProvider(portableAgent(agent)), Mode: clientsetup.Managed, ExpectedGeneration: initial.Generation})
				if err != nil || !facts.Registered {
					return p, 0, errOrRefused(err)
				}
				d := BootstrapDirectMCP{ID: id, OwnerID: initial.ID, Registered: facts.RegistrationPresent, Config: []byte(c.Registration), RuntimeRoot: []byte(c.RuntimeRoot)}
				for _, command := range c.Commands {
					d.Commands = append(d.Commands, []byte(command))
				}
				p.Direct = append(p.Direct, d)
			}
		}
	}
	sort.Slice(p.Bindings, func(i, j int) bool { return p.Bindings[i].ID < p.Bindings[j].ID })
	sort.Slice(p.Direct, func(i, j int) bool { return p.Direct[i].ID < p.Direct[j].ID })
	current, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery || current.ID != initial.ID || current.Owner != initial.Owner || current.Generation != initial.Generation || current.PendingMutation != nil {
		return p, 0, fmt.Errorf("concurrent_change: %w", errOrRefused(err))
	}
	finalPolicy, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil || finalPolicy.Installation.Recovery || finalPolicy.Installation.Ledger.Generation != initial.Generation || finalPolicy.Preimage != policy.Preimage {
		return p, 0, fmt.Errorf("concurrent_change: %w", errOrRefused(err))
	}
	if preimage != nil {
		*preimage = policy.Preimage
	}
	return p, initial.Generation, nil
}

func CheckBootstrapMCP(expected BootstrapMCPSelection, current BootstrapMCPProjection) error {
	want := expected.Projection
	if !want.PolicyPresent && current.PolicyPresent && expected.AllowPolicySeed && current.Enabled == expected.SeedEnabled {
		want.PolicyPresent = true
		want.Enabled = current.Enabled
	}
	if !reflect.DeepEqual(want, current) {
		return fmt.Errorf("concurrent_change: confirmed MCP projection changed: %w", ErrRefused)
	}
	return nil
}
func containsBootstrapClient(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func errOrRefused(err error) error {
	if err != nil {
		return err
	}
	return ErrRefused
}

// BootstrapPolicyRows exposes the effective enabled/route decisions for host
// summary without leaking raw policy into the immutable MCP projection.
func BootstrapPolicyRows(fields map[string]json.RawMessage) ([]string, error) {
	rows := []string{}
	if value, ok := fields["enabled"]; ok {
		rows = append(rows, "preserved enabled="+string(value))
	}
	if raw, ok := fields["route"]; ok {
		var route map[string]json.RawMessage
		if json.Unmarshal(raw, &route) != nil || route == nil {
			return nil, ErrRefused
		}
		// Display only effective notification routing authorities. Foreign policy
		// extensions remain owned by their writers and never become summary data.
		for _, key := range []string{"localRouting", "allowUnknownCaller", "allowCallerAsserted", "applicationPath", "teamID"} {
			if value, ok := route[key]; ok {
				if key == "applicationPath" || key == "teamID" {
					var s string
					if json.Unmarshal(value, &s) != nil {
						return nil, ErrRefused
					}
				} else {
					var v bool
					if json.Unmarshal(value, &v) != nil {
						return nil, ErrRefused
					}
				}
				rows = append(rows, "preserved route "+key+"="+string(value))
			}
		}
	}
	return rows, nil
}

func portableAgent(id string) portable.Integration { return portable.Integration(id) }
