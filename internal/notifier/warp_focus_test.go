package notifier

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/warpfocus"
)

var hostWarpFocusURL string

func TestMain(m *testing.M) {
	// Host shells inside Warp export WARP_FOCUS_URL; generic click-to-focus
	// tests must not inherit it or they assert the wrong execute path.
	hostWarpFocusURL = os.Getenv(warpfocus.FocusURLEnv)
	_ = os.Unsetenv(warpfocus.FocusURLEnv)
	_ = os.Unsetenv(warpfocus.SessionUUIDEnv)
	os.Exit(m.Run())
}

const warpTestSessionHex = "6b7be92641ae8ced80188a4d87e4b200"
const warpTestFocusURL = "warp://session/" + warpTestSessionHex

func clearWarpFocusEnv(t *testing.T) {
	t.Helper()
	t.Setenv(warpfocus.FocusURLEnv, "")
	t.Setenv(warpfocus.SessionUUIDEnv, "")
	t.Setenv("TMUX", "")
}

func setWarpHost(t *testing.T) {
	t.Helper()
	t.Setenv("__CFBundleIdentifier", "dev.warp.Warp-Stable")
	t.Setenv("TERM_PROGRAM", "WarpTerminal")
	t.Setenv("VSCODE_INJECTION", "")
	t.Setenv("VSCODE_GIT_IPC_HANDLE", "")
}

func TestBuildFocusScript_WarpUsesSessionURL(t *testing.T) {
	clearWarpFocusEnv(t)
	setWarpHost(t)
	t.Setenv(warpfocus.FocusURLEnv, warpTestFocusURL)
	t.Setenv(iTerm2SessionIDEnv, "")

	script := buildFocusScript("dev.warp.Warp-Stable", "/home/user/my-project")
	if !strings.Contains(script, "open '"+warpTestFocusURL+"'") {
		t.Fatalf("Warp click should open the session URL, got: %s", script)
	}
	if !strings.Contains(script, "||") {
		t.Fatalf("Warp click should fall back to focus-window, got: %s", script)
	}
	if !strings.Contains(script, "focus-window") {
		t.Fatalf("Warp fallback should keep focus-window, got: %s", script)
	}
	if strings.Contains(script, "osascript") {
		t.Errorf("Warp click should not use osascript, got: %s", script)
	}
}

func TestBuildFocusScript_WarpURLWorksWithoutCWD(t *testing.T) {
	clearWarpFocusEnv(t)
	setWarpHost(t)
	t.Setenv(warpfocus.FocusURLEnv, "warposs://session/"+warpTestSessionHex)
	t.Setenv(iTerm2SessionIDEnv, "")

	script := buildFocusScript("dev.warp.Warp-Stable", "")
	if script != "open 'warposs://session/"+warpTestSessionHex+"'" {
		t.Fatalf("Warp without cwd should only open the session URL, got: %s", script)
	}
	if strings.Contains(script, "focus-window") {
		t.Errorf("Warp without cwd should not add focus-window, got: %s", script)
	}
}

func TestBuildFocusScript_WarpUUIDFallback(t *testing.T) {
	clearWarpFocusEnv(t)
	setWarpHost(t)
	t.Setenv(warpfocus.FocusURLEnv, "")
	t.Setenv(warpfocus.SessionUUIDEnv, warpTestSessionHex)
	t.Setenv(iTerm2SessionIDEnv, "")

	script := buildFocusScript("dev.warp.Warp-Stable", "/tmp/proj")
	if !strings.Contains(script, "open '"+warpTestFocusURL+"'") {
		t.Fatalf("UUID-only Warp env should build warp://session URL, got: %s", script)
	}
}

func TestBuildFocusScript_WarpIgnoresConversationURL(t *testing.T) {
	clearWarpFocusEnv(t)
	setWarpHost(t)
	t.Setenv(warpfocus.FocusURLEnv, "warp://conversation/"+warpTestSessionHex)
	t.Setenv(iTerm2SessionIDEnv, "")

	script := buildFocusScript("dev.warp.Warp-Stable", "/home/user/my-project")
	if strings.Contains(script, "open '") {
		t.Fatalf("conversation URLs must not be used for click-to-focus, got: %s", script)
	}
	if !strings.Contains(script, "focus-window") {
		t.Fatalf("invalid Warp URL should fall back to focus-window, got: %s", script)
	}
}

