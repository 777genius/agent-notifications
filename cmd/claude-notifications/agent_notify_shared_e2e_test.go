//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	"github.com/777genius/agent-notifications/internal/agentnotify/sharedrpc"
	"github.com/777genius/agent-notifications/internal/agentnotify/sharedstate"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// This checks startup/status scalability, not native delivery throughput. The
// fixture's production composition panics if a native delivery is constructed.
// A per-chat primary, broken election, stdout logging, bridge-owned daemon, or
// failure to reconnect an initialized bridge makes this subprocess test fail.
func TestPortableSharedDaemonLifecycle(t *testing.T) {
	executable := managedFixtureExecutable(t) // Build outside the scenario budget.
	counts := []int{32}
	if os.Getenv("AGENT_NOTIFY_SHARED_STRESS") == "1" {
		counts = append(counts, 64, 128)
	}
	for _, peers := range counts {
		t.Run(strconv.Itoa(peers), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			f := newPortableSharedFixture(t, ctx, executable)
			clients := make([]*portableSharedBridge, peers)
			results := make(chan error, peers)
			start := make(chan struct{})
			began := time.Now()
			for i := range clients {
				go func(i int) {
					<-start
					c, err := f.startBridge(ctx)
					clients[i] = c
					if err == nil {
						switch i {
						case 0:
							c.setCaller(nil)
						case 1:
							c.setCaller(42)
						default:
							c.setCaller(fmt.Sprintf("TEST-shared-thread-%d", i))
						}
						err = c.initialize()
					}
					if err == nil {
						err = c.status(false)
					}
					if err == nil {
						_, err = f.record()
					}
					results <- err
				}(i)
			}
			close(start)
			for range clients {
				if err := <-results; err != nil {
					t.Error(err)
				}
			}
			defer func() {
				for _, c := range clients {
					if c != nil {
						if err := c.close(); err != nil {
							t.Error(err)
						}
					}
				}
			}()
			if t.Failed() {
				return
			}
			first, err := f.record()
			if err != nil {
				t.Fatal(err)
			}
			if len(f.records) != 1 {
				t.Fatalf("concurrent bridges elected %d daemon PIDs, want one", len(f.records))
			}
			t.Logf("%d real stdio clients initialized, listed tools and read status in %s; one shared daemon", peers, time.Since(began))

			// Keep one SDK session initialized across daemon termination. Closing
			// the other chats must not tie daemon lifetime to any bridge.
			for _, c := range clients[1:] {
				if err := c.close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.authenticate(ctx, first); err != nil {
				t.Fatalf("daemon did not survive bridge closure: %v", err)
			}
			if err := f.stopOwnedDaemon(first); err != nil {
				t.Fatal(err)
			}
			clients[0].setCaller("TEST-distinct-thread-after-restart")
			if err := clients[0].status(false); err != nil {
				t.Fatalf("initialized bridge did not reconnect: %v", err)
			}
			second, err := f.record()
			if err != nil {
				t.Fatal(err)
			}
			if second.PID == first.PID || second.Token == first.Token {
				t.Fatal("restart did not publish a distinct daemon epoch")
			}
			if err := clients[0].close(); err != nil {
				t.Fatal(err)
			}
			if err := f.authenticate(ctx, second); err != nil {
				t.Fatalf("daemon did not survive all bridges closing: %v", err)
			}

			// A new chat reuses that daemon. Removing its private binding root
			// while it is open must fail closed without recreating installation,
			// journal, or endpoint state at the old pathname.
			last, err := f.startBridge(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := last.close(); err != nil {
					t.Error(err)
				}
			}()
			if err := last.initialize(); err != nil {
				t.Fatal(err)
			}
			if err := last.status(false); err != nil {
				t.Fatal(err)
			}
			reused, err := f.record()
			if err != nil || reused != second {
				t.Fatal("new bridge did not reuse the surviving daemon")
			}
			if err := os.RemoveAll(f.binding.DataRoot); err != nil {
				t.Fatal(err)
			}
			if err := last.status(true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(f.binding.DataRoot); !os.IsNotExist(err) {
				t.Fatal("missing binding data root was recreated")
			}
			for _, name := range []string{"journal", "native-spool"} {
				if _, err := os.Lstat(filepath.Join(f.binding.ControlRoot, "state", name)); !os.IsNotExist(err) {
					t.Fatal("read-only shared status created delivery state")
				}
			}
		})
	}
}

type portableSharedFixture struct {
	binding          portable.Binding
	executable, name string
	runtimeSHA       string
	mu               sync.Mutex
	store            *sharedstate.Store
	records          map[int]sharedstate.Record
}

func newPortableSharedFixture(t *testing.T, ctx context.Context, executable string) *portableSharedFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := portable.Binding{Version: 1, Integration: portable.Codex, InstallationID: "TEST-shared-install", BindingID: "TEST-shared-binding", ScopeID: "user", Owner: "existing-installer", ScopeRoot: filepath.Join(root, "scope"), DataRoot: filepath.Join(root, "data"), ControlRoot: filepath.Join(root, "control"), GlobalConfig: filepath.Join(root, "global", "config.json"), RuntimeRoot: filepath.Join(root, "runtime"), Primary: "primary"}
	for _, dir := range []string{b.ScopeRoot, b.DataRoot, filepath.Dir(b.GlobalConfig)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	image, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(b.RuntimeRoot, b.Primary)
	r := installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: "TEST-shared", Files: []installruntime.File{{Path: primary, Data: image, Mode: 0700}}}
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
	f := &portableSharedFixture{binding: b, executable: executable, name: name, runtimeSHA: ledger.Files[primary].SHA256, records: make(map[int]sharedstate.Record)}
	t.Cleanup(func() {
		// Capture a daemon published just before an assertion/bridge failed.
		_, _ = f.record()
		for _, record := range f.records {
			if err := f.stopOwnedDaemon(record); err != nil {
				t.Error(err)
			}
		}
	})
	return f
}

