package uapinstaller

import (
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// IdentityRequest selects an installation without capturing a package snapshot.
type IdentityRequest struct {
	ClientID       string
	InstallationID string
	Allocate       bool
}

// IdentityReservation is the §7.2 identity view. BindingID is empty until an
// existing binding is found; a new install still needs Prepare for ActivePath.
type IdentityReservation struct {
	InstallationID string
	BindingID      string
	Scope          string
}

// ReserveIdentity returns installation and existing-binding IDs without
// creating TempRoot, capturing a snapshot, or mutating client config.
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
			if binding.Scope != "" {
				out.Scope = binding.Scope
			}
			return out, nil
		}
	}
	return out, nil
}
