//go:build linux

// ABOUTME: Window focus methods for Linux desktop environments.
// ABOUTME: Implements a fallback chain to focus windows on GNOME, KDE, Sway, and other compositors.
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/warpfocus"
)

// FocusMethod represents a method for focusing a window
type FocusMethod struct {
	Name string
	Fn   func(terminalName, folderName string) error
}

// GetFocusMethods returns the ordered list of focus methods to try
func GetFocusMethods() []FocusMethod {
	return []FocusMethod{
		{"activate-window-by-title extension", TryActivateWindowByTitle},
		{"GNOME Shell Eval (by window title)", TryGnomeShellEvalByTitle},
		{"GNOME Shell Eval (by app)", TryGnomeShellEval},
		{"GNOME Shell FocusApp", TryGnomeFocusApp},
		{"wlrctl", TryWlrctl},
		{"kdotool", TryKdotool},
		{"xdotool", TryXdotool},
	}
}

// FocusHints identifies the session that produced a notification: its terminal,
// project folder, window, and multiplexer pane. Every field is captured in the
// hook process, where the environment still describes that session. The daemon
// is shared by every session on the machine and outlives each of them, so it
// cannot re-derive any of this when the click eventually arrives.
type FocusHints struct {
	TerminalName  string
	FolderName    string
	WindowID      string
	WindowTitle   string
	WezTermPaneID string
	WezTermSocket string
	WarpFocusURL  string
	ZellijSession string
	ZellijPaneID  string
	ZellijTabName string
	ZellijMode    string
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
			if err := method.Fn(hints.TerminalName, hints.FolderName); err != nil {
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

// wezTermWindowTitle queries the WezTerm mux for the window title of the window
// containing paneID. Used to raise the exact WezTerm window when multiple instances
// are open (they share the same WM class and can't be distinguished via activateByWmClass).
// Returns empty string on any failure.
func wezTermWindowTitle(paneID, socketPath string) string {
	paneIDInt, err := strconv.Atoi(paneID)
	if err != nil {
		return ""
	}
	cmd := exec.Command("wezterm", "cli", "--no-auto-start", "list", "--format", "json")
	if strings.TrimSpace(socketPath) != "" {
		cmd.Env = append(os.Environ(), "WEZTERM_UNIX_SOCKET="+socketPath)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	var panes []struct {
		PaneID      int    `json:"pane_id"`
		WindowTitle string `json:"window_title"`
	}
	if err := json.Unmarshal(output, &panes); err != nil {
		return ""
	}
	for _, p := range panes {
		if p.PaneID == paneIDInt {
			return p.WindowTitle
		}
	}
	return ""
}

// TryWezTermPane activates a specific WezTerm pane by ID using the WezTerm CLI.
// This switches to the exact tab/pane where Claude is running.
// socketPath is passed via WEZTERM_UNIX_SOCKET env var (the CLI has no --unix-socket flag).
func TryWezTermPane(paneID, socketPath string) error {
	if _, err := exec.LookPath("wezterm"); err != nil {
		return fmt.Errorf("wezterm not installed")
	}

	// --no-auto-start: fail instead of spawning a new mux server when socket is wrong.
	cmd := exec.Command("wezterm", "cli", "--no-auto-start", "activate-pane", "--pane-id", paneID)
	if strings.TrimSpace(socketPath) != "" {
		cmd.Env = append(os.Environ(), "WEZTERM_UNIX_SOCKET="+socketPath)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wezterm cli activate-pane failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func tryX11WindowID(windowID string) error {
	normalizedID, err := normalizeX11WindowID(windowID)
	if err != nil {
		return fmt.Errorf("invalid X11 window id %q: %w", windowID, err)
	}

	var errs []string

	if err := activateWindowIDWithXdotool(normalizedID); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	if err := activateWindowIDWithWmctrl(normalizedID); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	return fmt.Errorf("exact X11 focus failed: %s", strings.Join(errs, "; "))
}

func activateWindowIDWithXdotool(windowID string) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	cmd := exec.Command("xdotool", "windowactivate", "--sync", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xdotool windowactivate failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func activateWindowIDWithWmctrl(windowID string) error {
	if _, err := exec.LookPath("wmctrl"); err != nil {
		return fmt.Errorf("wmctrl not installed")
	}

	cmd := exec.Command("wmctrl", "-i", "-a", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wmctrl -i -a failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func normalizeX11WindowID(windowID string) (string, error) {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return "", fmt.Errorf("empty window id")
	}

	id, err := strconv.ParseUint(windowID, 0, 64)
	if err != nil {
		return "", err
	}

	return strconv.FormatUint(id, 10), nil
}

func tryWindowTitle(windowTitle string) error {
	windowTitle = strings.TrimSpace(windowTitle)
	if windowTitle == "" {
		return fmt.Errorf("empty window title")
	}

	var errs []string

	if err := activateWindowTitleWithWmctrl(windowTitle); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	if err := activateWindowTitleWithXdotool(windowTitle); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	return fmt.Errorf("exact title focus failed: %s", strings.Join(errs, "; "))
}

func activateWindowTitleWithWmctrl(windowTitle string) error {
	if _, err := exec.LookPath("wmctrl"); err != nil {
		return fmt.Errorf("wmctrl not installed")
	}

	cmd := exec.Command("wmctrl", "-F", "-a", windowTitle)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wmctrl -F -a failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

func activateWindowTitleWithXdotool(windowTitle string) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	exactRegex := "^" + regexp.QuoteMeta(windowTitle) + "$"
	windowIDs, err := runXdotoolSearch("search", "--name", exactRegex)
	if err != nil {
		return err
	}
	if len(windowIDs) == 0 {
		return fmt.Errorf("no windows found for exact title")
	}

	for i := len(windowIDs) - 1; i >= 0; i-- {
		windowID := windowIDs[i]
		if err := activateWindowIDWithXdotool(windowID); err == nil {
			return nil
		}
	}

	return fmt.Errorf("xdotool could not activate any exact-title match")
}

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
	if _, err := exec.LookPath("kdotool"); err != nil {
		return fmt.Errorf("kdotool not installed")
	}

	// Search by class
	className := GetKdotoolClass(terminalName)
	searchCmd := exec.Command("kdotool", "search", "--class", className)
	output, err := searchCmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	if err != nil || outputStr == "" {
		return fmt.Errorf("no windows found via kdotool")
	}

	windowIDs := splitWindowIDs(outputStr)
	if folderName != "" && len(windowIDs) > 1 {
		matching, nonMatching := partitionWindowsByTitle(windowIDs, getKdotoolWindowName, windowTitleMatcher(terminalName, folderName))
		windowIDs = append(matching, nonMatching...)
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
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	searches := buildXdotoolSearches(terminalName, folderName)
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
		windowIDs = prioritizeXdotoolCandidates(windowIDs, search.label, terminalName, folderName)

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

type xdotoolSearch struct {
	label string
	args  []string
}

func buildXdotoolSearches(terminalName, folderName string) []xdotoolSearch {
	className := GetXdotoolClass(terminalName)
	searchTerm := GetSearchTermWithFolder(terminalName, folderName)

	searches := []xdotoolSearch{
		{
			label: "visible class search",
			args:  []string{"search", "--onlyvisible", "--class", className},
		},
		{
			label: "class search",
			args:  []string{"search", "--class", className},
		},
	}

	if searchTerm != "" {
		searches = append(searches,
			xdotoolSearch{
				label: "visible name search",
				args:  []string{"search", "--onlyvisible", "--name", searchTerm},
			},
			xdotoolSearch{
				label: "name search",
				args:  []string{"search", "--name", searchTerm},
			},
		)
	}

	return searches
}

func runXdotoolSearch(args ...string) ([]string, error) {
	cmd := exec.Command("xdotool", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w, output: %s", err, strings.TrimSpace(string(output)))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return nil, nil
	}

	return splitWindowIDs(outputStr), nil
}

func splitWindowIDs(output string) []string {
	lines := strings.Split(output, "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ids = append(ids, line)
	}
	return ids
}

func prioritizeXdotoolCandidates(windowIDs []string, searchLabel, terminalName, folderName string) []string {
	if folderName == "" {
		return windowIDs
	}
	if !strings.Contains(searchLabel, "class search") {
		return windowIDs
	}

	matching, nonMatching := partitionWindowsByTitle(windowIDs, getXdotoolWindowName, windowTitleMatcher(terminalName, folderName))
	if len(matching) == 0 {
		return windowIDs
	}

	// TryXdotool tries candidates from the end (xdotool lists bottom-most
	// windows first), so matches go last to be tried first, top-most first.
	return append(nonMatching, matching...)
}

// windowTitleMatcher returns the check for whether a window title belongs to
// folderName: strict for JetBrains, whose titles start with the project name,
// and a substring match for everything else.
func windowTitleMatcher(terminalName, folderName string) func(title string) bool {
	if isJetBrainsTerminalName(terminalName) {
		return func(title string) bool { return jetBrainsTitleMatches(title, folderName) }
	}
	return func(title string) bool { return title != "" && strings.Contains(title, folderName) }
}

// jetBrainsTitleMatches reports whether a JetBrains window title belongs to
// project. Titles are "<project>", "<project> – <file>" or
// "<project> [<path>] – <file>" (en dash), so a plain substring check would let
// "agent" match "agent-notifications".
func jetBrainsTitleMatches(title, project string) bool {
	if project == "" {
		return false
	}
	return title == project ||
		strings.HasPrefix(title, project+" – ") ||
		strings.HasPrefix(title, project+" [")
}

// partitionWindowsByTitle splits windowIDs by whether their title matches,
// keeping the original order within each group.
func partitionWindowsByTitle(windowIDs []string, windowName func(windowID string) string, matches func(title string) bool) (matching, nonMatching []string) {
	for _, windowID := range windowIDs {
		if matches(windowName(windowID)) {
			matching = append(matching, windowID)
		} else {
			nonMatching = append(nonMatching, windowID)
		}
	}
	return matching, nonMatching
}

func getKdotoolWindowName(windowID string) string {
	output, err := exec.Command("kdotool", "getwindowname", windowID).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func getXdotoolWindowName(windowID string) string {
	cmd := exec.Command("xdotool", "getwindowname", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// DetectFocusTools returns a map of available focus tools.
func DetectFocusTools() map[string]bool {
	tools := map[string]bool{}

	// Check command-line tools
	for _, tool := range []string{"wlrctl", "kdotool", "xdotool", "wmctrl", "gdbus", "busctl"} {
		_, err := exec.LookPath(tool)
		tools[tool] = err == nil
	}

	// Check GNOME activate-window-by-title extension
	cmd := exec.Command("busctl", "--user", "introspect",
		"org.gnome.Shell",
		"/de/lucaswerkmeister/ActivateWindowByTitle",
	)
	output, err := cmd.CombinedOutput()
	tools["activate-window-by-title"] = err == nil && strings.Contains(string(output), "activateBySubstring")

	return tools
}
