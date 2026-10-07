package setupwizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// MigrationBinding is the exact, versioned intent payload for replacing one
// historical portable consumer. A retry uses these bindings even if the UAP
// target or the process environment changed after confirmation.
type MigrationBinding struct {
	Old, New                       portable.Binding
	OldConsumerKey, NewConsumerKey string
}

type RemovalBinding struct {
	Old            portable.Binding
	OldConsumerKey string
}

func (r RemovalBinding) validate(client, installationID, bindingID string) error {
	key, _, _, err := r.Old.Registration()
	if err != nil || key != r.OldConsumerKey || string(r.Old.Integration) != client ||
		r.Old.InstallationID != installationID || r.Old.BindingID != bindingID {
		return fmt.Errorf("%w: removal_identity_drift", ErrRefused)
	}
	return nil
}

func prepareUninstallBindings(req *Request, snap installruntime.InstalledSnapshot, mat portablesetup.Materializer, id portablesetup.Identity, agents []portable.Integration) error {
	if req.Action != ActionUninstall || id.InstallationID == "" {
		return nil
	}
	for _, agent := range agents {
		client := string(agent)
		if removal, ok := req.RemovalBindings[client]; ok {
			if err := removal.validate(client, id.InstallationID, req.BindingIDs[client]); err != nil {
				return err
			}
			continue
		}
		bindings, err := liveClientBindings(mat, id.InstallationID, client)
		if err != nil {
			return err
		}
		if len(bindings) == 0 {
			continue
		}
		if len(bindings) != 1 {
			return fmt.Errorf("%w: client %s has %d bindings", ErrAmbiguousBinding, client, len(bindings))
		}
		live := bindings[0]
		if live.DataReceiptID == "" {
			return fmt.Errorf("%w: removal receipt missing for %s", ErrRefused, client)
		}
		dataRoot, err := bindingDataRoot(mat, id.InstallationID, live)
		if err != nil || dataRoot == "" {
			return fmt.Errorf("%w: removal data root missing for %s: %v", ErrRefused, client, err)
		}
		expected, err := committedTemplate(snap.Ledger, id, agent, live, dataRoot)
		if err != nil {
			// Intents published by older versions did not freeze the consumer.
			// Their existing retry path remains available after kernel revoke.
			if snap.Ledger.PendingMutation != nil && errors.Is(err, portablesetup.ErrAlreadyAbsent) {
				continue
			}
			return err
		}
		old, found, err := portable.ResolveCommittedBinding(snap.Ledger, expected)
		if err != nil || !found {
			return fmt.Errorf("%w: removal consumer unavailable for %s: %v", ErrRefused, client, err)
		}
		key, _, _, err := old.Registration()
		if err != nil {
			return err
		}
		removal := RemovalBinding{Old: old, OldConsumerKey: key}
		if err := removal.validate(client, id.InstallationID, live.ClientBindingID); err != nil {
			return err
		}
		if req.RemovalBindings == nil {
			req.RemovalBindings = map[string]RemovalBinding{}
		}
		req.RemovalBindings[client] = removal
	}
	return nil
}

func (m MigrationBinding) validate() error {
	oldKey, _, _, oldErr := m.Old.Registration()
	newKey, _, _, newErr := m.New.Registration()
	expectedNew := m.Old
	expectedNew.GlobalConfig = m.New.GlobalConfig
	expectedNew.Primary = m.New.Primary
	if oldErr != nil || newErr != nil || oldKey != m.OldConsumerKey || newKey != m.NewConsumerKey || oldKey == newKey ||
		expectedNew != m.New {
		return fmt.Errorf("%w: migration_identity_drift", ErrRefused)
	}
	return nil
}

func legacyGeneratedConfig(b portable.Binding) bool {
	return b.GlobalConfig == filepath.Join(b.RuntimeRoot, "global", "config.json")
}

