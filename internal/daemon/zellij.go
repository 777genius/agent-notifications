//go:build linux

// ABOUTME: Zellij pane focus for Linux click-to-focus.
// ABOUTME: Targets the exact pane that produced the notification, across tabs.
package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// zellijActionTimeout bounds the CLI call. It runs on the D-Bus action callback,
// so a zellij server that stops answering would otherwise wedge the daemon's
// click handling for every later notification, not just this one.
var zellijActionTimeout = 5 * time.Second

// TryZellijPane focuses paneID inside sessionName, switching tabs if the pane
// lives in one that is not current.
//
// focus-pane-id arrived in zellij 0.44.1; older versions reject the subcommand
// and the caller degrades to a raised window with no pane switch.
func TryZellijPane(sessionName, paneID string) error {
	if sessionName == "" || paneID == "" {
		return fmt.Errorf("zellij session name or pane id missing")
	}
	return runZellijAction(sessionName, "focus-pane-id", paneID)
}

// TryZellijTab brings the tab named tabName to the front. It is the fallback for
// zellij older than 0.44.1, and lands on whichever pane that tab last had
// focused, which need not be the one that raised the notification.
func TryZellijTab(sessionName, tabName string) error {
	if sessionName == "" || tabName == "" {
		return fmt.Errorf("zellij session name or tab name missing")
	}
	return runZellijAction(sessionName, "go-to-tab-name", tabName)
}

// runZellijAction runs `zellij -s <session> action <action> <target>`, treating
// "already focused" as success.
func runZellijAction(sessionName, action, target string) error {
	if _, err := exec.LookPath("zellij"); err != nil {
		return fmt.Errorf("zellij not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), zellijActionTimeout)
	defer cancel()

	// -s names the session explicitly. The daemon is spawned by whichever hook
	// first needed it and then keeps that session's ZELLIJ_* variables for the
	// rest of its life, so letting zellij infer the session from the environment
	// would act on whichever session happened to start the daemon.
	cmd := exec.CommandContext(ctx, "zellij", "-s", sessionName, "action", action, target)
	// Bound pipe draining too: a child can retain stdout after the CLI is killed.
	cmd.WaitDelay = 100 * time.Millisecond
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("zellij %s failed: %w", action, ctx.Err())
	}
	if err == nil || isZellijAlreadyFocused(string(output)) {
		return nil
	}
	return fmt.Errorf("zellij %s failed: %w, output: %s", action, err, strings.TrimSpace(string(output)))
}

// tryZellijFocus carries out the strategy the hook process chose.
func tryZellijFocus(hints FocusHints) error {
	switch hints.ZellijMode {
	case ZellijFocusModePane:
		return TryZellijPane(hints.ZellijSession, hints.ZellijPaneID)
	case ZellijFocusModeTab:
		return TryZellijTab(hints.ZellijSession, hints.ZellijTabName)
	default:
		return nil
	}
}

// isZellijAlreadyFocused reports whether zellij failed only because the pane was
// already focused, which is the outcome we wanted.
//
// This has to match the message rather than the exit status: zellij exits 2 both
// when the pane is already focused and when the pane does not exist at all.
func isZellijAlreadyFocused(output string) bool {
	return strings.Contains(strings.ToLower(output), "already focused")
}
