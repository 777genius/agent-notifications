package notifier

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/logging"
)

// IsZellij returns true if the current process is running inside a zellij session.
func IsZellij() bool {
	return os.Getenv("ZELLIJ") != ""
}

// getZellijPath returns the absolute path to the zellij binary.
// ClaudeNotifier.app runs without the user's PATH, so we need the full path.
func getZellijPath() string {
	if path, err := exec.LookPath("zellij"); err == nil {
		return path
	}
	return "zellij"
}

// zellijProbeTimeout bounds the capability probe. A variable so the test can
// shorten it; nothing else writes to it.
var zellijProbeTimeout = 2 * time.Second

// ZellijSupportsPaneFocus reports whether the installed zellij accepts
// focus-pane-id, which arrived in 0.44.1.
//
// It asks zellij rather than comparing version numbers: `action <name> --help`
// exits zero for a subcommand that exists and non-zero for one that does not,
// needs no running session, and leaves the running one alone. Any non-zero
// status counts as unsupported, which is what carries this across zellij's clap
// upgrade — the exit code for an unknown subcommand changed from 1 to 2 — and
// what makes an absent zellij degrade to the target that has always existed
// rather than to no focus at all.
//
// The probe is bounded: it runs in the hook process, ahead of the notification
// the user is waiting for, so a zellij that never answers must not hold that up.
// A timeout reads as unsupported, which lands on the tab path.
func ZellijSupportsPaneFocus() bool {
	ctx, cancel := context.WithTimeout(context.Background(), zellijProbeTimeout)
	defer cancel()

	return exec.CommandContext(ctx, getZellijPath(), "action", "focus-pane-id", "--help").Run() == nil
}

// zellijLayoutTimeout bounds the dump-layout call. A variable so the test can
// shorten it; nothing else writes to it.
var zellijLayoutTimeout = 2 * time.Second

// zellijLayoutWaitDelay is the grace Output gets to finish reading once the
// deadline has fired. Killing zellij does not close a pipe that a child of it
// still holds, and Output waits on that pipe, so without this the read outlives
// the deadline that was supposed to bound it.
const zellijLayoutWaitDelay = 100 * time.Millisecond

// GetZellijTabTarget returns the active tab name and session name for the current zellij session.
//
// dump-layout is answered by the running zellij server, so it can hang in a way
// that argument parsing cannot. It runs in the hook process, ahead of the
// notification the user is waiting for, so it is bounded: a session that stops
// answering costs the deadline and then reports a failed target, which every
// caller already treats as "no zellij focus" rather than "no notification".
func GetZellijTabTarget() (tabName, sessionName string, err error) {
	sessionName = os.Getenv("ZELLIJ_SESSION_NAME")
	if sessionName == "" {
		return "", "", fmt.Errorf("ZELLIJ_SESSION_NAME not set")
	}

	zellijPath := getZellijPath()

	ctx, cancel := context.WithTimeout(context.Background(), zellijLayoutTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, zellijPath, "action", "dump-layout")
	cmd.WaitDelay = zellijLayoutWaitDelay

	output, err := cmd.Output()
	// The kill surfaces as "signal: killed", which does not say why, so the
	// deadline is reported ahead of the process error.
	if ctx.Err() == context.DeadlineExceeded {
		return "", "", fmt.Errorf("zellij dump-layout timed out after %s: %w", zellijLayoutTimeout, ctx.Err())
	}
	if err != nil {
		return "", "", fmt.Errorf("failed to run zellij dump-layout: %w", err)
	}

	tabName = parseActiveTabName(string(output))
	if tabName == "" {
		return "", "", fmt.Errorf("could not determine active tab from zellij layout")
	}

	return tabName, sessionName, nil
}

// parseActiveTabName extracts the name of the focused tab from zellij dump-layout output (KDL format).
// It looks for lines matching: tab ... name="..." ... focus=true
func parseActiveTabName(layout string) string {
	for _, line := range strings.Split(layout, "\n") {
		trimmed := strings.TrimSpace(line)

		// Must be a top-level "tab" line (not "pane" or nested content)
		if !strings.HasPrefix(trimmed, "tab ") {
			continue
		}

		// Must have focus=true
		if !strings.Contains(trimmed, "focus=true") {
			continue
		}

		// Extract name="..."
		if name := extractKDLStringAttr(trimmed, "name"); name != "" {
			return name
		}
	}
	return ""
}

// extractKDLStringAttr extracts the value of a key="value" attribute from a KDL line.
func extractKDLStringAttr(line, key string) string {
	// Search for key="
	search := key + `="`
	idx := strings.Index(line, search)
	if idx < 0 {
		return ""
	}
	start := idx + len(search)
	// Find closing quote
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return ""
	}
	return line[start : start+end]
}

// buildZellijNotifierArgs constructs command-line arguments for terminal-notifier
// when running inside zellij. Uses -activate (to focus the terminal app)
// and -execute (to switch to the correct zellij tab) on click.
func buildZellijNotifierArgs(title, message, tabName, sessionName, bundleID string) []string {
	return buildZellijActionNotifierArgs(title, message, sessionName, bundleID, "go-to-tab-name", tabName)
}

// buildZellijActionNotifierArgs assembles the terminal-notifier invocation whose
// -execute runs a single `zellij action` against an explicitly named session.
func buildZellijActionNotifierArgs(title, message, sessionName, bundleID, action, target string) []string {
	zellijPath := getZellijPath()

	executeCmd := fmt.Sprintf(
		"'%s' -s '%s' action %s '%s'",
		zellijPath, sessionName, action, target,
	)

	args := []string{
		"-title", title,
		"-message", message,
		"-activate", bundleID,
		"-execute", executeCmd,
	}

	// Add group ID to prevent notification stacking issues
	args = append(args, "-group", fmt.Sprintf("claude-notif-%d", time.Now().UnixNano()))

	return args
}

// resolveZellijFocusMode decides how a zellij session should be brought forward.
// An explicit config value wins; otherwise the choice follows what the installed
// zellij can actually do.
func resolveZellijFocusMode(configured string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(configured)); normalized {
	case daemon.ZellijFocusModePane, daemon.ZellijFocusModeTab, daemon.ZellijFocusModeOff:
		return normalized
	case "", "auto":
		return autoZellijFocusMode()
	default:
		logging.Warn("Unknown zellijFocus value %q, falling back to auto", configured)
		return autoZellijFocusMode()
	}
}

// autoZellijFocusMode picks pane targeting only when it can actually happen:
// the environment must name both the session and the pane the action targets,
// and the installed zellij must accept the action. Anything missing means the
// tab path is the only one with a target. The environment is read before the
// binary is asked because the former is free and the latter execs zellij.
//
// Both halves are required because the action is `zellij -s <session> action
// focus-pane-id <pane>` — a pane ID on its own names nothing. The pair can
// arrive half-filled: $ZELLIJ alone is enough for the hints to be answered.
func autoZellijFocusMode() string {
	if sessionName, paneID := daemon.GetZellijFocusHints(); sessionName == "" || paneID == "" {
		return daemon.ZellijFocusModeTab
	}
	if ZellijSupportsPaneFocus() {
		return daemon.ZellijFocusModePane
	}
	return daemon.ZellijFocusModeTab
}
