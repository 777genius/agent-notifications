package notifier

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gen2brain/beeep"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/audio"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
)

const macOSPermissionDeniedMessage = "Notification permission denied. Enable in System Settings > Notifications."

var execCommand = exec.Command
var beeepNotify = beeep.Notify
var notifierGOOS = runtime.GOOS

// NotificationPermissionDeniedError indicates macOS rejected the native
// ClaudeNotifier path because notification permission is denied for the app.
type NotificationPermissionDeniedError struct {
	Details string
}

func (e *NotificationPermissionDeniedError) Error() string {
	if strings.TrimSpace(e.Details) == "" {
		return macOSPermissionDeniedMessage
	}
	return e.Details
}

// Notifier sends desktop notifications
type Notifier struct {
	cfg         *config.Config
	audioPlayer *audio.Player
	playerInit  sync.Once
	playerErr   error
	mu          sync.Mutex
	wg          sync.WaitGroup
	closing     bool // Prevents new sounds from being enqueued after Close() is called
}

// New creates a new notifier
func New(cfg *config.Config) *Notifier {
	return &Notifier{
		cfg: cfg,
	}
}

// isTimeSensitiveStatus returns true for statuses that should break through Focus Mode
func isTimeSensitiveStatus(status analyzer.Status) bool {
	switch status {
	case analyzer.StatusAPIError, analyzer.StatusAPIErrorOverloaded, analyzer.StatusSessionLimitReached,
		analyzer.StatusPermissionRequest:
		return true
	default:
		return false
	}
}

// SendDesktop sends a desktop notification.
// On macOS, it always prefers ClaudeNotifier/terminal-notifier to avoid
// Script Editor attribution and optionally enables click-to-focus.
// On Linux with clickToFocus enabled, it uses the background daemon.
// cwd is the working directory of the project; used for window-specific focus. May be empty.
func (n *Notifier) SendDesktop(status analyzer.Status, message, sessionID, cwd string) error {
	// Send terminal bell for terminal tab indicators (e.g. Ghostty, tmux,
	// Windows Terminal). Platform-specific; see bell_other.go / bell_windows.go.
	if n.cfg.IsTerminalBellEnabled() {
		sendTerminalBell()
	}

	if !n.cfg.IsDesktopEnabled() {
		logging.Debug("Desktop notifications disabled, skipping")
		return nil
	}

	statusInfo, exists := n.cfg.GetStatusInfo(string(status))
	if !exists {
		return fmt.Errorf("unknown status: %s", status)
	}

	presentation := legacyPresentation(status, message, statusInfo.Title, n.cfg.IsSessionLabelEnabled())
	title, cleanMessage, subtitle := presentation.Title, presentation.Body, presentation.Subtitle
	timeSensitive := presentation.TimeSensitive

	// Get app icon path if configured
	appIcon := n.cfg.Notifications.Desktop.AppIcon
	if appIcon != "" && !platform.FileExists(appIcon) {
		logging.Warn("App icon not found: %s, using default", appIcon)
		appIcon = ""
	}

	// macOS: prefer ClaudeNotifier/terminal-notifier so the common path keeps the
	// native app attribution. If that fails, fall back to beeep as a delivery
	// safety net rather than dropping the notification entirely.
	if platform.IsMacOS() {
		if IsTerminalNotifierAvailable() {
			if err := n.sendWithTerminalNotifier(
				title,
				cleanMessage,
				subtitle,
				sessionID,
				timeSensitive,
				cwd,
				n.cfg.Notifications.Desktop.ClickToFocus,
			); err != nil {
				var permissionErr *NotificationPermissionDeniedError
				if errors.As(err, &permissionErr) {
					logging.Warn("ClaudeNotifier permission denied on macOS: %v", err)
					return err
				}
				logging.Warn("ClaudeNotifier failed on macOS, falling back to beeep: %v", err)
			} else {
				logging.Debug("Desktop notification sent via ClaudeNotifier/terminal-notifier: title=%s", title)
				n.playSoundDetached(statusInfo.Sound)
				return nil
			}
		} else {
			logging.Warn("ClaudeNotifier not available on macOS, falling back to beeep (run /claude-notifications-go:init to install it)")
		}
	}

	// Linux: Try daemon for click-to-focus support
	if platform.IsLinux() && n.cfg.Notifications.Desktop.ClickToFocus {
		if err := sendLinuxNotification(title, cleanMessage, appIcon, n.cfg, cwd); err != nil {
			logging.Warn("Linux daemon notification failed, falling back to beeep: %v", err)
			// Fall through to beeep
		} else {
			logging.Debug("Desktop notification sent via Linux daemon: title=%s", title)
			n.playSoundDetached(statusInfo.Sound)
			return nil
		}
	}

	// Windows: use go-toast with protocol activation for click-to-focus support
	if platform.IsWindows() && n.cfg.Notifications.Desktop.ClickToFocus {
		if err := sendWindowsNotification(title, cleanMessage, appIcon, n.cfg, cwd); err != nil {
			logging.Warn("Windows click-to-focus notification failed, falling back to beeep: %v", err)
			// Fall through to beeep
		} else {
			logging.Debug("Desktop notification sent via Windows click-to-focus: title=%s", title)
			n.playSoundDetached(statusInfo.Sound)
			return nil
		}
	}

	// Standard path: beeep (Windows, Linux fallback)
	return n.sendWithBeeep(title, cleanMessage, appIcon, statusInfo.Sound)
}

