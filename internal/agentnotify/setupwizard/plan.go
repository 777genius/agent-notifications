package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// SetupPlan is the read-only preflight shown before TTY confirmation.
// Ready means the application service can mutate after --yes; it is not a
// committed installation result.
type SetupPlan struct {
	// annotations contain full preflight destinations/identities that otherwise
	// exist only in legacy Text. They are never reconstructed by parsing prose.
	annotations []string
	Text        string
	Ready       bool
	Request     Request
	Result      Result
}

// Plan preflights without publishing intent or applying hooks/MCP.
func Plan(ctx context.Context, req Request) (SetupPlan, error) {
	req.LocalConfig = copyLocalConfig(req.LocalConfig)
	ev := evaluate(ctx, &req, false)
	text := confirmPlan(req)
	plan := SetupPlan{Text: text, Request: req, Result: attachWizardCommand(req, ev.out)}
	if ev.stop {
		return plan, ev.err
	}
	if len(ev.notifyAgents) > 0 {
		if req.StateMigration != nil {
			if err := CheckStateMigration(ctx, req.ControlRoot, *req.StateMigration); err != nil {
				plan.Result = StateMigrationFailure(req, err)
				return plan, err
			}
		}
		migration, err := PrepareStateMigration(ctx, req.ControlRoot)
		if err != nil {
			plan.Result = StateMigrationFailure(req, err)
			return plan, err
		}
		if req.StateMigration != nil && *req.StateMigration != migration {
			plan.Result = StateMigrationFailure(req, uapinstaller.ErrPlanChanged)
			return plan, uapinstaller.ErrPlanChanged
		}
		req.StateMigration = &migration
		plan.Request = req
		if migration.Required {
			text += " " + stateMigrationSummary(migration)
			plan.annotations = append(plan.annotations, stateMigrationSummary(migration))
		}
	}
	if req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair {
		var releasePackage func()
		defer func() {
			if releasePackage != nil {
				releasePackage()
			}
		}()
		for _, agent := range ev.notifyAgents {
			if !explicitAbs(clientConfig(req, agent)) {
				ev.out.Outcome, ev.out.Reason = "incomplete", "client_config_required"
				plan.Result = attachWizardCommand(req, ev.out)
				return plan, ErrRefused
			}
			if !explicitAbs(clientExecutable(req, agent)) {
				ev.out.Outcome, ev.out.Reason = "incomplete", "client_executable_required"
				plan.Result = attachWizardCommand(req, ev.out)
				return plan, ErrRefused
			}
		}
		var recoveryPending bool
		var recoveryErr error
		text, recoveryPending, recoveryErr = annotatePendingRecovery(ctx, req, ev.snap, text, &ev.out)
		if recoveryErr != nil {
			plan.Text, plan.Result = text, attachWizardCommand(req, ev.out)
			return plan, recoveryErr
		}
		if len(ev.notifyAgents) > 0 && !recoveryPending {
			acquired, release, err := acquirePlanPackage(ctx, req, ev.notifyAgents)
			if err != nil {
				reason := "package_acquisition_failed"
				if strings.Contains(err.Error(), "package_required") {
					reason = "package_required"
				} else if strings.Contains(err.Error(), "recorded_package_unavailable") {
					reason = "recorded_package_unavailable"
				}
				ev.out.Outcome, ev.out.Reason = "incomplete", reason
				plan.Result = attachWizardCommand(req, ev.out)
				return plan, err
			}
			releasePackage = release
			mat, err := materializer(acquired, ev.snap, ev.runtimeRoot)
			if err != nil {
				ev.out.Outcome, ev.out.Reason = "incomplete", err.Error()
				plan.Result = attachWizardCommand(req, ev.out)
				return plan, err
			}
			plan.annotations = append(plan.annotations, "helper destination="+mat.Roots.HelperExecutable)
			explicitGlobal, explicitPrimary := req.GlobalConfig, req.Primary
			id, err := identity(acquired, ev.snap, ev.runtimeRoot, mat, req.Action == ActionInstall)
			if err != nil {
				if mapped, handled := mapAmbiguous(err, ev.out); handled {
					plan.Result = attachWizardCommand(req, mapped)
					return plan, err
				}
				ev.out.Outcome, ev.out.Reason = "incomplete", err.Error()
				plan.Result = attachWizardCommand(req, ev.out)
				return plan, err
			}
			req.InstallationID = id.InstallationID
			req.GlobalConfig = id.GlobalConfig
			acquired.InstallationID = id.InstallationID
			if req.Action == ActionUpdate || req.Action == ActionRepair {
				if mapped, bindErr := requireLiveNotifyBindings(req, mat, id, ev.notifyAgents, ev.out); bindErr != nil {
					plan.Result = attachWizardCommand(req, mapped)
					return plan, bindErr
				}
			}
			if req.Action == ActionRepair {
				bindRepairPackages(ctx, &acquired, ev.snap, ev.runtimeRoot, ev.notifyAgents)
				req.PackageRoots = copyPackageRoots(acquired.PackageRoots)
			}
			if !retainedMetadataUpdate(mat, id, req.Action) {
				if err := reserveClientBindings(&req, mat, ev.notifyAgents); err != nil {
					if mapped, handled := mapAmbiguous(err, ev.out); handled {
						plan.Result = attachWizardCommand(req, mapped)
						return plan, err
					}
					ev.out.Outcome, ev.out.Reason = "incomplete", err.Error()
					plan.Result = attachWizardCommand(req, ev.out)
					return plan, err
				}
			}
			acquired.BindingIDs = copyBindingIDs(req.BindingIDs)
			plan.Request = req
			text += " installation-id=" + id.InstallationID
			for _, agent := range ev.notifyAgents {
				if bid := req.BindingIDs[string(agent)]; bid != "" {
					text += " " + string(agent) + "-binding-id=" + bid
				}
			}
			if retainedMetadataUpdate(mat, id, req.Action) {
				if conflict := retainedPackageIdentityConflict(req.ControlRoot, id.InstallationID, acquired.PackageRoot); conflict {
					ev.out.Outcome, ev.out.Reason = "conflict", "package_identity"
					plan.Result = attachWizardCommand(req, ev.out)
					plan.Text = text + " package-identity-conflict"
					return plan, ErrRefused
				}
				digest, err := retainedUpdateTreeDigest(ctx, mat, acquired.PackageRoot)
				if err != nil {
					ev.out.Outcome, ev.out.Reason = "incomplete", err.Error()
					plan.Result = attachWizardCommand(req, ev.out)
					return plan, err
				}
				if err := bindIdentityField(&req.TreeDigest, digest); err != nil {
					ev.out.Outcome, ev.out.Reason = "incomplete", "source_identity_drift"
					plan.Result = attachWizardCommand(req, ev.out)
					return plan, err
				}
				text = annotateRetainedMetadataUpdate(text, ev.notifyAgents)
				ev.out.NextActions = append(ev.out.NextActions, NextAction{
					Kind: "data_compatibility", Reason: domain.PluginDataCompatibilityWarning,
				})
			} else {
				failed, reason, previewed, err := bindNotifyPreviews(ctx, &req, acquired, ev.snap, ev.runtimeRoot, ev.notifyAgents)
				if err != nil {
					if reason == "" {
						mapped, mappedErr := mapPreviewFailure(ctx, req, failed, mat, id, err, ev.out)
						plan.Text = annotateRequiredUpdate(text, mapped)
						if retained, emptyErr := mat.RetainedEmpty(id.InstallationID); emptyErr == nil && retained {
							plan.Text += " data_retained=true data-compatibility-warning"
						}
						plan.Result = attachWizardCommand(req, mapped)
						return plan, mappedErr
					}
					ev.out.Outcome, ev.out.Reason = "incomplete", reason
					plan.Result = attachWizardCommand(req, ev.out)
					return plan, err
				}
				if !previewed && req.Action == ActionRepair {
					clients := make([]string, 0, len(ev.notifyAgents))
					for _, agent := range ev.notifyAgents {
						clients = append(clients, string(agent))
					}
					warning := "warning: no repair preview succeeded for " + strings.Join(clients, ", ") + "; an exact package revision or update is required"
					text += " " + warning
					plan.annotations = append(plan.annotations, warning)
				}
			}
			if (req.Action == ActionUpdate || req.Action == ActionRepair) && !retainedMetadataUpdate(mat, id, req.Action) {
				if err := prepareLegacyMigrations(&req, ev.snap, mat, id, ev.notifyAgents, explicitGlobal, explicitPrimary); err != nil {
					ev.out.Outcome, ev.out.Reason = "incomplete", "migration_preflight_failed"
					plan.Result = attachWizardCommand(req, ev.out)
					return plan, err
				}
				if len(req.MigrationBindings) > 0 {
					text += " portable-identity-migration=true"
				}
				for _, agent := range ev.notifyAgents {
					if digest := liveBindingTreeDigest(mat, id.InstallationID, string(agent)); digest != "" {
						text += " " + string(agent) + "-source-digest=" + digest
						plan.annotations = append(plan.annotations, string(agent)+" current-source-digest="+digest)
					}
				}
			}
			for _, agent := range ev.notifyAgents {
				if _, migrating := req.MigrationBindings[string(agent)]; migrating || !liveNotifyClient(mat, id.InstallationID, string(agent)) {
					continue
				}
				target, err := targetIdentity(id, req, ev.snap, mat, agent)
				if err == nil {
					err = portable.ValidateGlobalConfigParent(target.GlobalConfig)
				}
				if err != nil {
					ev.out.Outcome, ev.out.Reason = "incomplete", "global_config_parent_invalid"
					plan.Result = attachWizardCommand(req, ev.out)
					return plan, err
				}
			}
			if req.TreeDigest != "" {
				text += " source-digest=" + req.TreeDigest
			}
			if req.HelperDigest != "" {
				text += " helper-digest=" + req.HelperDigest
			}
			if req.HelperVersion != "" {
				text += " helper-version=" + req.HelperVersion
			}
			acquired.TreeDigest = req.TreeDigest
			acquired.HelperDigest = req.HelperDigest
			acquired.HelperVersion = req.HelperVersion
		}
	} else {
		var recoveryErr error
		text, _, recoveryErr = annotatePendingRecovery(ctx, req, ev.snap, text, &ev.out)
		if recoveryErr != nil {
			plan.Text, plan.Result = text, attachWizardCommand(req, ev.out)
			return plan, recoveryErr
		}
	}
	if req.Action == ActionUninstall {
		if prereqs := uninstallManualPrerequisites(ctx, req, ev.snap, ev.runtimeRoot, ev.notifyAgents); len(prereqs) > 0 {
			text = strings.Replace(text, "required=none", "required=external-uninstall", 1)
			text += " required-external-uninstall=" + strings.Join(prereqs, ",")
			ev.out.NextActions = append(ev.out.NextActions, NextAction{
				Kind: "external-uninstall", Agents: prereqs, Reason: "attest_codex_plugin_removed",
			})
		}
	}
	ev.out.Outcome, ev.out.Reason = "ready", ""
	ev.out.InstallationID = req.InstallationID
	plan.Ready = true
	req.RuntimeRoot = ev.runtimeRoot
	plan.Text = text
	plan.Request = req
	plan.Result = ev.out
	return plan, nil
}

