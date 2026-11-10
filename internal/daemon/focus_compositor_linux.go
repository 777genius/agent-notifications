//go:build linux

package daemon

import (
	"fmt"
	"os/exec"
	"strings"
)

// TryWlrctl uses wlrctl for wlroots-based compositors (Sway, etc.).
func TryWlrctl(terminalName, folderName string) error {
	if _, err := exec.LookPath("wlrctl"); err != nil {
		return fmt.Errorf("wlrctl not installed")
	}

	// Try app_id first (more reliable)
	appID := GetWlrctlAppID(terminalName)
	cmd := exec.Command("wlrctl", "toplevel", "focus", "app_id:"+appID)
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Fallback to title
	searchTerm := GetSearchTermWithFolder(terminalName, folderName)
	cmd = exec.Command("wlrctl", "toplevel", "focus", "title:"+searchTerm)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wlrctl failed: %w, output: %s", err, string(output))
	}
	return nil
}

// TryKdotool uses kdotool for KDE Plasma.
func TryKdotool(terminalName, folderName string) error {
	return tryKdotool(FocusHints{TerminalName: terminalName, FolderName: folderName})
}

func tryKdotool(hints FocusHints) error {
	if _, err := exec.LookPath("kdotool"); err != nil {
		return fmt.Errorf("kdotool not installed")
	}

	// Search by class
	className := GetKdotoolClass(hints.TerminalName)
	searchCmd := exec.Command("kdotool", "search", "--class", className)
	output, err := searchCmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	if err != nil || outputStr == "" {
		return fmt.Errorf("no windows found via kdotool")
	}

	windowIDs := splitWindowIDs(outputStr)
	if hints.FolderName != "" && len(windowIDs) > 1 {
		ours, matching, rest := rankWindows(windowIDs, hints, getKdotoolWindowName, getKdotoolWindowPID)
		windowIDs = append(append(ours, matching...), rest...)
	}

	// windowactivate exits 0 even for an unknown window, so there is no failure
	// signal to fall back on: activate the best candidate only.
	cmd := exec.Command("kdotool", "windowactivate", windowIDs[0])
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kdotool windowactivate failed: %w", err)
	}
	return nil
}

// TryXdotool uses xdotool for X11-based desktop environments
// (XFCE, MATE, Cinnamon, i3, bspwm, and X11 sessions of GNOME/KDE).
func TryXdotool(terminalName, folderName string) error {
	return tryXdotool(FocusHints{TerminalName: terminalName, FolderName: folderName})
}

func tryXdotool(hints FocusHints) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	searches := buildXdotoolSearches(hints.TerminalName, hints.FolderName)
	seenIDs := make(map[string]struct{})
	foundMatch := false
	var errs []string

	for _, search := range searches {
		windowIDs, err := runXdotoolSearch(search.args...)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", search.label, err))
			continue
		}
		if len(windowIDs) == 0 {
			continue
		}

		foundMatch = true
		windowIDs = prioritizeXdotoolCandidates(windowIDs, search.label, hints)

		// xdotool returns bottom-most windows first; prefer the top-most candidate.
		for i := len(windowIDs) - 1; i >= 0; i-- {
			windowID := windowIDs[i]
			if _, exists := seenIDs[windowID]; exists {
				continue
			}
			seenIDs[windowID] = struct{}{}

			if err := activateWindowIDWithXdotool(windowID); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("%s (%s): %v", search.label, windowID, err))
			}
		}
	}

	if !foundMatch {
		return fmt.Errorf("no windows found via xdotool")
	}

	return fmt.Errorf("xdotool could not activate any matching window: %s", strings.Join(errs, "; "))
}
