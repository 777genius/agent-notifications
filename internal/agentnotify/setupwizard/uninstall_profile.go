package setupwizard

import (
	"fmt"

	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
)

// Uninstall must identify the profile of an unanchored managed Codex binding
// before an empty native listing can attest absence. Install and repair retain
// their legacy admission.
func guardUninstallLiveProfile(mat portablesetup.Materializer, installationID, clientID, profile string) error {
	if err := guardLiveProfile(mat, installationID, clientID, profile); err != nil {
		return err
	}
	if clientID != "codex" {
		return nil
	}
	bindings, err := liveClientBindings(mat, installationID, clientID)
	if err != nil {
		return err
	}
	if len(bindings) != 1 || profile == "" {
		return nil
	}
	live := bindings[0]
	if clientConfigAnchored(live) {
		return nil
	}
	dataRoot, err := bindingDataRoot(mat, installationID, live)
	if err != nil {
		return err
	}
	recorded, err := recordedLiveProfile(dataRoot, clientID)
	if err != nil {
		return err
	}
	root, err := codex.New().ResolveProfileRoot(profile)
	if err == nil {
		if recorded != "" {
			// Generic live-profile containment is not sufficient for Codex:
			// an ancestor can contain the managed artifact and another home.
			var known string
			known, err = codex.New().ResolveProfileRoot(recorded)
			if err == nil && root != known {
				err = fmt.Errorf("recorded Codex profile %s is not %s", known, root)
			}
		} else {
			err = codex.ValidateBindingProfile(root, live)
		}
	}
	if err != nil {
		return fmt.Errorf("%w: cannot prove selected profile for Codex binding %s; repair its profile association before uninstall: %v", ErrLiveProfileConflict, live.ClientBindingID, err)
	}
	return nil
}