func previewNotifyPlan(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, agent portable.Integration) (uapinstaller.Plan, error) {
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	id, err := identity(req, snap, runtimeRoot, mat, true)
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	if err := guardLiveProfile(mat, id.InstallationID, string(agent), clientConfig(req, agent)); err != nil {
		return uapinstaller.Plan{}, err
	}
	return mat.PreviewPlan(ctx, portablesetup.MaterializeRequest{
		LocalConfig: req.LocalConfig,
		Identity:    id, Integration: agent, PackageRoot: clientPackageRoot(req, agent),
		ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
		SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
		TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
		OperationID: wizardMutationID("plan-"+req.Action, agent, 0), Operation: wizardPackageOp(req.Action),
	})
}

func acquirePlanPackage(ctx context.Context, req Request, notifyAgents []portable.Integration) (Request, func(), error) {
	cleanup := func() {}
	if len(notifyAgents) == 0 {
		return req, cleanup, nil
	}
	recordedPath, recordedVersion := desiredPackage(req)
	if !explicitAbs(req.PackageRoot) && recordedPath == "" && recordedVersion != "" && req.ReleaseDownloadRoot == "" && req.PackageFetcher == nil {
		return req, cleanup, fmt.Errorf("%w: recorded_package_unavailable", ErrRefused)
	}
	packageRoot, release, err := resolvePackageRoot(ctx, req, recordedPath, recordedVersion)
	if err != nil {
		return req, cleanup, err
	}
	req.PackageRoot = packageRoot
	return req, release, nil
}

