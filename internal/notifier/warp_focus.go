package notifier

import (
	"strings"

	"github.com/777genius/agent-notifications/internal/warpfocus"
)

// warpFocusURL returns the originating Warp pane deep link when the current
// process (or its tmux session) exported one. Empty when not running in Warp
// or when the Warp build predates WARP_FOCUS_URL.
func warpFocusURL() string {
	return warpfocus.FromEnv()
}

// buildWarpFocusScript opens Warp's session URL on click so Warp itself
// raises the originating window, tab, and pane (including Agent chat in that
// pane). Falls back to the existing AXTitle focus-window path if open fails.
func buildWarpFocusScript(focusURL, bundleID, cwd string) string {
	openCmd := "open " + shellQuote(focusURL)
	if isUsableFocusCWD(cwd) {
		if fallback := buildBinaryFocusScript(bundleID, cwd, ""); fallback != "" {
			return openCmd + " >/dev/null 2>&1 || " + fallback
		}
	}
	return openCmd
}

// injectWarpFocusURL prepends `open <WARP_FOCUS_URL>` to a multiplexer
// -execute command so Warp+tmux/zellij still land on the originating Warp
// pane, then switch the inner multiplexer target.
func injectWarpFocusURL(args []string) []string {
	focusURL := warpFocusURL()
	if focusURL == "" {
		return args
	}
	openCmd := "open " + shellQuote(focusURL) + " >/dev/null 2>&1"
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == "-execute" {
			existing := strings.TrimSpace(out[i+1])
			if existing == "" {
				out[i+1] = openCmd
			} else {
				out[i+1] = openCmd + "; " + existing
			}
			return out
		}
	}
	return append(out, "-execute", openCmd)
}
