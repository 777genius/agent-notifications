package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/cursorevent"
	"github.com/777genius/agent-notifications/internal/cursorsource"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/testenv"
	"github.com/777genius/plugin-kit-ai/sdk/cursor"
)

func TestReadPluginManifestVersion(t *testing.T) {
	pluginRoot := t.TempDir()
	manifestDir := filepath.Join(pluginRoot, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(`{"version":"9.99.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := readPluginManifestVersion(pluginRoot); got != "9.99.0" {
		t.Fatalf("readPluginManifestVersion() = %q, want %q", got, "9.99.0")
	}
}

func TestMaybeScheduleWindowsLazyUpdateOnMismatch(t *testing.T) {
	pluginRoot := setupLazyUpdateTestPlugin(t, "9.99.0")
	withIsolatedLazyUpdateGlobals(t)

	var called int
	var gotRoot string
	scheduleWindowsLazyUpdate = func(root string) error {
		called++
		gotRoot = root
		return nil
	}

	maybeScheduleWindowsLazyUpdate(pluginRoot)
	maybeScheduleWindowsLazyUpdate(pluginRoot)

	if called != 1 {
		t.Fatalf("scheduleWindowsLazyUpdate called %d times, want 1", called)
	}
	if gotRoot != pluginRoot {
		t.Fatalf("scheduleWindowsLazyUpdate root = %q, want %q", gotRoot, pluginRoot)
	}
}

func TestMaybeScheduleWindowsLazyUpdateSkipsMatchingVersion(t *testing.T) {
	pluginRoot := setupLazyUpdateTestPlugin(t, version)
	withIsolatedLazyUpdateGlobals(t)

	scheduleWindowsLazyUpdate = func(root string) error {
		t.Fatalf("scheduleWindowsLazyUpdate called for matching version at %s", root)
		return nil
	}

	maybeScheduleWindowsLazyUpdate(pluginRoot)
}

func TestMaybeScheduleWindowsLazyUpdateRetriesAfterScheduleFailure(t *testing.T) {
	pluginRoot := setupLazyUpdateTestPlugin(t, "9.99.0")
	withIsolatedLazyUpdateGlobals(t)

	var called int
	scheduleWindowsLazyUpdate = func(root string) error {
		called++
		return errors.New("boom")
	}

	maybeScheduleWindowsLazyUpdate(pluginRoot)
	maybeScheduleWindowsLazyUpdate(pluginRoot)

	if called != 2 {
		t.Fatalf("scheduleWindowsLazyUpdate called %d times, want 2", called)
	}
}

func TestLazyUpdateQuoting(t *testing.T) {
	if got, want := shellSingleQuoted(`C:/Users/O'Brien/bin/install.sh`), `'C:/Users/O'"'"'Brien/bin/install.sh'`; got != want {
		t.Fatalf("shellSingleQuoted() = %q, want %q", got, want)
	}
	if got, want := powershellSingleQuoted(`C:\Users\O'Brien\bash.exe`), `'C:\Users\O''Brien\bash.exe'`; got != want {
		t.Fatalf("powershellSingleQuoted() = %q, want %q", got, want)
	}
}

func TestWindowsLazyUpdateCommandUsesThreadSleep(t *testing.T) {
	command := windowsLazyUpdatePowerShellCommand(`C:\Program Files\Git\bin\bash.exe`, `echo ok`)

	if strings.Contains(command, `Start-Sleep`) {
		t.Fatalf("lazy update command uses Start-Sleep: %s", command)
	}
	for _, want := range []string{
		`[System.Threading.Thread]::Sleep(750)`,
		`for ($i = 0; $i -lt 6; $i++)`,
		`& 'C:\Program Files\Git\bin\bash.exe' -lc 'echo ok'`,
		`if ($LASTEXITCODE -eq 0) { break }`,
		`[System.Threading.Thread]::Sleep(5000)`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("lazy update command missing %q: %s", want, command)
		}
	}
}

func TestNewExecHookUsesArgsWithoutShell(t *testing.T) {
	hook := newExecHook(`C:\Tools\claude-notifications.exe`, "Stop")

	if hook.Command != `C:\Tools\claude-notifications.exe` {
		t.Fatalf("newExecHook command = %q", hook.Command)
	}
	if want := []string{"handle-hook", "Stop"}; !reflect.DeepEqual(hook.Args, want) {
		t.Fatalf("newExecHook args = %#v, want %#v", hook.Args, want)
	}
	if hook.Shell != "" {
		t.Fatalf("newExecHook shell = %q, want empty", hook.Shell)
	}
}

