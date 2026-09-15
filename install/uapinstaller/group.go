package uapinstaller

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/planner"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/usecase"
)

func (e *Engine) prepareGroup(ctx context.Context, req Request) (*PreparedOperation, error) {
	if err := validateGroupTargets(req.Targets); err != nil {
		return nil, err
	}
	switch req.Operation {
	case OpInstall, OpUpdate, OpRepair:
		return e.prepareMutatingGroup(ctx, req)
	case OpRemove:
		return e.prepareRemoveGroup(ctx, req)
	default:
		return nil, fmt.Errorf("%w: unknown operation", ErrInvalidRequest)
	}
}

func validateGroupTargets(targets []ClientTarget) error {
	if len(targets) != 2 {
		return fmt.Errorf("%w: group operations accept exactly two Claude/Codex targets", ErrInvalidRequest)
	}
	seen := map[string]struct{}{}
	for _, target := range targets {
		if target.ClientID != "claude" && target.ClientID != "codex" {
			return fmt.Errorf("%w: client %q is not in this beta", ErrUnsupported, target.ClientID)
		}
		if _, ok := seen[target.ClientID]; ok {
			return fmt.Errorf("%w: duplicate client %s", ErrInvalidRequest, target.ClientID)
		}
		seen[target.ClientID] = struct{}{}
	}
	return nil
}