func annotateRetainedMetadataUpdate(text string, agents []portable.Integration) string {
	text = strings.Replace(text, "required=restart,request-permission,test-notification permission-dialog=explicit delivery=not_verified", "required=none permission-dialog=skipped delivery=not_verified", 1)
	text += " data_retained=true metadata-only data-compatibility-warning"
	var names []string
	for _, agent := range agents {
		names = append(names, string(agent))
	}
	if len(names) > 0 {
		text += " phases=1-update:" + strings.Join(names, ",")
	}
	return text
}

func annotateRequiredUpdate(text string, out Result) string {
	var updates, adds []string
	for _, next := range out.NextActions {
		switch next.Kind {
		case "update":
			updates = append(updates, next.Agents...)
		case "install":
			adds = append(adds, next.Agents...)
		}
	}
	if len(updates) == 0 {
		return text
	}
	text += " required-update=" + strings.Join(updates, ",")
	text += " phases=1-update:" + strings.Join(updates, ",")
	if len(adds) > 0 {
		text += ";2-add:" + strings.Join(adds, ",")
	}
	return text
}

func bindNotifyPreviews(ctx context.Context, req *Request, previewReq Request, snap installruntime.InstalledSnapshot, runtimeRoot string, notifyAgents []portable.Integration) (portable.Integration, string, bool, error) {
	if req == nil {
		return "", "", false, nil
	}
	previewed := false
	for _, agent := range notifyAgents {
		try := previewReq
		try.PackageRoots = req.PackageRoots
		// Plan has already resolved a zip into previewReq.PackageRoot. Only
		// per-client repair roots may override that acquired directory.
		if root := req.PackageRoots[string(agent)]; root != "" {
			try.PackageRoot = root
		}
		preview, err := previewNotifyPlan(ctx, try, snap, runtimeRoot, agent)
		if err != nil {
			if req.Action == ActionRepair && len(notifyAgents) > 1 && (exactRepairRevision(err) || portablesetup.IsUpdateRequired(err)) {
				continue
			}
			return agent, "", previewed, err
		}
		if reserved := req.BindingIDs[string(agent)]; reserved != "" && preview.BindingID != "" && preview.BindingID != reserved {
			return agent, "binding_id_drift", previewed, ErrRefused
		}
		if err := bindSourceIdentity(req, preview); err != nil {
			return agent, "source_identity_drift", previewed, err
		}
		previewed = true
		previewReq.TreeDigest = req.TreeDigest
		previewReq.HelperDigest = req.HelperDigest
		previewReq.HelperVersion = req.HelperVersion
	}
	return "", "", previewed, nil
}

