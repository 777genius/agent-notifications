package portablesetup

import (
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Recovery may change installed files, so first match the request against the
// durable, fingerprint-verified migration intent. The full UAP and locator
// checks below still run after replay has restored a readable installation.
func validateMigrationRecovery(req MaterializeRequest) error {
	if req.Operation != uapinstaller.OpRefreshProjection && !req.restoreMigrationProjection {
		if req.HandoffAction != "" {
			return fmt.Errorf("%w: handoff action override requires validated migration projection", ErrPreflight)
		}
		return nil
	}
	ledger, _, err := installruntime.ReadOwnership(req.Identity.ControlRoot)
	if err != nil {
		return err
	}
	intent, target, err := readMigrationIntentTarget(ledger, req.Identity.ControlRoot, req.Integration)
	if err != nil || intent.Action != req.HandoffAction || target.InstallationID != req.Identity.InstallationID || target.Profile != req.ClientConfigRoot {
		return fmt.Errorf("%w: migration recovery intent differs", ErrPreflight)
	}
	selected := target.NewBinding
	if req.restoreMigrationProjection {
		selected = target.OldBinding
	}
	want := Identity{InstallationID: selected.InstallationID, ComponentID: selected.ComponentID, Owner: selected.Owner,
		ScopeRoot: selected.ScopeRoot, ControlRoot: selected.ControlRoot, GlobalConfig: selected.GlobalConfig,
		RuntimeRoot: selected.RuntimeRoot, Primary: selected.Primary}
	got := req.Identity
	got.ScopeRoot, got.ControlRoot, got.GlobalConfig, got.RuntimeRoot = physicalRoot(got.ScopeRoot), physicalRoot(got.ControlRoot), physicalRoot(got.GlobalConfig), physicalRoot(got.RuntimeRoot)
	if got != want || selected.Integration != req.Integration {
		return fmt.Errorf("%w: migration recovery identity differs", ErrPreflight)
	}
	return nil
}

func (m Materializer) validateRefreshHandoff(req MaterializeRequest) error {
	if req.HandoffAction != "update" && req.HandoffAction != "repair" {
		return fmt.Errorf("%w: projection refresh requires update or repair intent", ErrPreflight)
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil || snap.Recovery {
		return fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
	}
	intent, target, err := readMigrationIntentTarget(snap.Ledger, req.Identity.ControlRoot, req.Integration)
	if err != nil || intent.Action != req.HandoffAction || target.InstallationID != req.Identity.InstallationID || target.Profile != req.ClientConfigRoot {
		return fmt.Errorf("%w: projection refresh intent differs", ErrPreflight)
	}
	old, replacement := *target.OldBinding, *target.NewBinding
	if old == replacement || old.Integration != req.Integration || old.InstallationID != req.Identity.InstallationID {
		return fmt.Errorf("%w: projection refresh identity differs", ErrPreflight)
	}
	sameIdentity := old
	sameIdentity.GlobalConfig, sameIdentity.Primary = replacement.GlobalConfig, replacement.Primary
	if sameIdentity != replacement {
		return fmt.Errorf("%w: projection refresh changes UAP identity", ErrPreflight)
	}
	oldKey, _, _, oldErr := old.Registration()
	newKey, _, _, newErr := replacement.Registration()
	if oldErr != nil || newErr != nil || target.OldConsumerKey != oldKey || target.NewConsumerKey != newKey || target.BindingID != old.BindingID {
		return fmt.Errorf("%w: projection refresh registration differs", ErrPreflight)
	}
	state, err := m.Store.Load()
	if err != nil {
		return err
	}
	installation, ok := findInstallation(state, old.InstallationID)
	if !ok {
		return fmt.Errorf("%w: UAP installation missing", ErrPreflight)
	}
	var client domain.ClientBinding
	count := 0
	for _, candidate := range installation.Clients {
		if candidate.ClientID == ClientID(req.Integration) {
			client = candidate
			count++
		}
	}
	if count != 1 || client.DataReceiptID != target.DataReceiptID {
		return fmt.Errorf("%w: UAP client or receipt ambiguous", ErrPreflight)
	}
	expectedOld, err := expectedStoredBinding(req.Identity, req.Integration, client, installation.DataReceipts[client.DataReceiptID])
	if err != nil {
		return err
	}
	variants, err := portable.CommittedBindingVariants(snap.Ledger, expectedOld)
	if err != nil || len(variants) == 0 || len(variants) > 2 {
		return fmt.Errorf("%w: persisted historical binding differs", ErrPreflight)
	}
	oldPresent, newPresent := false, false
	for _, variant := range variants {
		switch variant {
		case old:
			oldPresent = true
		case replacement:
			newPresent = true
		default:
			return fmt.Errorf("%w: foreign projection refresh consumer", ErrPreflight)
		}
	}
	if !oldPresent || (len(variants) == 2 && !newPresent) {
		return fmt.Errorf("%w: persisted historical binding differs", ErrPreflight)
	}
	wantNew, err := Complete(req.Identity, req.Integration, client.ClientID, client.Scope, client.TargetLocator, installation.DataReceipts[client.DataReceiptID].Locator)
	if err != nil || wantNew != replacement {
		return fmt.Errorf("%w: requested replacement differs from UAP binding", ErrPreflight)
	}
	if present, err := portable.ExactLocator(old); err != nil || !present {
		return fmt.Errorf("%w: old locator is absent or foreign", ErrPreflight)
	}
	if newPresent {
		if _, err := portable.ExactLocator(replacement); err != nil {
			return fmt.Errorf("%w: replacement locator is foreign", ErrPreflight)
		}
	}
	return nil
}

// validateRestoreMigrationHandoff authorizes a repair of precisely the old UAP
// projection frozen by a pending migration. A repair cannot borrow an unrelated
// update intent merely by supplying its action string.
func (m Materializer) validateRestoreMigrationHandoff(req MaterializeRequest) error {
	if req.HandoffAction != "update" && req.HandoffAction != "repair" {
		return fmt.Errorf("%w: migration restore requires update or repair intent", ErrPreflight)
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil || snap.Recovery {
		return fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
	}
	intent, target, err := readMigrationIntentTarget(snap.Ledger, req.Identity.ControlRoot, req.Integration)
	if err != nil || intent.Action != req.HandoffAction || target.InstallationID != req.Identity.InstallationID || target.Profile != req.ClientConfigRoot {
		return fmt.Errorf("%w: migration restore intent differs", ErrPreflight)
	}
	old, replacement := *target.OldBinding, *target.NewBinding
	if old == replacement || old.Integration != req.Integration || old.InstallationID != req.Identity.InstallationID {
		return fmt.Errorf("%w: migration restore identity differs", ErrPreflight)
	}
	sameIdentity := old
	sameIdentity.GlobalConfig, sameIdentity.Primary = replacement.GlobalConfig, replacement.Primary
	if sameIdentity != replacement {
		return fmt.Errorf("%w: migration restore changes UAP identity", ErrPreflight)
	}
	oldKey, _, _, oldErr := old.Registration()
	newKey, _, _, newErr := replacement.Registration()
	if oldErr != nil || newErr != nil || target.OldConsumerKey != oldKey || target.NewConsumerKey != newKey || target.BindingID != old.BindingID {
		return fmt.Errorf("%w: migration restore registration differs", ErrPreflight)
	}
	state, err := m.Store.Load()
	if err != nil {
		return err
	}
	installation, ok := findInstallation(state, old.InstallationID)
	if !ok {
		return fmt.Errorf("%w: UAP installation missing", ErrPreflight)
	}
	var client domain.ClientBinding
	count := 0
	for _, candidate := range installation.Clients {
		if candidate.ClientID == ClientID(req.Integration) {
			client = candidate
			count++
		}
	}
	if count != 1 || client.DataReceiptID != target.DataReceiptID {
		return fmt.Errorf("%w: UAP client or receipt ambiguous", ErrPreflight)
	}
	expectedOld, err := expectedStoredBinding(req.Identity, req.Integration, client, installation.DataReceipts[client.DataReceiptID])
	if err != nil || expectedOld != old {
		return fmt.Errorf("%w: requested old projection differs from UAP binding", ErrPreflight)
	}
	variants, err := portable.CommittedBindingVariants(snap.Ledger, expectedOld)
	if err != nil || len(variants) != 1 || variants[0] != old {
		return fmt.Errorf("%w: committed old consumer differs", ErrPreflight)
	}
	if present, err := portable.ExactLocator(old); err != nil || !present {
		return fmt.Errorf("%w: old locator is absent or foreign", ErrPreflight)
	}
	return nil
}
