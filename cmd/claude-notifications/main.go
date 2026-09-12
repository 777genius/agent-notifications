package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/audio"
	"github.com/777genius/agent-notifications/internal/codexsource"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/hooks"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/winfocus"
)

var version = config.ConsumerVersion

const windowsLazyUpdateRetryAfter = time.Hour

var (
	currentGOOS               = runtime.GOOS
	scheduleWindowsLazyUpdate = scheduleWindowsLazyUpdateImpl
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "portable-launch" || os.Args[1] == "portable-primary") {
		os.Exit(agentPortableMain(os.Args[1], os.Args[2:]))
	}

	if len(os.Args) > 1 && (os.Args[1] == "portable-install" || os.Args[1] == "portable-remove") {
		os.Exit(agentPortableSetupMain(os.Args[1], os.Args[2:]))
	}

	if len(os.Args) > 1 && os.Args[1] == "setup-notifications" {
		os.Exit(agentNotifySetupMain(os.Args[2:]))
	}

	if len(os.Args) > 1 && (os.Args[1] == "notify" || os.Args[1] == "mcp-server") {
		os.Exit(agentNotifyMain(os.Args[1], os.Args[2:], notifyruntime.Options{}))
	}

	// Initialize global error handler with panic recovery.
	// logToConsole=true: errors will be shown in console
	// exitOnCritical=false: don't exit on critical errors (let caller decide)
	// recoveryEnabled=true: recover from panics
	//
	// errorhandler.Init is once-only, so the console decision must happen
	// here: the Codex observation route runs with console output disabled so
	// that handled errors and panics never reach the process stdout/stderr.
	errorhandler.Init(!codexRouteRequested(os.Args), false, true)

	// Add global panic recovery
	defer errorhandler.HandlePanic()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "config":
		os.Exit(configCommand(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	case "internal-writer-protocol":
		fmt.Println(installruntime.WriterProtocolMarker)
	case "internal-install-runtime":
		if err := installRuntime(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "handle-hook":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Error: hook event name required\n")
			printUsage()
			os.Exit(1)
		}
		runHandleHook(os.Args[2:])
	case "focus-window":
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "Error: focus-window requires bundleID and cwd arguments\n")
			os.Exit(1)
		}
		opts, err := parseFocusWindowOptions(os.Args[4:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "focus-window: %v\n", err)
			os.Exit(1)
		}
		if err := notifier.FocusAppWindowWithOptions(os.Args[2], os.Args[3], opts); err != nil {
			fmt.Fprintf(os.Stderr, "focus-window: %v\n", err)
			os.Exit(1)
		}
	case "focus-windows":
		runFocusWindows(os.Args[2:])
	case "play-sound":
		runPlaySound(os.Args[2:])
	case "daemon", "--daemon":
		runDaemon()
	case "windows-hooks":
		runWindowsHooks(os.Args[2:])
	case "setup-codex":
		runSetupCodex(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("%s v%s\n", invocationName(), version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

type hookSettings struct {
	Hooks map[string][]hookMatcherGroup `json:"hooks"`
}

type hookMatcherGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Timeout int      `json:"timeout"`
	Shell   string   `json:"shell,omitempty"`
}

func runWindowsHooks(args []string) {
	exePath, err := parseWindowsHooksExecutable(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "windows-hooks: %v\n", err)
		os.Exit(1)
	}

	settings := newWindowsHookSettings(exePath)

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "windows-hooks: failed to render JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(out))
}

func newWindowsHookSettings(exePath string) hookSettings {
	return hookSettings{
		Hooks: map[string][]hookMatcherGroup{
			"PreToolUse": {
				{
					Matcher: "ExitPlanMode|AskUserQuestion",
					Hooks:   []hookCommand{newExecHook(exePath, "PreToolUse")},
				},
			},
			"Notification": {
				{
					Matcher: "permission_prompt",
					Hooks:   []hookCommand{newExecHook(exePath, "Notification")},
				},
			},
			"Stop": {
				{
					Hooks: []hookCommand{newExecHook(exePath, "Stop")},
				},
			},
			"SubagentStop": {
				{
					Hooks: []hookCommand{newExecHook(exePath, "SubagentStop")},
				},
			},
			"TeammateIdle": {
				{
					Hooks: []hookCommand{newExecHook(exePath, "TeammateIdle")},
				},
			},
		},
	}
}

