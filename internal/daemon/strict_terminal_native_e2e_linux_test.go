//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// This is executed by the native X11 fixture INSIDE a real xterm process.
// The release daemon, D-Bus notification, dunst and physical click are real.
// A synthetic consent fixture is not an installed Qoder/Gemini/Cursor CLI.
func TestStrictTerminalNativeSender(t *testing.T) {
	if os.Getenv("TEST_STRICT_TERMINAL_NATIVE") != "1" {
		t.Skip("explicit native X11 fixture only")
	}
	root := os.Getenv("TEST_STRICT_TERMINAL_ROOT")
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "TEST-strict-") {
		t.Fatal("private TEST root required")
	}
	name := os.Getenv("TEST_STRICT_TERMINAL_CASE")
	if name != "positive" && name != "revoke" && name != "policy_revoke" && name != "spoof" {
		t.Fatal("unsupported fixture case")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	control, runtime := filepath.Join(root, name, "control"), filepath.Join(root, name, "runtime")
	hook := filepath.Join(runtime, "hook")
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer",
		ConsumerID: "qoder-notifications",
		Files:      []installruntime.File{{Path: hook, Data: []byte("inert TEST hook"), Mode: 0700}},
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	ledger, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer",
		ConsumerID: "qoder-notifications", RefreshOnly: true, PolicyOnly: true,
		ExpectedGeneration: &ledger.Generation, PolicyEnabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, name, "config.json")
	content := []byte(`{"schemaVersion":2,"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":true}},"agents":{"qoder":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":1}}}}}` + "\n")
	if err := os.WriteFile(file, content, 0600); err != nil {
		t.Fatal(err)
	}
	d, err := config.ParseDocument(content, file, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := d.Effective(config.AssetContext{Agent: config.AgentQoder})
	if err != nil || !cfg.TerminalClickAuthorized() {
		t.Fatal("TEST consent fixture rejected", err)
	}
	origin, ok := CaptureStrictTerminal(ctx, cfg, config.AgentQoder, control, ledger.ID, ledger.Generation)
	if name == "spoof" {
		// Capture the genuine sender first, then forge only its window ID.
		// The daemon must reject this payload independently of the client.
		if !ok || origin == nil {
			t.Fatal("cannot construct bound negative target")
		}
		spoof := os.Getenv("TEST_STRICT_SPOOF_WINDOW")
		if spoof == "" {
			t.Fatal("no other window supplied")
		}
		// This exercises the XRes server-derived PID guard: even if a
		// different X11 client forges _NET_WM_PID to identify the genuine
		// terminal, it cannot acquire the other window's click target.
		if err := exec.CommandContext(ctx, "/usr/bin/xprop", "-id", spoof,
			"-f", "_NET_WM_PID", "32c", "-set", "_NET_WM_PID", strconv.Itoa(origin.OwnerPID)).Run(); err != nil {
			t.Fatal("failed to forge the harmless TEST EWMH property", err)
		}
		// A valid marker must NOT let another X11 client's window through:
		// XRes server ownership is checked independently from EWMH/PID and
		// the same string marker on a different native XID.
		if err := exec.CommandContext(ctx, "/usr/bin/xprop", "-id", spoof,
			"-f", "_AGENT_NOTIFICATIONS_STRICT_ORIGIN", "8s", "-set",
			"_AGENT_NOTIFICATIONS_STRICT_ORIGIN", origin.WindowNonce).Run(); err != nil {
			t.Fatal("failed to seed spoof window TEST marker", err)
		}
		t.Setenv("WINDOWID", spoof)
		if rejected, allowed := CaptureStrictTerminal(ctx, cfg, config.AgentQoder, control, ledger.ID, ledger.Generation); allowed || rejected != nil {
			t.Fatal("foreign WINDOWID was accepted at capture")
		}
		forged := *origin
		forged.WindowID = spoof
		client, err := NewClient()
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.SendNotificationContext(ctx, "TEST strict terminal", "TEST strict spoof", "", FocusHints{Strict: &forged}, 25)
		if err != nil || response == nil || !response.Success || response.FocusEnabled || response.NotificationID == 0 {
			t.Fatalf("daemon did not reject forged strict focus: %+v %v", response, err)
		}
		writeStrictNativeReceipt(t, root, name, false, origin.WindowID, os.Getpid(), response.NotificationID)
		return
	}
	if !ok || origin == nil {
		t.Fatal("no source-bound local X11 origin")
	}
	if !StrictTerminalAuthorized(ctx, origin) {
		t.Fatal("created native origin is not authorized")
	}
	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.SendNotificationContext(ctx, "TEST strict terminal", "TEST strict "+name, "", FocusHints{Strict: origin}, 25)
	if err != nil || response == nil || !response.Success || !response.FocusEnabled || response.NotificationID == 0 {
		t.Fatalf("native strict handoff not confirmed: %+v %v", response, err)
	}
	if name == "positive" {
		// A busy agent can run many PreToolUse hooks without creating another
		// banner. They must not invalidate the earlier notification's click.
		for i := 0; i < 12; i++ {
			next, qualified := CaptureStrictTerminal(ctx, cfg, config.AgentQoder, control, ledger.ID, ledger.Generation)
			if !qualified || next == nil || next.WindowNonce != origin.WindowNonce {
				t.Fatal("new hook evicted an earlier live window marker")
			}
		}
		if !StrictTerminalAuthorized(ctx, origin) {
			t.Fatal("earlier notification lost its click target")
		}
	}
	writeStrictNativeReceipt(t, root, name, true, origin.WindowID, os.Getpid(), response.NotificationID)
}

func writeStrictNativeReceipt(t *testing.T, root, scenario string, focus bool, window string, sender int, notificationID uint32) {
	t.Helper()
	data, err := json.Marshal(struct {
		Scenario       string `json:"scenario"`
		Focus          bool   `json:"focus"`
		Window         string `json:"window"`
		Sender         int    `json:"sender"`
		NotificationID uint32 `json:"notification_id"`
	}{scenario, focus, window, sender, notificationID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, scenario+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
