//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// The subprocess executes the production composition, never a substitute
// backend. A fixed clock permits Linux composition; native construction panics.
// This fixture sends read-only MCP status requests exclusively.
func init() {
	if len(os.Args) > 1 && (os.Args[1] == "portable-launch" || os.Args[1] == "portable-primary") {
		os.Exit(agentPortableRun(os.Args[1], os.Args[2:], notifyruntime.Options{BootClock: portableTestClock{}, DeliveryFactory: func(notifier.ManagedInstallation, string, notifier.BootClock) notifyruntime.Delivery {
			panic("unexpected native composition")
		}}))
	}
}
func TestPortableProductionBridge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	b := portable.Binding{Version: 1, Integration: portable.Codex, InstallationID: "fixture-install", BindingID: "fixture-binding", ScopeID: "user", Owner: "existing-installer", ScopeRoot: filepath.Join(root, "scope"), DataRoot: filepath.Join(root, "shared data"), ControlRoot: filepath.Join(root, "control"), GlobalConfig: filepath.Join(root, "global", "config.json"), RuntimeRoot: filepath.Join(root, "permanent runtime"), Primary: "primary"}
	pkg := filepath.Join(root, "package source with spaces")
	for _, p := range []string{b.ScopeRoot, b.DataRoot, filepath.Dir(b.GlobalConfig), pkg} {
		if e = os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	binary, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	launcher := filepath.Join(pkg, "launcher")
	if e = os.WriteFile(launcher, binary, 0700); e != nil {
		t.Fatal(e)
	}
	r := installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: "existing", Files: []installruntime.File{{Path: filepath.Join(b.RuntimeRoot, b.Primary), Data: binary, Mode: 0700}}}
	l, e := installruntime.Commit(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	b.ComponentID = l.ID
	r.Files = nil
	register := func(b portable.Binding) string {
		t.Helper()
		key, c, raw, e := b.Registration()
		if e != nil {
			t.Fatal(e)
		}
		r.ConsumerID = key
		r.Consumer = c
		if _, e = installruntime.Commit(ctx, r); e != nil {
			t.Fatal(e)
		}
		name, _ := b.Filename()
		if e = os.WriteFile(filepath.Join(b.DataRoot, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
		return name
	}
	codex := register(b)
	b.Integration = portable.Claude
	b.BindingID = "claude-binding"
	claude := register(b)
	// The installed primary refuses an old launcher image fingerprint, and a
	// package copy cannot impersonate the installed primary at a different path.
	for _, invalid := range []struct{ exe, hash string }{
		{filepath.Join(b.RuntimeRoot, b.Primary), strings.Repeat("0", 64)},
		{launcher, l.Files[filepath.Join(b.RuntimeRoot, b.Primary)].SHA256},
	} {
		cmd := exec.CommandContext(ctx, invalid.exe, "portable-primary", "--locator", codex, "--runtime-sha256", invalid.hash)
		cmd.Env = []string{"PLUGIN_ROOT=" + pkg, "PLUGIN_DATA=" + b.DataRoot}
		out, e := cmd.Output()
		if e == nil || len(out) != 0 {
			t.Fatal("unbound primary started protocol")
		}
	}

	for i, name := range []string{codex, claude} {
		command := launcher
		if i == 1 {
			command = filepath.Join(b.RuntimeRoot, b.Primary)
		}
		cmd := exec.CommandContext(ctx, command, "portable-launch", "--locator", name)
		cmd.Env = []string{"PLUGIN_ROOT=" + pkg, "PLUGIN_DATA=" + b.DataRoot}
		cmd.Dir = b.ScopeRoot
		in, e := cmd.StdinPipe()
		if e != nil {
			t.Fatal(e)
		}
		out, e := cmd.StdoutPipe()
		if e != nil {
			t.Fatal(e)
		}
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		// Reap on every assertion failure as well.
		done := false
		defer func() {
			if !done {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}()
		reader := bufio.NewReader(out)
		exchange := func(request string, wantID int) map[string]json.RawMessage {
			t.Helper()
			if _, e = fmt.Fprintln(in, request); e != nil {
				t.Fatal(e)
			}
			line, e := reader.ReadBytes('\n')
			if e != nil {
				t.Fatalf("protocol read: %v stderr=%s", e, diagnostics.String())
			}
			var response map[string]json.RawMessage
			if json.Unmarshal(line, &response) != nil || string(response["id"]) != fmt.Sprint(wantID) || response["error"] != nil || response["result"] == nil {
				t.Fatalf("stdout is not expected protocol: %s", line)
			}
			return response
		}
		exchange(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"foreign-client-label","version":"1"}}}`, 1)
		if _, e = fmt.Fprintln(in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); e != nil {
			t.Fatal(e)
		}
		got := exchange(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"notification_status","arguments":{}}}`, 2)
		if bytes.Contains(got["result"], []byte(`"isError":true`)) {
			t.Fatalf("status failed: %s", got["result"])
		}
		var status struct {
			StructuredContent struct {
				ContextReason string `json:"context_reason"`
			} `json:"structuredContent"`
		}
		if e = json.Unmarshal(got["result"], &status); e != nil {
			t.Fatal(e)
		}
		wantReason := "session_required"
		if i == 1 {
			wantReason = "session_unavailable"
		}
		if status.StructuredContent.ContextReason != wantReason {
			t.Fatalf("integration not from binding: %s", got["result"])
		}

		if i == 0 {
			if e = os.RemoveAll(pkg); e != nil {
				t.Fatal(e)
			}
		}
		exchange(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"notification_status","arguments":{}}}`, 3)
		if e = cmd.Process.Signal(syscall.SIGTERM); e != nil {
			t.Fatal(e)
		}
		_ = in.Close()
		_ = cmd.Wait()
		done = true
		if ctx.Err() != nil {
			t.Fatal("cancellation failed")
		}
		if diagnostics.Len() != 0 {
			t.Fatalf("unexpected diagnostics: %s", diagnostics.String())
		}
	}
	for _, p := range []string{"journal", "native-spool"} {
		if _, e = os.Stat(filepath.Join(b.ControlRoot, "state", p)); !os.IsNotExist(e) {
			t.Fatal("read-only bridge created delivery state")
		}
	}
	if e = os.RemoveAll(b.DataRoot); e != nil {
		t.Fatal(e)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(b.RuntimeRoot, b.Primary), "portable-launch", "--locator", claude)
	cmd.Env = []string{"PLUGIN_ROOT=" + pkg, "PLUGIN_DATA=" + b.DataRoot}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if e = cmd.Run(); e == nil || stdout.Len() != 0 {
		t.Fatal("missing locator did not fail silently before protocol")
	}
	if _, e = os.Stat(filepath.Join(b.RuntimeRoot, b.Primary)); e != nil {
		t.Fatal("cleanup relocated primary")
	}
}

type portableTestClock struct{}

func (portableTestClock) Now() (string, float64, error) { return "portable-test-boot", 100, nil }