// sendWithTerminalNotifier sends notification via terminal-notifier on macOS
// with click-to-focus support (clicking notification activates the terminal)
func (n *Notifier) sendWithTerminalNotifier(title, message, subtitle, sessionID string, timeSensitive bool, cwd string, clickToFocus bool) error {
	notifierPath, err := GetTerminalNotifierPath()
	if err != nil {
		return fmt.Errorf("terminal-notifier not found: %w", err)
	}

	bundleID := GetTerminalBundleID(n.cfg.Notifications.Desktop.TerminalBundleID)
	ghosttyTerminalID := ""
	if clickToFocus && isGhosttyBundleID(bundleID) {
		ghosttyTerminalID = loadStoredGhosttyTerminalID(sessionID)
	}

	var args []string
	if muxArgs, muxName := detectMultiplexerArgs(title, message, bundleID); muxArgs != nil {
		args = muxArgs
		logging.Debug("%s detected, using multiplexer-specific -execute", muxName)
	} else {
		if muxName != "" {
			logging.Debug("%s detected but target capture failed, falling back to -activate", muxName)
		}
		args = buildTerminalNotifierArgsWithOptions(title, message, bundleID, cwd, ghosttyTerminalID, clickToFocus)
	}

	// Append shared options: subtitle, threadID, timeSensitive, nosound
	if subtitle != "" {
		args = append(args, "-subtitle", subtitle)
	}
	if sessionID != "" {
		args = append(args, "-threadID", sessionID)
	}
	if timeSensitive {
		args = append(args, "-timeSensitive")
	}
	// Always suppress sound in Swift — Go manages sound via audio player
	args = append(args, "-nosound")

	if appPath, ok := claudeNotifierAppPath(notifierPath); ok {
		if err := runClaudeNotifierApp(appPath, args); err != nil {
			return err
		}
		logging.Debug("ClaudeNotifier executed via LaunchServices: bundleID=%s", bundleID)
		return nil
	}

	cmd := buildNotifierCommand(notifierPath, args)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("terminal-notifier error: %w, output: %s", err, string(output))
	}

	logging.Debug("terminal-notifier executed: bundleID=%s", bundleID)
	return nil
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
	if appPath, ok := claudeNotifierAppPath(notifierPath); ok {
		openArgs := []string{"-W", "-n", "-g", appPath, "--args", "-launchedViaLaunchServices"}
		openArgs = append(openArgs, args...)
		return exec.Command("open", openArgs...)
	}

	return exec.Command(notifierPath, args...)
}

