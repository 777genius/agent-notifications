//go:build linux

// ABOUTME: Sway click-to-focus: matches the hook's process ancestry against Sway's window tree.
// ABOUTME: Exact where wlrctl is not, because wlrctl can only select windows by app_id or title.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// swaymsgTimeout bounds each swaymsg call. The focus chain runs on the D-Bus
// action callback, so a compositor that stops answering would otherwise wedge
// the daemon's click handling for every later notification, not just this one.
var swaymsgTimeout = 2 * time.Second

// swayView is a window of the Sway layout tree.
type swayView struct {
	ID   int64  // con_id, which a swaymsg criteria selects the window by
	PID  int    // process that owns the window
	Name string // window title
}

// swayNode is the part of a get_tree node that finding windows reads.
type swayNode struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	PID           int        `json:"pid"`
	Nodes         []swayNode `json:"nodes"`
	FloatingNodes []swayNode `json:"floating_nodes"`
}

// GetSwayFocusHints returns the Sway IPC socket and the ancestry of this process
// when the session runs under Sway, or empty values otherwise. Sway names a
// window by the process that owns it, and the terminal's process is an ancestor
// of the hook.
//
// Both are read in the hook process. The daemon is spawned by whichever hook
// first needed it, so its own $SWAYSOCK may name a compositor that has since
// exited, and its own ancestry is not the session's.
func GetSwayFocusHints() (socket string, ancestorPIDs []int) {
	socket = strings.TrimSpace(os.Getenv("SWAYSOCK"))
	if socket == "" {
		return "", nil
	}
	return socket, processAncestorPIDs()
}

// processAncestorPIDs returns the PIDs of this process's ancestors, nearest
// first, starting with its parent. A PID is listed as soon as its child names it,
// even when its own stat cannot be read. The walk ends at init, at a parent
// cycle, at an unreadable stat, or after maxProcessAncestors processes.
func processAncestorPIDs() []int {
	var pids []int
	pid := getParentPID()
	for pid > 1 && len(pids) < maxProcessAncestors && !slices.Contains(pids, pid) {
		pids = append(pids, pid)

		ppid, err := readParentPID(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
		if err != nil {
			break
		}
		pid = ppid
	}
	return pids
}

// parseSwayViews returns the windows of a `swaymsg -t get_tree` reply, in tree
// order: at each level the tiling children come before the floating ones. That
// includes the scratchpad, which Sway keeps as the floating children of the
// __i3_scratch workspace. Only windows carry a pid, so containers, workspaces and
// outputs are skipped.
func parseSwayViews(data []byte) ([]swayView, error) {
	var root swayNode
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("unreadable sway tree: %w", err)
	}

	var views []swayView
	var collect func(node *swayNode)
	collect = func(node *swayNode) {
		if node.PID > 0 {
			views = append(views, swayView{ID: node.ID, PID: node.PID, Name: node.Name})
		}
		for i := range node.Nodes {
			collect(&node.Nodes[i])
		}
		for i := range node.FloatingNodes {
			collect(&node.FloatingNodes[i])
		}
	}
	collect(&root)
	return views, nil
}

// pickSwayView returns the window that hosts the session: one owned by the
// nearest ancestor process that owns any window. Among several windows of that
// process (a terminal server, an IDE with several projects) it prefers an
// exact hook-captured window title, then a title naming the session's folder,
// and finally the first window in tree order.
func pickSwayView(views []swayView, hints FocusHints) (swayView, bool) {
	titleMatches := windowTitleMatcher(hints)
	for _, pid := range hints.AncestorPIDs {
		var owned []swayView
		for _, view := range views {
			if view.PID == pid {
				owned = append(owned, view)
			}
		}
		if len(owned) == 0 {
			continue
		}
		if hints.WindowTitle != "" {
			for _, view := range owned {
				if view.Name == hints.WindowTitle {
					return view, true
				}
			}
		}
		if hints.FolderName != "" {
			for _, view := range owned {
				if titleMatches(view.Name) {
					return view, true
				}
			}
		}
		return owned[0], true
	}
	return swayView{}, false
}

// trySway raises the Sway window that hosts the session, found by process
// ancestry. wlrctl selects windows by app_id or title and activates every match,
// so it cannot tell apart several windows of one terminal. Sway's window tree
// records the process that owns each window, and for a terminal that process is
// an ancestor of the hook.
//
// It fails, leaving the chain to fall through to wlrctl, when the hook captured
// no Sway session, or when no ancestor owns a window. The latter is the case for
// a session inside tmux, whose server does not descend from the terminal.
func trySway(hints FocusHints) error {
	if hints.SwaySocket == "" || len(hints.AncestorPIDs) == 0 {
		return errors.New("no sway session captured by the hook")
	}
	swaymsg, err := exec.LookPath("swaymsg")
	if err != nil {
		return errors.New("swaymsg not installed")
	}

	tree, err := runSwaymsg(swaymsg, hints.SwaySocket, "-t", "get_tree", "-r")
	if err != nil {
		return fmt.Errorf("swaymsg get_tree failed: %w", err)
	}
	views, err := parseSwayViews(tree)
	if err != nil {
		return err
	}
	view, ok := pickSwayView(views, hints)
	if !ok {
		return fmt.Errorf("no sway window belongs to any of the session's %d ancestor processes", len(hints.AncestorPIDs))
	}

	// The criteria carries a number that Sway reported, so no text from the
	// notification reaches the command.
	reply, err := runSwaymsg(swaymsg, hints.SwaySocket, fmt.Sprintf("[con_id=%d]", view.ID), "focus")
	if failure := swayReplyError(reply); failure != nil {
		return fmt.Errorf("swaymsg focus of window %d failed: %w", view.ID, failure)
	}
	if err != nil {
		return fmt.Errorf("swaymsg focus of window %d failed: %w", view.ID, err)
	}
	return nil
}

// runSwaymsg runs swaymsg against socket and returns what it printed on stdout.
//
// -s names the socket explicitly. The daemon is spawned by whichever hook first
// needed it and then keeps that hook's environment for the rest of its life, so
// letting swaymsg read $SWAYSOCK would act on whichever compositor happened to
// be running then.
func runSwaymsg(swaymsg, socket string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), swaymsgTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, swaymsg, append([]string{"-s", socket}, args...)...)
	// Bound pipe draining too: a child can retain stdout after swaymsg is killed.
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil && ctx.Err() != nil {
		return out, fmt.Errorf("timed out after %s: %w", swaymsgTimeout, ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return out, fmt.Errorf("%w: %s", err, stderr)
		}
	}
	return out, err
}

// swayReplyError returns an error naming the first failed command in a Sway
// command reply, which is an array of {"success": bool, "error": string}. It
// returns nil when every command succeeded, and also when the reply is empty or
// not a command reply, since the exit status of swaymsg is then the only signal.
func swayReplyError(reply []byte) error {
	var results []struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(reply, &results) != nil {
		return nil
	}
	for _, result := range results {
		if result.Success {
			continue
		}
		if result.Error == "" {
			return errors.New("sway reported the command as failed")
		}
		return errors.New(result.Error)
	}
	return nil
}