func (f *portableSharedFixture) record() (sharedstate.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.store == nil {
		key := strings.TrimSuffix(strings.TrimPrefix(f.name, "agent-notify-"), ".json")
		store, err := sharedstate.Open(f.binding.DataRoot, key, f.runtimeSHA)
		if err != nil {
			return sharedstate.Record{}, err
		}
		f.store = store
	}
	r, err := f.store.Read()
	if err == nil {
		f.records[r.PID] = r
	}
	return r, err
}

func (f *portableSharedFixture) authenticate(ctx context.Context, r sharedstate.Record) error {
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", r.Address)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	token, err := hex.DecodeString(r.Token)
	if err != nil {
		return fmt.Errorf("invalid daemon token encoding")
	}
	secured, err := sharedrpc.AuthenticateClient(ctx, conn, f.store.Identity(), token)
	if err != nil {
		return err
	}
	return secured.Close()
}

// PID is diagnostic in production. This test signals it only after correlating
// it with the exact executable in this test's unique private installed runtime
// and the full expected daemon invocation; a discovery record alone is unsafe.
func (f *portableSharedFixture) stopOwnedDaemon(r sharedstate.Record) error {
	want := strings.Join([]string{filepath.Join(f.binding.RuntimeRoot, f.binding.Primary), "portable-shared-daemon", "--locator", f.name, "--runtime-sha256", f.runtimeSHA}, " ")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspection, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(r.PID), "-o", "stat=", "-o", "command=").Output()
	if err != nil && len(inspection) == 0 {
		// ps exits 1 when the previously owned daemon has already exited.
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("inspect TEST daemon process: %w", err)
	}
	state, command, found := strings.Cut(strings.TrimSpace(string(inspection)), " ")
	if strings.HasPrefix(state, "Z") {
		return nil // Already exited; never signal an unreaped process.
	}
	if !found || strings.TrimSpace(command) != want {
		return fmt.Errorf("refuse to signal PID %d: process is not the exact TEST daemon", r.PID)
	}
	process, err := os.FindProcess(r.PID)
	if err != nil {
		return err
	}
	defer func() { _ = process.Release() }()
	if err := process.Signal(syscall.SIGTERM); err != nil && err != os.ErrProcessDone {
		return err
	}
	// Wait for the authenticated endpoint to close before exercising restart.
	for ctx.Err() == nil {
		conn, err := net.DialTimeout("tcp", r.Address, 100*time.Millisecond)
		if err != nil {
			return nil
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("TEST daemon did not stop within cleanup budget")
}

type portableSharedDiagnostics struct {
	sync.Mutex
	bytes.Buffer
}

func (d *portableSharedDiagnostics) Write(p []byte) (int, error) {
	d.Lock()
	defer d.Unlock()
	return d.Buffer.Write(p)
}

func (d *portableSharedDiagnostics) text() string {
	d.Lock()
	defer d.Unlock()
	return d.String()
}

type portableSharedBridge struct {
	cmd         *exec.Cmd
	input       io.WriteCloser
	frames      chan []byte
	readErr     error
	diagnostics portableSharedDiagnostics
	done        chan error
	closeOnce   sync.Once
	closeErr    error
	nextID      int
	statusMeta  map[string]any
	wantReason  string
	wantScope   string
}

func (f *portableSharedFixture) startBridge(ctx context.Context) (*portableSharedBridge, error) {
	c := &portableSharedBridge{done: make(chan error, 1), frames: make(chan []byte, 16)}
	c.setCaller(nil)
	c.cmd = exec.CommandContext(ctx, f.executable, "portable-launch", "--data-root", f.binding.DataRoot, "--locator", f.name)
	c.cmd.Dir = f.binding.ScopeRoot
	c.cmd.Env = []string{"PLUGIN_DATA=" + f.binding.DataRoot}
	c.cmd.Stderr = &c.diagnostics
	in, err := c.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	c.input = in
	out, err := c.cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	if err := c.cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	go func() {
		// Drain stdout before Wait closes StdoutPipe, including any unexpected
		// trailing log line. The scenario has at most five response frames.
		reader := bufio.NewReader(out)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) != 0 {
				select {
				case c.frames <- line:
				default:
					c.readErr = fmt.Errorf("excess stdout frames from TEST bridge")
				}
			}
			if err != nil {
				if err != io.EOF {
					c.readErr = err
				}
				break
			}
		}
		close(c.frames)
		c.done <- c.cmd.Wait()
	}()
	return c, nil
}