// claudeNotifierAppPath extracts ClaudeNotifier.app from the embedded
// terminal-notifier-modern executable path.
func claudeNotifierAppPath(notifierPath string) (string, bool) {
	cleanPath := filepath.Clean(notifierPath)
	suffix := filepath.Join("Contents", "MacOS", "terminal-notifier-modern")
	if !strings.HasSuffix(cleanPath, suffix) {
		return "", false
	}

	bundlePath := strings.TrimSuffix(cleanPath, suffix)
	bundlePath = strings.TrimSuffix(bundlePath, string(filepath.Separator))
	if !strings.HasSuffix(bundlePath, "ClaudeNotifier.app") {
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

	// Add group ID to prevent notification stacking issues
	args = append(args, "-group", fmt.Sprintf("claude-notif-%d", time.Now().UnixNano()))

	return args
}

// buildFocusScript returns the shell command for -execute in terminal-notifier.
// For Warp: opens WARP_FOCUS_URL so Warp raises the originating window/tab/pane.
// For Ghostty: uses AXDocument attribute (OSC 7 CWD) via Accessibility API,
// falling back to plain app activation.
// For all apps (including Electron editors and regular terminals): invokes the
// binary's focus-window subcommand which uses CGS + AXTitle APIs to find and
// raise the correct window across Spaces.
// Returns "" when cwd is empty or unusable (caller should use -activate instead),
// except Warp/iTerm2 session targeting which can work without cwd.
func buildFocusScript(bundleID, cwd string) string {
	return buildFocusScriptWithOptions(bundleID, cwd, "")
}

func buildFocusScriptWithOptions(bundleID, cwd, ghosttyTerminalID string) string {
	if isIterm2BundleID(bundleID) {
		return buildIterm2FocusScript(cwd)
	}

	if focusURL := warpFocusURL(); focusURL != "" {
		return buildWarpFocusScript(focusURL, bundleID, cwd)
	}

	if isGhosttyBundleID(bundleID) {
		if cwd == "" {
			return ""
		}
		return buildGhosttyFocusScript(bundleID, cwd, ghosttyTerminalID)
	}

	if cwd == "" {
		return ""
	}

	folderName := filepath.Base(cwd)
	if folderName == "" || folderName == "." || folderName == string(filepath.Separator) {
		return ""
	}

	if isElectronEditorBundleID(bundleID) {
		return buildElectronEditorFocusScript(bundleID, cwd)
	}

	// All other terminals: use focus-window subcommand (AXTitle matching + CGS Space switching).
	// Previously used AppleScript (-execute osascript), but macOS Tahoe (26.x) broke
	// Automation permission prompts for notification click handlers — osascript fails silently.
	// The focus-window approach uses Accessibility + Screen Recording instead of Automation,
	// with graceful fallback to app-level activation when permissions are not granted.
	// See: https://github.com/777genius/agent-notifications/issues/47
	return buildBinaryFocusScript(bundleID, cwd, "")
}

// isElectronEditorBundleID reports whether bundleID belongs to an Electron-based
// editor (VS Code, Cursor, etc.). These apps don't support AppleScript window
// enumeration (-1708) and require the binary focus-window subcommand instead.
func isElectronEditorBundleID(bundleID string) bool {
	return bundleID == "com.microsoft.VSCode" ||
		bundleID == "com.microsoft.VSCodeInsiders" ||
		bundleID == "com.todesktop.230313mzl4w4u92" // Cursor
}

// isGhosttyBundleID reports whether bundleID is Ghostty.
func isGhosttyBundleID(bundleID string) bool {
	return bundleID == "com.mitchellh.ghostty"
}

// shellQuote wraps s in single quotes, escaping internal single quotes
// using the '\" technique (end quote, literal apostrophe, resume quote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildBinaryFocusScript builds the -execute script for apps that use the
// binary's focus-window subcommand (all macOS terminals including Electron editors and Ghostty).
// Returns "" (causing -activate fallback) if os.Executable() fails.
func buildBinaryFocusScript(bundleID, cwd, ghosttyTerminalID string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	cmd := shellQuote(exe) + " focus-window " + shellQuote(bundleID) + " " + shellQuote(cwd)
	if isGhosttyBundleID(bundleID) && ghosttyTerminalID != "" {
		cmd += " --ghostty-terminal-id " + shellQuote(ghosttyTerminalID)
	}
	return cmd
}

// buildElectronEditorFocusScript builds the -execute script for Electron-based
// editors (VS Code, Cursor). Invokes the binary's focus-window subcommand which
// activates the app, waits for AXWindows to populate, then raises the window
// matching cwd.
func buildElectronEditorFocusScript(bundleID, cwd string) string {
	return buildBinaryFocusScript(bundleID, cwd, "")
}

// buildGhosttyFocusScript builds the -execute script for Ghostty.
// Invokes the binary's focus-window subcommand which activates Ghostty,
// waits for AXWindows to populate, then raises the window matching cwd via
// AXDocument (OSC 7 file:// URL). AXDocument is window-level only; tabs and
// split panes within a window are not individually addressable.
func buildGhosttyFocusScript(bundleID, cwd, ghosttyTerminalID string) string {
	return buildBinaryFocusScript(bundleID, cwd, ghosttyTerminalID)
}

// cwdToFileURL converts an absolute path to a file:// URL. Ghostty exposes the
// window CWD (set via OSC 7) as a file:// URL in the AXDocument attribute.
// Uses net/url for RFC-3986-compliant percent-encoding.
func cwdToFileURL(cwd string) string {
	u := url.URL{Scheme: "file", Path: strings.TrimRight(cwd, "/") + "/"}
	return u.String()
}

// SendQuickNotification sends a one-off notification without requiring a
// Notifier instance.
// executeCmd is the shell command run when the user clicks the notification (may be empty).
func SendQuickNotification(title, message, executeCmd string) error {
	if notifierPath, err := GetTerminalNotifierPath(); err == nil {
		args := []string{
			"-title", title,
			"-message", message,
		}
		if executeCmd != "" {
			args = append(args, "-execute", executeCmd)
		}
		args = append(args,
			"-group", fmt.Sprintf("claude-quick-%d", time.Now().UnixNano()),
			"-nosound",
		)
		if output, err := buildNotifierCommand(notifierPath, args).CombinedOutput(); err == nil {
			return nil
		} else {
			logging.Debug("terminal-notifier failed: %v, output: %s", err, string(output))
		}
	}

	// Fallback: osascript (no click action, just informational)
	script := fmt.Sprintf(`display notification %q with title %q`, message, title)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		return fmt.Errorf("all notification methods failed: %w", err)
	}
	return nil
}

