package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsLazyUpdaterRejectsOldScriptBeforeDelegation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX inert process spies")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "executed")
	t.Setenv("ADAPTER_SPY", sentinel)
	spy := []byte("#!/bin/sh\nprintf executed >> \"$ADAPTER_SPY\"\n")
	for _, name := range []string{"install.sh", "powershell.exe", "bash.exe"} {
		if err := os.WriteFile(filepath.Join(bin, name), spy, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	err := scheduleWindowsLazyUpdateImpl(root)
	if err == nil || !strings.Contains(err.Error(), "protocol floor") {
		t.Fatalf("expected offline floor rejection, got %v", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("old writer/delegate executed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(bin, "install.sh"))
	if err != nil || string(data) != string(spy) {
		t.Fatal("script mutated")
	}
}

func TestDelayedLazyUpdateRechecksScriptAndUsesLiteralPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX inert process spies")
	}
	root := filepath.Join(t.TempDir(), "spaces ' $(touch INJECTED) ;")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "install.sh")
	sentinel := filepath.Join(root, "executed")
	t.Setenv("ADAPTER_SPY", sentinel)
	payload := []byte("#!/bin/sh\n# agent-notifications-managed-writer-protocol-v1\nprintf '%s\\n' \"$INSTALL_TARGET_DIR\" \"$1\" > \"$ADAPTER_SPY\"\n")
	if err := os.WriteFile(script, payload, 0700); err != nil {
		t.Fatal(err)
	}
	if err := requireCompatibleUpdateScript(script); err != nil {
		t.Fatal(err)
	}
	// Rollback after scheduling is rejected again at the actual exec boundary.
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \"$ADAPTER_SPY\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	run := func() error {
		cmd := exec.Command("sh", "-c", lazyUpdateShellCommand, "--", script, root)
		cmd.Dir = root
		return cmd.Run()
	}
	if err := run(); err == nil {
		t.Fatal("delayed rollback accepted")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("historical installer ran")
	}
	if err := os.WriteFile(script, payload, 0700); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != root+"\n--force\n" {
		t.Fatalf("literal arguments: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "INJECTED")); !os.IsNotExist(err) {
		t.Fatal("shell injection")
	}
}