func parseWindowsHooksExecutable(args []string) (string, error) {
	exeOverride := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--exe":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--exe requires a path")
			}
			i++
			exeOverride = args[i]
		default:
			return "", fmt.Errorf("unknown windows-hooks option: %s", args[i])
		}
	}

	if exeOverride != "" {
		return filepath.Abs(exeOverride)
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to detect executable path: %w", err)
	}

	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve executable path: %w", err)
	}

	if strings.EqualFold(filepath.Ext(exePath), ".exe") {
		return exePath, nil
	}

	pluginRoot := getPluginRoot()
	return filepath.Abs(filepath.Join(pluginRoot, "bin", "claude-notifications-windows-amd64.exe"))
}

func newExecHook(exePath, hookName string) hookCommand {
	return hookCommand{
		Type:    "command",
		Command: exePath,
		Args:    []string{"handle-hook", hookName},
		Timeout: 30,
	}
}

// runHandleHook routes a handle-hook invocation. Without a --product flag it
// is the legacy Claude path, byte-for-byte compatible with previous releases
// (extra non-flag argv is ignored exactly as before). With --product the
// parser is strict and any anomaly follows the fail-open observation
// contract: file-log only, empty output, exit 0.
func runHandleHook(args []string) {
	// A --product flag in place of the event name means the configured
	// command lost its event argument; that is a codex-shaped invocation,
	// so it follows the fail-open observation contract, not the loud UX.
	if isProductFlagToken(args[0]) {
		codexFailOpen(fmt.Sprintf("missing hook event name before %q", args[0]))
		return
	}

	hookEvent := args[0]
	rest := args[1:]

	if !hasProductFlag(rest) {
		handleHook(hookEvent)
		return
	}

	rawProduct, err := parseProductArgs(rest)
	if err != nil {
		codexFailOpen(fmt.Sprintf("invalid handle-hook arguments %q: %v", rest, err))
		return
	}

	product, err := codexsource.ValidateProductOverride(rawProduct)
	if err != nil {
		codexFailOpen(fmt.Sprintf("invalid product override: %v", err))
		return
	}

	switch product {
	case "claude":
		// Explicit claude override keeps the legacy route.
		handleHook(hookEvent)
	case "codex":
		handleCodexHook(hookEvent)
	default:
		codexFailOpen(fmt.Sprintf("product %q has no route", product))
	}
}

func isProductFlagToken(arg string) bool {
	return arg == "--product" || strings.HasPrefix(arg, "--product=")
}

func hasProductFlag(args []string) bool {
	for _, a := range args {
		if isProductFlagToken(a) {
			return true
		}
	}
	return false
}

// parseProductArgs accepts exactly one --product flag with a value and
// nothing else. Duplicate flags, unknown flags, and missing values are
// errors so a misconfigured hook cannot silently misroute a payload.
func parseProductArgs(args []string) (string, error) {
	product := ""
	seen := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--product":
			if seen {
				return "", fmt.Errorf("duplicate --product flag")
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("--product requires a value")
			}
			product = args[i+1]
			seen = true
			i++
		case strings.HasPrefix(args[i], "--product="):
			if seen {
				return "", fmt.Errorf("duplicate --product flag")
			}
			product = strings.TrimPrefix(args[i], "--product=")
			seen = true
		default:
			return "", fmt.Errorf("unknown argument %q", args[i])
		}
	}
	if !seen || product == "" {
		return "", fmt.Errorf("--product requires a value")
	}
	return product, nil
}

