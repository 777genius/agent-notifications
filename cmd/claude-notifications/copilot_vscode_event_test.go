package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type localMainCase struct {
	name, input     string
	args            []string
	stalled, closed bool
}

// Red: main enters legacy logging/flags, leaks native/SDK text, blocks a Stop,
// fails to close/join stalled stdin, or exits abnormally on a closed output pipe.
func TestCopilotVSCodeBuiltMainProcess(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "TEST-notifications.exe")
	goBinary := os.Getenv("TEST_GO_BINARY")
	if goBinary == "" {
		goBinary = "go"
	}
	build := exec.Command(goBinary, "build", "-p", "2", "-o", bin, ".")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	binaryBytes, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("built main SHA256=%x", sha256.Sum256(binaryBytes))
	root := t.TempDir()
	base := []string{"copilot-vscode-event", "--event", "Stop", "--control-root", filepath.Join(root, "TEST-control"), "--binding", "TEST-binding"}
	frame := `{"hook_event_name":"Stop","timestamp":"2026-10-02T06:45:01Z","stop_hook_active":false,"session_id":"TEST-private-id","prompt":"TEST-private-prompt","transcript_path":"TEST-private-file"}`
	cases := []localMainCase{
		{name: "eligible-default-denied", input: frame, args: base},
		{name: "malformed", input: `{"TEST-private-error":`, args: base},
		{name: "mismatch", input: strings.Replace(frame, `"Stop"`, `"Notification"`, 1), args: base},
		{name: "absent-bool", input: strings.Replace(frame, `"stop_hook_active":false,`, "", 1), args: base},
		{name: "null-bool", input: strings.Replace(frame, `false`, `null`, 1), args: base},
		{name: "invalid-bool", input: strings.Replace(frame, `false`, `"false"`, 1), args: base},
		{name: "active", input: strings.Replace(frame, `false`, `true`, 1), args: base},
		{name: "oversized", input: strings.Repeat("x", (1<<20)+1), args: base},
		{name: "conflicting", args: append(append([]string(nil), base...), "--event", "SubagentStop"), stalled: true},
		{name: "relative", args: []string{"copilot-vscode-event", "--event", "Stop", "--control-root", "relative", "--binding", "TEST"}, stalled: true},
		{name: "subagent", input: strings.Replace(frame, `"Stop"`, `"SubagentStop"`, 1), args: base},
		{name: "duplicate", args: append(append([]string(nil), base...), "--event", "Stop"), stalled: true},
		{name: "unknown", args: append(append([]string(nil), base...), "--unknown", "TEST-private"), stalled: true},
		{name: "missing", args: base[:len(base)-2], stalled: true},
		{name: "trailing", args: append(append([]string(nil), base...), "extra"), stalled: true},
		{name: "stalled", args: base, stalled: true},
		{name: "closed-output", input: frame, args: base, closed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkLocalMainProcess(t, bin, tc)
		})
	}
	t.Run("early-licenses", func(t *testing.T) { checkLocalEarlyExit(t, bin, false) })
	t.Run("early-licenses-invalid", func(t *testing.T) { checkLocalEarlyExit(t, bin, true) })
}

// Red: extracting early dispatch loses licenses' return or its legacy exit 2,
// causing fallthrough into global handling or observation-neutral output.
func checkLocalEarlyExit(t *testing.T, bin string, invalid bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := []string{"licenses"}
	wantExit := 0
	if invalid {
		args = append(args, "TEST-extra")
		wantExit = 2
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	home := t.TempDir()
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "TMPDIR=" + home}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_, stopBudget := startLocalProcess(t, cmd, ctx, cancel, 2*time.Second)
	defer stopBudget()
	err := cmd.Wait()
	stopBudget()
	if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantExit || (wantExit == 0 && err != nil) {
		t.Fatalf("early exit changed: %v", err)
	}
	if invalid {
		if stdout.Len() != 0 || stderr.String() != "Usage: claude-notifications licenses\n" {
			t.Fatal("invalid licenses output changed")
		}
	} else if stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatal("licenses fell through")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("early dispatch initialized global state")
	}
	t.Logf("argv=%q exit=%d stdoutSHA256=%x stderrSHA256=%x", cmd.Args, cmd.ProcessState.ExitCode(), sha256.Sum256(stdout.Bytes()), sha256.Sum256(stderr.Bytes()))
}

