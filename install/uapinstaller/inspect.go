package uapinstaller

import (
	"context"
	"fmt"
)

// Inspect is read-only. It does not recover journals or invoke host seams.
func (e *Engine) Inspect(_ context.Context) (Inspection, error) {
	state, err := e.store.Load()
	if err != nil {
		return Inspection{}, err
	}
	out := Inspection{}
	for _, installation := range state.Installations {
		item := InspectedInstallation{InstallationID: installation.InstallationID}
		for _, binding := range installation.Clients {
			receipt := installation.DataReceipts[binding.DataReceiptID]
			item.Bindings = append(item.Bindings, InspectedBinding{
				ClientID: binding.ClientID, BindingID: binding.ClientBindingID, Scope: binding.Scope,
				TargetPath: binding.TargetLocator, DataRoot: receipt.Locator,
			})
		}
		out.Installations = append(out.Installations, item)
	}
	return out, nil
}

// Recover finishes already recorded UAP transactions for this state root.
// It does not install another revision or activate a client.
func (e *Engine) Recover(ctx context.Context) error {
	if err := e.ensureDirs(); err != nil {
		return err
	}
	svc := e.lifecycle(nil, BindingFacts{})
	release, err := svc.Lock.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	if err := svc.Kernel.Recover(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrRecoveryRequired, err)
	}
	return nil
}
