// ABOUTME: Zellij focus strategy names: the notifier picks one, the click carries it out.
// ABOUTME: Untagged because the notifier half is compiled on every platform.
package daemon

// How a zellij session is brought to the front. The strategy is decided in the
// hook process, which reads the config and can interrogate the local zellij, and
// travels with the notification so the click only has to carry it out.
const (
	// ZellijFocusModePane targets the exact pane via focus-pane-id (zellij 0.44.1+).
	ZellijFocusModePane = "pane"
	// ZellijFocusModeTab targets a tab by name via go-to-tab-name. Approximate:
	// names are neither unique nor immutable, and a tab holds many panes.
	ZellijFocusModeTab = "tab"
	// ZellijFocusModeOff skips the zellij step and only raises the window.
	ZellijFocusModeOff = "off"
)
