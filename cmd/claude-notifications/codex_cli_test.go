package main

import (
	"bytes"
	"fmt"
	"github.com/777genius/agent-notifications/internal/testenv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	cliBinaryOnce sync.Once
	cliBinaryPath string
	cliBinaryErr  error
)

func buildCLIBinary(t *testing.T) string {
	t.Helper()
	cliBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cn-cli-test-")
		if err != nil {
			cliBinaryErr = err
			return
		}
		name := "claude-notifications-test"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		cliBinaryPath = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-p", "2", "-buildvcs=false", "-o", cliBinaryPath, ".")
		cmd.Env = testenv.Build(t, filepath.Join(dir, "build-home"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			cliBinaryErr = err
			t.Logf("go build output: %s", out)
		}
	})
	if cliBinaryErr != nil {
		t.Fatalf("failed to build test binary: %v", cliBinaryErr)
	}
	return cliBinaryPath
}

// codexCLIEnv builds an isolated environment: temp HOME (so the stable
// config path is sandboxed), a temp PLUGIN_ROOT (log destination), and a
// config that disables all delivery so automated tests never send real
// desktop notifications.
func codexCLIEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	pluginRoot := t.TempDir()

	cfgDir := filepath.Join(home, ".claude", "claude-notifications-go")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfg := `{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	env := append(testenv.Env(t, home), "PLUGIN_ROOT="+pluginRoot)
	return env
}

type cliResult struct {
	exitCode int
	stdout   string
	stderr   string
}

func runCLI(t *testing.T, env []string, stdin string, args ...string) cliResult {
	t.Helper()
	bin := buildCLIBinary(t)
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return cliResult{exitCode: code, stdout: stdout.String(), stderr: stderr.String()}
}

const cliStopPayload = `{"session_id":"01a05cd8-b495-7f80-a36b-cc0aa98efc05","turn_id":"01a05cd8-b51b-7343-8b75-b2d4ad9e276e","transcript_path":"/tmp/rollout.jsonl","cwd":"/tmp/proj","hook_event_name":"Stop","model":"gpt-5.6-sol","permission_mode":"bypassPermissions","stop_hook_active":false,"last_assistant_message":"OK"}`

func assertContained(t *testing.T, name string, res cliResult) {
	t.Helper()
	if res.exitCode != 0 {
		t.Errorf("%s: exit = %d, want 0", name, res.exitCode)
	}
	if res.stdout != "" {
		t.Errorf("%s: stdout = %q, want empty", name, res.stdout)
	}
	if res.stderr != "" {
		t.Errorf("%s: stderr = %q, want empty", name, res.stderr)
	}
}

// TestCodexCLIContainment executes the exact public command form and proves
// the observation contract: exit 0 with empty stdout/stderr for the success
// path and for every failure injection.
func TestCodexCLIContainment(t *testing.T) {
	env := codexCLIEnv(t)

	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{name: "valid stop", stdin: cliStopPayload, args: []string{"handle-hook", "Stop", "--product", "codex"}},
		{name: "valid permission request", stdin: `{"session_id":"s","turn_id":"t","hook_event_name":"PermissionRequest","tool_name":"shell","tool_input":{}}`, args: []string{"handle-hook", "PermissionRequest", "--product", "codex"}},
		{name: "malformed payload", stdin: `{oops`, args: []string{"handle-hook", "Stop", "--product", "codex"}},
		{name: "empty payload", stdin: ``, args: []string{"handle-hook", "Stop", "--product", "codex"}},
		{name: "oversized payload", stdin: `{"pad":"` + strings.Repeat("a", 1<<20) + `"}`, args: []string{"handle-hook", "Stop", "--product", "codex"}},
		{name: "unsupported event", stdin: cliStopPayload, args: []string{"handle-hook", "SessionStart", "--product", "codex"}},
		{name: "pretooluse with non-question tool", stdin: `{"session_id":"s","turn_id":"t","hook_event_name":"PreToolUse","tool_name":"shell","tool_input":{},"tool_use_id":"c1"}`, args: []string{"handle-hook", "PreToolUse", "--product", "codex"}},
		{name: "unknown product", stdin: cliStopPayload, args: []string{"handle-hook", "Stop", "--product", "cursor"}},
		{name: "duplicate flag", stdin: cliStopPayload, args: []string{"handle-hook", "Stop", "--product", "codex", "--product", "codex"}},
		{name: "unknown flag", stdin: cliStopPayload, args: []string{"handle-hook", "Stop", "--product", "codex", "--verbose"}},
		{name: "missing value", stdin: cliStopPayload, args: []string{"handle-hook", "Stop", "--product"}},
		{name: "missing event name", stdin: cliStopPayload, args: []string{"handle-hook", "--product", "codex"}},
	}
	for _, tc := range cases {
		res := runCLI(t, env, tc.stdin, tc.args...)
		assertContained(t, tc.name, res)
	}
}

// TestCodexCLIContainmentFailureInjections drives the codex route through the
// remaining containment matrix: config-load warnings and logger init failure
// must never reach the process output.
func TestCodexCLIContainmentFailureInjections(t *testing.T) {
	t.Run("corrupted stable config", func(t *testing.T) {
		home := t.TempDir()
		pluginRoot := t.TempDir()

		// Corrupt the stable config so the quiet loader takes its warning
		// path, and provide a valid legacy config (delivery disabled) so the
		// run cannot emit a real desktop notification.
		cfgDir := filepath.Join(home, ".claude", "claude-notifications-go")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{corrupted"), 0o644); err != nil {
			t.Fatalf("write corrupted config: %v", err)
		}
		legacyDir := filepath.Join(pluginRoot, "config")
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatalf("mkdir legacy: %v", err)
		}
		legacy := `{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}}}`
		if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte(legacy), 0o644); err != nil {
			t.Fatalf("write legacy config: %v", err)
		}

		env := []string{
			"HOME=" + home,
			"USERPROFILE=" + home,
			"PLUGIN_ROOT=" + pluginRoot,
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
			"PATH=" + os.Getenv("PATH"),
			"TMPDIR=" + t.TempDir(),
		}
		res := runCLI(t, env, cliStopPayload, "handle-hook", "Stop", "--product", "codex")
		assertContained(t, "corrupted stable config", res)
	})

	t.Run("logger init failure", func(t *testing.T) {
		home := t.TempDir()
		// PLUGIN_ROOT pointing at a regular file makes log-file creation
		// fail on every platform.
		notADir := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		env := []string{
			"HOME=" + home,
			"USERPROFILE=" + home,
			"PLUGIN_ROOT=" + notADir,
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
			"PATH=" + os.Getenv("PATH"),
			"TMPDIR=" + t.TempDir(),
		}
		res := runCLI(t, env, cliStopPayload, "handle-hook", "Stop", "--product", "codex")
		assertContained(t, "logger init failure", res)
	})
}

// TestCodexCLIDeliversWebhook is the positive end-to-end check through the
// real binary: a valid Codex Stop payload must reach an actual delivery
// channel (a local webhook sink), not just exit cleanly.
func TestCodexCLIDeliversWebhook(t *testing.T) {
	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case received <- body:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	home := t.TempDir()
	pluginRoot := t.TempDir()
	cfgDir := filepath.Join(home, ".claude", "claude-notifications-go")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfg := fmt.Sprintf(`{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":true,"preset":"slack","url":%q}}}`, srv.URL)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"PLUGIN_ROOT=" + pluginRoot,
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + t.TempDir(),
	}

	marker := fmt.Sprintf("webhook-e2e-%d", time.Now().UnixNano())
	payload := fmt.Sprintf(`{"session_id":"%s","turn_id":"turn-1","transcript_path":"/tmp/rollout.jsonl","cwd":"/tmp/proj","hook_event_name":"Stop","model":"gpt-5.6-sol","permission_mode":"bypassPermissions","stop_hook_active":false,"last_assistant_message":"Delivered %s."}`, marker, marker)

	res := runCLI(t, env, payload, "handle-hook", "Stop", "--product", "codex")
	assertContained(t, "webhook delivery", res)

	select {
	case body := <-received:
		if !strings.Contains(string(body), marker) {
			t.Fatalf("webhook body %q does not contain marker %q", body, marker)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("webhook sink received nothing within 10s")
	}
}

// TestLegacyClaudeCLIKeepsLoudErrors proves ordinary human/Claude CLI usage
// keeps the existing non-zero/stderr UX after the Codex wiring.
func TestLegacyClaudeCLIKeepsLoudErrors(t *testing.T) {
	env := codexCLIEnv(t)

	res := runCLI(t, env, `{oops`, "handle-hook", "Stop")
	if res.exitCode == 0 {
		t.Error("legacy malformed payload must exit non-zero")
	}
	if res.stderr == "" {
		t.Error("legacy malformed payload must report on stderr")
	}

	res = runCLI(t, env, ``, "handle-hook")
	if res.exitCode == 0 {
		t.Error("missing event name must exit non-zero")
	}
}