func (e *Engine) prepareMutatingGroup(ctx context.Context, req Request) (*PreparedOperation, error) {
	roots := make([]string, len(req.Targets))
	for i, target := range req.Targets {
		root := firstNonEmpty(target.PackageRoot, req.PackageRoot)
		if root == "" || !validRoot(root) {
			return nil, fmt.Errorf("%w: PackageRoot must be an explicit absolute clean path", ErrInvalidRequest)
		}
		if overlappingRoots(e.cfg.TempRoot, root) {
			return nil, fmt.Errorf("%w: TempRoot must not overlap PackageRoot", ErrInvalidRequest)
		}
		roots[i] = root
	}
	mixed := false
	for _, root := range roots[1:] {
		if root != roots[0] {
			mixed = true
			break
		}
	}
	if mixed && req.Operation != OpRepair {
		return nil, fmt.Errorf("%w: mixed package roots in one group are unpublished", ErrUnsupported)
	}
	if err := os.MkdirAll(e.cfg.TempRoot, 0700); err != nil {
		return nil, err
	}
	e.report(ProgressPrepare)
	handle := &PreparedOperation{engine: e, req: req}
	ldr, err := newLoader()
	if err != nil {
		return nil, err
	}
	cachedEnvelope := map[string]domain.PackageEnvelope{}
	cachedDigest := map[string]string{}
	envelopes := make([]domain.PackageEnvelope, len(req.Targets))
	for i, root := range roots {
		if envelope, ok := cachedEnvelope[root]; ok {
			envelopes[i] = envelope
		} else {
			snapshot, err := snapshotLocalPackage(ctx, e.cfg.TempRoot, root)
			if err != nil {
				_ = handle.closeLocked()
				return nil, err
			}
			handle.snapshots = append(handle.snapshots, snapshot)
			if handle.snapshot.Root == "" {
				handle.snapshot = snapshot
			}
			if err := e.assessSnapshot(ctx, snapshot); err != nil {
				_ = handle.closeLocked()
				return nil, err
			}
			if req.Operation == OpInstall {
				if err := e.refuseRecordedDigestRewrite(req.InstallationID, snapshot.TreeDigest); err != nil {
					_ = handle.closeLocked()
					return nil, err
				}
			}
			e.reuseMatchingSourceIdentity(req.InstallationID, &snapshot, req.Operation == OpUpdate)
			handle.snapshots[len(handle.snapshots)-1] = snapshot
			if i == 0 {
				handle.snapshot = snapshot
			}
			envelope, err := ldr.Load(ctx, domain.LoadInput{
				SnapshotRoot: snapshot.Root, TreeDigest: snapshot.TreeDigest,
				ExecutableFiles: snapshot.ExecutableFiles, Source: snapshot.Source,
			})
			if err != nil {
				_ = handle.closeLocked()
				return nil, err
			}
			cachedEnvelope[root] = envelope
			cachedDigest[root] = snapshot.TreeDigest
			envelopes[i] = envelope
		}
		if req.Operation == OpRepair {
			if err := e.refuseRepairRevisionRewrite(req.InstallationID, req.Targets[i].ClientID, cachedDigest[root]); err != nil {
				_ = handle.closeLocked()
				return nil, err
			}
		}
	}
	handle.envelope = envelopes[0]
	handle.envelopes = envelopes
	inputs, clients, err := e.groupAddInputs(req, envelopes, true, false)
	if err != nil {
		_ = handle.closeLocked()
		return nil, err
	}
	if req.Operation == OpUpdate || req.Operation == OpRepair {
		for _, client := range clients {
			if _, _, err := e.requireExistingBinding(Request{
				InstallationID: req.InstallationID, ClientID: string(client.ClientID),
				ClientConfigRoot: client.ConfigRoot, ClientExecutable: client.ExecutablePath,
				Operation: req.Operation,
			}); err != nil {
				_ = handle.closeLocked()
				return nil, err
			}
		}
	}
	helper, _ := e.helper()
	svc := e.lifecycle(helper, BindingFacts{})
	preview, err := e.previewGroup(ctx, svc, req, inputs)
	if err != nil {
		_ = handle.closeLocked()
		return nil, wrapLifecycleError(err)
	}
	missing := missingRequired(envelopes[0], req.RequiredComponents)
	for _, envelope := range envelopes[1:] {
		missing = append(missing, missingRequired(envelope, req.RequiredComponents)...)
	}
	helperVersion, helperDigest := e.helperIdentity()
	handle.clients = clients
	handle.client = clients[0]
	handle.plan = Plan{
		Operation: req.Operation, SourceRoot: roots[0], TreeDigest: envelopes[0].TreeDigest,
		DigestAlgorithm: handle.snapshot.DigestAlgorithm, ClientID: string(clients[0].ClientID),
		ConfigRoot: clients[0].ConfigRoot, InstallationID: firstNonEmpty(req.InstallationID, preview.InstallationID),
		HelperVersion: helperVersion, HelperDigest: helperDigest, RequiredMissing: missing,
		NoChange: groupPreviewUnchanged(preview),
	}
	if len(preview.Targets) > 0 {
		handle.plan.TargetPath = preview.Targets[0].Plan.ActivePath
		handle.plan.BindingID = domain.ComputeClientBindingID(handle.plan.InstallationID, string(preview.Targets[0].Plan.ClientID), string(preview.Targets[0].Plan.Scope), preview.Targets[0].Plan.ActivePath)
		handle.artifact = preview.Targets[0].Plan.PhysicalArtifactID
	}
	for i, target := range preview.Targets {
		client := clients[i]
		handle.plan.Targets = append(handle.plan.Targets, PlanTarget{
			ClientID: string(client.ClientID), ConfigRoot: client.ConfigRoot,
			TargetPath: target.Plan.ActivePath, TreeDigest: envelopes[i].TreeDigest,
			BindingID: domain.ComputeClientBindingID(handle.plan.InstallationID, string(target.Plan.ClientID), string(target.Plan.Scope), target.Plan.ActivePath),
			NoChange:  target.NoChange,
		})
	}
	handle.facts = BindingFacts{
		InstallationID: handle.plan.InstallationID, ClientID: handle.plan.ClientID,
		BindingID: handle.plan.BindingID, TargetPath: handle.plan.TargetPath,
		OperationID: req.OperationID, TreeDigest: envelopes[0].TreeDigest,
	}
	if len(missing) != 0 {
		_ = handle.closeLocked()
		return nil, fmt.Errorf("%w: %v", ErrIncomplete, missing)
	}
	return handle, nil
}

func (e *Engine) previewGroup(ctx context.Context, svc usecase.Service, req Request, inputs []usecase.AddInput) (usecase.GroupResult, error) {
	group := usecase.GroupInput{
		Targets: inputs, CompatibilityChecks: inputs, OperationGroupID: firstNonEmpty(req.OperationID, "group"),
		DryRun: true, Confirmed: false, Repair: req.Operation == OpRepair,
	}
	switch req.Operation {
	case OpUpdate:
		return svc.UpdateGroup(ctx, group)
	case OpRepair:
		return svc.RepairGroup(ctx, group)
	default:
		return svc.AddGroup(ctx, group)
	}
}

