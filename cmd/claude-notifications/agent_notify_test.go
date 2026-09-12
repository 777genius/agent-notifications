//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"syscall"
	"testing"
	"time"

	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

type agentNotifyTestBoot struct{}

func (agentNotifyTestBoot) Now() (string, float64, error) {
	return "test-boot", float64(time.Now().UnixNano()) / 1e9, nil
}

// This seam exists only in the test executable. Native launch is forbidden even
// if a regression reaches it. Production main always constructs default Options.
func TestAgentNotifyProcessHelper(t *testing.T) {
	mode := os.Getenv("AGENT_NOTIFY_TEST_HELPER")
	if mode == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	if mode == "production" {
		os.Args = append([]string{"claude-notifications"}, args...)
		main()
		os.Exit(0)
	}
	options := notifyruntime.Options{BootClock: agentNotifyTestBoot{}, DeliveryFactory: func(notifier.ManagedInstallation, string, notifier.BootClock) notifyruntime.Delivery {
		panic("native effects forbidden")
	}}
	if mode == "leak-check" {
		baseline := runtime.NumGoroutine()
		for i := 0; i < 20; i++ {
			if agentNotifyExecute(context.Background(), args[0], args[1:], options) != 0 {
				os.Exit(10)
			}
		}
		until := time.Now().Add(time.Second)
		for runtime.NumGoroutine() > baseline && time.Now().Before(until) {
			time.Sleep(time.Millisecond)
		}
		if runtime.NumGoroutine() > baseline {
			os.Exit(11)
		}
		var stacks bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)
		if strings.Contains(stacks.String(), "agentNotifyExecute.func") || strings.Contains(stacks.String(), "agentnotify/mcp.(*connection)") {
			os.Exit(12)
		}
		os.Exit(0)
	}
	files := []*os.File{os.Stdin, os.Stdout, os.Stderr}
	before := make([]int, len(files))
	for i, f := range files {
		before[i] = agentNotifyFlagsOf(t, f)
	}
	code := agentNotifyMain(args[0], args[1:], options)
	for i, f := range files {
		if agentNotifyFlagsOf(t, f) != before[i] {
			os.Exit(90)
		}
	}
	os.Exit(code)
}
func agentNotifyTestEnv(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	env := []string{"PATH=/nonexistent", "AGENT_NOTIFY_TEST_HELPER=fake-native"}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "CODEX_HOME", "TMPDIR"} {
		p := filepath.Join(root, key)
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
		env = append(env, key+"="+p)
	}
	// Isolated child env drops the parent coverage directory. Without this,
	// a coverage-instrumented helper writes a GOCOVERDIR warning to stderr and
	// can block forever when stderr is a filled backpressure pipe.
	cover := filepath.Join(root, "GOCOVERDIR")
	if err := os.Mkdir(cover, 0700); err != nil {
		t.Fatal(err)
	}
	env = append(env, "GOCOVERDIR="+cover)
	return env
}
func agentNotifyTestCommand(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, exe, append([]string{"-test.run=^TestAgentNotifyProcessHelper$", "--"}, args...)...)
	cmd.Env = agentNotifyTestEnv(t)
	cmd.WaitDelay = time.Second
	return cmd
}
func TestAgentNotifyFlags(t *testing.T) {
	cases := []struct {
		cmd   string
		args  []string
		valid bool
	}{
		{"notify", nil, true}, {"notify", []string{"--help"}, true}, {"notify", []string{"--context-file", "/private/context", "--help"}, true},
		{"notify", []string{"--integration", "codex"}, false}, {"notify", []string{"--help", "--help"}, false}, {"notify", []string{"--context-file", strings.Repeat("x", 4097)}, false},
		{"mcp-server", nil, false}, {"mcp-server", []string{"--help"}, true}, {"mcp-server", []string{"--integration", "codex"}, true}, {"mcp-server", []string{"--integration", "claude"}, true},
		{"mcp-server", []string{"--integration=codex"}, false}, {"mcp-server", []string{"--integration", "evil\nsecret"}, false}, {"mcp-server", []string{"--context-file", "x"}, false}, {"mcp-server", []string{"--integration", "codex", "--integration", "claude"}, false},
	}
	for _, c := range cases {
		_, _, got := agentNotifyFlags(c.cmd, c.args)
		if got != c.valid {
			t.Fatalf("%s %q valid=%v", c.cmd, c.args, got)
		}
	}
}
func TestAgentNotifyStatusMapping(t *testing.T) {
	for _, intent := range []bool{false, true} {
		for _, desktop := range []bool{false, true} {
			s := agentNotifyStatusValue(notifyruntime.Status{ExplicitIntent: intent, DesktopEnabled: desktop, Configuration: "configured", OfflineCapability: "eligible", Permission: "not_checked"})
			if s.Enabled != (intent && desktop) || s.Configuration != "configured" || s.Capability != "eligible" || s.ContextReason != "" {
				t.Fatalf("%+v", s)
			}
		}
	}
}
func TestAgentNotifyProcessHelpInvalidEOF(t *testing.T) {
	cases := []struct {
		args                []string
		input, output, diag string
		code                int
		production          bool
	}{
		{[]string{"notify", "--help"}, "", "Usage: notify", "", 0, true},
		{[]string{"mcp-server", "--help"}, "", "Usage: mcp-server", "", 0, true},
		{[]string{"notify", "--payload", "SECRET\nINJECT"}, "", "", "invalid_flags\n", 2, true},
		{[]string{"mcp-server", "--integration", "SECRET\nINJECT"}, "", "", "invalid_flags\n", 2, true},
		{[]string{"notify"}, "{SECRET", "", "invalid_content_json\n", 2, false},
		{[]string{"notify"}, "", "", "invalid_content_json\n", 2, false},
		{[]string{"notify"}, `{"title":"T","body":"B","category":"info","navigation":"none"}`, `"status":"rejected"`, "", 1, false},
		{[]string{"mcp-server", "--integration", "codex"}, "", "", "", 0, false},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			cmd := agentNotifyTestCommand(t, c.args...)
			if c.production {
				cmd.Env = append(cmd.Env, "AGENT_NOTIFY_TEST_HELPER=production")
			}
			cmd.Stdin = strings.NewReader(c.input)
			var out, diag bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &diag
			err := cmd.Run()
			code := 0
			if err != nil {
				if e, ok := err.(*exec.ExitError); ok {
					code = e.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != c.code || (c.output == "" && out.Len() != 0) || !strings.Contains(out.String(), c.output) || diag.String() != c.diag {
				t.Fatalf("code=%d out=%q diag=%q", code, out.String(), diag.String())
			}
			// No setup, logger, journal, or native spool appeared under private roots.
			for _, entry := range cmd.Env {
				pair := strings.SplitN(entry, "=", 2)
				if pair[0] == "HOME" || strings.HasPrefix(pair[0], "XDG_") || pair[0] == "CODEX_HOME" {
					files, e := os.ReadDir(pair[1])
					if e != nil || len(files) != 0 {
						t.Fatalf("unexpected state: %s %v", pair[0], files)
					}
				}
			}
		})
	}
}
func TestAgentNotifyProcessSignalRead(t *testing.T) {
	for _, args := range [][]string{{"notify"}, {"mcp-server", "--integration", "codex"}} {
		t.Run(args[0], func(t *testing.T) {
			cmd := agentNotifyTestCommand(t, args...)
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			var out, diag bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &diag
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
			start := time.Now()
			_ = cmd.Process.Signal(syscall.SIGTERM)
			err = cmd.Wait()
			if time.Since(start) > 2*time.Second || out.Len() != 0 {
				t.Fatalf("shutdown %v stdout %q err %v", time.Since(start), out.String(), err)
			}
			if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 2 {
				t.Fatalf("did not join gracefully: %v diag %q", err, diag.String())
			}
		})
	}
}
func TestAgentNotifyOSPipeCloseUnblocks(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(fmt.Sprint(write), func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			defer w.Close()
			source := r
			if write {
				source = w
			}
			stream, err := agentNotifyFile(source)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close(); stream.release() }()
			done := make(chan error, 1)
			go func() {
				var err error
				if write {
					_, err = stream.Write(make([]byte, 4<<20))
				} else {
					_, err = stream.Read(make([]byte, 1))
				}
				done <- err
			}()
			time.Sleep(30 * time.Millisecond)
			if err = stream.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				if err == nil {
					t.Fatal("blocking operation succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("Close did not unblock OS pipe")
			}
		})
	}
}
func TestAgentNotifyProcessMCP(t *testing.T) {
	cmd := agentNotifyTestCommand(t, "mcp-server", "--integration", "codex")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diag bytes.Buffer
	cmd.Stderr = &diag
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(out)
	exchange := func(frame string) map[string]any {
		t.Helper()
		if _, e := io.WriteString(in, frame+"\n"); e != nil {
			t.Fatal(e)
		}
		raw, e := reader.ReadBytes('\n')
		if e != nil {
			t.Fatalf("read: %v", e)
		}
		var v map[string]any
		if json.Unmarshal(raw, &v) != nil {
			t.Fatalf("stdout contamination %q", raw)
		}
		return v
	}
	exchange(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	_, _ = io.WriteString(in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	response := exchange(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"notification_status","arguments":{},"_meta":{"threadId":"test"}}}`)
	raw, _ := json.Marshal(response)
	if !bytes.Contains(raw, []byte(`"enabled":false`)) || !bytes.Contains(raw, []byte(`"context_reason":"available"`)) {
		t.Fatalf("status %s", raw)
	}
	_ = in.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatalf("EOF %v %q", err, diag.String())
	}
	if diag.Len() != 0 {
		t.Fatalf("diagnostics %q", diag.String())
	}
}

// Fill the actual inherited stdout pipe before starting: help must terminate
// without reading stdin even when no reader drains its output.
func TestAgentNotifyProcessBlockedHelp(t *testing.T) {
	cmd := agentNotifyTestCommand(t, "notify", "--help")
	cmd.Env = append(cmd.Env, "AGENT_NOTIFY_TEST_HELPER=production")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if err = agentNotifyFillPipe(w); err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = w
	var diag bytes.Buffer
	cmd.Stderr = &diag
	start := time.Now()
	err = cmd.Run()
	if time.Since(start) > 3*time.Second || err == nil || diag.String() != "output_failed\n" {
		t.Fatalf("blocked help duration %v err %v diag %q", time.Since(start), err, diag.String())
	}
}

func TestAgentNotifyProcessBlockedMCP(t *testing.T) {
	for _, finish := range []string{"signal", "EOF", "deadline"} {
		t.Run(finish, func(t *testing.T) {
			cmd := agentNotifyTestCommand(t, "mcp-server", "--integration", "codex")
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			defer w.Close()
			if err = agentNotifyFillPipe(w); err != nil {
				t.Fatal(err)
			}
			cmd.Stdout = w
			var diag bytes.Buffer
			cmd.Stderr = &diag
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_, err = io.WriteString(in, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`+"\n")
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
			start := time.Now()
			switch finish {
			case "signal":
				_ = cmd.Process.Signal(syscall.SIGTERM)
			case "EOF":
				_ = in.Close()
			}
			err = cmd.Wait()
			if time.Since(start) > 3*time.Second {
				t.Fatalf("blocked output did not join: %v %v", time.Since(start), err)
			}
			if err != nil {
				e, ok := err.(*exec.ExitError)
				if !ok || e.ExitCode() != 2 {
					t.Fatalf("shutdown failed: %v", err)
				}
			}
			if diag.Len() != 0 && diag.String() != "connection_closed\n" {
				t.Fatalf("unsafe diagnostic %q", diag.String())
			}
		})
	}
}

func TestAgentNotifyOSPipeWriteDeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	stream, err := agentNotifyFile(w)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close(); stream.release() }()
	start := time.Now()
	_, err = stream.Write(make([]byte, 4<<20))
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("write deadline %v %v", time.Since(start), err)
	}
}

func TestAgentNotifyNoWorkersAfterReturn(t *testing.T) {
	cmd := agentNotifyTestCommand(t, "mcp-server", "--integration", "codex")
	cmd.Env = append(cmd.Env, "AGENT_NOTIFY_TEST_HELPER=leak-check")
	cmd.Stdin = strings.NewReader("")
	var out, diag bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diag
	if err := cmd.Run(); err != nil || out.Len() != 0 || diag.Len() != 0 {
		t.Fatalf("leaked work: %v out %q diag %q", err, out.String(), diag.String())
	}
}
