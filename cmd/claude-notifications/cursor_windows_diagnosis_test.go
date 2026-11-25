//go:build windows

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Observe persisted TEST state without sending events or changing consent.
func TestWindowsCursorInstalledDiagnosis(t *testing.T) {
	root := os.Getenv("CURSOR_TEST_ROOT")
	if root == "" {
		t.Skip("requires owned Windows CI TEST state")
	}
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "TEST-cursor-windows-") {
		t.Fatal("not a TEST root")
	}
	started := time.Now()
	raw, err := os.ReadFile(filepath.Join(root, "diag-selector.txt"))
	if err != nil {
		t.Fatal("selector", err)
	}
	b, err := portable.ReadCursorBinding(string(raw))
	if err != nil {
		t.Fatal("binding", err)
	}
	t.Log("binding OK", time.Since(started))
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal("snapshot", err)
	}
	t.Log("snapshot OK", time.Since(started))
	scratch, err := os.MkdirTemp(filepath.Join(root, "tmp"), "TEST-diagnosis-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(scratch)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, fixed, err := cursorInstalledInputs(ctx, b, snapshot, scratch)
	if err != nil {
		t.Fatal("installed inputs", err)
	}
	t.Log("inputs OK", time.Since(started))
	gate, err := copilotvscodeinstall.NewCursorGate(b, cfg, fixed)
	if err != nil {
		t.Fatal("physical gate", err)
	}
	t.Log("physical gate OK", time.Since(started))
	binding, effective, err := gate.ConsumerBindingAndConfig(ctx)
	if err != nil {
		t.Fatal("binding/config", err)
	}
	t.Log("binding/config OK", time.Since(started))
	channels := gate.Channels(ctx, binding)
	t.Logf("persisted channels desktop=%t webhook=%t status_desktop=%t status_webhook=%t",
		channels.Desktop, channels.Webhook, effective.IsStatusDesktopEnabled("agent_stopping"), effective.IsStatusWebhookEnabled("agent_stopping"))

	// Claims record attempted channels, not successful effects. Read only the
	// qualified TEST fixture, never emit marker hashes or boot identity.
	rel, err := filepath.Rel(root, b.DataRoot)
	if err != nil || !filepath.IsLocal(rel) {
		t.Fatal("observation data is outside TEST root")
	}
	f, err := os.Open(filepath.Join(b.DataRoot, "observations.json"))
	if os.IsNotExist(err) {
		t.Log("observation claims absent")
		return
	}
	if err != nil {
		t.Log("observation claims unreadable")
		return
	}
	defer f.Close()
	const maxBytes = 48 * 1024
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		t.Log("observation claims unavailable or oversized")
		return
	}
	var state struct {
		Entries []struct {
			Bits uint8 `json:"bits"`
		} `json:"entries"`
	}
	if json.Unmarshal(data, &state) != nil || len(state.Entries) > 256 {
		t.Log("observation claims invalid")
		return
	}
	var counts [4]int
	for _, entry := range state.Entries {
		if entry.Bits == 0 || entry.Bits > 3 {
			t.Log("observation claim bits invalid")
			return
		}
		counts[entry.Bits]++
	}
	t.Logf("observation claim census desktop_only=%d webhook_only=%d both=%d", counts[1], counts[2], counts[3])
}