// codexRouteRequested reports whether this invocation must run under the
// fail-open observation contract (console output disabled). Any handle-hook
// call with a --product flag that is not an explicit valid "claude" override
// qualifies, including unparsable flag shapes.
func codexRouteRequested(argv []string) bool {
	if len(argv) < 3 || argv[1] != "handle-hook" {
		return false
	}
	// Event name replaced by the flag: fail-open containment route.
	if isProductFlagToken(argv[2]) {
		return true
	}
	rest := argv[3:]
	if !hasProductFlag(rest) {
		return false
	}
	product, err := parseProductArgs(rest)
	if err != nil {
		return true
	}
	return product != "claude"
}

// codexFailOpen records a routing problem to the file log and returns so the
// process exits 0 with empty stdout/stderr: notification failures must never
// block the host agent.
func codexFailOpen(msg string) {
	pluginRoot := getPluginRootForProduct("codex")
	if _, err := logging.InitLogger(pluginRoot); err != nil {
		return
	}
	defer func() { _ = logging.Close() }()
	logging.SetPrefix(fmt.Sprintf("PID:%d", os.Getpid()))
	logging.Error("handle-hook (codex route): %s", msg)
}

// handleCodexHook is the Codex observation route. Every failure is contained:
// file-log only, empty process output, exit 0. It never calls os.Exit(1) and
// never lets SDK or config warnings reach stdout/stderr.
func handleCodexHook(publicEvent string) {
	// Console output is already disabled: main() initialized the error
	// handler via codexRouteRequested before any fallible work.
	defer errorhandler.HandlePanic()

	pluginRoot := getPluginRootForProduct("codex")

	if _, err := logging.InitLogger(pluginRoot); err != nil {
		// No log sink available; stay silent per the observation contract.
		return
	}
	defer func() { _ = logging.Close() }()
	logging.SetPrefix(fmt.Sprintf("PID:%d", os.Getpid()))

	if _, ok := codexsource.InvocationForEvent(publicEvent); !ok {
		logging.Error("codex: unsupported event %q", publicEvent)
		return
	}

	// Bounded read: the single wire limit plus one byte to detect overflow.
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, codexsource.MaxPayloadBytes+1))
	if err != nil {
		logging.Error("codex: failed to read stdin: %v", err)
		return
	}
	if len(raw) > codexsource.MaxPayloadBytes {
		logging.Error("codex: payload exceeds %d bytes", codexsource.MaxPayloadBytes)
		return
	}

	handler, err := hooks.NewHandlerWithSource(pluginRoot, hooks.ProductCodex, hooks.NewCodexSource())
	if err != nil {
		logging.Error("codex: failed to create handler: %v", err)
		return
	}

	if err := handler.HandleHook(publicEvent, bytes.NewReader(raw)); err != nil {
		logging.Error("codex: hook failed: %v", err)
	}
}

// getPluginRootForProduct resolves the plugin root per product. Codex
// natively exports PLUGIN_ROOT for plugin-bundled hooks; CLAUDE_PLUGIN_ROOT
// remains only a compatibility fallback via the legacy resolver.
func getPluginRootForProduct(product string) string {
	if product == "codex" {
		if root := os.Getenv("PLUGIN_ROOT"); root != "" {
			return root
		}
	}
	return getPluginRoot()
}

func handleHook(hookEvent string) {
	// Add panic recovery for this function
	defer errorhandler.HandlePanic()

	// Determine plugin root
	pluginRoot := getPluginRoot()
	maybeScheduleWindowsLazyUpdate(pluginRoot)

	// Initialize logger
	if _, err := logging.InitLogger(pluginRoot); err != nil {
		errorhandler.HandleCriticalError(err, "Failed to initialize logger")
		os.Exit(1)
	}
	defer func() { _ = logging.Close() }()

	// Create handler
	handler, err := hooks.NewHandler(pluginRoot)
	if err != nil {
		errorhandler.HandleCriticalError(err, "Failed to create handler")
		os.Exit(1)
	}

	// Handle hook
	if err := handler.HandleHook(hookEvent, os.Stdin); err != nil {
		errorhandler.HandleCriticalError(err, "Failed to handle hook")
		os.Exit(1)
	}
}

