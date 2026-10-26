package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/qoderinstall"
)

func runQoderSetup(args []string, output io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(output, "usage: setup-qoder install|update|remove|recover|inspect|status [flags]")
		return 2
	}
	action := qoderinstall.Action(args[0])
	r := qoderinstall.DefaultRequest(action)
	f := flag.NewFlagSet("setup-qoder "+args[0], flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&r.ControlRoot, "control-root", r.ControlRoot, "managed control root")
	f.StringVar(&r.RuntimeRoot, "runtime-root", r.RuntimeRoot, "managed shared runtime root")
	f.StringVar(&r.BinarySource, "binary", "", "verified final executable")
	f.StringVar(&r.NativeSource, "native-app", "", "verified macOS notification helper")
	f.StringVar(&r.HomeDir, "home", r.HomeDir, "home used for default Qoder profile")
	f.StringVar(&r.ConfigRoot, "config-root", r.ConfigRoot, "explicit Qoder profile directory")
	f.StringVar(&r.ConfigRoot, "qoder-config-dir", r.ConfigRoot, "explicit Qoder profile directory")
	f.StringVar(&r.QoderExecutable, "qoder-executable", "", "absolute native Qoder CLI executable")
	f.StringVar(&r.ChannelPreimage, "channel-preimage", "", "frozen channel preimage")
	f.BoolVar(&r.Desktop, "desktop", false, "opt in to desktop notifications")
	f.BoolVar(&r.Webhook, "webhook", false, "opt in to webhook notifications")
	if err := f.Parse(args[1:]); err != nil {
		return 2
	}
	if f.NArg() != 0 {
		fmt.Fprintln(output, "unexpected positional argument")
		return 2
	}
	channelsSet := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "desktop" || v.Name == "webhook" {
			channelsSet = true
		}
	})
	if (action == qoderinstall.Install || action == qoderinstall.Update) && !channelsSet {
		fmt.Fprintln(output, "choose --desktop or --webhook explicitly")
		return 2
	}
	if r.QoderExecutable == "" && action != "inspect" && action != "status" && action != qoderinstall.Recover && action != "permission-status" && action != "request-permission" {
		fmt.Fprintln(output, "absolute --qoder-executable required")
		return 2
	}
	if r.QoderExecutable != "" && !filepath.IsAbs(r.QoderExecutable) {
		fmt.Fprintln(output, "absolute --qoder-executable required")
		return 2
	}
	timeout := 90 * time.Second
	if action == "request-permission" {
		timeout = 130 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if action == "permission-status" || action == "request-permission" {
		permission, err := qoderinstall.SetupPermission(ctx, r.ControlRoot, action == "request-permission")
		if err != nil {
			fmt.Fprintf(output, "setup-qoder: permission unavailable: %v\n", err)
			return 1
		}
		fmt.Fprintf(output, "Qoder notification permission: %s\n", permission)
		return 0
	}
	if action == "inspect" || action == "status" {
		status, err := qoderinstall.Inspect(ctx, r)
		if json.NewEncoder(output).Encode(status) != nil || err != nil || status.Status == "recovery-required" {
			return 1
		}
		return 0
	}
	if err := qoderinstall.Apply(ctx, r); err != nil {
		fmt.Fprintf(output, "setup-qoder: %v\n", err)
		return 1
	}
	fmt.Fprintf(output, "Qoder notifications %s complete\n", action)
	return 0
}
func qoderSetupMain(args []string) int {
	if len(args) > 0 && (args[0] == "inspect" || args[0] == "status") {
		return runQoderSetup(args, os.Stdout)
	}
	return runQoderSetup(args, os.Stderr)
}
