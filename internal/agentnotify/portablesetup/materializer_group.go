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

// GroupRemoveResult is one client's outcome from RemoveGroup.
type GroupRemoveResult struct {
	Integration   portable.Integration
	AlreadyAbsent bool
}

// validateGroupIdentities allows each client to retain its own scope while
// sharing the installation and managed runtime custody.
func validateGroupIdentities(reqs []MaterializeRequest) (map[string]Identity, error) {
	identities := make(map[string]Identity, len(reqs))
	base := reqs[0].Identity
	for _, req := range reqs {
		client := ClientID(req.Integration)
		if req.Integration != portable.Claude && req.Integration != portable.Codex && req.Integration != portable.CopilotVSCode && req.Integration != portable.Pi {
			return nil, fmt.Errorf("%w: unsupported group client %s", ErrPreflight, client)
		}
		if _, exists := identities[client]; exists {
			return nil, fmt.Errorf("%w: duplicate group client %s", ErrPreflight, client)
		}
		id := req.Identity
		if id.InstallationID != base.InstallationID || id.ComponentID != base.ComponentID || id.Owner != base.Owner ||
			physicalRoot(id.ControlRoot) != physicalRoot(base.ControlRoot) || physicalRoot(id.RuntimeRoot) != physicalRoot(base.RuntimeRoot) {
			return nil, fmt.Errorf("%w: group installation identity differs for %s", ErrPreflight, client)
		}
		identities[client] = id
	}
	return identities, nil
}

