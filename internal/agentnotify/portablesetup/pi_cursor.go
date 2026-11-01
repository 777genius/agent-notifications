package portablesetup

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// Pi's selected operation still shares data with retained Cursor bindings. The
// common engine must revalidate their original physical tokens, using the same
// qualified adapter as Cursor setup. This adds no Cursor target or activation.
func (m Materializer) piRetainedRegistry(req MaterializeRequest, registry *clients.Registry) (*clients.Registry, error) {
	if req.Integration != portable.Pi || req.Identity.InstallationID == "" {
		return registry, nil
	}
	if _, selected := registry.Lookup(domain.ClientCursor); selected {
		return registry, nil
	}
	state, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	installation, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok {
		return registry, nil
	}
	var bindingID string
	for _, c := range installation.Clients {
		if c.ClientID != string(domain.ClientCursor) || c.ProfileAuthority == nil || c.ProfileAuthority.IsZero() {
			continue
		}
		if bindingID != "" {
			return nil, fmt.Errorf("%w: ambiguous retained Cursor profile", ErrPreflight)
		}
		bindingID = c.ClientBindingID
	}
	if bindingID == "" {
		return registry, nil
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil {
		return nil, err
	}
	var retained *portable.Binding
	for _, consumer := range snapshot.Ledger.Consumers {
		var b portable.Binding
		if json.Unmarshal([]byte(consumer.Registration), &b) != nil || b.Integration != portable.Cursor || b.InstallationID != req.Identity.InstallationID || b.BindingID != bindingID {
			continue
		}
		if retained != nil || b.ControlRoot != req.Identity.ControlRoot || !portable.ExactCommittedBinding(snapshot.Ledger, b) {
			return nil, ErrPreflight
		}
		retained = &b
	}
	if retained == nil {
		return nil, fmt.Errorf("%w: retained Cursor consumer missing", ErrPreflight)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixed, err := cursorinstall.InstalledAuthority(ctx, *retained, snapshot, m.Roots.StateFile)
	if err != nil {
		return nil, err
	}
	adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, &fixed)
	if err != nil {
		return nil, err
	}
	return clients.NewRegistry(append(registry.All(), adapter)...)
}