func bindSourceIdentity(req *Request, preview uapinstaller.Plan) error {
	if req == nil {
		return nil
	}
	if req.Action != ActionRepair {
		if err := bindIdentityField(&req.TreeDigest, preview.TreeDigest); err != nil {
			return err
		}
	}
	if err := bindIdentityField(&req.HelperDigest, preview.HelperDigest); err != nil {
		return err
	}
	return bindIdentityField(&req.HelperVersion, preview.HelperVersion)
}

func bindIdentityField(dst *string, src string) error {
	if dst == nil || src == "" {
		return nil
	}
	if *dst == "" {
		*dst = src
		return nil
	}
	if *dst != src {
		return fmt.Errorf("%w: source_identity_drift", ErrRefused)
	}
	return nil
}

func mapPreviewFailure(ctx context.Context, req Request, agent portable.Integration, mat portablesetup.Materializer, id portablesetup.Identity, err error, out Result) (Result, error) {
	if mapped, handled := mapAmbiguous(err, out); handled {
		return mapped, err
	}
	if errors.Is(err, uapinstaller.ErrNotInstalled) {
		out.Outcome, out.Reason = "incomplete", "not_installed"
		return out, err
	}
	if errors.Is(err, uapinstaller.ErrCompatibilityUnavailable) || errors.Is(err, uapinstaller.ErrTargetFactsUnavailable) {
		return siblingCompatibilityUnavailable(agent, req, out), err
	}
	if portablesetup.IsUpdateRequired(err) {
		others, _ := mat.OtherLiveClients(id.InstallationID, string(agent))
		return updateRequired(req, agent, others, liveUpdateAgents(ctx, req, mat, id, agent), unboundSelectedAgents(req, mat, id, agent), out, err)
	}
	out.Outcome, out.Reason = "incomplete", "portable_preflight_failed"
	return out, err
}
