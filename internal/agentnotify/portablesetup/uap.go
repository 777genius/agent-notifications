package portablesetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/claude"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/ports"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const portableServerName = "agent-notify"
const clientFactsFile = "client-facts.json"

// NewRegistry is the explicit Notifications composition of the two supported
// native clients. The UAP SDK intentionally rejects a nil registry.
func NewRegistry() (*clients.Registry, error) {
	return clients.NewRegistry(claude.New(), codex.New())
}

// Identity is the operator-selected existing-installer surface. BindingID and
// DataRoot are completed from the committed UAP plan, not guessed from HOME.
type Identity struct {
	InstallationID, ComponentID, Owner, ScopeRoot, ControlRoot, GlobalConfig, RuntimeRoot, Primary string
}

// UAPRoots are explicit UAP state locations. None are derived from cwd/HOME.
type UAPRoots struct {
	StateFile, LockFile, OperationsDir, PluginDataBase, ManagedRoot string
	HelperExecutable, HelperVersion                                 string
	ClaudeRunner                                                    ports.CommandRunner
	CodexRunner                                                     ports.CommandRunner
	RequireLiveProfiles                                             bool
}

// MaterializeRequest selects one client. Integration is never taken from clientInfo.
type MaterializeRequest struct {
	Identity            Identity
	Integration         portable.Integration
	ExpectedGeneration  uint64
	PackageRoot         string
	ClientConfigRoot    string
	ClientExecutable    string
	SourceRevision      string
	SourceDigest        string
	TreeDigest          string
	HelperDigest        string
	HelperVersion       string
	Discovery           Discovery
	OperationID         string
	HelperExecutable    string
	ExternalUninstalled bool
	// HandoffAction is the host action (update or repair) for a one-client
	// refresh_projection migration. The UAP operation remains refresh_projection.
	HandoffAction string
	// HoldOnly publishes the uninstall reservation and returns without locator
	// revoke or UAP mutation. Wizard uses it to keep a Codex removal pending
	// until the host attests ExternalUninstalled.
	HoldOnly bool
	// KeepReservation leaves the kernel pending mutation in place. Wizard
	// publishes one SetupIntent for the whole confirmed operation and clears
	// it after the last target, including when several clients share it.
	KeepReservation bool
	// Operation selects install, update, or repair. Empty means install.
	Operation uapinstaller.Operation
}

type Materializer struct {
	Kernel Service
	Store  statev2.Store
	Roots  UAPRoots
}

type clientFact struct {
	BindingID  string `json:"binding_id"`
	ConfigRoot string `json:"config_root"`
	Executable string `json:"executable"`
}

func clientFactsPath(dataRoot string) string { return filepath.Join(dataRoot, clientFactsFile) }

func readClientFacts(dataRoot string) (map[string]clientFact, error) {
	body, err := os.ReadFile(clientFactsPath(dataRoot))
	if os.IsNotExist(err) {
		return map[string]clientFact{}, nil
	}
	if err != nil {
		return nil, err
	}
	var facts map[string]clientFact
	if err := json.Unmarshal(body, &facts); err != nil {
		return nil, err
	}
	if facts == nil {
		facts = map[string]clientFact{}
	}
	return facts, nil
}

func recordClientFact(dataRoot string, fact clientFact, clientID string) error {
	if dataRoot == "" || clientID == "" {
		return nil
	}
	facts, err := readClientFacts(dataRoot)
	if err != nil {
		return err
	}
	facts[clientID] = fact
	body, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	return os.WriteFile(clientFactsPath(dataRoot), body, 0600)
}

