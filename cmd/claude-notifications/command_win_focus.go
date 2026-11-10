package main

import (
	"fmt"
	"os"

	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/winfocus"
)

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
