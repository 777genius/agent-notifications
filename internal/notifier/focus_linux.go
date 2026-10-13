//go:build linux

package notifier

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/daemon"
)

// activeWindowQueryTimeout bounds the xdotool call so a stalled subprocess can
// never hang notification dispatch on the hook path. A timeout surfaces as an
// error, which terminalHasFocus treats as "unfocused" and delivers the notification.
const activeWindowQueryTimeout = 2 * time.Second

// Seams so tests can stub the X11 active-window query, the JetBrains IDE
// lookup and the active-window details query.
var (
	activeWindowID     = defaultActiveWindowID
	detectJetBrainsIDE = daemon.DetectJetBrainsIDE
	activeWindow       = defaultActiveWindow
)

// windowInfo describes the active window.
type windowInfo struct {
	pid   int
	title string
}

// terminalHasFocus reports whether the terminal window is the X11 active window.
//
// It compares the window manager's active window (_NET_ACTIVE_WINDOW, read via
// xdotool) against $WINDOWID, which X11 terminals export for their own window.
// When $WINDOWID is unset - typically under Wayland, where there is no portable
// active-window query - focus is treated as unknown and the notification is
// delivered. JetBrains IDE terminals are checked first and separately (see
// jetBrainsProjectHasFocus): they have no X11 window of their own, so a
// $WINDOWID there was inherited from whatever launched the IDE.
// Class-based matching alone is intentionally avoided: two terminal windows
// share a class, so it cannot tell "the window Claude runs in" from "another
// terminal", and a false match would swallow the notification.
func terminalHasFocus(_, cwd string) bool {
	if class, pid, ok := detectJetBrainsIDE(); ok {
		return jetBrainsProjectHasFocus(class, pid, cwd)
	}
	ours, ok := parseWindowID(os.Getenv("WINDOWID"))
	if !ok {
		return false // Wayland, or a terminal that does not export WINDOWID
	}
	activeRaw, err := activeWindowID()
	if err != nil {
		return false
	}
	active, ok := parseWindowID(activeRaw)
	if !ok {
		return false
	}
	return ours == active
}

// defaultActiveWindowID returns the X11 active window ID via xdotool, bounded by
// activeWindowQueryTimeout so a stalled subprocess cannot hang the hook.
func defaultActiveWindowID() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), activeWindowQueryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xdotool", "getactivewindow").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parseWindowID normalizes an X11 window ID (decimal from $WINDOWID, or
// hexadecimal like 0x1e00007 from some tools) to a comparable integer. It
// returns false when the input is empty or not a valid number.
func parseWindowID(raw string) (uint64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseUint(raw, 0, 64) // base 0 honors a 0x prefix
	if err != nil {
		return 0, false
	}
	return id, true
}

// jetBrainsProjectHasFocus reports whether the active window is this session's
// JetBrains project window: owned by the IDE process this session runs under,
// with a title naming this project (and its path when JetBrains shows one for
// same-named projects). The PID already identifies the IDE, so the window
// class is not compared (xdotool before 2021 cannot print it). Like the X11
// check it is window-level: it cannot tell whether the IDE's terminal tool
// window is open.
func jetBrainsProjectHasFocus(class string, pid int, cwd string) bool {
	projectPath := daemon.GetFocusProjectPath(class, cwd)
	if projectPath == "" {
		return false // not inside a JetBrains project: its window can't be confirmed
	}
	window, err := activeWindow()
	if err != nil {
		return false
	}
	return window.pid == pid &&
		daemon.JetBrainsTitleMatches(window.title, daemon.GetFocusFolderName(class, cwd), projectPath)
}

// defaultActiveWindow reads the active window's PID and title in one chained
// call: xdotool on X11, kdotool (KDE Plasma) otherwise. It is bounded
// by activeWindowQueryTimeout so a stalled subprocess cannot hang the hook.
func defaultActiveWindow() (windowInfo, error) {
	tool := "kdotool"
	if os.Getenv("XDG_SESSION_TYPE") == "x11" {
		tool = "xdotool"
	}
	ctx, cancel := context.WithTimeout(context.Background(), activeWindowQueryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, tool, "getactivewindow", "getwindowpid", "getwindowname").Output()
	if err != nil {
		return windowInfo{}, err
	}
	return parseWindowInfo(string(out))
}

// parseWindowInfo parses the PID and title lines printed by
// defaultActiveWindow. The title is everything after the PID line.
func parseWindowInfo(out string) (windowInfo, error) {
	lines := strings.SplitN(strings.TrimSuffix(out, "\n"), "\n", 2)
	if lines[0] == "" {
		return windowInfo{}, errors.New("empty active window output")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return windowInfo{}, err
	}
	info := windowInfo{pid: pid}
	if len(lines) == 2 {
		info.title = lines[1]
	}
	return info, nil
}