func (m Materializer) knownTargets(installationID, selected string) ([]uapinstaller.TargetFacts, error) {
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
	facts, err := readClientFacts(filepath.Dir(m.Roots.StateFile))
	if err != nil {
		return nil, err
	}
	var out []uapinstaller.TargetFacts
	for _, binding := range installation.Clients {
		if binding.ClientID == selected {
			continue
		}
		fact, ok := facts[binding.ClientID]
		if !ok || fact.BindingID != binding.ClientBindingID {
			continue
		}
		if info, err := os.Stat(fact.ConfigRoot); err != nil || !info.IsDir() {
			continue
		}
		if m.Roots.RequireLiveProfiles {
			receipt := installation.DataReceipts[binding.DataReceiptID]
			if _, err := os.Stat(filepath.Join(receipt.Locator, "live-profiles.json")); err != nil {
				continue
			}
		}
		out = append(out, uapinstaller.TargetFacts{ClientID: binding.ClientID, BindingID: binding.ClientBindingID, ConfigRoot: fact.ConfigRoot, Executable: fact.Executable})
	}
	return out, nil
}

// GroupRemoveResult is one client's outcome from RemoveGroup.
type GroupRemoveResult struct {
	Integration   portable.Integration
	AlreadyAbsent bool
}

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
	req.ExpectedGeneration = snap.Ledger.Generation
	return release, nil
}

// RecoverJournals restores the Notifications kernel journal, then UAP
// RecoverCurrent, without a new Prepare (§7.4.2).
func (m Materializer) RecoverJournals(ctx context.Context, req MaterializeRequest) error {
	return m.recoverOwnedJournals(ctx, req)
}

