package qoderinstall

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

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// This fixture models only the native CLI boundary. The assertions exercise
// the real transaction kernel, ownership ledger, consent and profile bytes.
func TestQoderNativeProcess(t *testing.T) {
	args := os.Args
	marker := -1
	for i, arg := range args {
		if arg == "qoder-native-fixture" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args = args[marker+1:]
	fail := func(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(3) }
	if len(args) < 7 || args[0] != "--config-dir" || args[2] != "plugins" {
		fail(fmt.Errorf("invalid native invocation"))
	}
	profile := args[1]
	regPath := filepath.Join(profile, filepath.FromSlash(registryName))
	settingsPath := filepath.Join(profile, "settings.json")
	read := func(path string) map[string]json.RawMessage {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return map[string]json.RawMessage{}
		}
		if err != nil {
			fail(err)
		}
		result, err := object(data)
		if err != nil {
			fail(err)
		}
		return result
	}
	reg := read(regPath)
	plugins, _ := object(reg["plugins"])
	settings := read(settingsPath)
	enabled, _ := object(settings["enabledPlugins"])
	// An external uninstall can write its referenced path. A live path here
	// would make the isolated-planning test fail before it can touch anything.
	if previous := plugins[PluginID]; len(previous) > 0 {
		var entries []struct {
			InstallPath string `json:"installPath"`
		}
		if json.Unmarshal(previous, &entries) != nil || len(entries) != 1 || !inside(profile, entries[0].InstallPath) {
			fail(fmt.Errorf("native planner exposed live cache path"))
		}
		if args[3] == "uninstall" {
			if err := os.WriteFile(filepath.Join(entries[0].InstallPath, ".orphaned_at"), []byte("native"), 0600); err != nil {
				fail(err)
			}
		}
	}
	result := map[string]any{"ok": true, "pluginId": PluginID}
	if args[3] == "install" {
		source := args[4]
		manifest := read(filepath.Join(source, ".qoder-plugin", "plugin.json"))
		var version string
		_ = json.Unmarshal(manifest["version"], &version)
		target := filepath.Join(profile, "plugins", "cache", "local", "agent-notifications-qoder", version)
		err := filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(source, path)
			to := filepath.Join(target, rel)
			if d.IsDir() {
				return os.MkdirAll(to, 0700)
			}
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			info, _ := d.Info()
			out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				return err
			}
			_, err = io.Copy(out, in)
			closeErr := out.Close()
			if err != nil {
				return err
			}
			return closeErr
		})
		if err != nil {
			fail(err)
		}
		plugins[PluginID] = mustJSON([]any{map[string]string{"scope": "user", "installPath": target, "version": version, "installedAt": "TEST-fixed-time"}})
		enabled[PluginID] = json.RawMessage("true")
		result["path"] = target
		result["version"] = version
	} else {
		delete(plugins, PluginID)
		delete(enabled, PluginID)
		result["fullyRemoved"] = true
	}
	reg["version"] = json.RawMessage("2")
	reg["plugins"] = mustJSON(plugins)
	settings["enabledPlugins"] = mustJSON(enabled)
	if err := os.MkdirAll(filepath.Dir(regPath), 0700); err != nil {
		fail(err)
	}
	if err := os.WriteFile(regPath, mustJSON(reg), 0600); err != nil {
		fail(err)
	}
	if err := os.WriteFile(settingsPath, mustJSON(settings), 0600); err != nil {
		fail(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func fixture(t *testing.T) (Request, context.Context) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell native fixture requires POSIX host")
	}
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(root, "native qoder")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestQoderNativeProcess$ -- qoder-native-fixture \"$@\"\n"
	if err = os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	r := Request{Action: Install, ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "runtime"), HomeDir: filepath.Join(root, "home"), ConfigRoot: filepath.Join(root, "profile"), QoderExecutable: wrapper, BinarySource: exe, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Version: "1.49.5", Webhook: true}
	r, err = normalize(r)
	if err != nil {
		t.Fatal(err)
	}
	r.RuntimeRoot, err = installruntime.CanonicalPath(r.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(r.ConfigRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(r.ConfigRoot, "settings.json"), []byte(`{"editor":{"keep":true},"enabledPlugins":{"foreign@local":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	return r, ctx
}
func mustApply(t *testing.T, ctx context.Context, r Request) {
	t.Helper()
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
}

func TestQoderLifecyclePreservesForeignProfileAndRetiresOwnedFiles(t *testing.T) {
	r, ctx := fixture(t)
	mustApply(t, ctx, r)
	for _, rel := range []string{"settings.json", registryName} {
		path := filepath.Join(r.ConfigRoot, filepath.FromSlash(rel))
		if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
			t.Fatalf("installer occupied native host lock: %s %v", path, err)
		}
		if info, err := os.Stat(path + ".agent-notifications-qoder.lock"); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("separate installer coordination lock missing: %s %v", path, err)
		}
	}
	status, err := Inspect(ctx, r)
	if err != nil || status.Status != "installed" || !status.Webhook || status.Desktop {
		t.Fatalf("inspection: %+v %v", status, err)
	}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := sharedBinaryName(r.GOOS, r.GOARCH)
	if _, ok := installruntime.OwnedFile(l, filepath.Join(r.RuntimeRoot, name)); !ok {
		t.Fatal("shared host executable is not ledger-owned")
	}
	old, err := readReceipt(r.ControlRoot, l)
	if err != nil {
		t.Fatal(err)
	}
	// Model a file shipped by the preceding package at the same version/root.
	legacy := filepath.Join(old.PackageRoot, "retired-owned.txt")
	old.Files = append(old.Files, legacy)
	before, err := installruntime.Fingerprint(filepath.Join(r.ControlRoot, receiptName))
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: ConsumerID, RefreshOnly: true, ExpectedGeneration: &l.Generation, Files: []installruntime.File{{Path: legacy, Data: []byte("prior shipped file"), Mode: 0600}, {Path: filepath.Join(r.ControlRoot, receiptName), Before: before, Data: mustJSON(old), Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	r.Action = Update
	mustApply(t, ctx, r)
	if _, err = os.Lstat(legacy); !os.IsNotExist(err) {
		t.Fatal("retired same-version shipped file survived")
	}
	// A sibling keeps the shared runtime alive during Qoder removal.
	l, _, _ = installruntime.ReadOwnership(r.ControlRoot)
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: "TEST-sibling", ExpectedGeneration: &l.Generation})
	if err != nil {
		t.Fatal(err)
	}
	r.Action = Remove
	mustApply(t, ctx, r)
	if _, err = os.Lstat(filepath.Join(r.RuntimeRoot, name)); err != nil {
		t.Fatal("Qoder removal deleted sibling runtime")
	}
	if _, err = os.Lstat(filepath.Join(old.PackageRoot, ".orphaned_at")); !os.IsNotExist(err) {
		t.Fatal("native staging wrote into live package")
	}
	data, err := os.ReadFile(filepath.Join(r.ConfigRoot, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := object(data)
	enabled, _ := object(settings["enabledPlugins"])
	if compact(settings["editor"]) != `{"keep":true}` || compact(enabled["foreign@local"]) != "false" || len(enabled[PluginID]) != 0 {
		t.Fatalf("foreign settings lost: %s", data)
	}
	if _, err = os.Lstat(filepath.Join(r.ControlRoot, receiptName)); !os.IsNotExist(err) {
		t.Fatal("receipt was not retired")
	}
	l, _, _ = installruntime.ReadOwnership(r.ControlRoot)
	if _, ok := l.Consumers[ConsumerID]; ok {
		t.Fatal("consumer was not retired")
	}
}

func TestQoderLifecycleRefusesUserEditedAndUnownedPackage(t *testing.T) {
	for _, kind := range []string{"edited", "unowned", "native-entry", "native-enabled", "stale-channel"} {
		t.Run(kind, func(t *testing.T) {
			r, ctx := fixture(t)
			mustApply(t, ctx, r)
			l, _, _ := installruntime.ReadOwnership(r.ControlRoot)
			old, err := readReceipt(r.ControlRoot, l)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(old.PackageRoot, "README.md")
			content := []byte("user edit")
			if kind == "unowned" {
				path = filepath.Join(old.PackageRoot, "user.txt")
			}
			registryPath := filepath.Join(r.ConfigRoot, filepath.FromSlash(registryName))
			registryBefore, _ := os.ReadFile(registryPath)
			if kind == "native-entry" {
				doc, _ := object(registryBefore)
				plugins, _ := object(doc["plugins"])
				plugins[PluginID] = mustJSON([]any{map[string]string{"scope": "user", "installPath": old.PackageRoot, "version": "user-edited"}})
				doc["plugins"] = mustJSON(plugins)
				registryBefore = mustJSON(doc)
				if err = os.WriteFile(registryPath, registryBefore, 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "native-enabled" {
				settingsPath := filepath.Join(r.ConfigRoot, "settings.json")
				data, _ := os.ReadFile(settingsPath)
				doc, _ := object(data)
				enabled, _ := object(doc["enabledPlugins"])
				enabled[PluginID] = json.RawMessage("false")
				doc["enabledPlugins"] = mustJSON(enabled)
				if err = os.WriteFile(settingsPath, mustJSON(doc), 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "stale-channel" {
				r.ChannelPreimage = "absent"
			} else if err = os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			r.Action = Update
			if err = Apply(ctx, r); err == nil {
				t.Fatal("unsafe update succeeded")
			}
			after, _ := os.ReadFile(registryPath)
			if string(after) != string(registryBefore) {
				t.Fatal("refused update changed native registration")
			}
			if kind == "edited" || kind == "unowned" {
				data, _ := os.ReadFile(path)
				if string(data) != string(content) {
					t.Fatal("user file was changed")
				}
				r.Action = Remove
				if err = Apply(ctx, r); err == nil {
					t.Fatal("unsafe removal succeeded")
				}
			}
			if kind == "native-entry" || kind == "native-enabled" {
				r.Action = Remove
				if err = Apply(ctx, r); err == nil {
					t.Fatal("removal overwrote user-edited native registration")
				}
				after, _ = os.ReadFile(registryPath)
				if string(after) != string(registryBefore) {
					t.Fatal("refused removal changed native registry")
				}
			}
		})
	}
}
