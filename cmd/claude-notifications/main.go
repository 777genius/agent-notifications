package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/thirdpartynotices"
)

var version = config.ConsumerVersion

const windowsLazyUpdateRetryAfter = time.Hour

var (
	currentGOOS               = runtime.GOOS
	scheduleWindowsLazyUpdate = scheduleWindowsLazyUpdateImpl
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "internal-linux-callback" || os.Args[1] == "setup-linux-callback") {
		os.Exit(linuxCallbackMain(os.Args[1], os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "opencode-runtime-profile" {
		os.Exit(runOpenCodeRuntimeProfile(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "opencode-clock" {
		os.Exit(runOpenCodeClock(os.Args[2:], os.Stdout))
	}
	if dispatchEarlyCommand() {
		return
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
		// Keep legacy output and retain every supported declaration in packaged bytes.
		if len(os.Args) == 3 && os.Args[2] == "--all" {
			fmt.Println(installruntime.LocalWriterProtocolMarker)
			fmt.Println(installruntime.OpenCodeWriterProtocolMarker)
			fmt.Println(installruntime.WindowsWriterProtocolMarker)
		}
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

// dispatchEarlyCommand preserves the observation routes before global logging.
func dispatchEarlyCommand() bool {
	if len(os.Args) > 1 && os.Args[1] == "pi-event" {
		os.Exit(runPiEvent(os.Args[2:], os.Stdin, os.Stdout, resolveInstalledPiConsumer))
	}
	if len(os.Args) > 1 && os.Args[1] == "installer-progress" {
		os.Exit(installerProgressMain(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "cursor-event" {
		os.Exit(runCursorEvent(os.Args[2:], os.Stdin, os.Stdout))
	}

	if len(os.Args) > 1 && os.Args[1] == "copilot-vscode-event" {
		os.Exit(runCopilotVSCodeEvent(os.Args[2:], os.Stdin, os.Stdout))
	}

	if len(os.Args) > 1 && os.Args[1] == "licenses" {
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "Usage: claude-notifications licenses")
			os.Exit(2)
		}
		fmt.Print(thirdpartynotices.Text())
		return true
	}
	if len(os.Args) > 1 && os.Args[1] == "gemini-event" {
		os.Exit(runGeminiEvent(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "setup-qoder" {
		os.Exit(qoderSetupMain(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "qoder-event" {
		runManagedQoderHook(os.Args[2:])
		return true
	}
	if len(os.Args) > 1 && os.Args[1] == "setup-gemini" {
		os.Exit(geminiSetupMain(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "setup-products" {
		os.Exit(setupProductsMain(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "opencode-event" {
		os.Exit(runOpenCodeEvent(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "setup-opencode" {
		os.Exit(openCodeSetupMain(os.Args[2:]))
	}
	if handled, code := dispatchManagedStdio(os.Args[1:], os.Stderr); handled {
		os.Exit(code)
	}

	if len(os.Args) > 1 && (os.Args[1] == "portable-launch" || os.Args[1] == "portable-primary" || os.Args[1] == "portable-shared-daemon") {
		os.Exit(agentPortableMain(os.Args[1], os.Args[2:]))
	}

	return false
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

func printUsage() {
	fmt.Println("agent-notifications - Smart notifications for Claude and Codex")
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
	fmt.Println("  agent-notifications setup-notifications wizard [--help]")
	fmt.Println("  agent-notifications portable-install [--help]")
	fmt.Println("  agent-notifications portable-remove [--help]")
	fmt.Println("  agent-notifications setup-codex --plugin-root <bundle>")
	fmt.Println("  agent-notifications help")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  handle-hook <HookName>  Handle a Claude hook event")
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
	fmt.Println("  setup-qoder             Install/update/remove/inspect/recover Qoder native plugin and channel consent")
	fmt.Println("  setup-gemini            Install/update/remove/inspect/recover Gemini user hooks and channel consent")
	fmt.Println("  setup-products          Select Claude/Codex/OpenCode/Gemini/Qoder through the setup terminal")
	fmt.Println("  setup-notifications     Direct MCP configure/status and setup-notifications wizard")
	fmt.Println("  config                  Shared configuration path/inspect/init/edit/preflight-update")
	fmt.Println("  version                 Show version information")
	fmt.Println("  licenses                Print terminal dependency copyright and license notices")
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
