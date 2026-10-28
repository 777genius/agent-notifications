//go:build darwin

package notifier

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/commandrun"
	"github.com/777genius/agent-notifications/internal/warpfocus"
)

func validInstalledRuntime(opts sendOptions) bool {
	return filepath.IsAbs(opts.runtimeRoot) && filepath.Clean(opts.runtimeRoot) == opts.runtimeRoot
}

func (n *Notifier) sendNativeDesktopContext(ctx context.Context, sound, title, body, subtitle, sessionID, cwd, _ string, opts sendOptions) error {
	if !validInstalledRuntime(opts) || !filepath.IsAbs(opts.nativeHelper) {
		return errors.New("installed_native_helper_required")
	}
	bundleID := terminalBundleContext(ctx, n.cfg.Notifications.Desktop.TerminalBundleID)
	args := []string{"-title", title, "-message", body}
	if n.cfg.Notifications.Desktop.ClickToFocus && bundleID != "" {
		if mux, ok := multiplexerArgsContext(ctx, title, body, bundleID, opts.runtimeRoot); ok {
			args = mux
		} else {
			script := focusScriptContext(ctx, bundleID, cwd, opts.runtimeRoot)
			if script != "" {
				args = append(args, "-execute", script)
			} else {
				args = append(args, "-activate", bundleID)
			}
		}
	}
	args = appendSharedNotifierOptions(args, subtitle, sessionID, false, n.cfg.ShouldReplaceNotificationsPerSession())
	if cue := n.nativeNotificationSound(sound, opts); cue != "" {
		args = append(args, "-soundFile", cue, "-soundVolume", strconv.FormatFloat(n.cfg.Notifications.Desktop.Volume, 'f', -1, 64))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if app, ok := claudeNotifierAppPath(opts.nativeHelper); ok {
		return runNativeAppContext(ctx, app, args)
	}
	_, err := commandrun.Output(ctx, exec.Command(opts.nativeHelper, args...))
	return err
}

func runNativeAppContext(ctx context.Context, app string, args []string) error {
	root, err := os.MkdirTemp("", "agent-notifications-open-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	stderr := filepath.Join(root, "stderr")
	argv := []string{"-W", "-n", "-g", "-i", "/dev/null", "-o", filepath.Join(root, "stdout"), "--stderr", stderr, app, "--args", "-launchedViaLaunchServices"}
	argv = append(argv, args...)
	err = commandrun.Run(ctx, exec.Command("/usr/bin/open", argv...))
	data, _ := os.ReadFile(stderr)
	if strings.Contains(string(data), macOSPermissionDeniedMessage) {
		return &NotificationPermissionDeniedError{}
	}
	if err != nil {
		return err
	}
	if len(data) != 0 {
		return errors.New("native_helper_reported_error")
	}
	return nil
}

func terminalBundleContext(parent context.Context, override string) string {
	if override != "" {
		return override
	}
	if bundle := os.Getenv("__CFBundleIdentifier"); bundle != "" {
		return bundle
	}
	if bundle := terminalBundleIDMap[os.Getenv("TERM_PROGRAM")]; bundle != "" {
		return bundle
	}
	if !IsTmux() {
		return ""
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	for _, flags := range [][]string{{}, {"-g"}} {
		argv := []string{}
		if socket := getTmuxSocketPath(); socket != "" {
			argv = append(argv, "-S", socket)
		}
		argv = append(argv, "show-environment")
		argv = append(argv, flags...)
		argv = append(argv, "TERM_PROGRAM")
		out, err := commandrun.Output(ctx, exec.Command(getTmuxPath(), argv...))
		if err != nil {
			continue
		}
		if bundle := terminalBundleIDMap[strings.TrimPrefix(strings.TrimSpace(string(out)), "TERM_PROGRAM=")]; bundle != "" {
			return bundle
		}
	}
	return ""
}

func itermHelperContext(parent context.Context, root string) (string, string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}
	python := filepath.Join(home, ".claude", "claude-notifications-go", "iterm2-venv", "bin", "python3")
	script := filepath.Join(root, "scripts", "iterm2-select-tab.py")
	for _, path := range []string{python, script} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			return "", "", false
		}
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if _, err = commandrun.Output(ctx, exec.Command(python, script, iTerm2HealthcheckFlag)); err != nil {
		return "", "", false
	}
	return python, script, true
}

func focusScriptContext(ctx context.Context, bundleID, cwd, root string) string {
	if isIterm2BundleID(bundleID) {
		python, script, ok := itermHelperContext(ctx, root)
		if !ok {
			return ""
		}
		command := shellQuote(python) + " " + shellQuote(script)
		if session := os.Getenv(iTerm2SessionIDEnv); session != "" {
			command += " --termid " + shellQuote(session)
		} else if !isUsableFocusCWD(cwd) {
			return ""
		}
		if isUsableFocusCWD(cwd) {
			command += " --cwd " + shellQuote(cwd)
		}
		return command + " >/dev/null 2>&1 || open -a iTerm"
	}
	if url := warpfocus.FromEnvContext(ctx); url != "" {
		return buildWarpFocusScript(url, bundleID, cwd)
	}
	if !isUsableFocusCWD(cwd) {
		return ""
	}
	return buildBinaryFocusScript(bundleID, cwd, "")
}

func multiplexerArgsContext(ctx context.Context, title, body, bundleID, root string) ([]string, bool) {
	var args []string
	switch {
	case IsTmux():
		// TMUX_PANE names this process's pane. A current-active-pane query after
		// the user changed tabs cannot establish the originating Pi pane.
		pane := os.Getenv("TMUX_PANE")
		if !strings.HasPrefix(pane, "%") {
			return nil, false
		}
		if isIterm2BundleID(bundleID) {
			python, script, ok := itermHelperContext(ctx, root)
			if !ok {
				return nil, false
			}
			command := shellQuote(python) + " " + shellQuote(script) + " --pane " + shellQuote(pane) + " --tmux-path " + shellQuote(getTmuxPath())
			if socket := getTmuxSocketPath(); socket != "" {
				command += " --socket " + shellQuote(socket)
			}
			args = []string{"-title", title, "-message", body, "-activate", bundleID, "-execute", command}
		} else {
			args = buildTmuxNotifierArgs(title, body, pane, bundleID)
		}
	case IsZellij():
		session := os.Getenv("ZELLIJ_SESSION_NAME")
		if session == "" {
			return nil, false
		}
		if pane := os.Getenv("ZELLIJ_PANE_ID"); pane != "" && zellijSupportsPaneFocusContext(ctx) {
			args = buildZellijActionNotifierArgs(title, body, session, bundleID, "focus-pane-id", pane)
		} else {
			tab, _, err := getZellijTabTargetContext(ctx)
			if err != nil {
				return nil, false
			}
			args = buildZellijNotifierArgs(title, body, tab, session, bundleID)
		}
	case IsWezTerm():
		pane, socket, err := GetWezTermPaneTarget()
		if err != nil {
			return nil, false
		}
		args = buildWezTermNotifierArgs(title, body, pane, socket, bundleID)
	case IsKitty():
		window, listen, err := GetKittyWindowTarget()
		if err != nil {
			return nil, false
		}
		args = buildKittyNotifierArgs(title, body, window, listen, bundleID)
	default:
		return nil, false
	}
	return injectResolvedWarpURL(args, warpfocus.FromEnvContext(ctx)), true
}

func hasNativeTerminal(parent context.Context) bool {
	if os.Getenv("__CFBundleIdentifier") != "" || terminalBundleIDMap[os.Getenv("TERM_PROGRAM")] != "" {
		return true
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	return terminalBundleContext(ctx, "") != ""
}
func desktopDNDContext(context.Context) bool { return false }
func terminalHasFocusContext(parent context.Context, _, cwd string) bool {
	ctx, cancel := context.WithTimeout(parent, focusQueryTimeout)
	defer cancel()
	bundle := terminalBundleContext(ctx, "")
	if bundle == "" {
		return false
	}
	front, err := commandrun.Output(ctx, exec.Command("/usr/bin/lsappinfo", "front"))
	if err != nil {
		return false
	}
	info, err := commandrun.Output(ctx, exec.Command("/usr/bin/lsappinfo", "info", "-only", "bundleid", strings.TrimSpace(string(front))))
	if err != nil {
		return false
	}
	active, ok := parseLsappinfoBundleID(string(info))
	return ok && strings.EqualFold(active, bundle) && ctx.Err() == nil && frontmostTerminalWindowMatchesCWD(bundle, cwd)
}
