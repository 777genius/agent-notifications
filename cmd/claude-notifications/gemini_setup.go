package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/777genius/agent-notifications/internal/geminiinstall"
)

func runGeminiSetup(args []string, output io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(output, "usage: setup-gemini install|update|remove|recover|inspect|status|permission-status|request-permission [flags]")
		return 2
	}
	action := geminiinstall.Action(args[0])
	r := geminiinstall.DefaultRequest(action)
	f := flag.NewFlagSet("setup-gemini "+args[0], flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&r.ControlRoot, "control-root", r.ControlRoot, "managed control root")
	f.StringVar(&r.RuntimeRoot, "runtime-root", r.RuntimeRoot, "managed runtime directory")
	f.StringVar(&r.BinarySource, "binary", r.BinarySource, "local executable for this platform to own")
	f.StringVar(&r.NativeSource, "native-app", r.NativeSource, "trusted macOS ClaudeNotifier.app source for desktop delivery")
	f.StringVar(&r.HomeDir, "home", r.HomeDir, "home used for default Gemini config")
	f.StringVar(&r.GeminiHome, "gemini-home", r.GeminiHome, "effective Gemini home parent")
	f.StringVar(&r.ConfigRoot, "config-root", r.ConfigRoot, "explicit Gemini config directory")
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
	if action == "inspect" || action == "status" {
		status, err := geminiinstall.Inspect(ctx, r)
		if encodeErr := json.NewEncoder(output).Encode(status); encodeErr != nil {
			return 1
		}
		if err != nil || status.Status == "recovery-required" {
			return 1
		}
		return 0
	}
	if action == "permission-status" || action == "request-permission" {
		permission, err := geminiinstall.SetupPermission(ctx, r.ControlRoot, action == "request-permission")
		if err != nil {
			_, _ = fmt.Fprintf(output, "setup-gemini: permission unavailable: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(output, "Gemini notification permission: %s\n", permission)
		return 0
	}
	if err := geminiinstall.Apply(ctx, r); err != nil {
		_, _ = fmt.Fprintf(output, "setup-gemini: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(output, "Gemini notifications %s complete\n", action); err != nil {
		return 1
	}
	return 0
}

func geminiSetupMain(args []string) int {
	if len(args) > 0 && (args[0] == "inspect" || args[0] == "status") {
		return runGeminiSetup(args, os.Stdout)
	}
	return runGeminiSetup(args, os.Stderr)
}
