package notifier

import (
	"fmt"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/logging"
)

// multiplexerHandler describes a terminal multiplexer integration.
type multiplexerHandler struct {
	name      string
	detect    func() bool
	buildArgs func(title, message, bundleID string, cfg *config.Config) ([]string, error)
}

// multiplexerHandlers is the ordered list of supported multiplexers.
// First detected wins.
var multiplexerHandlers = []multiplexerHandler{
	{"tmux", IsTmux, buildTmuxClickArgs},
	{"zellij", IsZellij, buildZellijClickArgs},
	{"wezterm", IsWezTerm, buildWezTermClickArgs},
	{"kitty", IsKitty, buildKittyClickArgs},
}

// detectMultiplexerArgs tries each registered multiplexer.
// Returns (args, name) if detected and target obtained,
// (nil, name) if detected but target failed,
// (nil, "") if no multiplexer detected.
func detectMultiplexerArgs(title, message, bundleID string, cfg *config.Config) ([]string, string) {
	for _, mux := range multiplexerHandlers {
		if !mux.detect() {
			continue
		}
		args, err := mux.buildArgs(title, message, bundleID, cfg)
		if err != nil {
			logging.Debug("%s detected but buildArgs failed: %v", mux.name, err)
			return nil, mux.name
		}
		return injectWarpFocusURL(args), mux.name
	}
	return nil, ""
}

// buildTmuxClickArgs captures tmux target and builds notifier args.
// For iTerm2 tmux -CC (control mode), uses the iTerm2 Python API helper
// instead of standard tmux select-window (which doesn't switch iTerm2 tabs).
func buildTmuxClickArgs(title, message, bundleID string, _ *config.Config) ([]string, error) {
	target, err := GetTmuxPaneTarget()
	if err != nil {
		return nil, err
	}

	if isIterm2BundleID(bundleID) {
		args, err := buildIterm2TmuxNotifierArgs(title, message, target, bundleID)
		if err == nil {
			logging.Debug("iTerm2 + tmux: using Python API helper for tab switching")
			return args, nil
		}
		if IsTmuxControlMode() {
			logging.Warn("tmux -CC mode detected but iTerm2 Python API unavailable: %v. "+
				"Tab switch may not work. Run bootstrap.sh to set up.", err)
		} else {
			return nil, fmt.Errorf("iTerm2 tmux helper unavailable in plain tmux mode: %w", err)
		}
	}

	if IsTmuxControlMode() {
		args, err := buildTmuxCCNotifierArgs(title, message, target, bundleID)
		if err != nil {
			logging.Warn("tmux -CC mode detected but iTerm2 Python API unavailable: %v. "+
				"Tab switch may not work. Run bootstrap.sh to set up.", err)
			// Fallback to standard tmux select-window
		} else {
			logging.Debug("tmux -CC mode: using iTerm2 Python API for tab switching")
			return args, nil
		}
	}

	return buildTmuxNotifierArgs(title, message, target, bundleID), nil
}

// buildZellijClickArgs captures zellij tab target and builds notifier args.
func buildZellijClickArgs(title, message, bundleID string, _ *config.Config) ([]string, error) {
	tabName, sessionName, err := GetZellijTabTarget()
	if err != nil {
		return nil, err
	}
	return buildZellijNotifierArgs(title, message, tabName, sessionName, bundleID), nil
}

// buildWezTermClickArgs captures WezTerm pane target and builds notifier args.
func buildWezTermClickArgs(title, message, bundleID string, _ *config.Config) ([]string, error) {
	paneID, socketPath, err := GetWezTermPaneTarget()
	if err != nil {
		return nil, err
	}
	return buildWezTermNotifierArgs(title, message, paneID, socketPath, bundleID), nil
}

// buildKittyClickArgs captures Kitty window target and builds notifier args.
func buildKittyClickArgs(title, message, bundleID string, _ *config.Config) ([]string, error) {
	windowID, listenOn, err := GetKittyWindowTarget()
	if err != nil {
		return nil, err
	}
	return buildKittyNotifierArgs(title, message, windowID, listenOn, bundleID), nil
}
