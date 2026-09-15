package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
)

func TestConfigE2EFreshSharedContextsAndCreateOnly(t *testing.T) {
	f := newSetupE2E(t)
	legacy := filepath.Join(f.home, ".claude", "claude-notifications-go", "config.json")
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	bin := buildCLIBinary(t)
	before := e2eSnapshot(t, f.root)
	output, err := f.run(t, "", bin, "config", "path", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var selected config.Selection
	if err := json.Unmarshal([]byte(output), &selected); err != nil {
		t.Fatal(err)
	}
	if selected.Source != "universal" || selected.Exists || selected.Path == legacy {
		t.Fatalf("wrong fresh policy: %+v", selected)
	}
	if !reflect.DeepEqual(before, e2eSnapshot(t, f.root)) {
		t.Fatal("path wrote files")
	}
	output, err = f.run(t, "", bin, "config", "init", "--json")
	if err != nil {
		t.Fatalf("init: %v %s", err, output)
	}
	raw := e2eRead(t, selected.Path)
	info, err := os.Stat(selected.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{filepath.Join(f.root, "claude-bundle"), filepath.Join(f.root, "codex-bundle")} {
		context := f
		context.env = append(append([]string(nil), f.env...), "PLUGIN_ROOT="+root, "CLAUDE_PLUGIN_ROOT="+root)
		output, err = context.run(t, "", bin, "config", "path", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var actual config.Selection
		if err := json.Unmarshal([]byte(output), &actual); err != nil {
			t.Fatal(err)
		}
		if actual.Path != selected.Path || !actual.Exists {
			t.Fatal("resource context changed canonical")
		}
		if output, err = context.run(t, "", bin, "config", "init", "--json"); err != nil {
			t.Fatalf("noop: %v %s", err, output)
		}
	}
	after, err := os.Stat(selected.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(e2eRead(t, selected.Path)) || info.Mode() != after.Mode() || !info.ModTime().Equal(after.ModTime()) {
		t.Fatal("create-only init rewrote canonical")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("fresh created legacy")
	}
}

func TestConfigE2EInvalidCodexHasSafeLogAndNoDelivery(t *testing.T) {
	f := newSetupE2E(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	const secret = "CANARY_nested_header_payload_URL"
	canonical := filepath.Join(f.home, ".claude", "claude-notifications-go", "config.json")
	raw := []byte(`{"notifications":{"webhook":{"enabled":true,"url":"` + server.URL + `/` + secret + `"}},"future":{"` + secret + `":null}, broken`)
	e2eWrite(t, canonical, raw)
	f.env = append(f.env, "PLUGIN_ROOT="+f.bundle)
	payload := `{"session_id":"isolated","hook_event_name":"Stop","cwd":"` + filepath.ToSlash(f.root) + `","last_assistant_message":"done"}`
	result := runCLI(t, f.env, payload, "handle-hook", "Stop", "--product", "codex")
	assertContained(t, "invalid canonical", result)
	if calls.Load() != 0 {
		t.Fatal("invalid configuration sent webhook")
	}
	log := string(e2eRead(t, filepath.Join(f.bundle, "notification-debug.log")))
	if !strings.Contains(log, "ConfigInvalid") || strings.Contains(log, secret) || strings.Contains(log, server.URL) {
		t.Fatal("unsafe or missing diagnostic")
	}
	if string(e2eRead(t, canonical)) != string(raw) {
		t.Fatal("hook changed canonical")
	}
	if _, err := os.Stat(canonical + ".lock"); !os.IsNotExist(err) {
		t.Fatal("hook created lock")
	}
}

func TestConfigE2EExplicitImportAndWizardCAS(t *testing.T) {
	f := newSetupE2E(t)
	binary := buildCLIBinary(t)
	legacy := filepath.Join(f.home, ".claude", "claude-notifications-go", "config.json")
	legacyBytes := e2eRead(t, legacy)
	canonical := filepath.Join(f.root, "explicit", "config.json")
	source := filepath.Join(f.root, "historical.json")
	raw := `{"statuses":{"task_complete":{"sound":"${CLAUDE_PLUGIN_ROOT}/sounds/old.mp3"}},"notifications":{"webhook":{"enabled":true,"url":"${UNSET_SECRET_URL}","headers":{"CANARY_header":"CANARY_value"}}},"future":{"CANARY_nested":9007199254740993}}`
	e2eWrite(t, source, []byte(raw))
	f.env = append(f.env, "AGENT_NOTIFICATIONS_CONFIG="+canonical)
	out, err := f.run(t, "", binary, "config", "init", "--from", source, "--json")
	if err != nil {
		t.Fatalf("explicit import: %v %s", err, out)
	}
	out, err = f.run(t, "", binary, "config", "inspect", "--json")
	if err != nil || strings.Contains(out, "CANARY") || strings.Contains(out, "UNSET_SECRET_URL") {
		t.Fatalf("safe inspect: %v %s", err, out)
	}
	var inspection config.Inspection
	if err := json.Unmarshal([]byte(out), &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection.Selection.Source != "explicit" || inspection.Selection.Path != canonical || inspection.Revision == "" {
		t.Fatal("override/revision ignored")
	}
	edit := `{"set":{"/statuses/task_complete/sound":"${CLAUDE_PLUGIN_ROOT}/sounds/new.mp3","/notifications/desktop/volume":0.25}}`
	out, err = f.run(t, edit, binary, "config", "edit", "--stdin", "--expect-revision", inspection.Revision)
	if err != nil {
		t.Fatalf("wizard: %v %s", err, out)
	}
	after := e2eRead(t, canonical)
	for _, literal := range []string{"CANARY_header", "CANARY_value", "CANARY_nested", "9007199254740993", "${UNSET_SECRET_URL}", "${CLAUDE_PLUGIN_ROOT}/sounds/new.mp3"} {
		if !strings.Contains(string(after), literal) {
			t.Fatal("lost raw wizard field")
		}
	}
	out, err = f.run(t, edit, binary, "config", "edit", "--stdin", "--expect-revision", inspection.Revision)
	if err == nil || !strings.Contains(out, "ConfigConflict") {
		t.Fatalf("stale wizard: %v %s", err, out)
	}
	if string(e2eRead(t, canonical)) != string(after) || string(e2eRead(t, legacy)) != string(legacyBytes) || string(e2eRead(t, source)) != raw {
		t.Fatal("CAS/import changed unrelated bytes")
	}
}

// This fixture invokes only version/config and a deliberately invalid Codex hook.
// All homes, resource paths and the canonical document belong to t.TempDir.
func TestUniversalConfigE2ELaunchersAndSilentCodex(t *testing.T) {
	f := newSetupE2E(t)
	binary := buildCLIBinary(t)
	for _, name := range []string{"agent-notifications", "claude-notifications"} {
		launcher := filepath.Join(f.root, name)
		if runtime.GOOS == "windows" {
			launcher += ".exe"
			e2eWrite(t, launcher, e2eRead(t, binary))
		} else if err := os.Symlink(binary, launcher); err != nil {
			t.Fatal(err)
		}
		output, err := f.run(t, "", launcher, "version")
		if err != nil || !strings.HasPrefix(output, name+" v") {
			t.Fatalf("launcher %s: %s %v", name, output, err)
		}
		canonical := filepath.Join(f.home, ".claude", "claude-notifications-go", "config.json")
		raw := []byte(`{"schemaVersion":2,"agents":{"claude":{"notifications":{"desktop":{"volume":0}}},"codex":{"notifications":{"desktop":{"sound":false}}}}}`)
		e2eWrite(t, canonical, raw)
		output, err = f.run(t, "", launcher, "config", "inspect", "--json")
		if err != nil {
			t.Fatalf("inspect: %s %v", output, err)
		}
		var inspection config.Inspection
		if err := json.Unmarshal([]byte(output), &inspection); err != nil || !inspection.Valid || inspection.SchemaVersion != 2 {
			t.Fatalf("inspection: %s %v", output, err)
		}
		if !bytes.Equal(raw, e2eRead(t, canonical)) {
			t.Fatal("inspection rewrote config")
		}
	}
	canonical := filepath.Join(f.home, ".claude", "claude-notifications-go", "config.json")
	e2eWrite(t, canonical, []byte(`{"schemaVersion":2,"agents":{"claude":{"notifications":{"desktop":{"volume":2}}}}}`))
	f.env = append(f.env, "PLUGIN_ROOT="+f.bundle)
	result := runCLI(t, f.env, `{"session_id":"isolated","hook_event_name":"Stop","cwd":"`+filepath.ToSlash(f.root)+`"}`, "handle-hook", "Stop", "--product", "codex")
	assertContained(t, "invalid inactive schema2 profile", result)
	log := string(e2eRead(t, filepath.Join(f.bundle, "notification-debug.log")))
	if !strings.Contains(log, "ConfigInvalid") {
		t.Fatalf("missing ConfigInvalid diagnostic: %s", log)
	}
}
