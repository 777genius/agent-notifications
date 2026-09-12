package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/codexsetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestShellAndSetupShareOwnership(t *testing.T) {
	root := t.TempDir()
	control := filepath.Join(root, "control")
	bundle := filepath.Join(root, "bundle")
	stage := filepath.Join(root, "stage")
	for _, path := range []string{stage, filepath.Join(bundle, "bin")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "claude-notifications-linux-amd64"), []byte("inert sender "+installruntime.WriterProtocolMarker), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex-hook-wrapper.sh", "codex-hook-wrapper.cmd"} {
		if err := os.WriteFile(filepath.Join(bundle, "bin", name), []byte("inert wrapper"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := installRuntime([]string{"--stage", stage, "--target", filepath.Join(bundle, "bin"), "--control-root", control}, io.Discard); err != nil {
		t.Fatal(err)
	}
	opts := codexsetup.Options{ControlRoot: control, PluginRoot: bundle, CodexHome: filepath.Join(root, "codex")}
	if _, err := codexsetup.Run(opts); err != nil {
		t.Fatal(err)
	}
	// A Codex lazy update refreshes the same registration; it must not invent
	// a standalone Claude consumer for the copied runtime.
	installedBin := filepath.Join(opts.CodexHome, codexsetup.InstallDirName, "bin")
	if err := installRuntime([]string{"--refresh", "--stage", installedBin, "--target", installedBin, "--entry", "claude-notifications-linux-amd64", "--control-root", control}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ledger installruntime.Ledger
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatal(err)
	}
	canonicalBundle, err := installruntime.CanonicalPath(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Consumers) != 2 || ledger.RuntimeRoot != canonicalBundle {
		t.Fatalf("split ownership: %+v", ledger)
	}
	opts.Remove = true
	if _, err := codexsetup.Run(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "bin", "claude-notifications-linux-amd64")); err != nil {
		t.Fatal("consumer removal deleted shared runtime")
	}
	if err := installRuntime([]string{"--remove", "--target", filepath.Join(bundle, "bin"), "--control-root", control}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "bin", "claude-notifications-linux-amd64")); !os.IsNotExist(err) {
		t.Fatal("final consumer retained owned binary")
	}
}

func TestWindowsManagedHooksPreserveForeignOnRemove(t *testing.T) {
	root := t.TempDir()
	stage, bin := filepath.Join(root, "stage"), filepath.Join(root, "plugin", "bin")
	hooks := filepath.Join(root, "plugin", "hooks", "hooks.json")
	for _, dir := range []string{stage, filepath.Dir(hooks)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	name := "claude-notifications-windows-amd64.exe"
	if err := os.WriteFile(filepath.Join(stage, name), []byte("inert executable "+installruntime.WriterProtocolMarker), 0755); err != nil {
		t.Fatal(err)
	}
	input := `{"foreignTop":{"future":true},"hooks":{"Other":null,"Stop":[{"matcher":"","annotation":{"keep":true},"hooks":[{"type":"command","command":"sh","args":["${CLAUDE_PLUGIN_ROOT}/bin/hook-wrapper.sh","handle-hook","Stop"],"timeout":30},{"command":"foreign","future":null}]}]}}`
	if err := os.WriteFile(hooks, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(root, "control")
	if err := installRuntime([]string{"--stage", stage, "--target", bin, "--entry", name, "--control-root", control}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	var installed map[string]json.RawMessage
	if err := json.Unmarshal(data, &installed); err != nil {
		t.Fatal(err)
	}
	installed["laterForeignEdit"] = json.RawMessage(`{"enabled":false}`)
	data, err = json.Marshal(installed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := installRuntime([]string{"--remove", "--target", bin, "--entry", name, "--control-root", control}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["foreignTop"] == nil || got["laterForeignEdit"] == nil {
		t.Fatal("lost foreign top-level fields")
	}
	events := got["hooks"].(map[string]any)
	if value, exists := events["Other"]; !exists || value != nil {
		t.Fatal("lost null foreign event")
	}
	group := events["Stop"].([]any)[0].(map[string]any)
	if group["annotation"] == nil || group["matcher"] != "" {
		t.Fatal("lost foreign group fields")
	}
	commands := group["hooks"].([]any)
	if len(commands) != 1 || commands[0].(map[string]any)["command"] != "foreign" {
		t.Fatalf("wrong remaining handlers: %s", data)
	}
	for _, name := range []string{name, "claude-notifications.bat", "agent-notifications.bat"} {
		if _, err := os.Stat(filepath.Join(bin, name)); !os.IsNotExist(err) {
			t.Fatal("owned Windows launcher survived final removal")
		}
	}
}

func TestManagedAliasAndMalformedConfigBoundaries(t *testing.T) {
	for _, windows := range []bool{false, true} {
		t.Run(fmt.Sprint(windows), func(t *testing.T) {
			root := t.TempDir()
			stage, bin := filepath.Join(root, "stage"), filepath.Join(root, "plugin", "bin")
			if err := os.MkdirAll(stage, 0700); err != nil {
				t.Fatal(err)
			}
			name := "claude-notifications-linux-amd64"
			if windows {
				name = "claude-notifications-windows-amd64.exe"
			}
			if err := os.WriteFile(filepath.Join(stage, name), []byte("inert "+installruntime.WriterProtocolMarker), 0755); err != nil {
				t.Fatal(err)
			}
			hooks := filepath.Join(root, "plugin", "hooks", "hooks.json")
			if windows {
				if err := os.MkdirAll(filepath.Dir(hooks), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(hooks, []byte("{malformed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			control := filepath.Join(root, "control")
			err := installRuntime([]string{"--stage", stage, "--target", bin, "--entry", name, "--control-root", control}, io.Discard)
			if windows {
				if err == nil {
					t.Fatal("malformed config accepted")
				}
				if _, err := os.Stat(filepath.Join(bin, name)); !os.IsNotExist(err) {
					t.Fatal("sender promoted before config refusal")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, launcher := range []string{"claude-notifications", "agent-notifications"} {
				if link, err := os.Readlink(filepath.Join(bin, launcher)); err != nil || link != name {
					t.Fatalf("stable alias %s: %q %v", launcher, link, err)
				}
			}
			if err := installRuntime([]string{"--remove", "--target", bin, "--control-root", control}, io.Discard); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(bin, "claude-notifications")); !os.IsNotExist(err) {
				t.Fatal("managed alias survived final removal")
			}
		})
	}
}