// sendWithBeeep sends notification via beeep (cross-platform)
func (n *Notifier) sendWithBeeep(title, message, appIcon, sound string) error {
	// Platform-specific AppName handling:
	// - Windows: Use fixed AppName to prevent registry pollution. Each unique AppName
	//   creates a persistent entry in HKEY_CURRENT_USER\SOFTWARE\Microsoft\Windows\
	//   CurrentVersion\Notifications\Settings\ that is never cleaned up.
	//   See: https://github.com/777genius/agent-notifications/issues/4
	// - macOS/Linux: Use unique AppName to prevent notification grouping/replacement,
	//   allowing multiple notifications to be displayed simultaneously.
	originalAppName := beeep.AppName
	if isNotifierWindows() {
		beeep.AppName = "Claude Code Notifications"
	} else {
		beeep.AppName = fmt.Sprintf("claude-notif-%d", time.Now().UnixNano())
	}
	defer func() {
		beeep.AppName = originalAppName
	}()

	// Send notification using beeep with proper title and clean message.
	err := notifyViaBeeep(title, message, appIcon)
	if err != nil && isWindowsToastFallbackSuccess(err) {
		logging.Warn("beeep.Notify returned a Windows toast false positive after PowerShell fallback: %v (AppName=%q, title=%q)", err, beeep.AppName, title)
		err = nil
	}
	if err != nil {
		logging.Error("beeep.Notify failed on %s: %v (AppName=%q, title=%q)", notifierGOOS, err, beeep.AppName, title)
	} else {
		logging.Debug("Desktop notification sent via beeep: title=%s", title)
	}

	// Play the sound regardless of the toast outcome. The desktop toast and the
	// audio cue are independent signals, and on Windows beeep.Notify routinely
	// returns an error even though the toast was delivered: go-toast falls back
	// to its PowerShell path when the WinRT COM call fails, but Push() still
	// returns the (joined) COM error. Gating the sound on that error therefore
	// drops the audio cue spuriously. See docs/troubleshooting.md, which already
	// documents the "doc.LoadXml(tmpl)" error as a harmless false positive.
	n.playSoundDetached(sound)

	return err
}

func notifyViaBeeep(title, message, appIcon string) error {
	if isNotifierWindows() {
		// go-toast initializes WinRT COM for the current OS thread. Pin the call
		// so RoInitialize, XmlDocument creation, LoadXml, and Show execute on the
		// same initialized apartment.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	return beeepNotify(title, message, appIcon)
}

func isNotifierWindows() bool {
	return notifierGOOS == "windows"
}

func isWindowsToastFallbackSuccess(err error) bool {
	if err == nil || !isNotifierWindows() || !strings.Contains(err.Error(), "doc.LoadXml(tmpl)") {
		return false
	}

	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		return false
	}

	parts := joined.Unwrap()
	return len(parts) == 1 && strings.Contains(parts[0].Error(), "doc.LoadXml(tmpl)")
}

// playSoundDetached spawns a detached child process to play the sound.
// The parent hook process does not wait for audio to finish, eliminating
// the ~3.6s delay from notifier.Close() wg.Wait().
// Falls back to playSoundAsync (inline playback) if spawn fails.
func (n *Notifier) playSoundDetached(sound string) {
	if !n.cfg.Notifications.Desktop.Sound || sound == "" {
		return
	}

	if !platform.FileExists(sound) {
		logging.Warn("Sound file not found: %s", sound)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		logging.Warn("Cannot resolve executable for detached sound, falling back to inline: %v", err)
		n.playSoundAsync(sound)
		return
	}

	args := []string{"play-sound", sound}
	volume := n.cfg.Notifications.Desktop.Volume
	if volume >= 0 && volume < 1.0 {
		args = append(args, "--volume", fmt.Sprintf("%.2f", volume))
	}
	if device := n.cfg.Notifications.Desktop.AudioDevice; device != "" {
		args = append(args, "--device", device)
	}

	cmd := exec.Command(exe, args...)
	platform.SetDetachedProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		logging.Warn("Failed to spawn detached sound process, falling back to inline: %v", err)
		n.playSoundAsync(sound)
		return
	}

	logging.Debug("Detached sound process spawned: pid=%d sound=%s", cmd.Process.Pid, sound)
	// Do NOT call cmd.Wait() — child process runs independently
}

