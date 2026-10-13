//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/creack/pty"
)

// Red regression: the real command retains the old whole-plan Title or asks
// twice/before Ready. Use production borrowed PTY handles, an isolated ready
// package/probe fixture and a fresh No after the complete final summary.
func TestWizardPublicLongScopeConsent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	t.Setenv("TERM", "dumb")
	t.Setenv("NO_COLOR", "1")
	prefix := filepath.Join(env.root, strings.Repeat("long-segment/", 40))
	env.claudeConfig = filepath.Join(prefix, "CLAUDE-END")
	env.codexHome = filepath.Join(prefix, "CODEX-END")
	for _, path := range []string{env.claudeConfig, env.codexHome} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	before, _, err := installruntime.ReadOwnership(env.control)
	if err != nil {
		t.Fatal(err)
	}
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("native PTY unavailable: %v", err)
	}
	defer func() { _ = slave.Close() }()
	defer func() { _ = master.Close() }()
	args := []string{"--action", "install", "--agents", "claude,codex", "--hooks", "false", "--agent-notify", "true",
		"--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime, "--global-config", env.global,
		"--claude-config", env.claudeConfig, "--codex-home", env.codexHome, "--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope}
	var data bytes.Buffer
	result := make(chan int, 1)
	returned := false
	defer func() {
		cancel()
		if !returned {
			_ = master.Close()
			select {
			case <-result:
			case <-time.After(2 * time.Second):
				t.Error("command did not join during cleanup")
			}
		}
	}()
	go func() { result <- executeSetupWizardWith(ctx, args, &data, slave, slave, true) }()
	transcript := make(chan string, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		var seen strings.Builder
		buffer := make([]byte, 2048)
		for seen.Len() < 128<<10 {
			n, e := master.Read(buffer)
			if n > 0 {
				seen.Write(buffer[:n])
			}
			if strings.Contains(seen.String(), "[y/N]") {
				transcript <- seen.String()
				return
			}
			if e != nil {
				transcript <- seen.String()
				return
			}
		}
		transcript <- seen.String()
	}()
	var shown string
	select {
	case shown = <-transcript:
	case <-ctx.Done():
		_ = master.Close()
		<-readDone
		t.Fatal("ready-plan prompt did not finish")
	}
	<-readDone
	for _, want := range []string{"CLAUDE-END", "CODEX-END", "installation-id=", "binding-id=", "claude: hooks=false MCP+skill=true", "codex: hooks=false MCP+skill=true"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("actual final summary omitted %q: %s", want, shown)
		}
	}
	if strings.Count(shown, "Apply this plan?") != 1 {
		t.Fatalf("final question count=%d", strings.Count(shown, "Apply this plan?"))
	}
	if _, err := master.Write([]byte("n\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-result:
		returned = true
		if code != 0 || !strings.Contains(data.String(), "cancelled") {
			t.Fatalf("No result=%d %s", code, data.String())
		}
	case <-ctx.Done():
		t.Fatal("fresh No did not return")
	}
	after, _, err := installruntime.ReadOwnership(env.control)
	if err != nil || after.Generation != before.Generation {
		t.Fatalf("No changed product ownership: %v", err)
	}
	if strings.Contains(data.String(), "\x1b") {
		t.Fatal("machine output contains ANSI")
	}
	if _, err := slave.Stat(); err != nil {
		t.Fatalf("borrowed terminal closed: %v", err)
	}
}
