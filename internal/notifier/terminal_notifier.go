package notifier

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/logging"
)

// selectTerminalNotifierArgs keeps the same opt-out across ordinary terminals
// and multiplexers. Detection is allowed only for a navigation-enabled send:
// a mux builder returns executable click actions even if the generic fallback
// would have omitted them.
func selectTerminalNotifierArgs(title, message, bundleID, cwd, ghosttyTerminalID string, clickToFocus bool, cfg *config.Config) []string {
	return selectTerminalNotifierArgsWithDetector(title, message, bundleID, cwd, ghosttyTerminalID, clickToFocus, cfg, detectMultiplexerArgs)
}

// The detector seam lets tests prove that disabled navigation never captures
// or invokes a terminal multiplexer, independently of machine environment.
func selectTerminalNotifierArgsWithDetector(title, message, bundleID, cwd, ghosttyTerminalID string, clickToFocus bool, cfg *config.Config, detect func(string, string, string, *config.Config) ([]string, string)) []string {
	if clickToFocus {
		if muxArgs, muxName := detect(title, message, bundleID, cfg); muxArgs != nil {
			logging.Debug("%s detected, using multiplexer-specific -execute", muxName)
			return muxArgs
		} else if muxName != "" {
			logging.Debug("%s detected but target capture failed, falling back to -activate", muxName)
		}
	}
	return buildTerminalNotifierArgsWithOptions(title, message, bundleID, cwd, ghosttyTerminalID, clickToFocus)
}

// sendWithTerminalNotifier sends notification via terminal-notifier on macOS
// with click-to-focus support (clicking notification activates the terminal)
func (n *Notifier) sendWithTerminalNotifier(title, message, subtitle, sessionID string, _ bool, cwd string, clickToFocus bool, soundFile ...string) error {
	notifierPath, err := GetTerminalNotifierPath()
	if err != nil {
		return fmt.Errorf("terminal-notifier not found: %w", err)
	}

	bundleID := GetTerminalBundleID(n.cfg.Notifications.Desktop.TerminalBundleID)
	ghosttyTerminalID := ""
	if clickToFocus && isGhosttyBundleID(bundleID) {
		ghosttyTerminalID = loadStoredGhosttyTerminalID(sessionID)
	}

	// Navigation is an explicit per-delivery choice. Multiplexer builders
	// generate -execute actions, so they must never run when focus is disabled.
	args := selectTerminalNotifierArgs(title, message, bundleID, cwd, ghosttyTerminalID, clickToFocus, n.cfg)

	// Keep the legacy boolean parameter for existing callers, but never opt
	// ordinary desktop notifications into an interruption that bypasses Focus.
	args = appendSharedNotifierOptions(args, subtitle, sessionID, false, n.cfg.ShouldReplaceNotificationsPerSession())
	if len(soundFile) > 0 && soundFile[0] != "" {
		// Keep -nosound for older helpers, which ignore these new flags. The
		// modern helper overrides it only with this explicit custom sound.
		args = append(args, "-soundFile", soundFile[0], "-soundVolume", strconv.FormatFloat(n.cfg.Notifications.Desktop.Volume, 'f', -1, 64))
	}

	if appPath, ok := claudeNotifierAppPath(notifierPath); ok {
		if err := runClaudeNotifierApp(appPath, args); err != nil {
			return err
		}
		logging.Debug("ClaudeNotifier executed via LaunchServices: bundleID=%s", bundleID)
		return nil
	}

	output, err := runLegacyNotifierCommand(legacyTerminalNotifierTimeout, notifierPath, args...)
	if err != nil {
		return fmt.Errorf("terminal-notifier error: %w, output: %s", err, string(output))
	}

	logging.Debug("terminal-notifier executed: bundleID=%s", bundleID)
	return nil
}

func runCommandWithTimeout(timeout time.Duration, notifierPath string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := buildNotifierCommandWithContext(ctx, notifierPath, args)
	// A child can retain the output pipes after the direct process exits or is
	// killed. Bound the drain as well; this does not terminate descendants.
	cmd.WaitDelay = legacyTerminalNotifierWaitDelay
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return output, fmt.Errorf("command timed out after %s: %w", timeout, ctx.Err())
	}
	return output, err
}

