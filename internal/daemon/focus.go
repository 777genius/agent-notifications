//go:build linux

// ABOUTME: Window focus methods for Linux desktop environments.
// ABOUTME: Implements a fallback chain to focus windows on GNOME, KDE, Sway, and other compositors.
package daemon

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/warpfocus"
)

// FocusMethod represents a method for focusing a window
type FocusMethod struct {
	Name string
	Fn   func(hints FocusHints) error
}

// GetFocusMethods returns the ordered list of focus methods to try
func GetFocusMethods() []FocusMethod {
	return []FocusMethod{
		{"activate-window-by-title extension", byName(TryActivateWindowByTitle)},
		{"GNOME Shell Eval (by window title)", byName(TryGnomeShellEvalByTitle)},
		{"GNOME Shell Eval (by app)", byName(TryGnomeShellEval)},
		{"GNOME Shell FocusApp", byName(TryGnomeFocusApp)},
		{"wlrctl", byName(TryWlrctl)},
		{"kdotool", tryKdotool},
		{"xdotool", tryXdotool},
	}
}

// byName adapts a focus method that only needs the terminal and folder names.
func byName(fn func(terminalName, folderName string) error) func(FocusHints) error {
	return func(hints FocusHints) error { return fn(hints.TerminalName, hints.FolderName) }
}

// FocusHints identifies the session that produced a notification: its terminal,
// project folder, window, and multiplexer pane. Every field is captured in the
// hook process, where the environment still describes that session. The daemon
// is shared by every session on the machine and outlives each of them, so it
// cannot re-derive any of this when the click eventually arrives.
type FocusHints struct {
	TerminalName  string
	FolderName    string
	ProjectPath   string // JetBrains project root; tells same-named projects apart
	IDEPID        int    // JetBrains IDE process; tells apart IDE processes with same-named projects
	WindowID      string
	WindowTitle   string
	WezTermPaneID string
	WezTermSocket string
	WarpFocusURL  string
	ZellijSession string
	ZellijPaneID  string
	ZellijTabName string
	ZellijMode    string
	Strict        *notification.TerminalTarget
	StrictSound   bool // native default cue only; ignored by legacy daemon messages
}

var openFocusURL = warpfocus.Open

// TryFocus attempts to focus a window using available tools.
// folderName is the project folder name used for title-based window search (may be empty).
// It tries each method in order until one succeeds.
func TryFocus(terminalName, folderName string) error {
	return TryFocusWithHints(FocusHints{TerminalName: terminalName, FolderName: folderName})
}

// TryFocusWithWindowID preserves the previous API for callers that only have an exact X11 window ID.
func TryFocusWithWindowID(terminalName, folderName, windowID string) error {
	return TryFocusWithHints(FocusHints{
		TerminalName: terminalName,
		FolderName:   folderName,
		WindowID:     windowID,
	})
}

// TryFocusWithHints attempts exact focus using hook-time hints first, then falls back to
// compositor-specific methods.
// wezTermPaneID and wezTermSocket enable tab-level focus for WezTerm.
// warpFocusURL is a Warp session deep link that focuses the originating window/tab/pane.
//
// For WezTerm, window-level focus runs first, then the pane switch runs after a short
// delay. This ordering matters: GNOME's XDG Activation Token is processed asynchronously
// after the window-level call and may restore the previously active tab if the pane
// switch runs first. Running the pane switch last ensures it wins.
// If all window-level methods fail but a pane ID is available, TryWezTermPane is tried
// as a last resort (activate-pane also raises the window on WezTerm).
func TryFocusWithHints(hints FocusHints) error {
	wezTermPaneID, wezTermSocket := normalizeWezTermFocusHints(hints.TerminalName, hints.WezTermPaneID, hints.WezTermSocket)
	windowFocused := false
	if url := warpfocus.Normalize(hints.WarpFocusURL); url != "" {
		windowFocused = openFocusURL(url) == nil
	}
	var exactErr, lastErr error

	if !windowFocused && strings.TrimSpace(hints.WindowID) != "" {
		if err := tryX11WindowID(hints.WindowID); err == nil {
			windowFocused = true
		} else {
			exactErr = err
		}
	}

	if !windowFocused && strings.TrimSpace(hints.WindowTitle) != "" {
		if err := tryWindowTitle(hints.WindowTitle); err == nil {
			windowFocused = true
		} else if exactErr != nil {
			exactErr = fmt.Errorf("%v; exact title focus failed: %v", exactErr, err)
		} else {
			exactErr = fmt.Errorf("exact title focus failed: %v", err)
		}
	}

	if !windowFocused {
		for _, method := range GetFocusMethods() {
			if err := method.Fn(hints); err != nil {
				lastErr = err
				continue
			}
			windowFocused = true
			break
		}
	}

	// Zellij multiplexes panes inside whichever window was just raised, so the pane
	// switch is independent of how — or whether — window-level focus succeeded, and
	// is attempted either way.
	var zellijErr error
	if hints.ZellijSession != "" {
		zellijErr = tryZellijFocus(hints)
	}

	if wezTermPaneID != "" {
		// When multiple WezTerm windows are open, activateByWmClass may have raised
		// the wrong one (both share the same WM class). Query the mux for the window
		// title of the specific WezTerm window containing our pane, then use
		// activateBySubstring to bring exactly that window to the front.
		if wt := wezTermWindowTitle(wezTermPaneID, wezTermSocket); wt != "" {
			cmd := exec.Command("busctl", "--user", "call",
				"org.gnome.Shell",
				"/de/lucaswerkmeister/ActivateWindowByTitle",
				"de.lucaswerkmeister.ActivateWindowByTitle",
				"activateBySubstring", "s", wt,
			)
			cmd.CombinedOutput() //nolint:errcheck // best-effort; non-GNOME systems will fail here
		}

		// Sleep briefly so GNOME's XDG Activation Token is processed before switching
		// tabs — otherwise the token may restore the previously active tab and undo
		// the pane switch.
		time.Sleep(150 * time.Millisecond)
		if err := TryWezTermPane(wezTermPaneID, wezTermSocket); err != nil && !windowFocused {
			// Neither window-level focus nor pane switch succeeded.
			if exactErr != nil && lastErr != nil {
				return fmt.Errorf("%v; fallback focus failed, last error: %v; wezterm pane: %v", exactErr, lastErr, err)
			}
			if exactErr != nil {
				return fmt.Errorf("%v; wezterm pane: %v", exactErr, err)
			}
			if lastErr != nil {
				return fmt.Errorf("all focus methods failed, last error: %v; wezterm pane: %v", lastErr, err)
			}
			return fmt.Errorf("wezterm pane focus failed: %v", err)
		}
		// Pane switch succeeded, or window was already raised (pane switch is best-effort).
		return zellijErr
	}

	if !windowFocused {
		// A raised window is the headline outcome: without it the pane switch is
		// invisible, so report that failure even when the pane switch worked.
		if exactErr != nil && lastErr != nil {
			return fmt.Errorf("%v; fallback focus failed, last error: %v", exactErr, lastErr)
		}
		if exactErr != nil {
			return exactErr
		}
		return fmt.Errorf("all focus methods failed, last error: %v", lastErr)
	}
	return zellijErr
}
