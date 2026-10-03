//go:build darwin

package notifier

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

// Regression: the macOS helper must receive literal session/question content
// while retaining the original opaque id for notification grouping/navigation.
// A disposable helper and intercepted process launch prevent real banners.
func TestSendDesktopNativeHookContentRetainsThreadID(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "bin", "ClaudeNotifier.app", "Contents", "MacOS", "terminal-notifier-modern")
	if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("test helper"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PLUGIN_ROOT", root)
	t.Setenv("TMUX", "")
	t.Setenv("STY", "")
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.ClickToFocus = false
	cfg.Notifications.Desktop.TerminalBundleID = "com.apple.Terminal"
	n := New(cfg)
	t.Cleanup(func() { _ = n.Close() })
	previous := execCommand
	var captured []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name != "open" {
			t.Fatalf("unexpected process launch: %s", name)
		}
		captured = append([]string(nil), args...)
		return exec.Command("/usr/bin/true")
	}
	t.Cleanup(func() { execCommand = previous })
	arg := func(flag string) string {
		for i := 0; i+1 < len(captured); i++ {
			if captured[i] == flag {
				return captured[i+1]
			}
		}
		return ""
	}
	err := n.SendDesktop(analyzer.StatusQuestion, "[OLD|wrong project] stale", "original-thread-id", root, WithHookPresentation(HookPresentation{
		SessionName: "Fix [SDK] | installer", Folder: "agent-notifications", Body: "Restart Codex?", Question: "Restart Codex?",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if arg("-threadID") != "original-thread-id" || arg("-subtitle") != "Fix [SDK] | installer · agent-notifications" || arg("-message") != "Restart Codex?" {
		t.Fatalf("native delivery changed identity/content: %v", captured)
	}
	if arg("-title") != cfg.Statuses["question"].Title+": Restart Codex?" {
		t.Fatalf("native question headline = %q", arg("-title"))
	}
}
