//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	if name == "spoof" {
		if spoof := os.Getenv("TEST_STRICT_SPOOF_WINDOW"); spoof == "" {
			t.Fatal("no other window supplied")
		} else {
			t.Setenv("WINDOWID", spoof)
		}
	}
	origin, ok := CaptureStrictTerminal(ctx, cfg, config.AgentQoder, control, ledger.ID, ledger.Generation)
	if name == "spoof" {
		if ok || origin != nil {
			t.Fatal("another terminal's WINDOWID was accepted as our ancestor")
		}
		writeStrictNativeReceipt(t, root, name, false, "", os.Getpid())
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
	writeStrictNativeReceipt(t, root, name, true, origin.WindowID, os.Getpid())
}

func writeStrictNativeReceipt(t *testing.T, root, scenario string, focus bool, window string, sender int) {
	t.Helper()
	data, err := json.Marshal(struct {
		Scenario string `json:"scenario"`
		Focus    bool   `json:"focus"`
		Window   string `json:"window"`
		Sender   int    `json:"sender"`
	}{scenario, focus, window, sender})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, scenario+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
