package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/codexsetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestShellAndSetupShareOwnership(t *testing.T) {
	root := t.TempDir()
	control := filepath.Join(root, "control")
	bundle := filepath.Join(root, "bundle")
	stage := filepath.Join(root, "stage")
	entry := "claude-notifications-linux-amd64"
	if runtime.GOOS == "windows" {
		entry = "claude-notifications-windows-" + runtime.GOARCH + ".exe"
	}
	for _, path := range []string{stage, filepath.Join(bundle, "bin")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, entry), []byte("inert sender "+installruntime.WriterProtocolMarker), 0755); err != nil {
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
	if err := installRuntime([]string{"--refresh", "--stage", installedBin, "--target", installedBin, "--entry", entry, "--control-root", control}, io.Discard); err != nil {
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
	if _, err := os.Stat(filepath.Join(bundle, "bin", entry)); err != nil {
		t.Fatal("consumer removal deleted shared runtime")
	}
	if err := installRuntime([]string{"--remove", "--target", filepath.Join(bundle, "bin"), "--control-root", control}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "bin", entry)); !os.IsNotExist(err) {
		t.Fatal("final consumer retained owned binary")
	}
}

func TestInstallRuntimeVersionedClaudeCacheRelocation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	control := filepath.Join(root, "control")
	entry := "claude-notifications-linux-amd64"
	if runtime.GOOS == "windows" {
		entry = "claude-notifications-windows-amd64.exe"
	}
	install := func(version, content string, relocate bool) error {
		stage := filepath.Join(root, "stage-"+version)
		if err := os.MkdirAll(stage, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, entry), []byte(content+installruntime.WriterProtocolMarker), 0700); err != nil {
			return err
		}
		args := []string{"--stage", stage, "--target", filepath.Join(cache, version, "bin"), "--entry", entry, "--control-root", control}
		if relocate {
			args = append(args, "--relocate-versioned-cache")
		}
		return installRuntime(args, io.Discard)
	}
	if err := install("1.45.7", "old", false); err != nil {
		t.Fatal(err)
	}
	oldRoot, newRoot := filepath.Join(cache, "1.45.7"), filepath.Join(cache, "1.45.12")
	oldPrimary := filepath.Join(oldRoot, "bin", entry)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "portable:existing",
		Consumer: installruntime.Consumer{Commands: []string{oldPrimary}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := install("1.45.12", "new", false); err == nil {
		t.Fatal("silent cache relocation")
	}
	if err := install("1.45.12", "new", true); err != nil {
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
	if ledger.Consumers["claude-hooks"].RuntimeRoot != newRoot || ledger.Consumers["portable:existing"].RuntimeRoot != oldRoot || ledger.RuntimeRoot != oldRoot ||
		!ledger.Files[oldPrimary].Exists || !ledger.Files[filepath.Join(newRoot, "bin", entry)].Exists {
		t.Fatalf("unexpected ownership after cache relocation: %+v", ledger)
	}
	if contents, err := os.ReadFile(oldPrimary); err != nil || string(contents) != "new"+installruntime.WriterProtocolMarker {
		t.Fatalf("retained portable primary was not upgraded: %q, %v", contents, err)
	}
}

// Reproduces #278: Claude restored the retired cache from the marketplace
// checkout, so the downloaded binary is gone, the skill carries the checkout's
// mode and the launcher points at the repository's relative link.
func TestInstallRuntimeRelocatesFromRestoredVersionedCache(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	control := filepath.Join(root, "control")
	entry := "claude-notifications-linux-amd64"
	if runtime.GOOS == "windows" {
		entry = "claude-notifications-windows-amd64.exe"
	}
	install := func(version, content string, relocate bool) error {
		stage := filepath.Join(root, "stage-"+version)
		if err := os.MkdirAll(stage, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, entry), []byte(content+installruntime.WriterProtocolMarker), 0700); err != nil {
			return err
		}
		args := []string{"--stage", stage, "--target", filepath.Join(cache, version, "bin"), "--entry", entry, "--control-root", control}
		if relocate {
			args = append(args, "--relocate-versioned-cache")
		}
		return installRuntime(args, io.Discard)
	}
	if err := install("1.45.18", "old", false); err != nil {
		t.Fatal(err)
	}
	oldRoot, newRoot := filepath.Join(cache, "1.45.18"), filepath.Join(cache, "1.46.0")
	oldSkill := filepath.Join(oldRoot, "skills", "agent-notifications", "SKILL.md")
	oldLauncher := filepath.Join(oldRoot, "bin", "agent-notifications")
	if err := os.Remove(filepath.Join(oldRoot, "bin", entry)); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows launchers are scripts and modes are normalized; drift the bytes.
		if err := os.WriteFile(oldSkill, []byte("checkout"), 0600); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.Chmod(oldSkill, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(oldLauncher); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("claude-notifications", oldLauncher); err != nil {
			t.Fatal(err)
		}
	}
	if err := install("1.46.0", "new", true); err != nil {
		t.Fatalf("restored old cache blocked the update: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ledger installruntime.Ledger
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatal(err)
	}
	newSkill := filepath.Join(newRoot, "skills", "agent-notifications", "SKILL.md")
	if ledger.RuntimeRoot != newRoot || ledger.Consumers["claude-hooks"].RuntimeRoot != newRoot ||
		!ledger.Files[filepath.Join(newRoot, "bin", entry)].Exists || !ledger.Files[newSkill].Exists {
		t.Fatalf("unexpected ownership after relocation: %+v", ledger)
	}
	for path := range ledger.Files {
		if strings.HasPrefix(path, oldRoot+string(filepath.Separator)) {
			t.Fatalf("retired cache still owned: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(oldRoot, "bin", entry)); !os.IsNotExist(err) {
		t.Fatal("retired cache binary republished")
	}
	if runtime.GOOS == "windows" {
		return
	}
	if target, err := os.Readlink(oldLauncher); err != nil || target != "claude-notifications" {
		t.Fatalf("retired launcher changed: %q, %v", target, err)
	}
	if info, err := os.Stat(oldSkill); err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("retired skill changed: %v, %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(newRoot, "bin", "agent-notifications")); err != nil || target != entry {
		t.Fatalf("new launcher not published: %q, %v", target, err)
	}
	if info, err := os.Stat(newSkill); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("new skill not published: %v, %v", info, err)
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
			if !windows && runtime.GOOS == "windows" {
				t.Skip("unix symlink aliases")
			}
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
