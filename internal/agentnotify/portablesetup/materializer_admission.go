package portablesetup

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func (m Materializer) beginMutation(ctx context.Context, req *MaterializeRequest) (func(), error) {
	release, err := installruntime.AcquireCoordinatorLease(ctx, req.Identity.ControlRoot)
	if err != nil {
		return nil, err
	}
	if _, err = installruntime.Recover(ctx, req.Identity.ControlRoot); err != nil {
		release()
		return nil, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil {
		release()
		return nil, err
	}
	if req.Integration == portable.CopilotVSCode && req.ExpectedGeneration != snap.Ledger.Generation {
		release()
		return nil, ErrConcurrentChange
	}
	req.ExpectedGeneration = snap.Ledger.Generation
	return release, nil
}

// RecoverJournals restores the Notifications kernel journal, then UAP
// RecoverCurrent, without a new Prepare (§7.4.2).
func (m Materializer) RecoverJournals(ctx context.Context, req MaterializeRequest) error {
	return m.recoverOwnedJournals(ctx, req)
}

// RecoverGroupJournals checks every selected client's recorded authority before
// recovering the shared kernel and UAP journals once.
func (m Materializer) RecoverGroupJournals(ctx context.Context, reqs []MaterializeRequest) error {
	return m.recoverOwnedJournals(ctx, reqs...)
}

// recoverOwnedJournals restores a pending Notifications kernel journal, then
// releases the coordinator lease before UAP Recover (§7.4.2).
func (m Materializer) recoverOwnedJournals(ctx context.Context, reqs ...MaterializeRequest) error {
	if ctx == nil || len(reqs) == 0 {
		return ErrPreflight
	}
	for _, req := range reqs[1:] {
		if physicalRoot(req.Identity.ControlRoot) != physicalRoot(reqs[0].Identity.ControlRoot) {
			return fmt.Errorf("%w: group recovery requires one control root", ErrPreflight)
		}
	}
	for _, req := range reqs {
		if err := m.VerifyRecoveryAuthority(ctx, req); err != nil {
			return err
		}
	}
	req := reqs[0]
	release, err := installruntime.AcquireCoordinatorLease(ctx, req.Identity.ControlRoot)
	if err != nil {
		return err
	}
	_, recoverErr := installruntime.Recover(ctx, req.Identity.ControlRoot)
	release()
	if recoverErr != nil {
		return recoverErr
	}
	eng, err := m.engine(req, new(uint64), nil)
	if err != nil {
		return err
	}
	_, err = eng.RecoverCurrent(ctx)
	return err
}

// VerifyRecoveryAuthority checks the selected client's recorded authority
// without acquiring a mutation lease or replaying either journal.
func (m Materializer) VerifyRecoveryAuthority(ctx context.Context, req MaterializeRequest) error {
	if ctx == nil {
		return ErrPreflight
	}
	if err := validateMigrationRecovery(req); err != nil {
		return err
	}
	if req.Integration == portable.CopilotVSCode {
		if err := m.validate(req, false); err != nil {
			return err
		}
		eng, err := m.engine(req, new(uint64), nil)
		if err != nil {
			return err
		}
		state, err := m.Store.Load()
		if err != nil {
			return err
		}
		for _, installation := range state.Installations {
			for _, binding := range installation.Clients {
				if binding.ClientID == ClientID(req.Integration) {
					if err := eng.VerifyProfileAuthority(ctx, installation.InstallationID, binding.ClientBindingID); err != nil {
						return err
					}
				}
			}
		}
	}

	if req.Integration == portable.Cursor {
		if err := m.validate(req, false); err != nil {
			return err
		}
		// Refuse original physical authority before AN recovery/lease effects.
		// Existing owners must revalidate their recorded token, never recapture.
		eng, err := m.engine(req, new(uint64), nil)
		if err != nil {
			return err
		}
		state, err := m.Store.Load()
		if err != nil {
			return err
		}
		recorded := false
		for _, installation := range state.Installations {
			for _, binding := range installation.Clients {
				if binding.ClientID == "cursor" {
					recorded = true
					if err := eng.VerifyProfileAuthority(ctx, installation.InstallationID, binding.ClientBindingID); err != nil {
						return err
					}
				}
			}
		}
		if !recorded {
			selected, _ := clients.As[*cursorinstall.Adapter](m.Registry, domain.ClientID("cursor"))
			profile, err := selected.ResolveProfileRoot(req.ClientConfigRoot)
			if err != nil {
				return err
			}
			// A fresh profile check grants no authority: discard its token.
			// Public Prepare still captures and freezes the actual new owner.
			if _, err := selected.CaptureProfileAuthority(ctx, domain.DetectedClient{ClientID: domain.ClientCursor, ConfigRoot: profile}); err != nil {
				return err
			}
		}
	}
	return nil
}

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}

func (m Materializer) validate(req MaterializeRequest, install bool) error {
	switch req.Integration {
	case portable.Codex, portable.Claude, portable.Pi:
	case portable.CopilotVSCode:
		if selected, ok := clients.As[*vscode.LocalAdapter](m.Registry, domain.ClientVSCode); !ok || selected == nil || req.LocalConfig == nil {
			return fmt.Errorf("%w: selected Local adapter and explicit selections required", ErrPreflight)
		}
		if filepath.Dir(req.LocalConfig.ProfileSettingsPath) != req.ClientConfigRoot {
			return ErrPreflight
		}
	case portable.Cursor:
		// Public UAP098 composes selected ProjectArgs. Only the explicitly
		// selected vendor adapter may enter; Prepare validates original ancestry.
		if selected, ok := clients.As[*cursorinstall.Adapter](m.Registry, domain.ClientID("cursor")); !ok || selected == nil {
			return fmt.Errorf("%w: selected Cursor adapter required", ErrPreflight)
		}
	default:
		return ErrPreflight
	}
	if !explicitAbs(req.ClientConfigRoot) {
		return fmt.Errorf("%w: client config root must be explicit", ErrPreflight)
	}
	if req.Integration == portable.Cursor {
		selected, _ := clients.As[*cursorinstall.Adapter](m.Registry, domain.ClientID("cursor"))
		if _, err := selected.ResolveProfileRoot(req.ClientConfigRoot); err != nil {
			return err
		}
	}
	if (install || req.Integration != portable.Pi) && (req.ClientExecutable == "" || !filepath.IsAbs(req.ClientExecutable)) {
		return fmt.Errorf("%w: client executable must be explicit", ErrPreflight)
	}
	if install && req.Integration == portable.Pi && portable.ValidatePiExecutable(req.ClientExecutable, runtime.GOOS) != nil {
		return fmt.Errorf("%w: Windows Pi requires the native standalone executable", ErrPreflight)
	}
	if install && !explicitAbs(req.PackageRoot) {
		return fmt.Errorf("%w: package root must be an explicit absolute path", ErrPreflight)
	}
	helper := m.Roots.HelperExecutable
	if req.HelperExecutable != "" {
		if !explicitAbs(req.HelperExecutable) {
			return fmt.Errorf("%w: helper override must be explicit", ErrPreflight)
		}
		helper = req.HelperExecutable
	}
	if !explicitAbs(helper) {
		return fmt.Errorf("%w: helper must be explicit", ErrPreflight)
	}
	return nil
}
