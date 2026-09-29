package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/777genius/agent-notifications/internal/opencodeinstall"
)

func runOpenCodeSetup(args []string, output io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(output, "usage: setup-opencode install|update|remove|recover [flags]")
		return 2
	}
	action := opencodeinstall.Action(args[0])
	r := opencodeinstall.DefaultRequest(action)
	f := flag.NewFlagSet("setup-opencode "+args[0], flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&r.ControlRoot, "control-root", r.ControlRoot, "managed control root")
	f.StringVar(&r.RuntimeRoot, "runtime-root", r.RuntimeRoot, "managed runtime directory")
	f.StringVar(&r.BinarySource, "binary", r.BinarySource, "local Linux amd64 executable to own")
	f.StringVar(&r.HomeDir, "home", r.HomeDir, "home used for default OpenCode config")
	f.StringVar(&r.XDGConfigHome, "xdg-config-home", r.XDGConfigHome, "XDG config home")
	f.StringVar(&r.OpenCodeConfigDir, "opencode-config-dir", r.OpenCodeConfigDir, "OpenCode config directory override")
	f.BoolVar(&r.Desktop, "desktop", false, "opt in to desktop notifications")
	f.BoolVar(&r.Webhook, "webhook", false, "opt in to webhook notifications")
	if err := f.Parse(args[1:]); err != nil {
		return 2
	}
	if f.NArg() != 0 {
		fmt.Fprintln(output, "unexpected positional argument")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := opencodeinstall.Apply(ctx, r); err != nil {
		fmt.Fprintf(output, "setup-opencode: %v\n", err)
		return 1
	}
	fmt.Fprintf(output, "OpenCode notifications %s complete\n", action)
	return 0
}

func openCodeSetupMain(args []string) int { return runOpenCodeSetup(args, os.Stderr) }
