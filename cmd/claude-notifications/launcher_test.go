package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestPrintUsageAdvertisesWizard(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	printUsage()
	os.Stdout = stdout
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("setup-notifications wizard")) {
		t.Fatalf("top-level help must advertise wizard for bootstrap: %s", body)
	}
}

func TestInvocationName(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	for _, tc := range []struct{ name, env, want string }{
		{"agent-notifications", "", "agent-notifications"},
		{"agent-notifications.exe", "", "agent-notifications"},
		{"claude-notifications", "", "claude-notifications"},
		{"claude-notifications", "agent-notifications", "claude-notifications"},
		{"claude-notifications-windows-amd64.exe", "agent-notifications", "agent-notifications"},
		{"claude-notifications-windows-amd64.exe", "claude-notifications", "claude-notifications"},
		{"claude-notifications-windows-arm64.exe", "", "claude-notifications"},
	} {
		os.Args = []string{tc.name}
		t.Setenv("AGENT_NOTIFICATIONS_LAUNCHER", tc.env)
		if got := invocationName(); got != tc.want {
			t.Errorf("%+v got %q", tc, got)
		}
	}
}

func TestBundlePrimaryLauncherPresent(t *testing.T) {
	// Git may materialize symlinks as plain files on Windows. Both checkout forms
	// must contain the same relative target; installation creates native launchers.
	path := "../../bin/agent-notifications"
	target, err := os.Readlink(path)
	if err != nil {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		target = string(raw)
	}
	if target != "claude-notifications" {
		t.Fatalf("unexpected bundle alias target %q", target)
	}
}
