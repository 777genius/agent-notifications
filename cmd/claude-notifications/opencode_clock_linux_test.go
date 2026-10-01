//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Regression: dispatch reaching ordinary config/setup can block on a hostile
// config FIFO or create state; a pure isolated CLI snapshot must finish silently.
// The child test calls actual main; it does not inject a substitute clock.
func TestOpenCodeClockIsolatedDispatch(t *testing.T) {
	if os.Getenv("AN_CLOCK_TEST_CHILD") == "1" {
		os.Args = []string{"claude-notifications", "opencode-clock", "--protocol", "1"}
		main()
		return
	}
	root := t.TempDir()
	config := filepath.Join(root, "config", "agent-notifications")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(config, "config.json"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOpenCodeClockIsolatedDispatch$")
	cmd.Dir = root
	cmd.Env = []string{"AN_CLOCK_TEST_CHILD=1", "HOME=" + root, "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_DATA_HOME=" + filepath.Join(root, "missing-data"), "CLAUDE_PLUGIN_ROOT=" + filepath.Join(root, "missing-plugin"), "AGENT_NOTIFICATIONS_CONFIG=" + filepath.Join(config, "config.json")}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err, stderr.String(), out.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &fields); err != nil || len(fields) != 8 || string(fields["protocol"]) != "1" || out.Len() > 1024 || stderr.Len() != 0 {
		t.Fatal(out.String(), stderr.String(), err)
	}
	// Only the hostile config fixture may exist afterwards.
	entries := 0
	if err := filepath.WalkDir(root, func(_ string, _ os.DirEntry, err error) error { entries++; return err }); err != nil || entries != 4 {
		t.Fatal("unexpected private state", entries, err)
	}
}
