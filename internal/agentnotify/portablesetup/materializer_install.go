package portablesetup

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func (m Materializer) Install(ctx context.Context, req MaterializeRequest) (_ portable.Binding, finalErr error) {
	if ctx == nil {
		return portable.Binding{}, ErrPreflight
	}
	if err := m.validate(req, true); err != nil {
		return portable.Binding{}, err
	}
	if req.Integration == portable.Pi {
		m.piHandoffDone = new(bool)
	}
	var closePackage func()
	var composeErr error
	m, req, closePackage, composeErr = m.composeLocalPackage(ctx, req, true)
	if composeErr != nil {
		return portable.Binding{}, composeErr
	}
	defer closePackage()

	template, err := Complete(req.Identity, req.Integration, ClientID(req.Integration), string(domain.ScopeUser), req.Identity.ScopeRoot, req.Identity.ControlRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	if err := m.recoverOwnedJournals(ctx, req); err != nil {
		return portable.Binding{}, err
	}
	release, err := m.beginMutation(ctx, &req)
	if err != nil {
		return portable.Binding{}, err
	}
	defer release()
	if _, err := m.engine(req, new(uint64), nil); err != nil {
		return portable.Binding{}, err
	}
	action := string(packageOperation(req))
	if req.Operation == uapinstaller.OpRefreshProjection {
		if err := m.validateRefreshHandoff(req); err != nil {
			return portable.Binding{}, err
		}
		action = req.HandoffAction
	} else if req.Operation == uapinstaller.OpRepair && req.restoreMigrationProjection {
		if err := m.validateRestoreMigrationHandoff(req); err != nil {
			return portable.Binding{}, err
		}
		action = req.HandoffAction
	} else if req.HandoffAction != "" {
		return portable.Binding{}, fmt.Errorf("%w: handoff action override requires validated migration projection", ErrPreflight)
	}
	gen, res, err := m.Kernel.handoffForward(ctx, Request{
		Binding: template, ExpectedGeneration: req.ExpectedGeneration, Discovery: req.Discovery,
		SourceRevision: req.SourceRevision, SourceDigest: req.SourceDigest,
		TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
		Profile: req.ClientConfigRoot,
	}, action)
	if err != nil {
		return portable.Binding{}, err
	}
	if (req.Operation == uapinstaller.OpRefreshProjection || req.restoreMigrationProjection) && res == nil {
		// A migration can have no direct-MCP discovery. In that case
		// handoffForward has no work, but the UAP commit still needs the
		// previously confirmed kernel reservation.
		snap, readErr := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
		if readErr != nil || snap.Ledger.PendingMutation == nil {
			return portable.Binding{}, fmt.Errorf("%w: migration projection reservation disappeared", ErrPreflight)
		}
		intent, _, readErr := readMigrationIntentTarget(snap.Ledger, req.Identity.ControlRoot, req.Integration)
		if readErr != nil || intent.Action != req.HandoffAction {
			return portable.Binding{}, fmt.Errorf("%w: migration projection intent changed", ErrPreflight)
		}
		cp := *snap.Ledger.PendingMutation
		res = &cp
	}
	generation := gen
	eng, err := m.engine(req, &generation, res)
	if err != nil {
		return portable.Binding{}, err
	}
	known, err := m.knownTargets(req.Identity.InstallationID, ClientID(req.Integration))
	if err != nil {
		return portable.Binding{}, err
	}
	result, err := m.apply(ctx, eng, uapinstaller.Request{
		Operation: publicPackageOperation(req), PackageRoot: req.PackageRoot, ClientID: ClientID(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID,
		RequiredComponents: requiredComponents(req),
		KnownTargets:       known,
	}, req)
	if err != nil {
		return portable.Binding{}, persistResult(result, wrapUpdateRequired(err))
	}
	defer func() { finalErr = finalizationResult(result, finalErr) }()
	pb, err := Complete(req.Identity, req.Integration, result.Binding.ClientID, result.Binding.Scope, result.Binding.TargetPath, result.Binding.DataRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	if pb.Integration == portable.Pi && m.piHandoffDone != nil && !*m.piHandoffDone {
		generation, err = m.commitPiBinding(ctx, req, pb, res)
		if err != nil {
			return portable.Binding{}, err
		}
	}
	if err := recordClientFact(filepath.Dir(m.Roots.StateFile), clientFact{BindingID: pb.BindingID, ConfigRoot: req.ClientConfigRoot, Executable: req.ClientExecutable}, ClientID(req.Integration)); err != nil {
		return portable.Binding{}, err
	}
	if !req.KeepReservation {
		if err := m.Kernel.finishHandoff(ctx, Request{Binding: pb, ExpectedGeneration: generation, Reservation: res}, res); err != nil {
			return portable.Binding{}, err
		}
	}
	return pb, nil
}

func packageOperation(req MaterializeRequest) uapinstaller.Operation {
	if req.Operation == "" {
		return uapinstaller.OpInstall
	}
	return req.Operation
}

// An explicit owned empty transition changes selected components through the
// public reviewed refresh route. The host intent retains its update/repair
// action; public Prepare still requires the same profile and installed owner.
func publicPackageOperation(req MaterializeRequest) uapinstaller.Operation {
	op := packageOperation(req)
	if req.Integration == portable.CopilotVSCode && req.LocalConfig != nil && !req.LocalConfig.NativeStop && len(req.LocalConfig.MCPServers) == 0 && len(req.LocalConfig.Skills) == 0 && (op == uapinstaller.OpUpdate || op == uapinstaller.OpRepair) {
		return uapinstaller.OpRefreshProjection
	}
	return op
}

func (m Materializer) Update(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	req.Operation = uapinstaller.OpUpdate
	return m.Install(ctx, req)
}

func (m Materializer) Repair(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	req.Operation = uapinstaller.OpRepair
	return m.Install(ctx, req)
}

// RestoreMigrationProjection repairs the exact old UAP projection while the
// frozen update or repair migration intent remains pending. The caller then
// refreshes to the replacement and retires this old binding.
func (m Materializer) RestoreMigrationProjection(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	req.Operation = uapinstaller.OpRepair
	req.KeepReservation = true
	req.restoreMigrationProjection = true
	if err := m.validateRestoreMigrationHandoff(req); err != nil {
		return portable.Binding{}, err
	}
	return m.Install(ctx, req)
}

// RefreshProjection reprojects one committed UAP client through Install's
// normal callback. The new portable consumer is committed first; the caller
// then retires the old binding with ReplaceCommittedBinding.
func (m Materializer) RefreshProjection(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	req.Operation = uapinstaller.OpRefreshProjection
	req.KeepReservation = true
	return m.Install(ctx, req)
}

func (m Materializer) SwitchRetained(ctx context.Context, req MaterializeRequest) (_ uapinstaller.Result, finalErr error) {
	if ctx == nil {
		return uapinstaller.Result{}, ErrPreflight
	}
	if req.Integration == portable.Cursor || req.Integration == portable.CopilotVSCode {
		if err := m.validate(req, true); err != nil {
			return uapinstaller.Result{}, err
		}
	}
	if !explicitAbs(req.PackageRoot) {
		return uapinstaller.Result{}, fmt.Errorf("%w: package root must be an explicit absolute path", ErrPreflight)
	}
	if req.Identity.InstallationID == "" {
		return uapinstaller.Result{}, fmt.Errorf("%w: installation id is required", ErrPreflight)
	}
	generation := req.ExpectedGeneration
	eng, err := m.engine(req, &generation, nil)
	if err != nil {
		return uapinstaller.Result{}, err
	}
	got, err := eng.SwitchRetained(ctx, uapinstaller.Request{
		PackageRoot: req.PackageRoot, InstallationID: req.Identity.InstallationID,
		OperationID: req.OperationID,
	}, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return got, persistResult(got, wrapUpdateRequired(err))
	}
	defer func() { finalErr = finalizationResult(got, finalErr) }()
	if got.Outcome != uapinstaller.OutcomeCompleted && got.Outcome != uapinstaller.OutcomeUnchanged {
		return got, fmt.Errorf("%w: %s", ErrPreflight, got.Reason)
	}
	view, inspectErr := eng.Inspect(ctx)
	if inspectErr != nil {
		return got, inspectErr
	}
	for _, installation := range view.Installations {
		if installation.InstallationID != req.Identity.InstallationID {
			continue
		}
		if got.Binding.TreeDigest != "" && installation.TreeDigest != got.Binding.TreeDigest {
			return got, fmt.Errorf("%w: retained source %s desired %s", ErrPreflight, installation.TreeDigest, got.Binding.TreeDigest)
		}
		if installation.TreeDigest == "" {
			return got, fmt.Errorf("%w: retained source digest is missing after switch", ErrPreflight)
		}
		if len(installation.Bindings) != 0 {
			return got, fmt.Errorf("%w: retained switch materialized a client", ErrPreflight)
		}
		return got, nil
	}
	return got, fmt.Errorf("%w: installation %s", ErrPreflight, req.Identity.InstallationID)
}
