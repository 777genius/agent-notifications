package main

import (
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"os/signal"
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
	if command == "portable-primary" {
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
	name, err := portable.ParseArgs(args)
	if err != nil {
		return 2
	}
	lease, err := portable.Acquire(ctx, os.Getenv("PLUGIN_DATA"), name)
	if err != nil {
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
	child := exec.CommandContext(ctx, lease.Executable, "portable-primary", "--locator", name, "--runtime-sha256", lease.SHA256)
	child.Env = []string{"PLUGIN_DATA=" + lease.Binding.DataRoot, "PLUGIN_ROOT=" + os.Getenv("PLUGIN_ROOT")}
	child.Dir = lease.Binding.RuntimeRoot
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	err = child.Start()
	lease.Release() // Primary revalidates; never retain component lock for stdio lifetime.
	if err != nil {
		return 2
	}
	if err = child.Wait(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return 2
	}
	return 0
}