func (m Materializer) ApplyGroup(ctx context.Context, reqs []MaterializeRequest) (_ []portable.Binding, finalErr error) {
	if ctx == nil || len(reqs) != 2 {
		return nil, ErrPreflight
	}
	for _, req := range reqs {
		if req.Integration == portable.Pi {
			m.piHandoffDone = new(bool)
		}
	}
	for i := range reqs {
		if err := m.validate(reqs[i], true); err != nil {
			return nil, err
		}
		if packageOperation(reqs[i]) != packageOperation(reqs[0]) {
			return nil, fmt.Errorf("%w: group requires one package operation", ErrPreflight)
		}
		if reqs[i].PackageRoot != reqs[0].PackageRoot {
			if packageOperation(reqs[0]) != uapinstaller.OpRepair || packageOperation(reqs[i]) != uapinstaller.OpRepair {
				return nil, fmt.Errorf("%w: group requires one package root", ErrPreflight)
			}
		}
	}
	identities, err := validateGroupIdentities(reqs)
	if err != nil {
		return nil, err
	}
	// Local's reserved binding contributes canonical Stop bytes to a shared
	// release snapshot. Mixed revision repair retains each client's frozen
	// package source instead of sharing Local's composed tree with siblings.
	sharedPackage := true
	for _, req := range reqs[1:] {
		sharedPackage = sharedPackage && req.PackageRoot == reqs[0].PackageRoot
	}
	reqs = append([]MaterializeRequest(nil), reqs...)
	handoffStarted := false
	for i := range reqs {
		if reqs[i].Integration != portable.CopilotVSCode {
			continue
		}
		composed, local, closePackage, err := m.composeLocalPackage(ctx, reqs[i], true)
		if err != nil {
			return nil, err
		}
		defer func() {
			if !handoffStarted {
				closePackage()
			}
		}()
		m, reqs[i] = composed, local
		if sharedPackage {
			for j := range reqs {
				reqs[j].PackageRoot = local.PackageRoot
			}
		}
	}
	// Validate all binding templates before recovery or reservation mutation.
	templates := make([]portable.Binding, len(reqs))
	for i, req := range reqs {
		templates[i], err = Complete(req.Identity, req.Integration, ClientID(req.Integration), string(domain.ScopeUser), req.Identity.ScopeRoot, req.Identity.ControlRoot)
		if err != nil {
			return nil, err
		}
	}
	if err := m.recoverOwnedJournals(ctx, reqs...); err != nil {
		return nil, err
	}
	release, err := m.beginMutation(ctx, &reqs[0])
	if err != nil {
		return nil, err
	}
	defer release()
	generation := reqs[0].ExpectedGeneration
	var res *installruntime.PendingMutation
	// The shared source must survive any outcome after the first handoff starts.
	handoffStarted = true
	for i := range reqs {
		gen, next, err := m.Kernel.handoffForward(ctx, Request{
			Binding: templates[i], ExpectedGeneration: generation, Discovery: reqs[i].Discovery,
			SourceRevision: reqs[i].SourceRevision, SourceDigest: reqs[i].SourceDigest,
			TreeDigest: reqs[i].TreeDigest, HelperDigest: reqs[i].HelperDigest, HelperVersion: reqs[i].HelperVersion,
			Profile: reqs[i].ClientConfigRoot,
		}, string(packageOperation(reqs[i])))
		if err != nil {
			return nil, GroupHandoffError{Integration: reqs[i].Integration, Err: err}
		}
		generation = gen
		if next != nil {
			res = next
		}
		reqs[i].ExpectedGeneration = generation
	}
	engineReq := reqs[0]
	for _, req := range reqs {
		if req.Integration == portable.Claude {
			engineReq = req
			break
		}
	}
	eng, err := m.engineWithIdentities(engineReq, &generation, res, identities)
	if err != nil {
		return nil, err
	}
	var targets []uapinstaller.ClientTarget
	for _, req := range reqs {
		targets = append(targets, uapinstaller.ClientTarget{
			ClientID: ClientID(req.Integration), ClientConfigRoot: req.ClientConfigRoot,
			ClientExecutable: req.ClientExecutable, PackageRoot: req.PackageRoot,
			ExternalUninstalled: req.ExternalUninstalled,
		})
	}
	result, err := m.apply(ctx, eng, uapinstaller.Request{
		Operation: packageOperation(reqs[0]), PackageRoot: reqs[0].PackageRoot,
		InstallationID: reqs[0].Identity.InstallationID, OperationID: reqs[0].OperationID,
		RequiredComponents: groupRequiredComponents(reqs), ClientExecutable: reqs[0].ClientExecutable,
		Targets: targets,
	}, reqs...)
	if err != nil {
		return nil, persistResult(result, wrapUpdateRequired(err))
	}
	defer func() { finalErr = finalizationResult(result, finalErr) }()
	view, err := eng.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	bindings, err := installedGroupBindings(reqs, result, view)
	if err != nil {
		return nil, err
	}
	var out []portable.Binding
	for i, req := range reqs {
		pb := bindings[i]
		if pb.Integration == portable.Pi && m.piHandoffDone != nil && !*m.piHandoffDone {
			generation, err = m.commitPiBinding(ctx, req, pb, res)
			if err != nil {
				return nil, err
			}
		}
		if err := recordClientFact(filepath.Dir(m.Roots.StateFile), clientFact{BindingID: pb.BindingID, ConfigRoot: req.ClientConfigRoot, Executable: req.ClientExecutable}, ClientID(req.Integration)); err != nil {
			return nil, err
		}
		if !req.KeepReservation {
			if err := m.Kernel.finishHandoff(ctx, Request{Binding: pb, ExpectedGeneration: generation, Reservation: res}, res); err != nil {
				return nil, err
			}
		}
		out = append(out, pb)
	}
	return out, nil
}