func checkLocalMainProcess(t *testing.T, bin string, tc localMainCase) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, tc.args...)
	home := t.TempDir()
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "TMPDIR=" + home}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if tc.closed {
		setClosedLocalOutput(t, cmd)
	}
	start := time.Now()
	started, stopBudget := startLocalProcess(t, cmd, ctx, cancel, 3*time.Second)
	defer stopBudget()
	startExpired := ctx.Err() != nil
	var written int
	var writeErr, closeErr error
	if !tc.stalled {
		written, writeErr = io.WriteString(in, tc.input)
		closeErr = in.Close()
	}
	waitStarted := time.Now()
	err = cmd.Wait()
	executionElapsed := time.Since(started)
	stopBudget()
	t.Logf("argv=%q inputSHA256=%x elapsed=%s exit=%d deadline=%v stdoutSHA256=%x stderrSHA256=%x startElapsed=%s startExpired=%t stdinElapsed=%s stdinAttempted=%t stdinWritten=%d stdinWriteOK=%t stdinCloseOK=%t waitElapsed=%s", cmd.Args, sha256.Sum256([]byte(tc.input)), time.Since(start), cmd.ProcessState.ExitCode(), ctx.Err(), sha256.Sum256(stdout.Bytes()), sha256.Sum256(stderr.Bytes()), started.Sub(start), startExpired, waitStarted.Sub(started), !tc.stalled, written, !tc.stalled && writeErr == nil, !tc.stalled && closeErr == nil, time.Since(waitStarted))
	if tc.closed {
		if cmd.ProcessState.ExitCode() != 1 {
			t.Fatal("closed output did not fail", err)
		}
	} else if err != nil {
		t.Fatal("non-neutral exit", err)
	}
	if ctx.Err() != nil || executionElapsed > 2500*time.Millisecond || stderr.Len() != 0 || (!tc.closed && stdout.String() != "{}\n") {
		t.Fatalf("process contract failed: %q %q", stdout.String(), stderr.String())
	}
	if tc.stalled && tc.name != "stalled" && executionElapsed > 600*time.Millisecond {
		t.Fatal("invalid argv read stdin")
	}
	if tc.name == "stalled" && executionElapsed < 900*time.Millisecond {
		t.Fatal("stdin was never observed")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("public denied entry wrote effects/config/logs")
	}
}

// Cold Windows image admission can block Start before the child can execute.
// Bound that phase separately without spending the command's execution budget.
func startLocalProcess(t *testing.T, cmd *exec.Cmd, ctx context.Context, cancel context.CancelFunc, budget time.Duration) (time.Time, func()) {
	t.Helper()
	startup := time.AfterFunc(15*time.Second, cancel)
	err := cmd.Start()
	startupStopped := startup.Stop()
	started := time.Now()
	if !startupStopped || ctx.Err() != nil {
		cancel()
		if err == nil {
			_ = cmd.Wait()
		}
		t.Fatal("process startup exceeded its separate admission budget", err)
	}
	if err != nil {
		t.Fatal("process startup failed", err)
	}
	execution := time.AfterFunc(budget, cancel)
	return started, func() {
		if !execution.Stop() {
			cancel()
		}
	}
}

type panicLocalInput struct{ read, closed bool }

func (p *panicLocalInput) Read([]byte) (int, error) {
	p.read = true
	panic("TEST-private-read")
}
func (p *panicLocalInput) Close() error { p.closed = true; return nil }

// Panic injection is test-only at the owned reader boundary; the production
// executable has no environment hook or injected allow mechanism.
func TestCopilotVSCodeTransportPanicAndJoinedReader(t *testing.T) {
	if os.Getenv("TEST_LOCAL_PANIC_CHILD") == "1" {
		_, _ = io.WriteString(os.Stderr, "TEST-panic-reader-ready\n")
		input := &panicLocalInput{}
		code := runCopilotVSCodeEvent([]string{"--event", "Stop", "--control-root", os.Getenv("TEST_LOCAL_PANIC_ROOT"), "--binding", "TEST"}, input, os.Stdout)
		if !input.read || !input.closed {
			code = 1
		}
		os.Exit(code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCopilotVSCodeTransportPanicAndJoinedReader$")
	cmd.Env = append(os.Environ(), "TEST_LOCAL_PANIC_CHILD=1", "TEST_LOCAL_PANIC_ROOT="+t.TempDir(),
		"GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	admission := time.AfterFunc(15*time.Second, cancel)
	if err := cmd.Start(); err != nil {
		admission.Stop()
		t.Fatal(err)
	}
	// Keep stderr collection owned until EOF, including every handshake failure.
	type captured struct {
		marker, rest string
		err          error
	}
	capture := make(chan captured, 1)
	ready := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(stderr)
		marker, err := reader.ReadString('\n')
		ready <- marker
		rest, readErr := io.ReadAll(reader)
		if err == nil {
			err = readErr
		}
		capture <- captured{marker, string(rest), err}
	}()
	marker := <-ready
	admitted := admission.Stop() && ctx.Err() == nil && marker == "TEST-panic-reader-ready\n"
	if !admitted {
		cancel()
	}
	var execution *time.Timer
	if admitted {
		execution = time.AfterFunc(2*time.Second, cancel)
	}
	// Collect stderr before Wait closes its pipe; cancellation terminates child.
	observed := <-capture
	waitErr := cmd.Wait()
	if execution != nil && !execution.Stop() {
		cancel()
	}
	if !admitted || waitErr != nil || observed.err != nil || ctx.Err() != nil ||
		stdout.String() != "{}\n" || observed.rest != "" {
		t.Fatalf("panic transport readiness/collection failed: admitted=%v wait=%v read=%v marker=%q stderr=%q",
			admitted, waitErr, observed.err, observed.marker, observed.rest)
	}
}

func setClosedLocalOutput(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	_ = r.Close()
	t.Cleanup(func() { _ = w.Close() })
	cmd.Stdout = w
}
