//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

func probePortableStartup(ctx context.Context, executable string, b portable.Binding, name string) (resultErr error) {
	cmd := exec.CommandContext(ctx, executable, "portable-launch", "--data-root", b.DataRoot, "--locator", name)
	cmd.Dir = b.ScopeRoot
	cmd.Env = []string{"PLUGIN_DATA=" + b.DataRoot}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
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
	// A legacy primary can inherit both output pipes. Killing only its launcher
	// leaves ReadBytes blocked, so cancel the owned group and close our pipe ends.
	terminate := func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = input.Close()
		_ = output.Close()
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = terminate
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return err
	}
	defer func() {
		_ = terminate()
		_ = cmd.Wait() // Sole Wait owner; joins the stderr writer before reading it.
		if resultErr != nil {
			if ctx.Err() != nil {
				resultErr = fmt.Errorf("%w: %v", ctx.Err(), resultErr)
			}
			resultErr = fmt.Errorf("%w; stderr=%s", resultErr, diagnostic.String())
		}
	}()
	reader := bufio.NewReader(output)
	exchange := func(id int, method, params string) (json.RawMessage, error) {
		if _, err := fmt.Fprintf(input, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":%q,\"params\":%s}\n", id, method, params); err != nil {
			return nil, err
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
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

func TestPortableStartupCancellationClosesInheritedPipes(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "TEST-silent-launcher")
	// The silent child inherits stdout AND stderr. CommandContext's default
	// launcher-only kill cannot unblock the probe's synchronous protocol read.
	script := "#!/bin/sh\n/bin/sleep 60 &\nchild=$!\nprintf '%s %s\\n' \"$$\" \"$child\" > \"$PLUGIN_DATA/ready\"\nwait \"$child\"\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- probePortableStartup(ctx, executable, portable.Binding{ScopeRoot: root, DataRoot: root}, "TEST-locator")
	}()
	var launcher, child int
	var childGroup int
	joined := false
	defer func() {
		cancel()
		// These PIDs come solely from the fresh owned script. Before Wait has
		// returned, clean its group or its one child even when testing old code.
		if !joined && launcher > 0 {
			if group, err := syscall.Getpgid(launcher); err == nil && group == launcher {
				_ = syscall.Kill(-launcher, syscall.SIGKILL)
			}
		}
		if child > 0 && childGroup > 0 {
			// Also clean a survivor if a regression closes pipes without killing
			// descendants. Verify its recorded group and exact fixture command.
			group, err := syscall.Getpgid(child)
			command, inspectErr := exec.Command("ps", "-p", strconv.Itoa(child), "-o", "command=").Output()
			if err == nil && group == childGroup && inspectErr == nil && strings.TrimSpace(string(command)) == "/bin/sleep 60" {
				_ = syscall.Kill(child, syscall.SIGKILL)
			}
		}
		if !joined {
			select {
			case err := <-done:
				if err == nil {
					t.Error("canceled TEST startup probe unexpectedly succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Error("TEST startup probe did not join after fixture cleanup")
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(root, "ready"))
		if err == nil {
			if _, err := fmt.Sscanf(string(raw), "%d %d", &launcher, &child); err == nil && launcher > 0 && child > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if launcher <= 0 || child <= 0 {
		t.Fatal("TEST silent child did not publish readiness")
	}
	childGroup, err := syscall.Getpgid(child)
	if err != nil {
		t.Fatalf("inspect ready TEST child group: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled startup returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup probe hung on a silent child's inherited pipes after cancellation")
	}
	for _, pid := range []int{launcher, child} {
		deadline := time.Now().Add(time.Second)
		for {
			out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
				break
			}
			if err != nil {
				t.Fatalf("inspect TEST process: %v", err)
			}
			if strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
				break // An unreaped orphan has exited and holds no inherited pipes.
			}
			if time.Now().After(deadline) {
				t.Fatalf("owned TEST process %d remained running after cancellation", pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