type pluginManifest struct {
	Version string `json:"version"`
}

func maybeScheduleWindowsLazyUpdate(pluginRoot string) {
	if currentGOOS != "windows" {
		return
	}

	pluginVersion := readPluginManifestVersion(pluginRoot)
	if pluginVersion == "" || pluginVersion == version {
		return
	}

	stampKey := version + "->" + pluginVersion
	stampPath := windowsLazyUpdateStampPath(pluginRoot)
	if windowsLazyUpdateRecentlyScheduled(stampPath, stampKey) {
		return
	}

	stampWritten := writeWindowsLazyUpdateStamp(stampPath, stampKey) == nil
	if err := scheduleWindowsLazyUpdate(pluginRoot); err != nil && stampWritten {
		_ = os.Remove(stampPath)
	}
}

func readPluginManifestVersion(pluginRoot string) string {
	data, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}

	var manifest pluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ""
	}
	return manifest.Version
}

func windowsLazyUpdateStampPath(pluginRoot string) string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		cacheDir = filepath.Join(pluginRoot, ".cache")
	}
	return filepath.Join(cacheDir, "claude-notifications-go", "windows-lazy-update-stamp")
}

func windowsLazyUpdateRecentlyScheduled(stampPath, stampKey string) bool {
	info, err := os.Stat(stampPath)
	if err != nil {
		return false
	}
	if time.Since(info.ModTime()) > windowsLazyUpdateRetryAfter {
		return false
	}

	data, err := os.ReadFile(stampPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == stampKey
}

func writeWindowsLazyUpdateStamp(stampPath, stampKey string) error {
	if err := os.MkdirAll(filepath.Dir(stampPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(stampPath, []byte(stampKey+"\n"), 0o600)
}

func scheduleWindowsLazyUpdateImpl(pluginRoot string) error {
	installScript := filepath.Join(pluginRoot, "bin", "install.sh")
	if err := requireCompatibleUpdateScript(installScript); err != nil {
		return err
	}

	powershellPath, err := findWindowsPowerShell()
	if err != nil {
		return err
	}

	bashPath, err := findWindowsBash()
	if err != nil {
		return err
	}

	targetDir := filepath.ToSlash(filepath.Join(pluginRoot, "bin"))
	installScript = filepath.ToSlash(installScript)
	shCommand := "INSTALL_TARGET_DIR=" + shellSingleQuoted(targetDir) + " " + shellSingleQuoted(installScript) + " --force"
	psCommand := windowsLazyUpdatePowerShellCommand(bashPath, shCommand)

	cmd := exec.Command(powershellPath, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", psCommand)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err == nil {
		cmd.Stdin = devNull
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}

	if err := cmd.Start(); err != nil {
		if devNull != nil {
			_ = devNull.Close()
		}
		return err
	}

	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	if devNull != nil {
		_ = devNull.Close()
	}
	return nil
}

func windowsLazyUpdatePowerShellCommand(bashPath, shCommand string) string {
	// Thread.Sleep remains reliable in the detached, NUL-backed Windows
	// PowerShell process where Start-Sleep can stall before launching bash.
	return "$ErrorActionPreference = 'SilentlyContinue'; " +
		"[System.Threading.Thread]::Sleep(750); " +
		"for ($i = 0; $i -lt 6; $i++) { " +
		"& " + powershellSingleQuoted(bashPath) + " -lc " + powershellSingleQuoted(shCommand) + "; " +
		"if ($LASTEXITCODE -eq 0) { break }; " +
		"[System.Threading.Thread]::Sleep(5000) }"
}

func findWindowsPowerShell() (string, error) {
	if path, err := exec.LookPath("powershell.exe"); err == nil {
		return path, nil
	}

	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("powershell.exe not found")
}

func findWindowsBash() (string, error) {
	if override := os.Getenv("CLAUDE_NOTIFICATIONS_BASH"); override != "" {
		if _, err := os.Stat(override); err == nil {
			return override, nil
		}
		return "", fmt.Errorf("CLAUDE_NOTIFICATIONS_BASH not found: %s", override)
	}

	for _, name := range []string{"bash.exe", "bash"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	for _, candidate := range windowsBashCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("bash.exe not found")
}

func windowsBashCandidates() []string {
	var candidates []string
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
		if root == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(root, "Git", "bin", "bash.exe"),
			filepath.Join(root, "Programs", "Git", "bin", "bash.exe"),
		)
	}
	return candidates
}

func shellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func powershellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func getPluginRoot() string {
	// Try CLAUDE_PLUGIN_ROOT environment variable first
	if root := os.Getenv("CLAUDE_PLUGIN_ROOT"); root != "" {
		return root
	}

	// Try to find plugin root relative to executable
	exe, err := os.Executable()
	if err == nil {
		// Executable is in bin/, so plugin root is parent directory
		exeDir := filepath.Dir(exe)
		if filepath.Base(exeDir) == "bin" {
			return filepath.Dir(exeDir)
		}
		// Otherwise, try parent of executable dir
		return filepath.Dir(exeDir)
	}

	// Fallback to current directory
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// runPlaySound plays a sound file and exits. Designed to be spawned as a detached
// child process so the parent hook process does not wait for audio to finish.
// Usage: play-sound <path> [--volume <0.0-1.0>] [--device <name>]
func runPlaySound(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "play-sound: sound file path required\n")
		os.Exit(1)
	}

	soundPath := args[0]
	volume := 1.0
	deviceName := ""

	// Parse optional flags
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--volume":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseFloat(args[i], 64); err == nil {
					volume = v
				}
			}
		case "--device":
			if i+1 < len(args) {
				i++
				deviceName = args[i]
			}
		}
	}

	player, err := audio.NewPlayer(deviceName, volume)
	if err != nil {
		fmt.Fprintf(os.Stderr, "play-sound: failed to init player: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = player.Close() }()

	if err := player.Play(soundPath); err != nil {
		fmt.Fprintf(os.Stderr, "play-sound: failed to play %s: %v\n", soundPath, err)
		os.Exit(1)
	}
}

// runFocusWindows handles a click on a Windows toast notification. Windows
// launches the registered claude-notify-focus: protocol with the encoded focus
// context as the single argument; we decode it and raise the terminal window.
func runFocusWindows(args []string) {
	// Clicking a toast's protocol-activation link launches this process with no
	// inherited console; Windows auto-creates and shows one for the duration of
	// this console-subsystem binary. Detach immediately to cut the visible
	// flash as short as possible.
	winfocus.HideConsole()

	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: focus-windows requires a focus URI argument")
		os.Exit(1)
	}

	// Best-effort logging to notification-debug.log to aid click diagnostics.
	if _, err := logging.InitLogger(getPluginRoot()); err == nil {
		defer func() { _ = logging.Close() }()
	}
	logging.Debug("focus-windows invoked: %s", args[0])

	ctx, err := winfocus.DecodeURI(args[0])
	if err != nil {
		logging.Warn("focus-windows: failed to decode URI: %v", err)
		fmt.Fprintf(os.Stderr, "focus-windows: %v\n", err)
		os.Exit(1)
	}
	if !ctx.HasTarget() {
		err := fmt.Errorf("focus-windows: empty focus context")
		logging.Warn("%v", err)
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	if err := winfocus.Focus(ctx); err != nil {
		logging.Warn("focus-windows: %v", err)
		fmt.Fprintf(os.Stderr, "focus-windows: %v\n", err)
		os.Exit(1)
	}
	logging.Debug("focus-windows: raised window for %+v", ctx)
}

func parseFocusWindowOptions(args []string) (notifier.FocusWindowOptions, error) {
	var opts notifier.FocusWindowOptions

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ghostty-terminal-id":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--ghostty-terminal-id requires a value")
			}
			i++
			opts.GhosttyTerminalID = args[i]
		default:
			return opts, fmt.Errorf("unknown focus-window option: %s", args[i])
		}
	}

	return opts, nil
}

