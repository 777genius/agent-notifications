package uapinstaller

import (
	"context"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/planner"
)

// IdentityRequest selects an installation without capturing a package snapshot.
type IdentityRequest struct {
	ClientID       string
	InstallationID string
	Allocate       bool
	// DeclaredName is the plugin.json name used to derive a prospective
	// BindingID for a new install. Empty leaves BindingID unset until Prepare
	// or an existing binding is found.
	DeclaredName     string
	ClientConfigRoot string
}

// IdentityReservation is the §7.2 identity view. BindingID and TargetPath are
// filled from an existing binding, or derived from DeclaredName without
// staging when this is a new install.
type IdentityReservation struct {
	InstallationID string
	BindingID      string
	Scope          string
	TargetPath     string
}

// ReserveIdentity returns installation and binding IDs without creating
// TempRoot, capturing a snapshot, or mutating client config.
func (e *Engine) ReserveIdentity(req IdentityRequest) (IdentityReservation, error) {
	switch req.ClientID {
	case "", string(domain.ClientCodex), string(domain.ClientClaude):
	default:
		return IdentityReservation{}, fmt.Errorf("%w: client %q is not in this beta", ErrUnsupported, req.ClientID)
	}
	out := IdentityReservation{Scope: string(domain.ScopeUser), InstallationID: req.InstallationID}
	state, err := e.store.Load()
	if err != nil {
		return IdentityReservation{}, err
	}
	if out.InstallationID == "" {
		switch len(state.Installations) {
		case 1:
			out.InstallationID = state.Installations[0].InstallationID
		case 0:
			if req.Allocate {
				id, err := domain.NewInstallationID()
				if err != nil {
					return IdentityReservation{}, err
				}
				out.InstallationID = id
			}
		default:
			return IdentityReservation{}, fmt.Errorf("%w: %d installations; pass InstallationID", ErrAmbiguousInstallations, len(state.Installations))
		}
	}
	if req.ClientID == "" || out.InstallationID == "" {
		return out, nil
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != out.InstallationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID != req.ClientID {
				continue
			}
			out.BindingID = binding.ClientBindingID
			out.TargetPath = binding.TargetLocator
			if binding.Scope != "" {
				out.Scope = binding.Scope
			}
			return out, nil
		}
	}
	e.reserveNewBinding(&out, req)
	return out, nil
}

func (e *Engine) reserveNewBinding(out *IdentityReservation, req IdentityRequest) {
	if req.DeclaredName == "" || out.InstallationID == "" || req.ClientID == "" {
		return
	}
	physicalID := domain.ComputePhysicalArtifactID(req.DeclaredName, out.InstallationID)
	client := domain.DetectedClient{ClientID: domain.ClientID(req.ClientID), Status: domain.DetectionDetected, ConfigRoot: req.ClientConfigRoot}
	target, err := planner.Planner{ManagedRoot: e.cfg.ManagedRoot}.ResolveTarget(context.Background(), client, domain.ScopeUser, physicalID)
	if err != nil {
		return
	}
	out.TargetPath = target.ActivePath
	out.BindingID = domain.ComputeClientBindingID(out.InstallationID, req.ClientID, out.Scope, target.ActivePath)
}
