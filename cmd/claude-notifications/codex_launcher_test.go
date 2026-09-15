package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// launcherFixture materializes the shipped launchers next to a stub binary in
// a sandbox so wrapper behavior is tested without network or real installs.
type launcherFixture struct {
	home      string
	pluginDir string
	binDir    string
	callLog   string
}

func newLauncherFixture(t *testing.T, binaryVersion string) launcherFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher test")
	}

	root := repoRoot(t)
	fx := launcherFixture{
		home:      t.TempDir(),
		pluginDir: t.TempDir(),
	}
	fx.binDir = filepath.Join(fx.pluginDir, "bin")
	fx.callLog = filepath.Join(fx.pluginDir, "calls.log")

	if err := os.MkdirAll(fx.binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, script := range []string{"hook-wrapper.sh", "codex-hook-wrapper.sh"} {
		data, err := os.ReadFile(filepath.Join(root, "bin", script))
		if err != nil {
			t.Fatalf("read %s: %v", script, err)
		}
		if err := os.WriteFile(filepath.Join(fx.binDir, script), data, 0o755); err != nil {
			t.Fatalf("write %s: %v", script, err)
		}
	}

	stub := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"" + fx.callLog + "\"\n" +
		"if [ \"$1\" = \"version\" ]; then echo \"claude-notifications v" + binaryVersion + "\"; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(fx.binDir, "claude-notifications"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub binary: %v", err)
	}

	manifestDir := filepath.Join(fx.pluginDir, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	manifest := `{"name":"claude-notifications-go","version":"` + binaryVersion + `"}`
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	return fx
}

func (fx launcherFixture) env() []string {
	return []string{
		"HOME=" + fx.home,
		"PATH=" + os.Getenv("PATH"),
		"XDG_CACHE_HOME=" + filepath.Join(fx.home, ".cache"),
	}
}

func (fx launcherFixture) calls(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(fx.callLog)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	return string(data)
}

// TestCodexLauncherWritesNothingUnderClaudeHome is the pointer-file
// regression gate: a Codex-route launch must perform zero writes under
// ~/.claude (the pointer file is consumed by old-version Claude shims and
// alternating products would ping-pong it).
func TestCodexLauncherWritesNothingUnderClaudeHome(t *testing.T) {
	fx := newLauncherFixture(t, "9.9.9")

	cmd := exec.Command("sh", filepath.Join(fx.binDir, "codex-hook-wrapper.sh"),
		"handle-hook", "Stop", "--product", "codex")
	cmd.Env = fx.env()
	cmd.Stdin = strings.NewReader(`{}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("launcher output = %q, want empty", out)
	}

	if _, err := os.Stat(filepath.Join(fx.home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("codex route created ~/.claude (err=%v); pointer-file guard broken", err)
	}

	calls := fx.calls(t)
	if !strings.Contains(calls, "handle-hook Stop --product codex") {
		t.Fatalf("binary did not receive the hook argv; calls:\n%s", calls)
	}
}

// TestClaudeWrapperStillWritesPointer proves the Claude route (no
// CN_PRODUCT) keeps its pointer-file behavior after the guard.
func TestClaudeWrapperStillWritesPointer(t *testing.T) {
	fx := newLauncherFixture(t, "9.9.9")

	cmd := exec.Command("sh", filepath.Join(fx.binDir, "hook-wrapper.sh"),
		"handle-hook", "Stop")
	cmd.Env = fx.env()
	cmd.Stdin = strings.NewReader(`{}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}

	ptr := filepath.Join(fx.home, ".claude", "claude-notifications-go", "plugin-root")
	data, err := os.ReadFile(ptr)
	if err != nil {
		t.Fatalf("pointer file missing on claude route: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatal("pointer file empty")
	}
}

// TestCodexLauncherOldBinaryGuard: a binary older than the first
// Codex-capable release must never be executed with a Codex payload; the
// launcher exits 0 silently instead.
func TestCodexLauncherOldBinaryGuard(t *testing.T) {
	fx := newLauncherFixture(t, "1.41.0") // pre-Codex version, matches manifest → no install attempt

	cmd := exec.Command("sh", filepath.Join(fx.binDir, "codex-hook-wrapper.sh"),
		"handle-hook", "Stop", "--product", "codex")
	cmd.Env = fx.env()
	cmd.Stdin = strings.NewReader(`{}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher must exit 0: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("launcher output = %q, want empty", out)
	}

	calls := fx.calls(t)
	if strings.Contains(calls, "handle-hook") {
		t.Fatalf("pre-Codex binary was executed with a Codex payload; calls:\n%s", calls)
	}
}
