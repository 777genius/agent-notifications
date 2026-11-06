package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/777genius/agent-notifications/internal/claudesource"
	"github.com/777genius/agent-notifications/internal/codexsource"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/hooks"
	"github.com/777genius/agent-notifications/internal/logging"
)

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

	// Qoder's public hook API is supported by SDK 1.3, while its host
	// detector registry does not yet advertise Qoder. Select this explicit
	// application route without inferring a product from the environment.
	product := rawProduct
	if product != "qoder" {
		product, err = codexsource.ValidateProductOverride(rawProduct)
		if err != nil {
			codexFailOpen(fmt.Sprintf("invalid product override: %v", err))
			return
		}
	}

	switch product {
	case "claude":
		// Explicit claude override keeps the legacy route.
		handleHook(hookEvent)
	case "codex":
		handleCodexHook(hookEvent)
	case "qoder":
		handleQoderHook(hookEvent)
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
	if product == "qoder" {
		if root := os.Getenv("QODER_PLUGIN_ROOT"); root != "" {
			return root
		}
	}
	if product == "codex" || product == "qoder" {
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
	handler, err := hooks.NewHandlerWithClaudeSource(pluginRoot, claudesource.Source{})
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