// playSoundAsync plays sound asynchronously if enabled (inline fallback)
func (n *Notifier) playSoundAsync(sound string) {
	if n.cfg.Notifications.Desktop.Sound && sound != "" {
		// Check if notifier is closing to prevent WaitGroup race
		n.mu.Lock()
		if n.closing {
			n.mu.Unlock()
			logging.Warn("Sound blocked: notifier is closing (notification may have been sent without sound)")
			return
		}
		n.wg.Add(1)
		n.mu.Unlock()

		// Use SafeGo to protect against panics in sound playback goroutine
		errorhandler.SafeGo(func() {
			defer n.wg.Done()
			n.playSound(sound)
		})
	}
}

// initPlayer initializes the audio player once
func (n *Notifier) initPlayer() error {
	n.playerInit.Do(func() {
		deviceName := n.cfg.Notifications.Desktop.AudioDevice
		volume := n.cfg.Notifications.Desktop.Volume

		player, err := audio.NewPlayer(deviceName, volume)
		if err != nil {
			n.playerErr = err
			logging.Error("Failed to initialize audio player: %v", err)
			return
		}

		n.audioPlayer = player

		if deviceName != "" {
			logging.Debug("Audio player initialized with device: %s, volume: %.0f%%", deviceName, volume*100)
		} else {
			logging.Debug("Audio player initialized with default device, volume: %.0f%%", volume*100)
		}
	})

	return n.playerErr
}

// playSound plays a sound file using the audio module
func (n *Notifier) playSound(soundPath string) {
	if !platform.FileExists(soundPath) {
		logging.Warn("Sound file not found: %s", soundPath)
		return
	}

	// Initialize player once
	if err := n.initPlayer(); err != nil {
		logging.Error("Failed to initialize audio player: %v", err)
		return
	}

	// Play sound
	if err := n.audioPlayer.Play(soundPath); err != nil {
		logging.Error("Failed to play sound %s: %v", soundPath, err)
		return
	}

	volume := n.cfg.Notifications.Desktop.Volume
	logging.Debug("Sound played successfully: %s (volume: %.0f%%)", soundPath, volume*100)
}

// Close waits for all sounds to finish playing and cleans up resources
func (n *Notifier) Close() error {
	// Set closing flag to prevent new sounds from being enqueued
	n.mu.Lock()
	n.closing = true
	n.mu.Unlock()

	// Wait for all sounds to finish
	n.wg.Wait()

	// Close audio player if it was initialized
	n.mu.Lock()
	if n.audioPlayer != nil {
		if err := n.audioPlayer.Close(); err != nil {
			logging.Warn("Failed to close audio player: %v", err)
		}
		n.audioPlayer = nil
		logging.Debug("Audio player closed")
	}
	n.mu.Unlock()

	return nil
}

// extractSessionInfo extracts session name and git branch from message
// Format: "[session-name|branch] message" or "[session-name] message"
// Returns session name, git branch (may be empty), and clean message
func extractSessionInfo(message string) (sessionName, gitBranch, cleanMessage string) {
	message = strings.TrimSpace(message)

	// Check if message starts with [
	if !strings.HasPrefix(message, "[") {
		return "", "", message
	}

	// Find closing bracket
	closingIdx := strings.Index(message, "]")
	if closingIdx == -1 {
		return "", "", message
	}

	// Extract content inside brackets
	bracketContent := message[1:closingIdx]

	// Check if there's a pipe separator for git branch
	if pipeIdx := strings.Index(bracketContent, "|"); pipeIdx != -1 {
		sessionName = bracketContent[:pipeIdx]
		gitBranch = bracketContent[pipeIdx+1:]
	} else {
		sessionName = bracketContent
		gitBranch = ""
	}

	// Extract clean message (everything after "] ")
	cleanMessage = strings.TrimSpace(message[closingIdx+1:])

	return sessionName, gitBranch, cleanMessage
}
