//go:build linux

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
)

func recordedNotify(t *testing.T, app string, replaces uint32, icon, title, body string, actions []string, hints map[string]dbus.Variant, expiry int32) map[string]any {
	t.Helper()
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "record.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	r := &receiver{file: file, control: root}
	id, rpcErr := r.Notify(app, replaces, icon, title, body, actions, hints, expiry)
	if id != 1 || rpcErr != nil {
		t.Fatalf("unexpected Notify result: id=%d error=%v", id, rpcErr)
	}
	if _, err = file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err = json.NewDecoder(file).Decode(&row); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestReceiverControlledDesktopPresentation(t *testing.T) {
	cases := []struct{ name, title, body string }{
		{"completion_session_label", "✅ [TEST completion]", "Task completed"},
		{"manual_compaction", "✅ [TEST manual compaction]", "Task completed"},
		{"foreground_parent", "✅ [TEST actual tool child]", "Task completed"},
		{"scope_root", "✅ [TEST scope root]", "Task completed"},
		{"fork_root", "✅ [TEST scope root (fork #1)]", "Task completed"},
		{"retry", "✅ [P0 terminal retry]", "Task completed"},
		{"question_optional_text_absent", "❓ Question [TEST form]", "OpenCode asked a question"},
		{"question_with_label", "❓ P0 test choice?", "TEST form\nP0 test choice?"},
		{"question_without_label", "❓ P0 test choice?", "P0 test choice?"},
		{"permission_label", "OpenCode [TEST permission]", "OpenCode requested permission"},
		{"error_label", "OpenCode [TEST error]", "An error needs your attention"},
		{"fallback_completion", "OpenCode", "Task completed"},
		{"fallback_question", "OpenCode", "OpenCode asked a question"},
		{"fallback_permission", "OpenCode", "OpenCode requested permission"},
		{"fallback_error", "OpenCode", "An error needs your attention"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := recordedNotify(t, "agent-notifications", 0, "", c.title, c.body, nil, map[string]dbus.Variant{"suppress-sound": dbus.MakeVariant(true)}, 14794)
			if row["valid"] != true || row["body"] != c.body || row["count"] != float64(1) || row["silent"] != true || row["actions"] != float64(0) || row["expiry"] != float64(14794) {
				t.Fatal("controlled desktop contract rejected or core receipt changed")
			}
		})
	}
}

func TestReceiverRejectsUnexpectedPresentationAndCoreFields(t *testing.T) {
	cases := []struct {
		name, app, icon, title, body string
		replaces                    uint32
		actions                     []string
		hints                       map[string]dbus.Variant
		expiry                      int32
	}{
		{name: "unknown_title", title: "✅ [PRIVATE_unknown]"},
		{name: "unknown_body", body: "PRIVATE_unknown"},
		{name: "wrong_title_body_pair", title: "OpenCode [TEST permission]"},
		{name: "unknown_question", title: "❓ PRIVATE_unknown", body: "PRIVATE_unknown"},
		{name: "wrong_application", app: "foreign"},
		{name: "replacement", replaces: 1},
		{name: "icon", icon: "foreign"},
		{name: "actions", actions: []string{"default", "Open"}},
		{name: "extra_hint", hints: map[string]dbus.Variant{"suppress-sound": dbus.MakeVariant(true), "foreign": dbus.MakeVariant(true)}},
		{name: "sound", hints: map[string]dbus.Variant{"suppress-sound": dbus.MakeVariant(false)}},
		{name: "missing_hint", hints: map[string]dbus.Variant{}},
		{name: "expired", expiry: -1},
		{name: "zero_expiry"},
		{name: "over_budget", expiry: 15001},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.app == "" {
				c.app = "agent-notifications"
			}
			if c.title == "" {
				c.title = "✅ [TEST completion]"
			}
			if c.body == "" {
				c.body = "Task completed"
			}
			if c.hints == nil {
				c.hints = map[string]dbus.Variant{"suppress-sound": dbus.MakeVariant(true)}
			}
			if c.expiry == 0 && c.name != "zero_expiry" {
				c.expiry = 14794
			}
			row := recordedNotify(t, c.app, c.replaces, c.icon, c.title, c.body, c.actions, c.hints, c.expiry)
			if row["valid"] != false {
				t.Fatal("invalid desktop contract accepted")
			}
			if _, stored := row["body"]; stored {
				t.Fatal("invalid payload retained")
			}
		})
	}
}
