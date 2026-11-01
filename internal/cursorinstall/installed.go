package cursorinstall

import (
	"context"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// InstalledAuthority reconstructs the existing fixed Cursor composition from
// its committed consumer and acknowledged UAP receipts. Physical tokens remain
// recorded authority: the adapter revalidates them and never recaptures them.
func InstalledAuthority(ctx context.Context, b portable.Binding, snapshot installruntime.InstalledSnapshot, statePath string) (Authority, error) {
	deny := func() (Authority, error) {
		return Authority{}, portable.ErrInvalid
	}
	expectedState := filepath.Join(filepath.Dir(b.ControlRoot), "uap", "state", "state-v2.json")
	if ctx == nil || ctx.Err() != nil || statePath != expectedState || b.Integration != portable.Cursor || b.CheckSnapshot(snapshot) != nil {
		return deny()
	}
	state, err := (statev2.Store{Path: statePath}).Load()
	if err != nil {
		return Authority{}, err
	}
	var selected *domain.ClientBinding
	for _, installation := range state.Installations {
		if installation.InstallationID != b.InstallationID {
			continue
		}
		c, ok := installation.Clients[b.BindingID]
		if selected != nil || !ok || installation.NeedsRebind || c.ClientBindingID != b.BindingID ||
			c.ClientID != string(domain.ClientCursor) || c.Scope != b.ScopeID || c.Scope != string(domain.ScopeUser) ||
			c.ClientBindingID != domain.ComputeClientBindingID(b.InstallationID, c.ClientID, c.Scope, c.TargetLocator) ||
			c.NativeProfileRoot != b.ScopeRoot || c.ProfileNamespace != filepath.Dir(statePath) || c.ProfileAuthority == nil ||
			c.ProfileAuthority.IsZero() || c.ProfileAuthority.Facts().CanonicalRoot != b.ScopeRoot ||
			c.SelectedDelivery.Validate() != nil || c.SelectedDelivery.ValidateClient(domain.ClientCursor) != nil ||
			c.SelectedDelivery.ValidateCursorObjects(c.NativeObjects) != nil {
			return deny()
		}
		data, ok := installation.DataReceipts[c.DataReceiptID]
		if !ok || data.Locator != b.DataRoot {
			return deny()
		}
		selected = &c
	}
	if selected == nil {
		return deny()
	}
	facts, ok := selected.SelectedDelivery.CursorFacts()
	executable, err := portable.ResolvePrimaryExecutable(snapshot.Ledger, b.Primary)
	name, nameErr := b.Filename()
	if !ok || err != nil || nameErr != nil || facts.ProfileRoot != b.ScopeRoot ||
		facts.Selector != filepath.Join(b.DataRoot, name) || facts.Executable != executable || ctx.Err() != nil {
		return deny()
	}
	return Authority{ProfileRoot: facts.ProfileRoot, CursorVersion: facts.CursorVersion,
		QualificationID: facts.QualificationID, Executable: executable,
		ExecutableDigest: "sha256:" + snapshot.Ledger.Files[executable].SHA256, Selector: facts.Selector, ObjectID: facts.ObjectID}, nil
}