func printUsage() {
	fmt.Println("agent-notifications - Smart notifications for Claude Code and Codex")
	fmt.Println()
	fmt.Printf("Version: %s\n", version)
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  agent-notifications handle-hook <HookName>")
	fmt.Println("  agent-notifications daemon")
	fmt.Println("  agent-notifications notify [--context-file PATH] [--help]")
	fmt.Println("  agent-notifications mcp-server --integration codex|claude [--help]")
	fmt.Println("  agent-notifications windows-hooks [--exe <path>]")
	fmt.Println("  agent-notifications version")
	fmt.Println("  agent-notifications config <path|inspect|init|edit|preflight-update>")
	fmt.Println("  agent-notifications setup-notifications [--help]")
	fmt.Println("  agent-notifications portable-install [--help]")
	fmt.Println("  agent-notifications portable-remove [--help]")
	fmt.Println("  agent-notifications setup-codex --plugin-root <bundle>")
	fmt.Println("  agent-notifications help")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  handle-hook <HookName>  Handle a Claude Code hook event")
	fmt.Println("                          HookName: PreToolUse, Stop, SubagentStop, Notification")
	fmt.Println("  daemon                  Run the notification daemon (Linux only)")
	fmt.Println("                          For click-to-focus support on desktop notifications")
	fmt.Println("  focus-window <bundleID> <cwd> [--ghostty-terminal-id <id>]")
	fmt.Println("                          Focus specific app window (internal, used by click-to-focus)")
	fmt.Println("  focus-windows <uri>     Raise the terminal window encoded in a click-to-focus URI")
	fmt.Println("                          (internal, Windows; invoked by the toast protocol handler)")
	fmt.Println("  windows-hooks           Print exec-form hook JSON for Windows settings")
	fmt.Println("                          Does not modify ~/.claude/settings.json")
	fmt.Println("  setup-codex             Register Codex CLI hooks (macOS, Linux, Windows)")
	fmt.Println("                          [--print] [--dry-run] [--codex-home <dir>] [--plugin-root <dir>]")
	fmt.Println("                          [--agent-notify|--skip-agent-notify] [--navigation none]")
	fmt.Println("                          [--allow-unknown-caller true|false --allow-caller-asserted true|false]")
	fmt.Println("  config                  Shared configuration path/inspect/init/edit/preflight-update")
	fmt.Println("  version                 Show version information")
	fmt.Println("  help                    Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  # Handle PreToolUse hook (reads JSON from stdin)")
	fmt.Println("  echo '{\"session_id\":\"test\",\"tool_name\":\"ExitPlanMode\"}' | agent-notifications handle-hook PreToolUse")
	fmt.Println()
	fmt.Println("  # Handle Stop hook")
	fmt.Println("  echo '{\"session_id\":\"test\",\"transcript_path\":\"/path/to/transcript.jsonl\"}' | agent-notifications handle-hook Stop")
	fmt.Println()
	fmt.Println("  # Run notification daemon (Linux only, started automatically)")
	fmt.Println("  agent-notifications daemon")
	fmt.Println()
	fmt.Println("  # Print Windows exec-form hook configuration")
	fmt.Println("  agent-notifications windows-hooks")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  AGENT_NOTIFICATIONS_ROOT  Resource bundle root for config placeholders")
	fmt.Println("  CLAUDE_PLUGIN_ROOT        Permanent resource-root alias; Claude hook root")
	fmt.Println()
}

// invocationName preserves the permanent legacy launcher's version identity.
func invocationName() string {
	name := strings.ToLower(filepath.Base(os.Args[0]))
	if name == "agent-notifications" || name == "agent-notifications.exe" || (strings.HasPrefix(name, "claude-notifications-windows-") && os.Getenv("AGENT_NOTIFICATIONS_LAUNCHER") == "agent-notifications") {
		return "agent-notifications"
	}
	return "claude-notifications"
}