func groupPreviewUnchanged(preview usecase.GroupResult) bool {
	if len(preview.Targets) == 0 {
		return false
	}
	for _, target := range preview.Targets {
		if !target.NoChange {
			return false
		}
	}
	return true
}

func (e *Engine) groupAddInputs(req Request, envelopes []domain.PackageEnvelope, dry, confirmed bool) ([]usecase.AddInput, []domain.DetectedClient, error) {
	if len(envelopes) != len(req.Targets) {
		return nil, nil, fmt.Errorf("%w: group envelope count", ErrInvalidRequest)
	}
	var inputs []usecase.AddInput
	var clients []domain.DetectedClient
	for i, target := range req.Targets {
		client, err := detectedClient(Request{
			Operation: req.Operation, ClientID: target.ClientID,
			ClientConfigRoot: target.ClientConfigRoot, ClientExecutable: firstNonEmpty(target.ClientExecutable, req.ClientExecutable),
		})
		if err != nil {
			return nil, nil, err
		}
		inputs = append(inputs, usecase.AddInput{
			Envelope: envelopes[i], Client: client, Scope: domain.ScopeUser, DryRun: dry, Confirmed: confirmed,
			PersistAuthoritativeObservations: e.persistObservations,
			InstallationID:                   req.InstallationID, OperationID: req.OperationID,
			BackendExecutable: client.ExecutablePath,
		})
		clients = append(clients, client)
	}
	return inputs, clients, nil
}

func (e *Engine) prepareRemoveGroup(ctx context.Context, req Request) (*PreparedOperation, error) {
	selector := firstNonEmpty(req.Selector, req.InstallationID)
	if selector == "" {
		return nil, fmt.Errorf("%w: remove requires InstallationID or Selector", ErrInvalidRequest)
	}
	state, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	installation, ok := findInstall(state, selector)
	if !ok {
		return nil, fmt.Errorf("%w: installation %s is not installed", ErrInvalidRequest, selector)
	}
	e.report(ProgressPrepare)
	e.report(ProgressPreflight)
	handle := &PreparedOperation{engine: e, req: req}
	helperVersion, helperDigest := e.helperIdentity()
	handle.plan = Plan{
		Operation: OpRemove, InstallationID: installation.InstallationID,
		HelperVersion: helperVersion, HelperDigest: helperDigest,
	}
	var live int
	for _, target := range req.Targets {
		client, err := detectedClient(Request{
			Operation: OpRemove, ClientID: target.ClientID,
			ClientConfigRoot: target.ClientConfigRoot, ClientExecutable: firstNonEmpty(target.ClientExecutable, req.ClientExecutable),
		})
		if err != nil {
			return nil, err
		}
		handle.clients = append(handle.clients, client)
		binding, receipt, found := findBinding(installation, client.ClientID)
		item := PlanTarget{ClientID: string(client.ClientID), ConfigRoot: client.ConfigRoot, NoChange: !found}
		if found {
			if err := e.removalPreflight(ctx, client, binding, receipt); err != nil {
				return nil, err
			}
			item.TargetPath = binding.TargetLocator
			item.BindingID = binding.ClientBindingID
			live++
			if handle.plan.TargetPath == "" {
				handle.plan.ClientID = item.ClientID
				handle.plan.ConfigRoot = item.ConfigRoot
				handle.plan.TargetPath = item.TargetPath
				handle.plan.BindingID = item.BindingID
				handle.client = client
				handle.artifact = binding.PhysicalArtifact
				handle.facts = BindingFacts{
					InstallationID: installation.InstallationID, ClientID: item.ClientID,
					BindingID: item.BindingID, Scope: binding.Scope, TargetPath: item.TargetPath,
					DataRoot: receipt.Locator, DataReceiptID: binding.DataReceiptID,
					OperationID: req.OperationID,
				}
			}
		}
		handle.plan.Targets = append(handle.plan.Targets, item)
	}
	if live == 0 {
		handle.plan.NoChange = true
	}
	return handle, nil
}

