//go:build windows

package main

import (
	"context"
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
	_, _, err = gate.ConsumerBindingAndConfig(ctx)
	if err != nil {
		t.Fatal("binding/config", err)
	}
	t.Log("binding/config OK", time.Since(started))
}