func runClaudeNotifierApp(appPath string, args []string) error {
	tempDir, err := os.MkdirTemp("", "claude-notifier-open-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir for ClaudeNotifier launch: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	stdoutPath := filepath.Join(tempDir, "stdout.log")
	stderrPath := filepath.Join(tempDir, "stderr.log")

	openArgs := []string{
		"-W",
		"-n",
		"-g",
		"-i", "/dev/null",
		"-o", stdoutPath,
		"--stderr", stderrPath,
		appPath,
		"--args",
		"-launchedViaLaunchServices",
	}
	openArgs = append(openArgs, args...)

	cmd := execCommand("open", openArgs...)
	runErr := cmd.Run()

	stderrOutput, _ := os.ReadFile(stderrPath)
	stderrText := strings.TrimSpace(string(stderrOutput))
	if strings.Contains(stderrText, macOSPermissionDeniedMessage) {
		return &NotificationPermissionDeniedError{Details: stderrText}
	}

	if runErr != nil {
		if stderrText != "" {
			return fmt.Errorf("ClaudeNotifier open failed: %w, stderr: %s", runErr, stderrText)
		}
		return fmt.Errorf("ClaudeNotifier open failed: %w", runErr)
	}

	if stderrText != "" {
		return fmt.Errorf("ClaudeNotifier reported an error: %s", stderrText)
	}

	return nil
}

// buildNotifierCommand builds the execution command for a notifier binary.
// ClaudeNotifier.app must be launched via LaunchServices so
// UNUserNotificationCenter gets valid bundle metadata under hardened runtime.
func buildNotifierCommand(notifierPath string, args []string) *exec.Cmd {
	return buildNotifierCommandWithContext(context.Background(), notifierPath, args)
}

func buildNotifierCommandWithContext(ctx context.Context, notifierPath string, args []string) *exec.Cmd {
	if appPath, ok := claudeNotifierAppPath(notifierPath); ok {
		openArgs := []string{"-W", "-n", "-g", appPath, "--args", "-launchedViaLaunchServices"}
		openArgs = append(openArgs, args...)
		return exec.CommandContext(ctx, "open", openArgs...)
	}

	return exec.CommandContext(ctx, notifierPath, args...)
}

// claudeNotifierAppPath recognizes modern helpers in conventional bundles, the
// product-owned alias, and durable managed generation bundles.
func claudeNotifierAppPath(notifierPath string) (string, bool) {
	cleanPath := filepath.Clean(notifierPath)
	suffix := filepath.Join("Contents", "MacOS", "terminal-notifier-modern")
	if !strings.HasSuffix(cleanPath, suffix) {
		return "", false
	}

	bundlePath := strings.TrimSuffix(cleanPath, suffix)
	bundlePath = strings.TrimSuffix(bundlePath, string(filepath.Separator))
	name := filepath.Base(bundlePath)
	if name != "ClaudeNotifier.app" && name != "AgentNotifications.app" &&
		(!strings.HasPrefix(name, "generation-") || !strings.HasSuffix(name, ".app")) {
		return "", false
	}

	return bundlePath, true
}

// buildTerminalNotifierArgs constructs command-line arguments for terminal-notifier.
// When cwd is provided, uses -execute with a focus script instead of -activate.
// Exported for testing purposes.
func buildTerminalNotifierArgs(title, message, bundleID, cwd string, clickToFocus bool) []string {
	return buildTerminalNotifierArgsWithOptions(title, message, bundleID, cwd, "", clickToFocus)
}

func buildTerminalNotifierArgsWithOptions(title, message, bundleID, cwd, ghosttyTerminalID string, clickToFocus bool) []string {
	args := []string{
		"-title", title,
		"-message", message,
	}

	if clickToFocus {
		// Note: -sender option removed because it conflicts with -activate on macOS Sequoia (15.x)
		// Using -sender causes click-to-focus to stop working.
		if script := buildFocusScriptWithOptions(bundleID, cwd, ghosttyTerminalID); script != "" {
			args = append(args, "-execute", script)
		} else {
			args = append(args, "-activate", bundleID)
		}
	}

	return args
}

// notificationGroupID is the terminal-notifier -group value, which is the
// UNNotificationRequest.identifier: delivering a request that reuses an
// identifier replaces the previous banner in place.
//
// By default the identifier is unique per notification, so nothing is ever
// replaced: a shared -group would collapse every ClaudeNotifier toast into one
// slot, letting a Question from chat B hide a Completed from chat A.
//
// When replacePerSession is set, the identifier is keyed on the Claude/Codex
// session instead, so a session's own newer notification replaces its previous
// banner while other sessions stay untouched. Empty/"unknown" ids always stay
// unique so unrelated notifications are never collapsed. Conversation grouping
// in Notification Center is -threadID, independent of this switch.
func notificationGroupID(sessionID string, replacePerSession bool) string {
	if replacePerSession {
		if id := strings.TrimSpace(sessionID); id != "" && !strings.EqualFold(id, "unknown") {
			return notificationGroupPrefix + id
		}
	}
	return uniqueNotificationGroupID()
}

// uniqueNotificationGroupID returns a never-reused -group value.
func uniqueNotificationGroupID() string {
	seq := uniqueNotificationGroupSeq.Add(1)
	return fmt.Sprintf("%s%d-%d", notificationGroupPrefix, time.Now().UnixNano(), seq)
}

// booleanNotifierFlags are notifier flags that consume no value, so the token
// that follows them is not one of their arguments. Every other token that
// begins with "-" is treated as a flag whose next token is its value.
var booleanNotifierFlags = map[string]struct{}{
	"-timeSensitive": {},
	"-nosound":       {},
	"-ignoreDnD":     {},
}

// setNotifierFlag replaces flag's value when flag appears at a flag position,
// otherwise appends the flag/value pair. Matching only flag positions keeps an
// argument value that equals flag (for example a notification title of
// "-group") from being mistaken for the flag and corrupting the argument that
// follows it.
func setNotifierFlag(args []string, flag, value string) []string {
	rewritten := make([]string, 0, len(args)+2)
	replaced := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !replaced && arg == flag {
			rewritten = append(rewritten, flag, value)
			replaced = true
			// Drop the stale value that followed the original flag.
			if i+1 < len(args) {
				i++
			}
			continue
		}
		rewritten = append(rewritten, arg)
		// A non-boolean flag consumes the next token as its value; skip it so a
		// value that looks like a flag is never matched as one.
		if strings.HasPrefix(arg, "-") {
			if _, isBoolean := booleanNotifierFlags[arg]; !isBoolean && i+1 < len(args) {
				i++
				rewritten = append(rewritten, args[i])
			}
		}
	}
	if !replaced {
		rewritten = append(rewritten, flag, value)
	}
	return rewritten
}

