package portablesetup

import (
	"context"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/install/uapinstaller"
)

func findInstallation(state domain.StateFileV2, id string) (domain.Installation, bool) {
	for _, item := range state.Installations {
		if item.InstallationID == id {
			return item, true
		}
	}
	return domain.Installation{}, false
}

// RetainedEmpty reports a data_retained installation with zero live clients.
func (m Materializer) RetainedEmpty(installationID string) (bool, error) {
	if installationID == "" {
		return false, nil
	}
	state, err := m.Store.Load()
	if err != nil {
		return false, err
	}
	installation, ok := findInstallation(state, installationID)
	if !ok {
		return false, nil
	}
	return installation.DataRetained && len(installation.Clients) == 0, nil
}

func (m Materializer) OtherLiveClients(installationID, adding string) ([]string, error) {
	adding = ClientID(Integration(adding))
	if installationID == "" {
		return nil, nil
	}
	state, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	installation, ok := findInstallation(state, installationID)
	if !ok {
		return nil, nil
	}
	var others []string
	for _, binding := range installation.Clients {
		if binding.ClientID != adding {
			others = append(others, string(Integration(binding.ClientID)))
		}
	}
	return others, nil
}

func (m Materializer) PreviewInstall(ctx context.Context, req MaterializeRequest) (uapinstaller.Plan, error) {
	return m.previewInstall(ctx, req, true)
}

// PreviewPlan is read-only Prepare for host confirmation. It does not recover
// journals, publish intent, or Apply.
func (m Materializer) PreviewPlan(ctx context.Context, req MaterializeRequest) (uapinstaller.Plan, error) {
	return m.previewInstall(ctx, req, false)
}

func (m Materializer) previewInstall(ctx context.Context, req MaterializeRequest, recover bool) (uapinstaller.Plan, error) {
	if ctx == nil {
		return uapinstaller.Plan{}, ErrPreflight
	}
	if err := m.validate(req, true); err != nil {
		return uapinstaller.Plan{}, err
	}
	var closePackage func()
	var composeErr error
	m, req, closePackage, composeErr = m.composeLocalPackage(ctx, req, false)
	if composeErr != nil {
		return uapinstaller.Plan{}, composeErr
	}
	defer closePackage()

	eng, err := m.engine(req, new(uint64), nil)
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	known, err := m.knownTargets(req.Identity.InstallationID, ClientID(req.Integration))
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	if recover {
		if err := m.recoverOwnedJournals(ctx, req); err != nil {
			return uapinstaller.Plan{}, err
		}
	}
	prepared, err := eng.Prepare(ctx, uapinstaller.Request{
		Operation: publicPackageOperation(req), PackageRoot: req.PackageRoot, ClientID: ClientID(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID + "-preview",
		RequiredComponents: requiredComponents(req),
		KnownTargets:       known,
	})
	if err != nil {
		return uapinstaller.Plan{}, wrapUpdateRequired(err)
	}
	defer func() { _ = prepared.Close() }()
	if err := confirmLocalSelection(req, prepared.Plan()); err != nil {
		return uapinstaller.Plan{}, err
	}
	return prepared.Plan(), nil
}

func (m Materializer) GuardSecondClient(ctx context.Context, req MaterializeRequest) error {
	others, err := m.OtherLiveClients(req.Identity.InstallationID, ClientID(req.Integration))
	if err != nil {
		return err
	}
	if len(others) == 0 {
		return nil
	}
	plan, err := m.PreviewPlan(ctx, req)
	if err != nil {
		return wrapUpdateRequired(err)
	}
	state, err := m.Store.Load()
	if err != nil {
		return err
	}
	installation, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok {
		return nil
	}
	if installation.Source.TreeDigest != "" && plan.TreeDigest != "" && installation.Source.TreeDigest != plan.TreeDigest {
		return fmt.Errorf("%w: recorded digest %s desired %s", ErrUpdateRequired, installation.Source.TreeDigest, plan.TreeDigest)
	}
	return nil
}