func TestWindowsHookSettingsUseExecFormForAllHooks(t *testing.T) {
	settings := newWindowsHookSettings(`C:\Tools\claude-notifications-windows-amd64.exe`)
	expected := map[string]string{
		"PreToolUse":   "PreToolUse",
		"Notification": "Notification",
		"Stop":         "Stop",
		"SubagentStop": "SubagentStop",
		"TeammateIdle": "TeammateIdle",
	}

	for hookEvent, expectedArg := range expected {
		groups := settings.Hooks[hookEvent]
		if len(groups) != 1 {
			t.Fatalf("%s groups = %d, want 1", hookEvent, len(groups))
		}
		if len(groups[0].Hooks) != 1 {
			t.Fatalf("%s commands = %d, want 1", hookEvent, len(groups[0].Hooks))
		}

		hook := groups[0].Hooks[0]
		if hook.Command != `C:\Tools\claude-notifications-windows-amd64.exe` {
			t.Fatalf("%s command = %q", hookEvent, hook.Command)
		}
		if want := []string{"handle-hook", expectedArg}; !reflect.DeepEqual(hook.Args, want) {
			t.Fatalf("%s args = %#v, want %#v", hookEvent, hook.Args, want)
		}
		if hook.Shell != "" {
			t.Fatalf("%s shell = %q, want empty", hookEvent, hook.Shell)
		}
		if strings.Contains(hook.Command, "|") {
			t.Fatalf("%s command contains shell pipe: %q", hookEvent, hook.Command)
		}
	}
}

func TestWindowsHookSettingsJSONHasNoShellSyntax(t *testing.T) {
	data, err := json.Marshal(newWindowsHookSettings(`C:\Tools\claude-notifications-windows-amd64.exe`))
	if err != nil {
		t.Fatal(err)
	}

	jsonText := string(data)
	for _, forbidden := range []string{
		`"shell"`,
		"$input",
		"powershell",
		"hook-wrapper",
		".bat",
		".cmd",
	} {
		if strings.Contains(jsonText, forbidden) {
			t.Fatalf("windows hooks JSON contains %q: %s", forbidden, jsonText)
		}
	}
}

func TestWindowsHookSettingsPreserveSpecialPathChars(t *testing.T) {
	exePath := `C:\Users\O'Brien\A $pecial Dir\claude-notifications-windows-amd64.exe`
	settings := newWindowsHookSettings(exePath)
	hook := settings.Hooks["Stop"][0].Hooks[0]

	if hook.Command != exePath {
		t.Fatalf("command = %q, want %q", hook.Command, exePath)
	}
	if strings.Contains(hook.Command, "`") || strings.Contains(hook.Command, "\"") {
		t.Fatalf("command should not be shell-quoted: %q", hook.Command)
	}

	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var decoded hookSettings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Hooks["Stop"][0].Hooks[0].Command; got != exePath {
		t.Fatalf("decoded command = %q, want %q", got, exePath)
	}
}