// appendSharedNotifierOptions adds subtitle, session thread, replacement group,
// and Swift-only flags that every macOS delivery path (plain and multiplexer)
// shares.
func appendSharedNotifierOptions(args []string, subtitle, sessionID string, timeSensitive, replacePerSession bool) []string {
	if subtitle != "" {
		args = append(args, "-subtitle", subtitle)
	}
	if id := strings.TrimSpace(sessionID); id != "" && !strings.EqualFold(id, "unknown") {
		// Group in Notification Center by Claude/Codex session without replacing banners.
		args = append(args, "-threadID", id)
	}
	args = setNotifierFlag(args, "-group", notificationGroupID(sessionID, replacePerSession))
	if timeSensitive {
		args = append(args, "-timeSensitive")
	}
	// Silent by default, including older helpers without custom-sound support.
	args = append(args, "-nosound")
	return args
}

// nativeNotificationSound selects the configured cue for system-managed audio.
// macOS owns its playback so Focus suppresses the banner and sound together.
func (n *Notifier) nativeNotificationSound(sound string, opts sendOptions) string {
	if opts.muteSound || !n.cfg.Notifications.Desktop.Sound || n.cfg.Notifications.Desktop.Volume <= 0 || sound == "" {
		return ""
	}
	absolute, err := filepath.Abs(sound)
	if err != nil {
		logging.Warn("Cannot resolve notification sound: %v", err)
		return ""
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		logging.Warn("Notification sound file unavailable: %s", absolute)
		return ""
	}
	return absolute
}
