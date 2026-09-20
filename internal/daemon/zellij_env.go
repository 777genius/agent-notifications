// ABOUTME: What the environment says about the surrounding zellij pane.
// ABOUTME: Untagged so the notifier and the daemon read it the same way on every platform.
package daemon

import (
	"os"
	"strings"
)

// InZellij reports whether this process is running inside a zellij pane.
//
// $ZELLIJ is zellij's own marker, but it does not always survive: a process
// started outside the pane's shell — a background job, a supervisor, anything
// that rebuilds the environment — can inherit ZELLIJ_SESSION_NAME and
// ZELLIJ_PANE_ID without it. Those two name the pane on their own, which is
// everything a focus request needs, so accept them as sufficient rather than
// discarding a usable target for want of a marker that carries no extra
// information.
func InZellij() bool {
	if os.Getenv("ZELLIJ") != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("ZELLIJ_SESSION_NAME")) != "" &&
		strings.TrimSpace(os.Getenv("ZELLIJ_PANE_ID")) != ""
}

// GetZellijFocusHints returns the zellij session and pane that produced the
// notification, or empty strings when not running under zellij.
//
// The pane is read from the environment rather than asked of zellij when the
// click arrives, because by then "the focused pane" is whatever the user is
// looking at — precisely the pane they are not trying to get back to.
func GetZellijFocusHints() (sessionName, paneID string) {
	if !InZellij() {
		return "", ""
	}
	return strings.TrimSpace(os.Getenv("ZELLIJ_SESSION_NAME")),
		strings.TrimSpace(os.Getenv("ZELLIJ_PANE_ID"))
}