func (c *portableSharedBridge) exchange(method string, params any) (json.RawMessage, error) {
	c.nextID++
	request := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", c.nextID, method, params}
	if err := json.NewEncoder(c.input).Encode(request); err != nil {
		return nil, err
	}
	line, ok := <-c.frames
	if !ok {
		return nil, fmt.Errorf("%s response missing; stderr=%s", method, c.diagnostics.text())
	}
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(line, &response) != nil || response.JSONRPC != "2.0" || response.ID != c.nextID || len(response.Error) != 0 || len(response.Result) == 0 {
		return nil, fmt.Errorf("stdout is not expected MCP response to %s: %s", method, line)
	}
	return response.Result, nil
}

func (c *portableSharedBridge) initialize() error {
	if _, err := c.exchange("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "TEST-shared-stdio", "version": "1"}}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(c.input, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
		return err
	}
	raw, err := c.exchange("tools/list", map[string]any{})
	if err != nil {
		return err
	}
	var listed struct{ Tools []struct{ Name string } }
	if json.Unmarshal(raw, &listed) != nil || len(listed.Tools) != 2 {
		return fmt.Errorf("unexpected tool listing: %s", raw)
	}
	found := map[string]bool{}
	for _, tool := range listed.Tools {
		found[tool.Name] = true
	}
	if !found["notify"] || !found["notification_status"] {
		return fmt.Errorf("missing notification tools")
	}
	return nil
}

func (c *portableSharedBridge) setCaller(id any) {
	c.statusMeta = nil
	c.wantReason, c.wantScope = "session_required", "shared_anonymous"
	if id == nil {
		return
	}
	c.statusMeta = map[string]any{"threadId": id}
	if _, valid := id.(string); valid {
		c.wantReason, c.wantScope = "available", "session"
	} else {
		c.wantReason = "invalid_session"
	}
}

func (c *portableSharedBridge) status(wantError bool) error {
	params := map[string]any{"name": "notification_status", "arguments": map[string]any{}}
	if c.statusMeta != nil {
		params["_meta"] = c.statusMeta
	}
	raw, err := c.exchange("tools/call", params)
	if err != nil {
		return err
	}
	var result struct {
		IsError           bool `json:"isError"`
		StructuredContent struct {
			ContextReason string `json:"context_reason"`
			Reason        string `json:"reason"`
			DedupScope    string `json:"dedup_scope"`
			RateScope     string `json:"rate_scope"`
		} `json:"structuredContent"`
	}
	if json.Unmarshal(raw, &result) != nil || result.IsError != wantError {
		return fmt.Errorf("unexpected status result: %s", raw)
	}
	if wantError {
		if result.StructuredContent.Reason != "configuration_unavailable" {
			return fmt.Errorf("missing binding did not produce bounded configuration refusal: %s", raw)
		}
	} else if result.StructuredContent.ContextReason != c.wantReason || result.StructuredContent.DedupScope != c.wantScope || result.StructuredContent.RateScope != c.wantScope {
		return fmt.Errorf("unexpected call context: %s", raw)
	}
	return nil
}

func (c *portableSharedBridge) close() error {
	c.closeOnce.Do(func() {
		_ = c.input.Close()
		select {
		case err := <-c.done:
			if err != nil {
				c.closeErr = fmt.Errorf("TEST bridge exit: %w", err)
			}
		case <-time.After(5 * time.Second):
			_ = c.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case err := <-c.done:
				if err != nil {
					c.closeErr = fmt.Errorf("TEST bridge exit: %w", err)
				}
			case <-time.After(5 * time.Second):
				_ = c.cmd.Process.Kill()
				_ = <-c.done
				c.closeErr = fmt.Errorf("TEST bridge did not stop after stdin closure or SIGTERM")
			}
		}
		for tail := range c.frames {
			if len(bytes.TrimSpace(tail)) != 0 {
				c.closeErr = fmt.Errorf("unexpected trailing stdout from TEST bridge: %s", tail)
			}
		}
		if c.readErr != nil {
			c.closeErr = fmt.Errorf("TEST bridge stdout read: %w", c.readErr)
		}
		if diagnostic := c.diagnostics.text(); diagnostic != "" {
			c.closeErr = fmt.Errorf("unexpected TEST bridge diagnostics: %s", diagnostic)
		}
	})
	return c.closeErr
}
