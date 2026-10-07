//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestWindowsSetupNotificationsEnableNoneWithoutNative(t *testing.T) {
	root := setupCommandRoot(t)
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	control := filepath.Join(root, "control")
	runtimeRoot := filepath.Join(root, "runtime")
	global := filepath.Join(root, "global.json")
	command := filepath.Join(runtimeRoot, "bin", "claude-notifications")
	_, e := installruntime.Commit(setupCommandContext(t), installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "hooks",
		Consumer: installruntime.Consumer{Commands: []string{command}},
		Files:    []installruntime.File{{Path: command, Data: []byte("#!/bin/sh\n# " + installruntime.WriterProtocolMarker + "\nexit 0\n"), Mode: 0700}},
	})
	if e != nil {
		t.Fatal(e)
	}
	setupCommandWrite(t, global, `{"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":false}}}`, 0600)
	c := agentNotifySetupComposition{
		globalConfigPath: func() (string, error) { return global, nil },
		setup: func(o *notifysetup.Options) {
			o.Platform = "windows"
			o.JournalClock = journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "fixture", Seconds: 100, Available: true} })
		},
	}
	s, e := installruntime.ReadInstalledSnapshot(control)
	if e != nil {
		t.Fatal(e)
	}
	args := []string{"enable", "--control-root", control, "--json", "--expected-generation", strconv.FormatUint(s.Ledger.Generation, 10), "--global-config", global, "--navigation", "none", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false"}
	r := setupCommandRun(t, setupCommandContext(t), args, c, 0)
	if !r.ExplicitIntent || !r.RuntimeEligible {
		t.Fatal(r)
	}
	if _, e := journal.Open(setupCommandContext(t), journal.Options{Root: filepath.Join(control, "state", "journal"), Clock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "fixture", Seconds: 100, Available: true} })}); e != nil {
		t.Fatal(e)
	}
}