func (e *Engine) applyGroup(ctx context.Context, prepared *PreparedOperation) (Result, error) {
	if prepared.req.Operation == OpRemove {
		return e.applyRemoveGroup(ctx, prepared)
	}
	result := Result{
		Operation: prepared.req.Operation, InstallationID: prepared.plan.InstallationID,
		Binding: prepared.facts,
	}
	pending, err := e.reconcileGroupHostHandoff(ctx, prepared, &result)
	if err != nil {
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
		e.report(ProgressCommit)
		return result, err
	}
	if prepared.plan.NoChange && !pending {
		result.Outcome = OutcomeUnchanged
		result.NoChange = true
		for _, target := range prepared.plan.Targets {
			result.Targets = append(result.Targets, ClientResult{
				ClientID: target.ClientID, BindingID: target.BindingID, TreeDigest: target.TreeDigest,
			})
		}
		return result, nil
	}
	if _, err := e.helper(); err != nil {
		return Result{Operation: prepared.req.Operation, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	if err := e.ensureDirs(); err != nil {
		return Result{Operation: prepared.req.Operation, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	helper, err := e.helper()
	if err != nil {
		return Result{Operation: prepared.req.Operation, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	svc := e.lifecycle(helper, prepared.facts)
	envelopes := prepared.envelopes
	if len(envelopes) == 0 {
		envelopes = make([]domain.PackageEnvelope, len(prepared.req.Targets))
		for i := range envelopes {
			envelopes[i] = prepared.envelope
		}
	}
	inputs, _, err := e.groupAddInputs(prepared.req, envelopes, false, true)
	if err != nil {
		return Result{Operation: prepared.req.Operation, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	e.report(ProgressStage)
	group := usecase.GroupInput{
		Targets: inputs, CompatibilityChecks: inputs,
		OperationGroupID: firstNonEmpty(prepared.req.OperationID, "group"),
		Confirmed:        true, Repair: prepared.req.Operation == OpRepair,
	}
	var got usecase.GroupResult
	switch prepared.req.Operation {
	case OpUpdate:
		got, err = svc.UpdateGroup(ctx, group)
	case OpRepair:
		got, err = svc.RepairGroup(ctx, group)
	default:
		got, err = svc.AddGroup(ctx, group)
	}
	err = wrapLifecycleError(err)
	result.InstallationID = firstNonEmpty(got.InstallationID, prepared.plan.InstallationID)
	state, loadErr := e.store.Load()
	if loadErr != nil && err == nil && !got.Mutated && !prepared.plan.NoChange {
		result.Outcome = OutcomeIncomplete
		result.Reason = "committed state is unknown"
		return result, loadErr
	}
	if loadErr == nil {
		if installation, ok := findInstall(state, result.InstallationID); ok {
			e.attachLiveGroupResult(&result, prepared, installation)
		}
	}
	if err != nil {
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
		if strings.Contains(err.Error(), "run update separately") {
			result.Outcome = OutcomeConflict
			result.Reason = "update_required"
		}
		return result, err
	}
	if pending && e.anyGroupHostHandoffPending(prepared) {
		result.Outcome = OutcomeIncomplete
		result.Reason = "committed binding is not activated"
		return result, fmt.Errorf("%s", result.Reason)
	}
	if !pending && (prepared.plan.NoChange || (!got.Mutated && groupPreviewUnchanged(got))) {
		result.Outcome = OutcomeUnchanged
		result.NoChange = true
		return result, nil
	}
	result.Outcome = OutcomeCompleted
	e.report(ProgressCommit)
	e.report(ProgressActivate)
	e.report(ProgressVerify)
	e.report(ProgressComplete)
	return result, nil
}

func (e *Engine) applyRemoveGroup(ctx context.Context, prepared *PreparedOperation) (Result, error) {
	result := Result{Operation: OpRemove, InstallationID: prepared.plan.InstallationID, Binding: prepared.facts}
	var live []usecase.RemoveInput
	for i, target := range prepared.plan.Targets {
		item := ClientResult{ClientID: target.ClientID, BindingID: target.BindingID}
		if target.NoChange {
			item.Materialization = string(domain.MaterializationAbsent)
			result.Targets = append(result.Targets, item)
			continue
		}
		client := prepared.clients[i]
		live = append(live, usecase.RemoveInput{
			Selector: prepared.plan.InstallationID, Client: client, Scope: domain.ScopeUser,
			Confirmed: true, OperationID: prepared.req.OperationID, BackendExecutable: client.ExecutablePath,
			ExternalUninstalled: prepared.req.Targets[i].ExternalUninstalled || prepared.req.ExternalUninstalled,
		})
		result.Targets = append(result.Targets, item)
	}
	if len(live) == 0 {
		result.Outcome = OutcomeUnchanged
		result.Reason = "already_absent"
		result.NoChange = true
		result.DataRetained = true
		return result, nil
	}
	state, err := e.store.Load()
	if err != nil {
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
		return result, err
	}
	installation, ok := findInstall(state, prepared.plan.InstallationID)
	if !ok {
		err := fmt.Errorf("%w: installation %s is not installed", ErrInvalidRequest, prepared.plan.InstallationID)
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
		return result, err
	}
	for i, target := range prepared.plan.Targets {
		if target.NoChange {
			continue
		}
		client := prepared.clients[i]
		binding, receipt, found := findBinding(installation, client.ClientID)
		if !found {
			err := fmt.Errorf("%w: client %s is not installed", ErrInvalidRequest, client.ClientID)
			result.Outcome = OutcomeIncomplete
			result.Reason = err.Error()
			return result, err
		}
		if err := e.removalPreflight(ctx, client, binding, receipt); err != nil {
			result.Outcome = OutcomeIncomplete
			result.Reason = err.Error()
			return result, err
		}
	}
	helper, _ := e.helper()
	svc := e.lifecycle(helper, prepared.facts)
	e.report(ProgressStage)
	got, err := svc.RemoveGroup(ctx, usecase.RemoveGroupInput{
		Selector: prepared.plan.InstallationID, Targets: live,
		OperationGroupID: firstNonEmpty(prepared.req.OperationID, "remove-group"), Confirmed: true,
	})
	result.InstallationID = firstNonEmpty(got.InstallationID, prepared.plan.InstallationID)
	if err != nil {
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
		return result, wrapLifecycleError(err)
	}
	if !got.Mutated {
		result.Outcome = OutcomeUnchanged
		result.NoChange = true
	} else {
		result.Outcome = OutcomeCompleted
		e.report(ProgressCommit)
		e.report(ProgressActivate)
		e.report(ProgressVerify)
		e.report(ProgressComplete)
	}
	if view, inspectErr := e.observe(); inspectErr == nil {
		for _, installation := range view.Installations {
			if installation.InstallationID == result.InstallationID {
				result.DataRetained = installation.DataRetained
			}
		}
	}
	return result, nil
}

func (e *Engine) reconcileGroupHostHandoff(ctx context.Context, prepared *PreparedOperation, result *Result) (bool, error) {
	state, err := e.store.Load()
	if err != nil {
		return false, nil
	}
	installationID := firstNonEmpty(prepared.req.InstallationID, prepared.plan.InstallationID)
	installation, ok := findInstall(state, installationID)
	if !ok {
		return false, nil
	}
	pending := false
	for _, client := range prepared.clients {
		binding, receipt, found := findBinding(installation, client.ClientID)
		if !found {
			continue
		}
		if hostHandoffPending(binding) {
			pending = true
		}
		if e.cfg.OnCommittedBinding == nil {
			continue
		}
		facts := BindingFacts{
			InstallationID: firstNonEmpty(installation.InstallationID, installationID),
			ClientID:       binding.ClientID,
			BindingID:      binding.ClientBindingID,
			Scope:          binding.Scope,
			TargetPath:     binding.TargetLocator,
			DataRoot:       receipt.Locator,
			DataReceiptID:  binding.DataReceiptID,
			OperationID:    prepared.req.OperationID,
			TreeDigest:     recordedBindingDigest(binding, planClientDigest(prepared.plan, binding.ClientID)),
		}
		if err := e.cfg.OnCommittedBinding(ctx, facts); err != nil {
			e.attachLiveGroupResult(result, prepared, installation)
			if result.Client.ClientID == "" {
				result.Client = liveClientResult(binding, prepared.req.RequiredComponents, facts.TreeDigest)
				result.Binding = facts
			}
			return true, err
		}
	}
	return pending, nil
}

func (e *Engine) anyGroupHostHandoffPending(prepared *PreparedOperation) bool {
	state, err := e.store.Load()
	if err != nil {
		return false
	}
	installation, ok := findInstall(state, firstNonEmpty(prepared.req.InstallationID, prepared.plan.InstallationID))
	if !ok {
		return false
	}
	for _, client := range prepared.clients {
		binding, _, found := findBinding(installation, client.ClientID)
		if found && hostHandoffPending(binding) {
			return true
		}
	}
	return false
}

func (e *Engine) attachLiveGroupResult(result *Result, prepared *PreparedOperation, installation domain.Installation) {
	result.InstallationID = firstNonEmpty(installation.InstallationID, result.InstallationID)
	result.Targets = nil
	for _, client := range prepared.clients {
		binding, receipt, found := findBinding(installation, client.ClientID)
		if !found {
			continue
		}
		item := liveClientResult(binding, prepared.req.RequiredComponents, planClientDigest(prepared.plan, binding.ClientID))
		result.Targets = append(result.Targets, item)
		_ = storeLiveProfile(receipt.Locator, binding.ClientID, client.ConfigRoot)
		if result.Client.ClientID == "" {
			result.Client = item
			result.Binding = BindingFacts{
				InstallationID: result.InstallationID, ClientID: binding.ClientID,
				BindingID: binding.ClientBindingID, Scope: binding.Scope, TargetPath: binding.TargetLocator,
				DataRoot: receipt.Locator, DataReceiptID: binding.DataReceiptID,
				OperationID: prepared.req.OperationID, TreeDigest: item.TreeDigest,
			}
		}
	}
}

func (e *Engine) confirmGroupPlan(ctx context.Context, prepared *PreparedOperation) error {
	plan := prepared.plan
	if plan.Operation == OpInstall {
		if err := e.refuseRecordedDigestRewrite(plan.InstallationID, plan.TreeDigest); err != nil {
			return err
		}
	}
	state, err := e.store.Load()
	if err != nil {
		return nil
	}
	installation, ok := findInstall(state, plan.InstallationID)
	if !ok {
		if plan.Operation == OpRemove && !plan.NoChange {
			return fmt.Errorf("%w: installation is not installed", ErrPlanChanged)
		}
		return nil
	}
	for i, target := range plan.Targets {
		binding, _, found := findBinding(installation, domain.ClientID(target.ClientID))
		if plan.Operation == OpRemove {
			if target.NoChange {
				if found {
					return fmt.Errorf("%w: live target does not match confirmed plan", ErrPlanChanged)
				}
				continue
			}
			if !found {
				return fmt.Errorf("%w: client is not installed", ErrPlanChanged)
			}
			if target.BindingID != "" && binding.ClientBindingID != target.BindingID {
				return fmt.Errorf("%w: live binding does not match confirmed plan", ErrPlanChanged)
			}
			if target.TargetPath != "" && binding.TargetLocator != target.TargetPath {
				return fmt.Errorf("%w: live target does not match confirmed plan", ErrPlanChanged)
			}
			continue
		}
		if !found {
			continue
		}
		if target.TargetPath != "" && binding.TargetLocator != "" && binding.TargetLocator != target.TargetPath {
			return fmt.Errorf("%w: live target does not match confirmed plan", ErrPlanChanged)
		}
		if i == 0 && prepared.artifact != "" && plan.Operation == OpInstall {
			client := prepared.clients[i]
			resolved, resolveErr := (planner.Planner{ManagedRoot: e.cfg.ManagedRoot}).ResolveTarget(ctx, client, domain.ScopeUser, prepared.artifact)
			if resolveErr != nil {
				return fmt.Errorf("%w: %v", ErrPlanChanged, resolveErr)
			}
			if target.TargetPath != "" && resolved.ActivePath != target.TargetPath {
				return fmt.Errorf("%w: live target does not match confirmed plan", ErrPlanChanged)
			}
		}
	}
	return nil
}
