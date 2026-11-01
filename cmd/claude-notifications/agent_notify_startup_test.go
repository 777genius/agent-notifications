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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Exercise the real two-process bridge against one shared installation, as
// Desktop does when several chats and plugin discovery start concurrently.
// The opt-in payload reproduces the approximately 230MB installed ledger that
// exposed redundant snapshot hashing; ordinary CI keeps the fixture small.
func TestPortableConcurrentStartup(t *testing.T) {
	executable := managedFixtureExecutable(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := portable.Binding{Version: 1, Integration: portable.Codex, InstallationID: "startup-test", BindingID: "startup-binding", ScopeID: "user", Owner: "existing-installer", ScopeRoot: filepath.Join(root, "scope"), DataRoot: filepath.Join(root, "data"), ControlRoot: filepath.Join(root, "control"), GlobalConfig: filepath.Join(root, "global", "config.json"), RuntimeRoot: filepath.Join(root, "runtime"), Primary: "primary"}
	for _, dir := range []string{b.ScopeRoot, b.DataRoot, filepath.Dir(b.GlobalConfig)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	r := installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: "startup", Files: []installruntime.File{{Path: filepath.Join(b.RuntimeRoot, b.Primary), Data: body, Mode: 0700}}}
	if os.Getenv("AGENT_NOTIFY_STARTUP_STRESS") == "1" {
		padding := make([]byte, 32<<20)
		for i := 0; i < 7; i++ {
			r.Files = append(r.Files, installruntime.File{Path: filepath.Join(b.RuntimeRoot, fmt.Sprintf("owned-asset-%d", i)), Data: padding, Mode: 0600})
		}
	}
	ledger, err := installruntime.Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	b.ComponentID = ledger.ID
	key, consumer, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	r.Files, r.ConsumerID, r.Consumer = nil, key, consumer
	if _, err := installruntime.Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	name, err := portable.Publish(b)
	if err != nil {
		t.Fatal(err)
	}
	const peers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < peers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			began := time.Now()
			if err := probePortableStartup(ctx, executable, b, name); err != nil {
				t.Errorf("parallel startup failed after %s: %v", time.Since(began), err)
			}
		}()
	}
	close(start)
	wg.Wait()
	for _, name := range []string{"journal", "native-spool"} {
		if _, err := os.Lstat(filepath.Join(b.ControlRoot, "state", name)); !os.IsNotExist(err) {
			t.Fatal("read-only startup created delivery state")
		}
	}
}

func probePortableStartup(ctx context.Context, executable string, b portable.Binding, name string) error {
	cmd := exec.CommandContext(ctx, executable, "portable-launch", "--data-root", b.DataRoot, "--locator", name)
	cmd.Dir = b.ScopeRoot
	cmd.Env = []string{"PLUGIN_DATA=" + b.DataRoot}
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return err
	}
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return err
	}
	defer func() { _ = input.Close(); _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() }()
	reader := bufio.NewReader(output)
	exchange := func(id int, method, params string) (json.RawMessage, error) {
		if _, err := fmt.Fprintf(input, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":%q,\"params\":%s}\n", id, method, params); err != nil {
			return nil, err
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// Reap before reading stderr, which exec writes concurrently.
			_ = cmd.Wait()
			return nil, fmt.Errorf("%s: %w; stderr=%s", method, err, diagnostic.String())
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &response); err != nil || response.ID != id || len(response.Error) != 0 || len(response.Result) == 0 {
			return nil, fmt.Errorf("invalid %s response: %s", method, line)
		}
		return response.Result, nil
	}
	if _, err := exchange(1, "initialize", `{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"TEST-startup","version":"1"}}`); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(input, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
		return err
	}
	raw, err := exchange(2, "tools/list", `{}`)
	if err != nil {
		return err
	}
	var listed struct{ Tools []struct{ Name string } }
	if err := json.Unmarshal(raw, &listed); err != nil {
		return err
	}
	found := map[string]bool{}
	for _, tool := range listed.Tools {
		found[tool.Name] = true
	}
	if len(listed.Tools) != 2 || !found["notification_status"] || !found["notify"] {
		return fmt.Errorf("missing notification tools: %s", raw)
	}
	raw, err = exchange(3, "tools/call", `{"name":"notification_status","arguments":{}}`)
	if err != nil {
		return err
	}
	var status struct {
		IsError           bool `json:"isError"`
		StructuredContent struct {
			ContextReason string `json:"context_reason"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(raw, &status); err != nil || status.IsError || status.StructuredContent.ContextReason != "session_required" {
		return fmt.Errorf("invalid read-only status: %s", raw)
	}
	return nil
}
