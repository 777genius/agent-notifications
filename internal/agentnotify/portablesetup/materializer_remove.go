package portablesetup

import (
	"context"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Remove uninstalls one live client. It preflights the managed artifact and
// refuses pending journals before locator revoke. The wizard recovers first.
func (m Materializer) Remove(ctx context.Context, req MaterializeRequest) (finalErr error) {
	if ctx == nil {
		return ErrPreflight
	}
	if req.Integration == portable.Cursor || req.Integration == portable.CopilotVSCode {
		if err := m.validate(req, false); err != nil {
			return err
		}
	}
	state, err := m.Store.Load()
	if err != nil {
		return err
	}
	installation, ok := findInstallation(state, req.Identity.InstallationID)
	if ok && installation.DataRetained && len(installation.Clients) == 0 {
		return nil
	}
	if err := m.validate(req, false); err != nil {
		return err
	}
	state, err = m.Store.Load()
	if err != nil {
		return err
	}
	installation, ok = findInstallation(state, req.Identity.InstallationID)
	if ok && installation.DataRetained && len(installation.Clients) == 0 {
		return nil
	}
	if !ok {
		return fmt.Errorf("%w: portable binding is not installed", ErrPreflight)
	}
	var found *domain.ClientBinding
	var receipt domain.DataReceipt
	for _, binding := range installation.Clients {
		if binding.ClientID != ClientID(req.Integration) {
			continue
		}
		if found != nil {
			return fmt.Errorf("%w: duplicate UAP client identity", ErrPreflight)
		}
		item := binding
		found = &item
		receipt = installation.DataReceipts[binding.DataReceiptID]
	}
	if found == nil {
		return ErrAlreadyAbsent
	}
	if req.Integration == portable.Cursor || req.Integration == portable.CopilotVSCode {
		eng, err := m.engine(req, new(uint64), nil)
		if err != nil {
			return err
		}
		if err := eng.VerifyProfileAuthority(ctx, installation.InstallationID, found.ClientBindingID); err != nil {
			return err
		}
	}
	release, err := m.beginMutation(ctx, &req)
	if err != nil {
		return err
	}
	defer release()
	snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil || snap.Recovery {
		return fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
	}
	eng, err := m.engine(req, &req.ExpectedGeneration, nil)
	if err != nil {
		return err
	}
	prepared, err := m.prepareRemove(ctx, eng, uapinstaller.Request{
		Operation: uapinstaller.OpRemove, ClientID: ClientID(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID,
		ExternalUninstalled: req.ExternalUninstalled,
	}, req)
	if err != nil {
		return err
	}
	defer func() { _ = prepared.Close() }()
	pb, needsFreeze, err := m.removableBindingForRetry(ctx, req, *found, receipt, snap.Ledger)
	if err != nil {
		return err
	}
	if needsFreeze {
		generation, err := m.freezeLegacyUninstallBindings(ctx, []legacyUninstallBinding{{
			identity: req.Identity, client: *found, receipt: receipt, profile: req.ClientConfigRoot, binding: pb,
		}})
		if err != nil {
			return err
		}
		req.ExpectedGeneration = generation
		snap, err = installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
		if err != nil || snap.Recovery {
			return fmt.Errorf("%w: frozen uninstall snapshot unavailable", ErrPreflight)
		}
		pb, err = resolveRemovableBinding(req.Identity, req.Integration, *found, receipt, req.ClientConfigRoot, snap.Ledger)
		if err != nil {
			return err
		}
	}
	kernelReq := Request{
		Binding: pb, ExpectedGeneration: req.ExpectedGeneration, Discovery: req.Discovery,
		SourceRevision: req.SourceRevision, SourceDigest: req.SourceDigest, Profile: req.ClientConfigRoot,
		DataReceiptID: found.DataReceiptID,
	}
	res, err := m.Kernel.matchingReservation(kernelReq, "uninstall")
	if err != nil {
		return err
	}
	if res == nil {
		published, created, err := m.Kernel.publishIntent(ctx, kernelReq, req.ExpectedGeneration, "uninstall", "revoke-locator", []string{"direct-mcp"})
		if err != nil {
			return err
		}
		kernelReq.ExpectedGeneration = published.Generation
		req.ExpectedGeneration = published.Generation
		res = created
	}
	kernelReq.Reservation = res
	if req.HoldOnly {
		return fmt.Errorf("%w: %w", ErrPreflight, ErrExternalUninstall)
	}
	if err := m.Kernel.RevokeBinding(ctx, kernelReq); err != nil {
		return err
	}
	removed, err := eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return persistResult(removed, err)
	}
	defer func() { finalErr = finalizationResult(removed, finalErr) }()
	if req.KeepReservation {
		return nil
	}
	if req.Discovery.ConfigPath == "" {
		return m.Kernel.finishHandoff(ctx, kernelReq, res)
	}
	snap, err = installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil {
		return err
	}
	reqBody := Request{Binding: pb, ExpectedGeneration: snap.Ledger.Generation, Discovery: req.Discovery, Reservation: res, Profile: req.ClientConfigRoot}
	if _, err = m.Kernel.HandoffReverse(ctx, reqBody); err != nil {
		return err
	}
	return m.Kernel.finishHandoff(ctx, reqBody, res)
}