func TestBuildTerminalNotifierArgs_WarpSessionURL(t *testing.T) {
	clearWarpFocusEnv(t)
	setWarpHost(t)
	t.Setenv(warpfocus.FocusURLEnv, warpTestFocusURL)
	t.Setenv(iTerm2SessionIDEnv, "")

	args := buildTerminalNotifierArgs("Title", "Message", "dev.warp.Warp-Stable", "/home/user/my-project", true)
	execVal := getArgValue(args, "-execute")
	if !strings.Contains(execVal, "open '"+warpTestFocusURL+"'") {
		t.Fatalf("-execute should open Warp session URL, got: %s", execVal)
	}
}

func TestInjectWarpFocusURL_PrependsOpenToTmuxExecute(t *testing.T) {
	clearWarpFocusEnv(t)
	setWarpHost(t)
	t.Setenv(warpfocus.FocusURLEnv, warpTestFocusURL)

	args := []string{
		"-title", "Title",
		"-message", "Message",
		"-activate", "dev.warp.Warp-Stable",
		"-execute", "'/usr/bin/tmux' select-window -t '%42'",
	}
	got := injectWarpFocusURL(args)
	execVal := getArgValue(got, "-execute")
	if !strings.HasPrefix(execVal, "open '"+warpTestFocusURL+"' >/dev/null 2>&1; ") {
		t.Fatalf("Warp URL should be opened before tmux select, got: %s", execVal)
	}
	if !strings.Contains(execVal, "select-window -t '%42'") {
		t.Fatalf("tmux command should be preserved, got: %s", execVal)
	}
}

func TestInjectWarpFocusURL_NoopWithoutWarp(t *testing.T) {
	clearWarpFocusEnv(t)
	args := []string{"-execute", "tmux select-pane"}
	got := injectWarpFocusURL(args)
	if getArgValue(got, "-execute") != "tmux select-pane" {
		t.Fatalf("inject without Warp env should be a no-op, got: %v", got)
	}
}

func TestBuildFocusScript_IgnoresInheritedWarpURLForCursor(t *testing.T) {
	clearWarpFocusEnv(t)
	t.Setenv("__CFBundleIdentifier", "com.todesktop.230313mzl4w4u92")
	t.Setenv("TERM_PROGRAM", "WarpTerminal")
	t.Setenv("VSCODE_INJECTION", "1")
	t.Setenv(warpfocus.FocusURLEnv, warpTestFocusURL)
	t.Setenv(iTerm2SessionIDEnv, "")

	script := buildFocusScript("com.todesktop.230313mzl4w4u92", "/home/user/proj")
	if strings.Contains(script, "open '") {
		t.Fatalf("Cursor launched from Warp must not open WARP_FOCUS_URL, got: %s", script)
	}
	if !strings.Contains(script, "focus-window") {
		t.Fatalf("Cursor click should keep focus-window, got: %s", script)
	}
}

func TestWarpFocusExecute_IsValidShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not available")
	}
	clearWarpFocusEnv(t)
	setWarpHost(t)
	t.Setenv(warpfocus.FocusURLEnv, warpTestFocusURL)
	t.Setenv(iTerm2SessionIDEnv, "")

	script := buildFocusScript("dev.warp.Warp-Stable", "/home/user/my-project")
	cmd := exec.Command("sh", "-n", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Warp execute script is not valid shell: %v\n%s\nscript: %s", err, out, script)
	}
}

func TestWarpFocusURL_LiveHostEnvIsSessionLink(t *testing.T) {
	if hostWarpFocusURL == "" {
		t.Skip("host process did not export WARP_FOCUS_URL")
	}
	got := warpfocus.Normalize(hostWarpFocusURL)
	if got == "" {
		t.Fatalf("host WARP_FOCUS_URL is not a session deep link: %q", hostWarpFocusURL)
	}
	setWarpHost(t)
	t.Setenv(warpfocus.FocusURLEnv, hostWarpFocusURL)
	script := buildFocusScript("dev.warp.Warp-Stable", "/tmp/proj")
	if !strings.Contains(script, "open '"+got+"'") {
		t.Fatalf("live Warp env should bake session URL into click handler, got: %s", script)
	}
}