// installedGroupBindings verifies each selected client in one post-apply
// observation. Unselected sibling bindings remain outside this operation.
func installedGroupBindings(reqs []MaterializeRequest, result uapinstaller.Result, view uapinstaller.Inspection) ([]portable.Binding, error) {
	if view.Recovery.Required {
		return nil, uapinstaller.ErrRecoveryRequired
	}
	out := make([]portable.Binding, len(reqs))
	for i, req := range reqs {
		if result.InstallationID != "" && result.InstallationID != req.Identity.InstallationID {
			return nil, fmt.Errorf("%w: group result installation differs", ErrPreflight)
		}
		// UAP's selected result binds a client to its exact published target.
		// Other profiles for that client are not selected by this operation.
		selectedID := ""
		for _, selected := range result.Targets {
			if selected.ClientID != ClientID(req.Integration) || selected.BindingID == "" {
				continue
			}
			if selectedID != "" && selectedID != selected.BindingID {
				return nil, fmt.Errorf("%w: ambiguous group result for %s", ErrPreflight, req.Integration)
			}
			selectedID = selected.BindingID
		}
		if selectedID == "" && result.Binding.ClientID == ClientID(req.Integration) {
			selectedID = result.Binding.BindingID
		}
		if selectedID == "" {
			return nil, fmt.Errorf("%w: group result omitted binding identity for %s", ErrPreflight, req.Integration)
		}
		matches := 0
		for _, installation := range view.Installations {
			if installation.InstallationID != req.Identity.InstallationID {
				continue
			}
			for _, binding := range installation.Bindings {
				if binding.ClientID != ClientID(req.Integration) || binding.BindingID != selectedID {
					continue
				}
				matches++
				if matches != 1 {
					return nil, fmt.Errorf("%w: duplicate group client %s", ErrPreflight, req.Integration)
				}
				pb, err := Complete(req.Identity, req.Integration, binding.ClientID, binding.Scope, binding.TargetPath, binding.DataRoot)
				if err != nil {
					return nil, err
				}
				if pb.BindingID != binding.BindingID {
					return nil, fmt.Errorf("%w: group binding identity differs for %s", ErrPreflight, req.Integration)
				}
				out[i] = pb
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("%w: group result omitted %s", ErrPreflight, req.Integration)
		}
	}
	return out, nil
}

// RemoveGroup uninstalls both clients in one UAP RemoveGroup. It preflights
// the managed artifacts and refuses pending journals before locator revoke.
func (m Materializer) RemoveGroup(ctx context.Context, reqs []MaterializeRequest) (_ []GroupRemoveResult, finalErr error) {
	if ctx == nil || len(reqs) != 2 {
		return nil, ErrPreflight
	}
	for i := range reqs {
		if err := m.validate(reqs[i], false); err != nil {
			return nil, err
		}
		if reqs[i].HoldOnly {
			return nil, fmt.Errorf("%w: group remove does not hold a Codex attestation", ErrPreflight)
		}
	}
	if _, err := validateGroupIdentities(reqs); err != nil {
		return nil, err
	}
	state, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	installation, ok := findInstallation(state, reqs[0].Identity.InstallationID)
	if !ok {
		return nil, fmt.Errorf("%w: portable binding is not installed", ErrPreflight)
	}
	if installation.DataRetained && len(installation.Clients) == 0 {
		return []GroupRemoveResult{
			{Integration: reqs[0].Integration, AlreadyAbsent: true},
			{Integration: reqs[1].Integration, AlreadyAbsent: true},
		}, nil
	}
	release, err := m.beginMutation(ctx, &reqs[0])
	if err != nil {
		return nil, err
	}
	defer release()
	generation := reqs[0].ExpectedGeneration
	engineReq := reqs[0]
	for _, req := range reqs {
		if req.Integration == portable.Claude {
			engineReq = req
			break
		}
	}
	eng, err := m.engine(engineReq, &generation, nil)
	if err != nil {
		return nil, err
	}
	var targets []uapinstaller.ClientTarget
	for _, req := range reqs {
		targets = append(targets, uapinstaller.ClientTarget{
			ClientID: ClientID(req.Integration), ClientConfigRoot: req.ClientConfigRoot,
			ClientExecutable: req.ClientExecutable, ExternalUninstalled: req.ExternalUninstalled,
		})
	}
	prepared, err := m.prepareRemove(ctx, eng, uapinstaller.Request{
		Operation: uapinstaller.OpRemove, InstallationID: reqs[0].Identity.InstallationID,
		OperationID: reqs[0].OperationID, ClientExecutable: engineReq.ClientExecutable,
		ExternalUninstalled: reqs[0].ExternalUninstalled || reqs[1].ExternalUninstalled,
		Targets:             targets,
	}, reqs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = prepared.Close() }()
	// Resolve both before any revocation so an ambiguous second client cannot
	// leave the first client removed while its sibling remains unresolved.
	bindings := make([]portable.Binding, len(reqs))
	clients := make([]domain.ClientBinding, len(reqs))
	receipts := make([]domain.DataReceipt, len(reqs))
	var legacyEntries []legacyUninstallBinding
	var intentTargets []IntentTarget
	for i, req := range reqs {
		matches := 0
		for _, client := range installation.Clients {
			if client.ClientID != ClientID(req.Integration) {
				continue
			}
			matches++
			if matches > 1 {
				return nil, fmt.Errorf("%w: duplicate UAP client identity", ErrPreflight)
			}
			snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
			if err != nil || snap.Recovery {
				return nil, fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
			}
			clients[i] = client
			receipts[i] = installation.DataReceipts[client.DataReceiptID]
			var needsFreeze bool
			bindings[i], needsFreeze, err = m.removableBindingForRetry(ctx, req, client, receipts[i], snap.Ledger)
			if err != nil {
				return nil, err
			}
			if needsFreeze {
				legacyEntries = append(legacyEntries, legacyUninstallBinding{
					identity: req.Identity, client: client, receipt: receipts[i], profile: req.ClientConfigRoot, binding: bindings[i],
				})
			}
			if _, err := portable.ExactLocator(bindings[i]); err != nil {
				return nil, fmt.Errorf("%w: existing locator differs for %s: %v", ErrPreflight, req.Integration, err)
			}
			key, _, _, err := bindings[i].Registration()
			if err != nil {
				return nil, err
			}
			b := bindings[i]
			intentTargets = append(intentTargets, IntentTarget{
				Client: string(Integration(client.ClientID)), InstallationID: req.Identity.InstallationID,
				BindingID: client.ClientBindingID, DataReceiptID: client.DataReceiptID,
				Profile: req.ClientConfigRoot, OldConsumerKey: key, OldBinding: &b,
				Units: []string{"direct-mcp"},
			})
		}
	}
	if len(legacyEntries) != 0 {
		generation, err = m.freezeLegacyUninstallBindings(ctx, legacyEntries)
		if err != nil {
			return nil, err
		}
		snap, err := installruntime.ReadInstalledSnapshot(reqs[0].Identity.ControlRoot)
		if err != nil || snap.Recovery {
			return nil, fmt.Errorf("%w: frozen group uninstall snapshot unavailable", ErrPreflight)
		}
		for i, req := range reqs {
			if clients[i].ClientID == "" {
				continue
			}
			bindings[i], err = resolveRemovableBinding(req.Identity, req.Integration, clients[i], receipts[i], req.ClientConfigRoot, snap.Ledger)
			if err != nil {
				return nil, err
			}
		}
	}
	var res *installruntime.PendingMutation
	var lastPB portable.Binding
	live := 0
	out := make([]GroupRemoveResult, len(reqs))
	for i, req := range reqs {
		out[i].Integration = req.Integration
		var found *domain.ClientBinding
		for _, binding := range installation.Clients {
			if binding.ClientID != ClientID(req.Integration) {
				continue
			}
			item := binding
			found = &item
		}
		if found == nil {
			out[i].AlreadyAbsent = true
			continue
		}
		pb := bindings[i]
		if pb == (portable.Binding{}) {
			return nil, fmt.Errorf("%w: historical binding not resolved", ErrPreflight)
		}
		kernelReq := Request{
			Binding: pb, ExpectedGeneration: generation, Discovery: req.Discovery,
			SourceRevision: req.SourceRevision, SourceDigest: req.SourceDigest, Profile: req.ClientConfigRoot,
			DataReceiptID: found.DataReceiptID, IntentTargets: intentTargets, Reservation: res,
		}
		matched, err := m.Kernel.matchingReservation(kernelReq, "uninstall")
		if err != nil {
			return nil, err
		}
		if matched != nil {
			res = matched
			kernelReq.Reservation = res
		}
		if res == nil {
			published, created, err := m.Kernel.publishIntent(ctx, kernelReq, generation, "uninstall", "revoke-locator", []string{"direct-mcp"})
			if err != nil {
				return nil, err
			}
			generation = published.Generation
			res = created
			kernelReq.ExpectedGeneration = generation
			kernelReq.Reservation = res
		}
		if err := m.Kernel.RevokeBinding(ctx, kernelReq); err != nil {
			return nil, err
		}
		snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
		if err != nil {
			return nil, err
		}
		generation = snap.Ledger.Generation
		lastPB = pb
		live++
	}
	if live == 0 {
		return out, nil
	}
	removed, err := eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return nil, persistResult(removed, err)
	}
	defer func() { finalErr = finalizationResult(removed, finalErr) }()
	byClient := map[string]uapinstaller.ClientResult{}
	for _, item := range removed.Targets {
		byClient[item.ClientID] = item
	}
	for i, req := range reqs {
		item, ok := byClient[ClientID(req.Integration)]
		if ok && item.Materialization == string(domain.MaterializationAbsent) {
			out[i].AlreadyAbsent = true
		}
	}
	if !reqs[0].KeepReservation && res != nil {
		if err := m.Kernel.finishHandoff(ctx, Request{Binding: lastPB, ExpectedGeneration: generation, Reservation: res}, res); err != nil {
			return nil, err
		}
	}
	return out, nil
}