func prepareLegacyMigrations(req *Request, snap installruntime.InstalledSnapshot, mat portablesetup.Materializer, id portablesetup.Identity, agents []portable.Integration, explicitGlobal, explicitPrimary string) error {
	if req.Action != ActionUpdate && req.Action != ActionRepair {
		return nil
	}
	if len(req.MigrationBindings) != 0 {
		for _, agent := range agents {
			migration, ok := req.MigrationBindings[string(agent)]
			if ok {
				if err := migration.validate(); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, agent := range agents {
		bindings, err := liveClientBindings(mat, id.InstallationID, string(agent))
		if err != nil {
			return err
		}
		if len(bindings) != 1 {
			return fmt.Errorf("%w: client %s has %d bindings", ErrAmbiguousBinding, agent, len(bindings))
		}
		live := bindings[0]
		dataRoot, err := bindingDataRoot(mat, id.InstallationID, live)
		if err != nil || dataRoot == "" {
			return fmt.Errorf("%w: data root unavailable for %s: %v", ErrRefused, agent, err)
		}
		expected, err := committedTemplate(snap.Ledger, id, agent, live, dataRoot)
		if err != nil {
			return err
		}
		old, found, err := portable.ResolveCommittedBinding(snap.Ledger, expected)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: committed binding missing for %s", ErrRefused, agent)
		}
		newBinding := old
		if legacyGeneratedConfig(old) && explicitGlobal == "" {
			selected, err := config.Resolve(config.SnapshotEnv())
			if err != nil {
				return err
			}
			newBinding.GlobalConfig = selected.Path
		}
		if old.Primary == "primary" && explicitPrimary == "" {
			newBinding.Primary = portable.PlatformPrimary()
		}
		if newBinding == old {
			continue
		}
		oldKey, _, _, err := old.Registration()
		if err != nil {
			return err
		}
		newKey, _, _, err := newBinding.Registration()
		if err != nil {
			return err
		}
		migration := MigrationBinding{Old: old, New: newBinding, OldConsumerKey: oldKey, NewConsumerKey: newKey}
		if err := migration.validate(); err != nil {
			return err
		}
		if req.MigrationBindings == nil {
			req.MigrationBindings = map[string]MigrationBinding{}
		}
		req.MigrationBindings[string(agent)] = migration
	}
	return nil
}

func targetIdentity(id portablesetup.Identity, req Request, snap installruntime.InstalledSnapshot, mat portablesetup.Materializer, agent portable.Integration) (portablesetup.Identity, error) {
	if migration, ok := req.MigrationBindings[string(agent)]; ok {
		return identityFromBinding(id, migration.New), nil
	}
	if removal, ok := req.RemovalBindings[string(agent)]; ok {
		return identityFromBinding(id, removal.Old), nil
	}
	bindings, err := liveClientBindings(mat, id.InstallationID, string(agent))
	if err != nil {
		return id, err
	}
	if len(bindings) == 0 {
		return id, nil
	}
	if len(bindings) != 1 {
		return id, fmt.Errorf("%w: client %s has %d bindings", ErrAmbiguousBinding, agent, len(bindings))
	}
	dataRoot, err := bindingDataRoot(mat, id.InstallationID, bindings[0])
	if err != nil || dataRoot == "" {
		return id, fmt.Errorf("%w: data root unavailable for %s: %v", ErrRefused, agent, err)
	}
	expected, err := committedTemplate(snap.Ledger, id, agent, bindings[0], dataRoot)
	if err != nil {
		if errors.Is(err, portablesetup.ErrAlreadyAbsent) {
			return id, nil
		}
		return id, err
	}
	committed, found, err := portable.ResolveCommittedBinding(snap.Ledger, expected)
	if err != nil || !found {
		return id, fmt.Errorf("%w: binding unavailable for %s: %v", ErrRefused, agent, err)
	}
	return identityFromBinding(id, committed), nil
}

func identityFromBinding(id portablesetup.Identity, binding portable.Binding) portablesetup.Identity {
	id.InstallationID = binding.InstallationID
	id.ComponentID = binding.ComponentID
	id.Owner = binding.Owner
	id.ScopeRoot = binding.ScopeRoot
	id.ControlRoot = binding.ControlRoot
	id.GlobalConfig = binding.GlobalConfig
	id.RuntimeRoot = binding.RuntimeRoot
	id.Primary = binding.Primary
	return id
}

func committedTemplate(ledger installruntime.Ledger, id portablesetup.Identity, agent portable.Integration, live domain.ClientBinding, dataRoot string) (portable.Binding, error) {
	var expected portable.Binding
	for key, consumer := range ledger.Consumers {
		if !strings.HasPrefix(key, "portable:") {
			continue
		}
		var candidate portable.Binding
		if json.Unmarshal([]byte(consumer.Registration), &candidate) != nil ||
			candidate.Integration != agent || candidate.InstallationID != id.InstallationID ||
			candidate.BindingID != live.ClientBindingID || candidate.ScopeID != live.Scope ||
			!samePortableRoot(candidate.DataRoot, dataRoot) || !samePortableRoot(candidate.ControlRoot, id.ControlRoot) ||
			candidate.ComponentID != id.ComponentID || candidate.Owner != id.Owner || !samePortableRoot(candidate.RuntimeRoot, id.RuntimeRoot) {
			continue
		}
		actualKey, actualConsumer, _, err := candidate.Registration()
		if err != nil || actualKey != key || actualConsumer.Registration != consumer.Registration {
			return portable.Binding{}, fmt.Errorf("%w: invalid historical consumer for %s", ErrRefused, agent)
		}
		if expected.Version != 0 {
			return portable.Binding{}, fmt.Errorf("%w: multiple historical consumers for %s", ErrAmbiguousBinding, agent)
		}
		expected = candidate
	}
	if expected.Version == 0 {
		return portable.Binding{}, fmt.Errorf("%w: committed binding missing for %s", portablesetup.ErrAlreadyAbsent, agent)
	}
	return expected, nil
}

// Complete only canonicalizes OS-owned path aliases, not arbitrary user symlinks.
func samePortableRoot(a, b string) bool {
	if a == b {
		return true
	}
	left, leftErr := installruntime.PhysicalPath(a)
	right, rightErr := installruntime.PhysicalPath(b)
	return leftErr == nil && rightErr == nil && left == right
}

func migrationAlreadyProjected(ledger installruntime.Ledger, migration MigrationBinding) (bool, error) {
	if !portable.ExactCommittedBinding(ledger, migration.New) {
		return false, nil
	}
	return portable.ExactLocator(migration.New)
}

func replaceMigratedBinding(ctx context.Context, req Request, mat portablesetup.Materializer, migration MigrationBinding, projected portable.Binding) error {
	if err := migration.validate(); err != nil {
		return err
	}
	if projected != migration.New {
		return fmt.Errorf("%w: projected binding differs from confirmed intent", ErrRefused)
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return err
	}
	if snap.Ledger.PendingMutation == nil {
		return fmt.Errorf("%w: migration reservation missing", ErrRefused)
	}
	ready, err := migrationAlreadyProjected(snap.Ledger, migration)
	if err != nil || !ready {
		return fmt.Errorf("%w: replacement locator not committed: %v", ErrRefused, err)
	}
	reservation := *snap.Ledger.PendingMutation
	return mat.Kernel.ReplaceCommittedBinding(ctx, migration.Old, portablesetup.Request{
		Binding: migration.New, ExpectedGeneration: snap.Ledger.Generation, Reservation: &reservation,
	})
}
