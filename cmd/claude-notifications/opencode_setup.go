package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/777genius/agent-notifications/internal/opencodeinstall"
	"github.com/777genius/agent-notifications/internal/opencodeplugin"
)

func runOpenCodeSetup(args []string, output io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(output, "usage: setup-opencode install|update|remove|recover|permission-status|request-permission [flags]")
		return 2
	}
	action := opencodeinstall.Action(args[0])
	r := opencodeinstall.DefaultRequest(action)
	r.Renderer = opencodeplugin.RegistrationRenderer{}
	f := flag.NewFlagSet("setup-opencode "+args[0], flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&r.ControlRoot, "control-root", r.ControlRoot, "managed control root")
	f.StringVar(&r.RuntimeRoot, "runtime-root", r.RuntimeRoot, "managed runtime directory")
	f.StringVar(&r.BinarySource, "binary", r.BinarySource, "local executable for this platform to own")
	f.StringVar(&r.NativeSource, "native-app", r.NativeSource, "trusted macOS ClaudeNotifier.app source for desktop delivery")
	f.StringVar(&r.HomeDir, "home", r.HomeDir, "home used for default OpenCode config")
	f.StringVar(&r.XDGConfigHome, "xdg-config-home", r.XDGConfigHome, "XDG config home")
	f.StringVar(&r.OpenCodeConfigDir, "opencode-config-dir", r.OpenCodeConfigDir, "OpenCode config directory override")
	f.BoolVar(&r.Desktop, "desktop", false, "opt in to desktop notifications")
	f.BoolVar(&r.Webhook, "webhook", false, "opt in to webhook notifications")
	if err := f.Parse(args[1:]); err != nil {
		return 2
	}
	if f.NArg() != 0 {
		_, _ = fmt.Fprintln(output, "unexpected positional argument")
		return 2
	}
	timeout := 30 * time.Second
	if action == "request-permission" {
		timeout = 130 * time.Second
	} else if r.GOOS == "darwin" && r.Desktop {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if action == "permission-status" || action == "request-permission" {
		permission, err := opencodeinstall.SetupPermission(ctx, r.ControlRoot, action == "request-permission")
		if err != nil {
			_, _ = fmt.Fprintf(output, "setup-opencode: permission unavailable: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(output, "OpenCode notification permission: %s\n", permission)
		return 0
	}
	if err := opencodeinstall.Apply(ctx, r); err != nil {
		_, _ = fmt.Fprintf(output, "setup-opencode: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(output, "OpenCode notifications %s complete\n", action); err != nil {
		return 1
	}
	return 0
}

func openCodeSetupMain(args []string) int { return runOpenCodeSetup(args, os.Stderr) }
