package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Portable dispatch precedes legacy initialization and never writes stdout.
func agentPortableMain(command string, args []string) int {
	return agentPortableRun(command, args, notifyruntime.Options{})
}
func agentPortableRun(command string, args []string, options notifyruntime.Options) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var expectedHash string
	if command == "portable-primary" || command == "portable-shared-daemon" {
		if len(args) != 4 || args[2] != "--runtime-sha256" || len(args[3]) != 64 {
			return 2
		}
		if decoded, e := hex.DecodeString(args[3]); e != nil || len(decoded) != 32 {
			return 2
		}
		expectedHash = args[3]
		args = args[:2]
	} else if command != "portable-launch" {
		return 2
	}
	dataRoot := os.Getenv("PLUGIN_DATA")
	if command == "portable-launch" && len(args) > 0 && args[0] == "--data-root" {
		if len(args) != 4 || !filepath.IsAbs(args[1]) || filepath.Clean(args[1]) != args[1] {
			return 2
		}
		dataRoot = args[1]
		args = args[2:]
	}
	name, err := portable.ParseArgs(args)
	if err != nil {
		return 2
	}
	if command == "portable-launch" {
		return agentPortableBridge(ctx, dataRoot, name, options)
	}
	if command == "portable-shared-daemon" {
		discovery, err := portable.Discover(ctx, dataRoot, name)
		if err != nil || discovery.SHA256 != expectedHash {
			return 2
		}
		return agentPortableSharedDaemon(ctx, discovery.Binding, name, expectedHash, options)
	}
	lease, err := portable.Acquire(ctx, dataRoot, name)
	if err != nil {
		reason := "portable_binding_invalid"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "installation_lease_timeout"
		} else if errors.Is(err, context.Canceled) {
			reason = "startup_canceled"
		}
		// stdout belongs to MCP. Keep diagnostics bounded and never expose raw
		// errors, locator content, environment, or filesystem paths.
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s\n", command, reason)
		return 2
	}
	if command == "portable-primary" {
		current, err := os.Executable()
		if err != nil || !portable.SameInstalledFile(current, lease.Executable) || expectedHash != lease.SHA256 {
			lease.Release()
			return 2
		}
		b := lease.Binding
		lease.Release()
		options.ControlRoot = b.ControlRoot
		options.GlobalConfig = b.GlobalConfig
		options.JournalRoot = ""
		options.SpoolRoot = ""
		options.ReadSnapshot = func(c context.Context, root string) (installruntime.PolicySnapshot, error) {
			snap, e := installruntime.ReadPolicySnapshot(c, root)
			if e == nil {
				e = b.CheckSnapshot(snap.Installation)
			}
			return snap, e
		}
		return agentNotifyExecute(ctx, "mcp-server", []string{"--integration", string(b.Integration)}, options)
	}
	lease.Release()
	return 2
}

func portableChildEnvironment(dataRoot, pluginRoot string) []string {
	return append(os.Environ(), "PLUGIN_DATA="+dataRoot, "PLUGIN_ROOT="+pluginRoot)
}
