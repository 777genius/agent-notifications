package qoderinstall

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"
	"io"
	"os"
	"os/exec"
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
	if len(args) < 5 || args[0] != "--config-dir" || args[2] != "plugins" {
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
	if args[3] == "list" {
		entries := []map[string]string{}
		for id, raw := range plugins {
			var rows []struct{ Version string }
			if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
				fail(fmt.Errorf("ambiguous registry"))
			}
			entries = append(entries, map[string]string{"id": id, "version": rows[0].Version})
		}
		_ = json.NewEncoder(os.Stdout).Encode(entries)
		os.Exit(0)
	}
	result := map[string]any{"ok": true, "pluginId": PluginID}
	switch args[3] {
	case "install":
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
			defer func() { _ = in.Close() }()
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
	case "uninstall":
		if raw := plugins[PluginID]; len(raw) > 0 {
			var rows []struct{ InstallPath string }
			if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 || !inside(profile, rows[0].InstallPath) {
				fail(fmt.Errorf("invalid native package path"))
			}
			if err := os.RemoveAll(rows[0].InstallPath); err != nil {
				fail(err)
			}
		}
		delete(plugins, PluginID)
		delete(enabled, PluginID)
		result["fullyRemoved"] = true
	default:
		fail(fmt.Errorf("unsupported native fixture action"))
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
	r := Request{Action: Install, ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "runtime"), HomeDir: filepath.Join(root, "home"), ConfigRoot: filepath.Join(root, "profile"), QoderExecutable: wrapper, BinarySource: exe, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Version: "1.49.5", Webhook: true, Runner: nativeTestRunner{}}
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
	if old.InstallationID == "" || old.UAPBindingID == "" || old.Pending {
		t.Fatalf("UAP identity missing: %+v", old)
	}
	for path := range old.Files {
		if _, owned := installruntime.OwnedFile(l, path); owned {
			t.Fatalf("native cache has two owners: %s", path)
		}
	}
	snapshot, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	desktop, webhook := ChannelsFromSnapshot(snapshot, r.ControlRoot, old.Executable, old.Binding)
	if desktop || !webhook {
		t.Fatal("UAP-bound receipt does not authorize selected delivery")
	}
	// Updating the package is UAP's lifecycle, including native replacement.
	r.Action = Update
	r.Version = "1.49.6"
	mustApply(t, ctx, r)
	if _, err := os.Stat(old.PackageRoot); !os.IsNotExist(err) {
		t.Fatal("native previous package survived update")
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

// Preserve the real CLI argv/environment and filesystem effects without using
// the production containment transport in a unit fixture.
type nativeTestRunner struct{ dir string }

func (runner nativeTestRunner) Run(ctx context.Context, command ports.Command) (ports.CommandResult, error) {
	child := exec.CommandContext(ctx, command.Argv[0], command.Argv[1:]...)
	child.Env, child.Dir = command.Env, command.Dir
	if runner.dir != "" {
		child.Dir = runner.dir
	}
	out, err := child.Output()
	result := ports.CommandResult{Stdout: out}
	if e, ok := err.(*exec.ExitError); ok {
		result.ExitCode = e.ExitCode()
		result.Stderr = e.Stderr
		return result, nil
	}
	return result, err
}

func TestQoderRemovalRetryAfterUAPCompletion(t *testing.T) {
	r, ctx := fixture(t)
	mustApply(t, ctx, r)
	r.Action = Remove
	r.Fault = func(point string) error {
		if point == "qoder_uap_removed" {
			return fmt.Errorf("TEST interruption")
		}
		return nil
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("fault did not interrupt removal")
	}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	old, err := readReceipt(r.ControlRoot, l)
	if err != nil || !old.Pending || !old.Removing {
		t.Fatalf("removal intent missing: %+v %v", old, err)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if desktop, webhook := ChannelsFromSnapshot(s, r.ControlRoot, old.Executable, old.Binding); desktop || webhook {
		t.Fatal("interrupted removal retained consent")
	}
	r.Fault = nil
	mustApply(t, ctx, r)
	l, _, _ = installruntime.ReadOwnership(r.ControlRoot)
	if _, ok := l.Consumers[ConsumerID]; ok {
		t.Fatal("retry did not retire host consumer")
	}
}

func TestQoderPendingNativeEditRefusesRetry(t *testing.T) {
	for _, extra := range []bool{false, true} {
		t.Run(fmt.Sprintf("extra_%t", extra), func(t *testing.T) {
			r, ctx := fixture(t)
			r.Fault = func(point string) error {
				if point == "qoder_uap_installed" {
					return fmt.Errorf("TEST interruption")
				}
				return nil
			}
			if err := Apply(ctx, r); err == nil {
				t.Fatal("fault did not interrupt handoff")
			}
			l, _, _ := installruntime.ReadOwnership(r.ControlRoot)
			old, err := readReceipt(r.ControlRoot, l)
			if err != nil || !old.Pending {
				t.Fatalf("pending receipt: %+v %v", old, err)
			}
			_, _, root, err := nativeRegistration(r.ConfigRoot)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "README.md")
			if extra {
				path = filepath.Join(root, "user.txt")
			}
			content := []byte("TEST user change after native activation")
			if err = os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			r.Fault = nil
			if err = Apply(ctx, r); err == nil {
				t.Fatal("pending retry deleted an unowned or edited native file")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != string(content) {
				t.Fatal("refused retry changed native file")
			}
			s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			if desktop, webhook := ChannelsFromSnapshot(s, r.ControlRoot, old.Executable, old.Binding); desktop || webhook {
				t.Fatal("incomplete handoff enabled consent")
			}
		})
	}
}

func TestQoderUpdatePreservesOmittedChannelsAndConsentCAS(t *testing.T) {
	r, ctx := fixture(t)
	r.Desktop, r.Webhook = false, false
	mustApply(t, ctx, r)
	r.Action, r.PreserveChannels = Update, true
	r.Desktop, r.Webhook = true, true
	mustApply(t, ctx, r)
	status, err := Inspect(ctx, r)
	if err != nil || status.Desktop || status.Webhook {
		t.Fatalf("both-off was lost: %+v %v", status, err)
	}
	r.PreserveChannels = false
	r.Webhook = true
	mustApply(t, ctx, r)
	r.Desktop, r.Webhook = false, false
	r.Fault = func(point string) error {
		if point != "qoder_consent_revoked" {
			return nil
		}
		return setChannels(ctx, r.ControlRoot, r.RuntimeRoot, true, false, "")
	}
	if err = Apply(ctx, r); err == nil || !strings.Contains(err.Error(), "concurrent_change") {
		t.Fatalf("concurrent consent was adopted: %v", err)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	desktop, webhook, err := channels(s.Fields)
	if err != nil || !desktop || webhook {
		t.Fatal("user's concurrent consent was overwritten")
	}
}

func TestQoderNativeCLIInstalledLifecycle(t *testing.T) {
	executable := os.Getenv("AGENT_NOTIFY_QODER_NATIVE_EXE")
	if executable == "" {
		t.Skip("explicit pinned disposable Qoder CLI required")
	}
	r, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	r.QoderExecutable = executable
	isolated := t.TempDir()
	project := filepath.Join(isolated, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	r.Runner = nativeTestRunner{dir: project}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		dir := filepath.Join(isolated, key)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	t.Setenv("HOME", isolated)
	t.Setenv("USERPROFILE", isolated)
	t.Setenv("QODER_CONFIG_DIR", filepath.Join(isolated, "ambient-default"))
	mustApply(t, ctx, r)
	status, err := Inspect(ctx, r)
	if err != nil || status.Status != "installed" || !status.Webhook {
		t.Fatalf("native installed result: %+v %v", status, err)
	}
	r.Action = Update
	r.Version = "1.49.6"
	mustApply(t, ctx, r)
	r.Action = Remove
	mustApply(t, ctx, r)
	if _, err = os.Stat(filepath.Join(isolated, "ambient-default")); !os.IsNotExist(err) {
		t.Fatal("native lifecycle touched ambient profile")
	}
}

func TestQoderPendingUpdateBeforeNativeEffectsCanRetry(t *testing.T) {
	r, ctx := fixture(t)
	mustApply(t, ctx, r)
	r.Action, r.Version = Update, "1.49.6"
	r.Fault = func(point string) error {
		if point == "qoder_runtime_prepared" {
			return fmt.Errorf("TEST interruption before native effects")
		}
		return nil
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("fault did not interrupt update")
	}
	r.Fault = nil
	mustApply(t, ctx, r)
	status, err := Inspect(ctx, r)
	if err != nil || !status.Webhook {
		t.Fatalf("pending update did not resume: %+v %v", status, err)
	}
}

func TestQoderFreshInstallRefusesOrphanNativeCache(t *testing.T) {
	r, ctx := fixture(t)
	root := filepath.Join(r.ConfigRoot, "plugins", "cache", "local", "agent-notifications-qoder", r.Version)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "user.txt")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err == nil || !strings.Contains(err.Error(), "without managed ownership") {
		t.Fatalf("orphan cache was adopted: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "keep" {
		t.Fatal("orphan cache was changed")
	}
	if _, err := os.Stat(filepath.Join(r.ConfigRoot, filepath.FromSlash(registryName))); !os.IsNotExist(err) {
		t.Fatal("native CLI was dispatched before orphan refusal")
	}
}

func TestQoderRetiredReceiptIsRefusedWithoutAdoption(t *testing.T) {
	r, ctx := fixture(t)
	mustApply(t, ctx, r)
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	old, err := readReceipt(r.ControlRoot, l)
	if err != nil {
		t.Fatal(err)
	}
	old.Schema = 1
	path := filepath.Join(r.ControlRoot, receiptName)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: ConsumerID, RefreshOnly: true, ExpectedGeneration: &l.Generation, Files: []installruntime.File{{Path: path, Before: before, Data: mustJSON(old), Mode: 0600}}}); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(r.ConfigRoot, filepath.FromSlash(registryName))
	nativeBefore, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{Update, Remove} {
		r.Action = action
		if err = Apply(ctx, r); err == nil || !strings.Contains(err.Error(), "legacy installation adoption is not supported") {
			t.Fatalf("retired receipt was adopted by %s: %v", action, err)
		}
	}
	nativeAfter, err := os.ReadFile(registry)
	if err != nil || string(nativeAfter) != string(nativeBefore) {
		t.Fatal("refusing retired receipt changed native registration")
	}
}
