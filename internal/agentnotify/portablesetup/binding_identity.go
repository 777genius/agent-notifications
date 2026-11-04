package portablesetup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func physicalRoot(path string) string {
	got, err := installruntime.PhysicalPath(path)
	if err != nil {
		return path
	}
	return got
}

func Complete(id Identity, integration portable.Integration, clientID, scope, activePath, dataRoot string) (portable.Binding, error) {
	if clientID != ClientID(integration) {
		return portable.Binding{}, ErrPreflight
	}
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
	if client.ClientID != ClientID(integration) || client.DataReceiptID == "" || receipt.DataReceiptID != client.DataReceiptID || receipt.Locator == "" || receipt.Scope != client.Scope {
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
