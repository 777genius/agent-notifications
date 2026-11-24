//go:build linux

package daemon

import (
	"fmt"
	"os/exec"
	"strings"
)

// TryActivateWindowByTitle uses the activate-window-by-title GNOME extension.
// https://extensions.gnome.org/extension/5021/activate-window-by-title/
// This method does NOT require unsafe_mode and works on GNOME 42+.
//
// Search order:
//  1. activateBySubstring with the folder-specific term, when available — ensures
//     the correct project window is focused when multiple windows of the same app
//     are open (e.g. two VS Code windows for different projects).
//  2. activateByWmClass — reliable for Wayland-native terminals whose WM class is
//     a reverse-domain app ID (e.g. com.mitchellh.ghostty, org.wezfurlong.wezterm)
//     and whose window title does not contain the app name.
//  3. activateBySubstring with the generic terminal name as a final fallback.
func TryActivateWindowByTitle(terminalName, folderName string) error {
	gnomeActivate := func(method, arg string) bool {
		cmd := exec.Command("busctl", "--user", "call",
			"org.gnome.Shell",
			"/de/lucaswerkmeister/ActivateWindowByTitle",
			"de.lucaswerkmeister.ActivateWindowByTitle",
			method, "s", arg,
		)
		output, err := cmd.CombinedOutput()
		return err == nil && strings.Contains(strings.TrimSpace(string(output)), "true")
	}

	// Step 1: folder-specific substring search (e.g. VS Code with a project folder).
	// Only attempted when GetSearchTermWithFolder produces a different term than the
	// plain terminal name — i.e. when the folder name is actually being used.
	folderTerm := GetSearchTermWithFolder(terminalName, folderName)
	if folderTerm != GetSearchTerm(terminalName) {
		if gnomeActivate("activateBySubstring", folderTerm) {
			return nil
		}
	}

	// Step 2: WM class match — most reliable for Wayland-native terminals.
	if wmClass := GetGnomeWmClass(terminalName); wmClass != "" {
		if gnomeActivate("activateByWmClass", wmClass) {
			return nil
		}
	}

	// Step 3: generic substring fallback.
	genericTerm := GetSearchTerm(terminalName)
	cmd := exec.Command("busctl", "--user", "call",
		"org.gnome.Shell",
		"/de/lucaswerkmeister/ActivateWindowByTitle",
		"de.lucaswerkmeister.ActivateWindowByTitle",
		"activateBySubstring", "s", genericTerm,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("activate-window-by-title extension not available: %w, output: %s", err, string(output))
	}
	// busctl can succeed (exit 0) even when no window was activated; check the boolean.
	outputStr := strings.TrimSpace(string(output))
	if strings.Contains(outputStr, "false") || outputStr == "" {
		return fmt.Errorf("activate-window-by-title: no window activated for %q (output: %s)", genericTerm, outputStr)
	}
	return nil
}

// TryGnomeShellEvalByTitle uses GNOME Shell's Eval to find and focus window by title.
// Requires unsafe_mode or development-tools enabled.
func TryGnomeShellEvalByTitle(terminalName, folderName string) error {
	searchTerm := escapeJS(GetSearchTermWithFolder(terminalName, folderName))

	// JavaScript to find window by title and activate it
	js := fmt.Sprintf(`
		(function() {
			let start = Date.now();
			let found = false;
			global.get_window_actors().forEach(function(actor) {
				let win = actor.get_meta_window();
				let title = win.get_title() || '';
				if (title.indexOf('%s') !== -1) {
					win.activate(start);
					found = true;
				}
			});
			return found ? 'activated' : 'no matching window';
		})()
	`, searchTerm)

	cmd := exec.Command("gdbus", "call",
		"--session",
		"--dest", "org.gnome.Shell",
		"--object-path", "/org/gnome/Shell",
		"--method", "org.gnome.Shell.Eval",
		js,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gdbus Eval failed: %w, output: %s", err, string(output))
	}

	outputStr := string(output)
	if strings.Contains(outputStr, "no matching window") {
		return fmt.Errorf("no window with title containing %q", searchTerm)
	}
	if strings.Contains(outputStr, "false") && !strings.Contains(outputStr, "activated") {
		return fmt.Errorf("Shell.Eval blocked (GNOME 41+ security) - install unsafe-mode-menu extension or activate-window-by-title extension")
	}

	return nil
}

// TryGnomeShellEval uses GNOME Shell's Eval method to activate an app.
// Requires unsafe_mode or development-tools enabled.
func TryGnomeShellEval(terminalName, folderName string) error {
	appID := escapeJS(GetAppID(terminalName))

	// JavaScript to find and activate the app's windows
	js := fmt.Sprintf(`
		(function() {
			let app = Shell.AppSystem.get_default().lookup_app('%s');
			if (app) {
				app.activate();
				return 'activated';
			}
			return 'app not found';
		})()
	`, appID)

	cmd := exec.Command("gdbus", "call",
		"--session",
		"--dest", "org.gnome.Shell",
		"--object-path", "/org/gnome/Shell",
		"--method", "org.gnome.Shell.Eval",
		js,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gdbus Eval failed: %w, output: %s", err, string(output))
	}

	outputStr := string(output)
	if strings.Contains(outputStr, "app not found") {
		return fmt.Errorf("app not found via Shell.Eval")
	}
	if strings.Contains(outputStr, "false") && !strings.Contains(outputStr, "activated") {
		return fmt.Errorf("Shell.Eval blocked (GNOME 41+ security) - install unsafe-mode-menu extension or activate-window-by-title extension")
	}

	return nil
}

// TryGnomeFocusApp uses GNOME Shell's FocusApp method (available since GNOME 45).
func TryGnomeFocusApp(terminalName, folderName string) error {
	appID := GetAppID(terminalName)

	cmd := exec.Command("gdbus", "call",
		"--session",
		"--dest", "org.gnome.Shell",
		"--object-path", "/org/gnome/Shell",
		"--method", "org.gnome.Shell.FocusApp",
		appID,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gdbus FocusApp failed: %w, output: %s", err, string(output))
	}
	outputStr := strings.TrimSpace(string(output))
	if strings.Contains(outputStr, "false") || outputStr == "" {
		return fmt.Errorf("gdbus FocusApp reported no activation for %q (output: %s)", appID, outputStr)
	}
	return nil
}