func setupLazyUpdateTestPlugin(t *testing.T, pluginVersion string) string {
	t.Helper()

	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugin")
	for _, dir := range []string{
		filepath.Join(pluginRoot, ".claude-plugin"),
		filepath.Join(pluginRoot, "bin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	manifest := []byte(`{"version":"` + pluginVersion + `"}`)
	if err := os.WriteFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "bin", "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return pluginRoot
}

func withIsolatedLazyUpdateGlobals(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	testenv.Set(t, root)

	oldGOOS := currentGOOS
	oldSchedule := scheduleWindowsLazyUpdate
	currentGOOS = "windows"

	t.Cleanup(func() {
		currentGOOS = oldGOOS
		scheduleWindowsLazyUpdate = oldSchedule
	})
}

// Regression: permissive flag parsing admits repeated/extra selectors, or reads
// stdin before refusing invalid argv. Each rejected form must close untouched IO.
func TestCursorEventGrammar(t *testing.T) {
	root := t.TempDir()
	selector := filepath.Join(root, "TEST-binding.json")
	valid := []string{"stop", "--binding", selector}
	if got, ok := parseCursorEventArgs(valid); !ok || got != selector {
		t.Fatal("fixed grammar refused")
	}
	cases := [][]string{
		nil, {"Stop", "--binding", selector}, {"stop", "--binding"},
		{"stop", "--binding", "relative"}, {"stop", "--binding", root + "/../TEST.json"},
		{"stop", "--binding", selector + "/"}, {"stop", "--binding", selector + "\n"},
		{"stop", "--binding", selector + string([]byte{0xff})},
		{"stop", "--binding=" + selector}, {"stop", "--binding", selector, "--binding", selector},
		{"stop", "--binding", selector, "extra"}, {"stop", "--foreign", selector},
	}
	for _, argv := range cases {
		if _, ok := parseCursorEventArgs(argv); ok {
			t.Fatalf("accepted %q", argv)
		}
		input := &cursorTrackedInput{reader: strings.NewReader("TEST-private-unread")}
		var out bytes.Buffer
		if code := runCursorEvent(argv, input, &out); code != 0 || out.String() != "{}\n" || input.reads != 0 || !input.closed {
			t.Fatalf("invalid argv crossed stdin/output boundary: %q code=%d out=%q reads=%d closed=%t", argv, code, out.String(), input.reads, input.closed)
		}
	}
}

type cursorTrackedInput struct {
	reader       io.Reader
	reads, bytes int
	closed       bool
}

func (r *cursorTrackedInput) Read(p []byte) (int, error) {
	r.reads++
	n, err := r.reader.Read(p)
	r.bytes += n
	return n, err
}
func (r *cursorTrackedInput) Close() error { r.closed = true; return nil }

func cursorSDKFrame(t *testing.T) []byte {
	t.Helper()
	// Marshal the actual pinned public event; expected neutral output is literal.
	body, err := json.Marshal(cursor.StopEvent{ConversationID: "TEST-private-conversation", GenerationID: "TEST-private-generation", HookEventName: "stop", Status: cursor.StopStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// Regression: malformed/oversize SDK frames leak content, unbounded reads drain
// native stdin, or a missing/foreign/unregistered locator reaches effects/state.
func TestCursorEventPrivateDenial(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "TEST-missing.json")
	malformed := filepath.Join(root, "TEST-malformed.json")
	if err := os.WriteFile(malformed, []byte(`{"TEST-private":`), 0600); err != nil {
		t.Fatal(err)
	}
	b := portable.Binding{Version: 1, Integration: portable.Cursor, InstallationID: "TEST-installation", BindingID: "TEST-binding", ScopeID: "user", ComponentID: "TEST-component", Owner: "existing-installer", ScopeRoot: root, DataRoot: root, ControlRoot: filepath.Join(root, "TEST-control"), GlobalConfig: filepath.Join(root, "TEST-config"), RuntimeRoot: root, Primary: "bin/TEST-observer"}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	_, _, raw, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	unregistered := filepath.Join(root, name)
	if err := os.WriteFile(unregistered, raw, 0600); err != nil {
		t.Fatal(err)
	}
	foreign := b
	foreign.Integration = portable.Claude
	foreignName, err := foreign.Filename()
	if err != nil {
		t.Fatal(err)
	}
	_, _, foreignRaw, err := foreign.Registration()
	if err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(root, foreignName)
	if err := os.WriteFile(foreignPath, foreignRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := portable.ReadCursorBinding(unregistered); err != nil {
		t.Fatal("TEST private Cursor locator unreadable", err)
	}
	for _, path := range []string{missing, malformed, foreignPath} {
		if _, err := portable.ReadCursorBinding(path); err == nil {
			t.Fatalf("private reader admitted %s", path)
		}
	}
	frame := cursorSDKFrame(t)
	cases := []struct {
		name, selector string
		payload        []byte
	}{
		{"missing", missing, frame}, {"malformed-binding", malformed, frame}, {"foreign", foreignPath, frame}, {"unregistered", unregistered, frame},
		{"malformed-sdk", missing, []byte(`{"conversation_id":"TEST-private"`)},
		{"oversize-sdk", missing, bytes.Repeat([]byte("x"), cursorsource.MaxPayloadBytes+2)},
	}
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := &cursorTrackedInput{reader: bytes.NewReader(tc.payload)}
			var output bytes.Buffer
			if code := runCursorEvent([]string{"stop", "--binding", tc.selector}, input, &output); code != 0 || output.String() != "{}\n" || !input.closed || input.bytes > cursorsource.MaxPayloadBytes+1 {
				t.Fatalf("denied event transport: code=%d out=%q closed=%t bytes=%d", code, output.String(), input.closed, input.bytes)
			}
		})
	}
	after, err := os.ReadDir(root)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("denied event created state/effect files", err)
	}
}

// Regression: real early dispatch falls through logging or legacy argv handling.
// A subprocess runs actual main with native SDK stdin and a missing TEST locator.
func TestCursorEventMainDispatch(t *testing.T) {
	if os.Getenv("TEST_CURSOR_MAIN_CHILD") == "1" {
		os.Args = []string{os.Args[0], "cursor-event", "stop", "--binding", os.Getenv("TEST_CURSOR_SELECTOR")}
		main()
		os.Exit(99)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCursorEventMainDispatch$")
	// The isolated environment also needs a private sink for instrumented child
	// coverage, so stderr remains an assertion about the event transport.
	// Measure the transport deadline without the race runtime's one-second exit sleep.
	cmd.Env = []string{"GORACE=atexit_sleep_ms=0", "TEST_CURSOR_MAIN_CHILD=1", "TEST_CURSOR_SELECTOR=" + filepath.Join(root, "TEST-missing.json"), "HOME=" + root, "XDG_CONFIG_HOME=" + root, "XDG_CACHE_HOME=" + root, "TMPDIR=" + root, "GOCOVERDIR=" + t.TempDir()}
	cmd.Stdin = bytes.NewReader(cursorSDKFrame(t))
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil || ctx.Err() != nil || out.String() != "{}\n" || stderr.Len() != 0 {
		t.Fatalf("dispatch: %v stdout=%q stderr=%q", err, out.String(), stderr.String())
	}
	// Each child gets its own harness budget for startup and the stdin deadline.
	blockedCtx, blockedCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer blockedCancel()
	blocked := exec.CommandContext(blockedCtx, os.Args[0], "-test.run=^TestCursorEventMainDispatch$")
	blocked.Env = cmd.Env
	pipe, err := blocked.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pipe.Close() }()
	out.Reset()
	stderr.Reset()
	blocked.Stdout, blocked.Stderr = &out, &stderr
	started := time.Now()
	if err := blocked.Run(); err != nil || blockedCtx.Err() != nil || out.String() != "{}\n" || stderr.Len() != 0 || time.Since(started) < 900*time.Millisecond || time.Since(started) > 2500*time.Millisecond {
		t.Fatalf("inherited blocked stdin: %v stdout=%q stderr=%q elapsed=%s", err, out.String(), stderr.String(), time.Since(started))
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("early dispatch wrote global state", err)
	}
}

// Regression: cancellation returns without closing/joining its reader, or panic
// escapes the private transport; failed neutral writes must still return exit 1.
func TestCursorEventInputAndOutputFailure(t *testing.T) {
	root := t.TempDir()
	argv := []string{"stop", "--binding", filepath.Join(root, "TEST-missing.json")}
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	var out bytes.Buffer
	started := time.Now()
	if code := runCursorEvent(argv, r, &out); code != 0 || out.String() != "{}\n" || time.Since(started) > 2500*time.Millisecond {
		t.Fatal("blocked input escaped budget")
	}
	if _, err := w.Write([]byte("TEST")); err != io.ErrClosedPipe {
		t.Fatal("blocked reader was not closed/joined", err)
	}
	joining := &cursorJoiningInput{release: make(chan struct{}), joined: make(chan struct{})}
	out.Reset()
	if code := runCursorEvent(argv, joining, &out); code != 0 || out.String() != "{}\n" {
		t.Fatal("cancellable input failed neutral transport")
	}
	select {
	case <-joining.joined:
	default:
		t.Fatal("transport returned before reader completed after Close")
	}
	input := &cursorTrackedInput{reader: cursorPanicReader{}}
	out.Reset()
	if code := runCursorEvent(argv, input, &out); code != 0 || out.String() != "{}\n" || !input.closed {
		t.Fatal("panic did not close/join privately")
	}
	if code := runCursorEvent(argv, io.NopCloser(bytes.NewReader(cursorSDKFrame(t))), cursorFailWriter{}); code != 1 {
		t.Fatalf("write failure exit=%d", code)
	}
}

// Read completion deliberately follows Close so a close-without-join breaks.
type cursorJoiningInput struct {
	release, joined chan struct{}
	once            sync.Once
}

func (r *cursorJoiningInput) Read([]byte) (int, error) {
	<-r.release
	time.Sleep(50 * time.Millisecond)
	close(r.joined)
	return 0, io.EOF
}
func (r *cursorJoiningInput) Close() error { r.once.Do(func() { close(r.release) }); return nil }

type cursorPanicReader struct{}

func (cursorPanicReader) Read([]byte) (int, error) { panic("TEST-private-reader") }

type cursorFailWriter struct{}

func (cursorFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// Nearest meaningful deadline boundary without affirmative installed authority:
// real admission + shared stdin + actual typed source + Consumer. A renewed
// deadline after stdin would incorrectly turn expired into not_registered.
func TestCursorEventOriginalAdmissionDeadline(t *testing.T) {
	clock := notifier.SystemBootClock{}
	ctx, deadline, cancel, err := observation.Admission(context.Background(), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	// Pre-stdin work spends this invocation's budget before source decoding.
	time.Sleep(150 * time.Millisecond)
	r, w := io.Pipe()
	frame := cursorSDKFrame(t)
	go func() { _, _ = w.Write(frame); _ = w.Close() }()
	payload, ok := readLocalPayload(ctx, r)
	if !ok {
		t.Fatal("bounded stdin failed")
	}
	remaining, ok := observation.Remaining(clock, deadline)
	if !ok || remaining > 3900*time.Millisecond {
		t.Fatal("pre-stdin time did not spend original budget", remaining)
	}
	facts, err := cursorsource.Decode(ctx, cursorsource.Selector, payload)
	if err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	receipt := (cursorevent.Consumer{Clock: clock}).Consume(context.Background(), facts, deadline)
	if receipt.Status != "suppressed" || receipt.Reason != "expired" {
		t.Fatalf("original deadline replaced: %+v", receipt)
	}
}