// recoverOwnedJournals restores a pending Notifications kernel journal, then
// releases the coordinator lease before UAP Recover (§7.4.2).
func (m Materializer) recoverOwnedJournals(ctx context.Context, req MaterializeRequest) error {
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

func physicalRoot(path string) string {
	got, err := installruntime.PhysicalPath(path)
	if err != nil {
		return path
	}
	return got
}

func Complete(id Identity, integration portable.Integration, clientID, scope, activePath, dataRoot string) (portable.Binding, error) {
	if scope == "" {
		scope = string(domain.ScopeUser)
	}
	b := portable.Binding{
		Version: 1, Integration: integration, InstallationID: id.InstallationID,
		BindingID: domain.ComputeClientBindingID(id.InstallationID, clientID, scope, activePath),
		ScopeID:   scope, ComponentID: id.ComponentID, Owner: id.Owner, ScopeRoot: physicalRoot(id.ScopeRoot),
		DataRoot: physicalRoot(dataRoot), ControlRoot: physicalRoot(id.ControlRoot), GlobalConfig: physicalRoot(id.GlobalConfig),
		RuntimeRoot: physicalRoot(id.RuntimeRoot), Primary: id.Primary,
	}
	if _, _, _, err := b.Registration(); err != nil {
		return portable.Binding{}, fmt.Errorf("%w: integration=%s bindingID=%s scopeID=%s dataRoot=%q activePath=%q primary=%q", err, integration, b.BindingID, b.ScopeID, dataRoot, activePath, id.Primary)
	}
	return b, nil
}

// resolveStoredBinding binds UAP's committed client and data receipt to the
// exact portable consumer bytes, including historical GlobalConfig/Primary.
func resolveStoredBinding(id Identity, integration portable.Integration, client domain.ClientBinding, receipt domain.DataReceipt, ledger installruntime.Ledger) (portable.Binding, error) {
	expected, err := expectedStoredBinding(id, integration, client, receipt)
	if err != nil {
		return portable.Binding{}, err
	}
	b, found, err := portable.ResolveCommittedBinding(ledger, expected)
	if err != nil || !found {
		return portable.Binding{}, fmt.Errorf("%w: exact historical consumer missing or ambiguous", ErrPreflight)
	}
	return b, nil
}

func expectedStoredBinding(id Identity, integration portable.Integration, client domain.ClientBinding, receipt domain.DataReceipt) (portable.Binding, error) {
	if client.ClientID != string(integration) || client.DataReceiptID == "" || receipt.DataReceiptID != client.DataReceiptID || receipt.Locator == "" || receipt.Scope != client.Scope {
		return portable.Binding{}, fmt.Errorf("%w: UAP client/data receipt mismatch", ErrPreflight)
	}
	expected, err := Complete(id, integration, client.ClientID, client.Scope, client.TargetLocator, receipt.Locator)
	if err != nil {
		return portable.Binding{}, err
	}
	if expected.BindingID != client.ClientBindingID {
		return portable.Binding{}, fmt.Errorf("%w: UAP target binding ID mismatch", ErrPreflight)
	}
	return expected, nil
}

// resolveRemovableBinding accepts a vanished consumer only when the pending
// uninstall intent froze its exact identity before the revoke. The UAP client
// and receipt must still agree; a foreign locator is never removed.
func resolveRemovableBinding(id Identity, integration portable.Integration, client domain.ClientBinding, receipt domain.DataReceipt, profile string, ledger installruntime.Ledger) (portable.Binding, error) {
	expected, err := expectedStoredBinding(id, integration, client, receipt)
	if err != nil {
		return portable.Binding{}, err
	}
	variants, err := portable.CommittedBindingVariants(ledger, expected)
	if err != nil || len(variants) > 1 {
		return portable.Binding{}, fmt.Errorf("%w: committed binding ambiguous or invalid", ErrPreflight)
	}
	if len(variants) == 1 {
		if ledger.PendingMutation != nil {
			target, err := readUninstallIntentTarget(ledger, expected, client.DataReceiptID, profile)
			if err != nil {
				return portable.Binding{}, err
			}
			if target.OldBinding != nil {
				frozen, err := frozenUninstallBinding(ledger, expected, client.DataReceiptID, profile)
				if err != nil || frozen != variants[0] {
					return portable.Binding{}, fmt.Errorf("%w: committed binding differs from confirmed uninstall", ErrPreflight)
				}
			} else if target.OldConsumerKey != "" || target.NewBinding != nil || target.NewConsumerKey != "" {
				return portable.Binding{}, fmt.Errorf("%w: incomplete confirmed uninstall binding", ErrPreflight)
			}
		}
		return variants[0], nil
	}
	old, err := frozenUninstallBinding(ledger, expected, client.DataReceiptID, profile)
	if err != nil {
		return portable.Binding{}, err
	}
	if _, err := portable.ExactLocator(old); err != nil {
		return portable.Binding{}, err
	}
	return old, nil
}

func frozenUninstallBinding(ledger installruntime.Ledger, expected portable.Binding, receiptID, profile string) (portable.Binding, error) {
	target, err := readUninstallIntentTarget(ledger, expected, receiptID, profile)
	if err != nil {
		return portable.Binding{}, err
	}
	if target.OldBinding == nil || target.NewBinding != nil || target.NewConsumerKey != "" || target.DataReceiptID != receiptID {
		return portable.Binding{}, fmt.Errorf("%w: uninstall intent target differs", ErrPreflight)
	}
	old := *target.OldBinding
	wantKey, _, _, err := old.Registration()
	if err != nil || target.OldConsumerKey != wantKey {
		return portable.Binding{}, fmt.Errorf("%w: uninstall registration differs", ErrPreflight)
	}
	expected.GlobalConfig, expected.Primary = old.GlobalConfig, old.Primary
	if expected != old {
		return portable.Binding{}, fmt.Errorf("%w: uninstall UAP identity differs", ErrPreflight)
	}
	return old, nil
}

func readUninstallIntentTarget(ledger installruntime.Ledger, expected portable.Binding, receiptID, profile string) (IntentTarget, error) {
	pending := ledger.PendingMutation
	if pending == nil || pending.Owner != expected.Owner || pending.IntentRef != IntentPath(expected.ControlRoot) {
		return IntentTarget{}, fmt.Errorf("%w: uninstall reservation missing", ErrPreflight)
	}
	path := IntentPath(expected.ControlRoot)
	wantFile, ok := ledger.Files[path]
	actualFile, err := installruntime.Fingerprint(path)
	if err != nil || !ok || actualFile != wantFile {
		return IntentTarget{}, fmt.Errorf("%w: uninstall intent file differs", ErrPreflight)
	}
	intent, err := ReadIntent(expected.ControlRoot)
	if err != nil || intent.SetupIntentID != pending.ID || intent.Action != "uninstall" {
		return IntentTarget{}, fmt.Errorf("%w: uninstall intent missing", ErrPreflight)
	}
	var target *IntentTarget
	for i := range intent.Targets {
		if intent.Targets[i].Client != string(expected.Integration) {
			continue
		}
		if target != nil {
			return IntentTarget{}, fmt.Errorf("%w: uninstall intent target ambiguous", ErrPreflight)
		}
		target = &intent.Targets[i]
	}
	if target == nil || target.Client != string(expected.Integration) || target.InstallationID != expected.InstallationID || target.BindingID != expected.BindingID || (target.DataReceiptID != "" && target.DataReceiptID != receiptID) || target.Profile != profile {
		return IntentTarget{}, fmt.Errorf("%w: uninstall intent target differs", ErrPreflight)
	}
	return *target, nil
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
	if err != nil || intent.Action != req.HandoffAction || target.InstallationID != req.Identity.InstallationID {
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
		if candidate.ClientID == string(req.Integration) {
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

func refuseConflictingLocator(b portable.Binding) error {
	name, err := b.Filename()
	if err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(b.DataRoot, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, _, raw, err := b.Registration()
	if err != nil {
		return err
	}
	if !bytes.Equal(body, raw) {
		return fmt.Errorf("%w: existing locator identity does not match committed binding", ErrPreflight)
	}
	return nil
}

func NewMaterializer(roots UAPRoots) (Materializer, error) {
	for _, p := range []string{roots.StateFile, roots.LockFile, roots.OperationsDir, roots.PluginDataBase, roots.ManagedRoot, roots.HelperExecutable} {
		if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return Materializer{}, fmt.Errorf("%w: UAP roots must be explicit absolute paths", ErrPreflight)
		}
	}
	if roots.HelperVersion == "" {
		roots.HelperVersion = "agent-notify-portable-v1"
	}
	return Materializer{Kernel: Service{}, Store: statev2.Store{Path: roots.StateFile}, Roots: roots}, nil
}

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

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}

func (m Materializer) validate(req MaterializeRequest, install bool) error {
	switch req.Integration {
	case portable.Codex, portable.Claude:
	default:
		return ErrPreflight
	}
	if !explicitAbs(req.ClientConfigRoot) {
		return fmt.Errorf("%w: client config root must be explicit", ErrPreflight)
	}
	if req.ClientExecutable == "" || !filepath.IsAbs(req.ClientExecutable) {
		return fmt.Errorf("%w: client executable must be explicit", ErrPreflight)
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

func (m Materializer) engine(req MaterializeRequest, generation *uint64, res *installruntime.PendingMutation) (*uapinstaller.Engine, error) {
	helper := m.Roots.HelperExecutable
	if req.HelperExecutable != "" {
		helper = req.HelperExecutable
	}
	runner := m.Roots.ClaudeRunner
	if req.Integration == portable.Codex {
		runner = m.Roots.CodexRunner
	}
	registry, err := NewRegistry()
	if err != nil {
		return nil, err
	}
	return uapinstaller.New(uapinstaller.Config{
		StateRoot:            filepath.Dir(m.Roots.StateFile),
		StateFile:            m.Roots.StateFile,
		LockFile:             m.Roots.LockFile,
		OperationsDir:        m.Roots.OperationsDir,
		PluginDataBase:       m.Roots.PluginDataBase,
		ManagedRoot:          m.Roots.ManagedRoot,
		TempRoot:             filepath.Join(filepath.Dir(m.Roots.StateFile), "tmp"),
		HelperExecutable:     helper,
		HelperVersion:        m.Roots.HelperVersion,
		Runner:               runner,
		Registry:             registry,
		TrustedLocalPackages: true,
		ServerName:           portableServerName,
		ProjectArgs: func(facts uapinstaller.BindingFacts) ([]string, error) {
			b, err := Complete(req.Identity, integrationOf(facts), facts.ClientID, facts.Scope, facts.TargetPath, facts.DataRoot)
			if err != nil {
				return nil, err
			}
			name, err := b.Filename()
			if err != nil {
				return nil, err
			}
			return []string{"portable-launch", "--locator", name}, nil
		},
		OnCommittedBinding: func(ctx context.Context, facts uapinstaller.BindingFacts) error {
			pb, err := Complete(req.Identity, integrationOf(facts), facts.ClientID, facts.Scope, facts.TargetPath, facts.DataRoot)
			if err != nil {
				return err
			}
			if pb.BindingID != facts.BindingID && facts.BindingID != "" {
				return fmt.Errorf("%w: binding identity does not match committed UAP client", ErrPreflight)
			}
			if err := refuseConflictingLocator(pb); err != nil {
				return err
			}
			if _, err := m.Kernel.CommitBinding(ctx, Request{Binding: pb, ExpectedGeneration: *generation, Reservation: res}); err != nil {
				return err
			}
			snap, err := installruntime.ReadInstalledSnapshot(pb.ControlRoot)
			if err != nil {
				return err
			}
			*generation = snap.Ledger.Generation
			return nil
		},
	})
}

func (m Materializer) apply(ctx context.Context, eng *uapinstaller.Engine, req uapinstaller.Request, expected MaterializeRequest) (uapinstaller.Result, error) {
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		return uapinstaller.Result{}, err
	}
	defer func() { _ = prepared.Close() }()
	if err := confirmPreparedIdentity(expected, prepared.Plan()); err != nil {
		return uapinstaller.Result{}, err
	}
	return eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
}

// prepareRemove is the §5.5.2 read-only removal preflight. It refuses a pending
// journal and verifies the managed artifact before locator revoke or Apply.
func (m Materializer) prepareRemove(ctx context.Context, eng *uapinstaller.Engine, req uapinstaller.Request, expected MaterializeRequest) (*uapinstaller.PreparedOperation, error) {
	view, err := eng.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	if view.Recovery.Required {
		reason := view.Recovery.Reason
		if reason == "" {
			reason = "pending transactions remain"
		}
		return nil, fmt.Errorf("%w: %s", uapinstaller.ErrRecoveryRequired, reason)
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := confirmPreparedIdentity(expected, prepared.Plan()); err != nil {
		_ = prepared.Close()
		return nil, err
	}
	return prepared, nil
}

func confirmPreparedIdentity(req MaterializeRequest, plan uapinstaller.Plan) error {
	if err := matchOptionalIdentity("tree digest", req.TreeDigest, plan.TreeDigest); err != nil {
		return err
	}
	if err := matchOptionalIdentity("helper digest", req.HelperDigest, plan.HelperDigest); err != nil {
		return err
	}
	return matchOptionalIdentity("helper version", req.HelperVersion, plan.HelperVersion)
}

func matchOptionalIdentity(name, expected, got string) error {
	if expected == "" || got == "" || expected == got {
		return nil
	}
	return fmt.Errorf("%w: %s %s desired %s", ErrSourceIdentityDrift, name, expected, got)
}

func (m Materializer) Install(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	if ctx == nil {
		return portable.Binding{}, ErrPreflight
	}
	if err := m.validate(req, true); err != nil {
		return portable.Binding{}, err
	}
	template, err := Complete(req.Identity, req.Integration, string(req.Integration), string(domain.ScopeUser), req.Identity.ScopeRoot, req.Identity.ControlRoot)
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
	} else if req.HandoffAction != "" {
		return portable.Binding{}, fmt.Errorf("%w: handoff action override is only for projection refresh", ErrPreflight)
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
	if req.Operation == uapinstaller.OpRefreshProjection && res == nil {
		// A migration can have no direct-MCP discovery. In that case
		// handoffForward has no work, but the UAP commit still needs the
		// previously confirmed kernel reservation.
		snap, readErr := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
		if readErr != nil || snap.Ledger.PendingMutation == nil {
			return portable.Binding{}, fmt.Errorf("%w: projection refresh reservation disappeared", ErrPreflight)
		}
		intent, _, readErr := readMigrationIntentTarget(snap.Ledger, req.Identity.ControlRoot, req.Integration)
		if readErr != nil || intent.Action != req.HandoffAction {
			return portable.Binding{}, fmt.Errorf("%w: projection refresh intent changed", ErrPreflight)
		}
		cp := *snap.Ledger.PendingMutation
		res = &cp
	}
	generation := gen
	eng, err := m.engine(req, &generation, res)
	if err != nil {
		return portable.Binding{}, err
	}
	known, err := m.knownTargets(req.Identity.InstallationID, string(req.Integration))
	if err != nil {
		return portable.Binding{}, err
	}
	result, err := m.apply(ctx, eng, uapinstaller.Request{
		Operation: packageOperation(req), PackageRoot: req.PackageRoot, ClientID: string(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID,
		RequiredComponents: []string{"mcp", "skills"},
		KnownTargets:       known,
	}, req)
	if err != nil {
		return portable.Binding{}, persistResult(result, wrapUpdateRequired(err))
	}
	pb, err := Complete(req.Identity, req.Integration, result.Binding.ClientID, result.Binding.Scope, result.Binding.TargetPath, result.Binding.DataRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	if err := recordClientFact(filepath.Dir(m.Roots.StateFile), clientFact{BindingID: pb.BindingID, ConfigRoot: req.ClientConfigRoot, Executable: req.ClientExecutable}, string(req.Integration)); err != nil {
		return portable.Binding{}, err
	}
	if !req.KeepReservation {
		if err := m.Kernel.finishHandoff(ctx, Request{Binding: pb, ExpectedGeneration: generation, Reservation: res}, res); err != nil {
			return portable.Binding{}, err
		}
	}
	return pb, nil
}

func integrationOf(facts uapinstaller.BindingFacts) portable.Integration {
	switch facts.ClientID {
	case string(portable.Claude):
		return portable.Claude
	case string(portable.Codex):
		return portable.Codex
	default:
		return portable.Integration(facts.ClientID)
	}
}

func (m Materializer) ApplyGroup(ctx context.Context, reqs []MaterializeRequest) ([]portable.Binding, error) {
	if ctx == nil || len(reqs) != 2 {
		return nil, ErrPreflight
	}
	for i := range reqs {
		if err := m.validate(reqs[i], true); err != nil {
			return nil, err
		}
		if reqs[i].PackageRoot != reqs[0].PackageRoot {
			if packageOperation(reqs[0]) != uapinstaller.OpRepair || packageOperation(reqs[i]) != uapinstaller.OpRepair {
				return nil, fmt.Errorf("%w: group requires one package root", ErrPreflight)
			}
		}
	}
	if err := m.recoverOwnedJournals(ctx, reqs[0]); err != nil {
		return nil, err
	}
	release, err := m.beginMutation(ctx, &reqs[0])
	if err != nil {
		return nil, err
	}
	defer release()
	generation := reqs[0].ExpectedGeneration
	var res *installruntime.PendingMutation
	for i := range reqs {
		template, err := Complete(reqs[i].Identity, reqs[i].Integration, string(reqs[i].Integration), string(domain.ScopeUser), reqs[i].Identity.ScopeRoot, reqs[i].Identity.ControlRoot)
		if err != nil {
			return nil, err
		}
		gen, next, err := m.Kernel.handoffForward(ctx, Request{
			Binding: template, ExpectedGeneration: generation, Discovery: reqs[i].Discovery,
			SourceRevision: reqs[i].SourceRevision, SourceDigest: reqs[i].SourceDigest,
			TreeDigest: reqs[i].TreeDigest, HelperDigest: reqs[i].HelperDigest, HelperVersion: reqs[i].HelperVersion,
			Profile: reqs[i].ClientConfigRoot,
		}, string(packageOperation(reqs[i])))
		if err != nil {
			return nil, err
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
	eng, err := m.engine(engineReq, &generation, res)
	if err != nil {
		return nil, err
	}
	var targets []uapinstaller.ClientTarget
	for _, req := range reqs {
		targets = append(targets, uapinstaller.ClientTarget{
			ClientID: string(req.Integration), ClientConfigRoot: req.ClientConfigRoot,
			ClientExecutable: req.ClientExecutable, PackageRoot: req.PackageRoot,
			ExternalUninstalled: req.ExternalUninstalled,
		})
	}
	result, err := m.apply(ctx, eng, uapinstaller.Request{
		Operation: packageOperation(reqs[0]), PackageRoot: reqs[0].PackageRoot,
		InstallationID: reqs[0].Identity.InstallationID, OperationID: reqs[0].OperationID,
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: reqs[0].ClientExecutable,
		Targets: targets,
	}, reqs[0])
	if err != nil {
		return nil, persistResult(result, wrapUpdateRequired(err))
	}
	var out []portable.Binding
	for _, req := range reqs {
		var facts uapinstaller.BindingFacts
		for _, item := range result.Targets {
			if item.ClientID == string(req.Integration) {
				facts.ClientID = item.ClientID
				facts.BindingID = item.BindingID
				break
			}
		}
		if state, loadErr := eng.Inspect(ctx); loadErr == nil {
			for _, installation := range state.Installations {
				if installation.InstallationID != result.InstallationID && reqs[0].Identity.InstallationID != installation.InstallationID {
					continue
				}
				for _, binding := range installation.Bindings {
					if binding.ClientID == string(req.Integration) {
						facts.InstallationID = installation.InstallationID
						facts.ClientID = binding.ClientID
						facts.BindingID = binding.BindingID
						facts.Scope = binding.Scope
						facts.TargetPath = binding.TargetPath
						facts.DataRoot = binding.DataRoot
					}
				}
			}
		}
		if facts.ClientID == "" {
			return nil, fmt.Errorf("%w: group result omitted %s", ErrPreflight, req.Integration)
		}
		pb, err := Complete(req.Identity, req.Integration, facts.ClientID, facts.Scope, facts.TargetPath, facts.DataRoot)
		if err != nil {
			return nil, err
		}
		if err := recordClientFact(filepath.Dir(m.Roots.StateFile), clientFact{BindingID: pb.BindingID, ConfigRoot: req.ClientConfigRoot, Executable: req.ClientExecutable}, string(req.Integration)); err != nil {
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

// RemoveGroup uninstalls both clients in one UAP RemoveGroup. It preflights
// the managed artifacts and refuses pending journals before locator revoke.
func (m Materializer) RemoveGroup(ctx context.Context, reqs []MaterializeRequest) ([]GroupRemoveResult, error) {
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
			ClientID: string(req.Integration), ClientConfigRoot: req.ClientConfigRoot,
			ClientExecutable: req.ClientExecutable, ExternalUninstalled: req.ExternalUninstalled,
		})
	}
	prepared, err := m.prepareRemove(ctx, eng, uapinstaller.Request{
		Operation: uapinstaller.OpRemove, InstallationID: reqs[0].Identity.InstallationID,
		OperationID: reqs[0].OperationID, ClientExecutable: engineReq.ClientExecutable,
		ExternalUninstalled: reqs[0].ExternalUninstalled || reqs[1].ExternalUninstalled,
		Targets:             targets,
	}, reqs[0])
	if err != nil {
		return nil, err
	}
	defer func() { _ = prepared.Close() }()
	// Resolve both before any revocation so an ambiguous second client cannot
	// leave the first client removed while its sibling remains unresolved.
	bindings := make([]portable.Binding, len(reqs))
	var intentTargets []IntentTarget
	for i, req := range reqs {
		matches := 0
		for _, client := range installation.Clients {
			if client.ClientID != string(req.Integration) {
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
			bindings[i], err = resolveRemovableBinding(req.Identity, req.Integration, client, installation.DataReceipts[client.DataReceiptID], req.ClientConfigRoot, snap.Ledger)
			if err != nil {
				return nil, err
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
				Client: client.ClientID, InstallationID: req.Identity.InstallationID,
				BindingID: client.ClientBindingID, DataReceiptID: client.DataReceiptID,
				Profile: req.ClientConfigRoot, OldConsumerKey: key, OldBinding: &b,
				Units: []string{"direct-mcp"},
			})
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
			if binding.ClientID != string(req.Integration) {
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
	byClient := map[string]uapinstaller.ClientResult{}
	for _, item := range removed.Targets {
		byClient[item.ClientID] = item
	}
	for i, req := range reqs {
		item, ok := byClient[string(req.Integration)]
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

func packageOperation(req MaterializeRequest) uapinstaller.Operation {
	if req.Operation == "" {
		return uapinstaller.OpInstall
	}
	return req.Operation
}

func (m Materializer) Update(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	req.Operation = uapinstaller.OpUpdate
	return m.Install(ctx, req)
}

func (m Materializer) Repair(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	req.Operation = uapinstaller.OpRepair
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

func (m Materializer) SwitchRetained(ctx context.Context, req MaterializeRequest) (uapinstaller.Result, error) {
	if ctx == nil {
		return uapinstaller.Result{}, ErrPreflight
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

func IsUpdateRequired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUpdateRequired) || errors.Is(err, uapinstaller.ErrUpdateRequired) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "run update separately") ||
		strings.Contains(msg, "use switch to change source") ||
		strings.Contains(msg, "is sticky to")
}

func wrapUpdateRequired(err error) error {
	if err == nil {
		return nil
	}
	if IsUpdateRequired(err) && !errors.Is(err, ErrUpdateRequired) {
		return fmt.Errorf("%w: %w", ErrUpdateRequired, err)
	}
	return err
}

// ResultError keeps the installer Result when a mutation already happened.
// Mapping that to a bool or an empty Binding would hide a managed commit.
type ResultError struct {
	Result uapinstaller.Result
	Err    error
}

func (e ResultError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Result.Reason
}

func (e ResultError) Unwrap() error { return e.Err }

func persistResult(result uapinstaller.Result, err error) error {
	if err == nil {
		return nil
	}
	if result.Outcome == "" && result.Client.Materialization == "" && result.Binding.BindingID == "" {
		return err
	}
	return ResultError{Result: result, Err: err}
}

func (m Materializer) OtherLiveClients(installationID, adding string) ([]string, error) {
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
			others = append(others, binding.ClientID)
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
	eng, err := m.engine(req, new(uint64), nil)
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	known, err := m.knownTargets(req.Identity.InstallationID, string(req.Integration))
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	if recover {
		if err := m.recoverOwnedJournals(ctx, req); err != nil {
			return uapinstaller.Plan{}, err
		}
	}
	prepared, err := eng.Prepare(ctx, uapinstaller.Request{
		Operation: packageOperation(req), PackageRoot: req.PackageRoot, ClientID: string(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID + "-preview",
		RequiredComponents: []string{"mcp", "skills"},
		KnownTargets:       known,
	})
	if err != nil {
		return uapinstaller.Plan{}, wrapUpdateRequired(err)
	}
	defer func() { _ = prepared.Close() }()
	return prepared.Plan(), nil
}

func (m Materializer) GuardSecondClient(ctx context.Context, req MaterializeRequest) error {
	others, err := m.OtherLiveClients(req.Identity.InstallationID, string(req.Integration))
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

// Remove uninstalls one live client. It preflights the managed artifact and
// refuses pending journals before locator revoke. The wizard recovers first.
func (m Materializer) Remove(ctx context.Context, req MaterializeRequest) error {
	if ctx == nil {
		return ErrPreflight
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
		if binding.ClientID != string(req.Integration) {
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
	release, err := m.beginMutation(ctx, &req)
	if err != nil {
		return err
	}
	defer release()
	snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil || snap.Recovery {
		return fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
	}
	pb, err := resolveRemovableBinding(req.Identity, req.Integration, *found, receipt, req.ClientConfigRoot, snap.Ledger)
	if err != nil {
		return err
	}
	eng, err := m.engine(req, &req.ExpectedGeneration, nil)
	if err != nil {
		return err
	}
	prepared, err := m.prepareRemove(ctx, eng, uapinstaller.Request{
		Operation: uapinstaller.OpRemove, ClientID: string(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID,
		ExternalUninstalled: req.ExternalUninstalled,
	}, req)
	if err != nil {
		return err
	}
	defer func() { _ = prepared.Close() }()
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
