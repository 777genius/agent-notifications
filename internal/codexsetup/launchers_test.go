package codexsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsBundleLaunchers(t *testing.T) {
	bin := t.TempDir()
	binary := "claude-notifications-windows-arm64.exe"
	if err := os.WriteFile(filepath.Join(bin, binary), []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := installBundleLaunchers(bin, "windows", "arm64"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-notifications", "claude-notifications"} {
		data, err := os.ReadFile(filepath.Join(bin, name+".bat"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"%~dp0`+binary+`" %*`) || !strings.Contains(string(data), "set AGENT_NOTIFICATIONS_LAUNCHER="+name) {
			t.Fatalf("wrong wrapper: %s", data)
		}
		for _, suffix := range []string{"", ".bat", ".cmd"} {
			if !runtimeBinary(name + suffix) {
				t.Fatal("launcher missing from allowlist", name+suffix)
			}
		}
	}
}
