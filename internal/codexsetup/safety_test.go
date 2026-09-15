package codexsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestForeignEmptyAndMatcherPreserved(t *testing.T) {
	for _, input := range []string{
		`{"hooks":{"Other":[],"Null":null,"Stop":[{"matcher":"","hooks":[]},{"matcher":{"future":true},"hooks":null},{"annotation":true},null]}}`,
	} {
		var before hooksFile
		if err := json.Unmarshal([]byte(input), &before); err != nil {
			t.Fatal(err)
		}
		merged, _, _ := mergeHooks(before, t.TempDir())
		encoded, err := json.Marshal(merged)
		if err != nil {
			t.Fatal(err)
		}
		var original, actual map[string]any
		if err := json.Unmarshal([]byte(input), &original); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &actual); err != nil {
			t.Fatal(err)
		}
		oldHooks := original["hooks"].(map[string]any)
		newHooks := actual["hooks"].(map[string]any)
		for event, groups := range oldHooks {
			got := newHooks[event]
			if event == "Stop" {
				got = got.([]any)[:len(groups.([]any))]
			}
			if !reflect.DeepEqual(groups, got) {
				t.Fatalf("%s: %v became %v", event, groups, got)
			}
		}
	}
}

// Lazy download and version refresh must remain available in the stable copy.
func TestCopyPreservesUpdaterAssets(t *testing.T) {
	src, dst := fakeBundle(t), t.TempDir()
	files := map[string]string{
		".claude-plugin/plugin.json":                       `{"version":"1.42.0"}`,
		".claude-plugin/private-note":                      "not a runtime asset",
		"bin/install.sh":                                   "#!/bin/sh\nexit 0\n",
		"bin/bootstrap.sh":                                 "not used by the Codex launcher",
		"bin/claude-notifications-windows-amd64-focus.exe": "amd64 GUI focus helper",
		"bin/claude-notifications-windows-arm64-focus.exe": "arm64 GUI focus helper",
		"bin/claude-notifications-linux-amd64-focus.exe":   "not a supported helper",
	}
	for name, content := range files {
		path := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyBundle(src, dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".claude-plugin/plugin.json", "bin/install.sh", "bin/claude-notifications-windows-amd64-focus.exe", "bin/claude-notifications-windows-arm64-focus.exe"} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(name)))
		if err != nil || string(got) != files[name] {
			t.Fatalf("runtime asset %s missing or altered: %v", name, err)
		}
	}
	for _, name := range []string{".claude-plugin/private-note", "bin/bootstrap.sh", "bin/claude-notifications-linux-amd64-focus.exe"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Fatalf("unexpected asset %s: %v", name, err)
		}
	}
}

func TestCopyFailureLeavesLiveAssets(t *testing.T) {
	src, dst := fakeBundle(t), t.TempDir()
	if err := copyBundle(src, dst); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dst, "bin", "codex-hook-wrapper.sh")
	before, _ := os.ReadFile(live)
	if err := os.WriteFile(filepath.Join(src, "bin", "codex-hook-wrapper.sh"), []byte("new"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(src, "sounds", "broken")); err != nil {
		t.Skip(err)
	}
	if err := copyBundle(src, dst); err == nil {
		t.Fatal("expected staging failure")
	}
	after, _ := os.ReadFile(live)
	if string(before) != string(after) {
		t.Fatal("live bundle changed on copy failure")
	}
}

func TestCopyRejectsOverlapAndAliases(t *testing.T) {
	src := fakeBundle(t)
	for _, dst := range []string{src, filepath.Join(src, "nested"), filepath.Dir(src)} {
		if err := copyBundle(src, dst); err == nil {
			t.Fatalf("accepted overlap %s", dst)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Skip(err)
	}
	if err := copyBundle(src, alias); err == nil {
		t.Fatal("accepted alias")
	}
	if err := copyBundle(src, filepath.Join(alias, "new")); err == nil {
		t.Fatal("accepted nested alias")
	}
}

func TestRuntimeSelectionAndRollback(t *testing.T) {
	src, dst := fakeBundle(t), t.TempDir()
	for _, name := range []string{".env", "notification-debug.log", ".claude/worktrees/secret", "bin/private-token"} {
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("secret fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyBundle(src, dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", "notification-debug.log", ".claude", "bin/private-token"} {
		if _, err := os.Lstat(filepath.Join(dst, name)); !os.IsNotExist(err) {
			t.Fatalf("copied %s", name)
		}
	}
	path := filepath.Join(dst, "bin", "codex-hook-wrapper.sh")
	old, _ := os.ReadFile(path)
	if err := os.WriteFile(filepath.Join(src, "bin", "codex-hook-wrapper.sh"), []byte("replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	rollback, finish, err := stageBundle(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(path)
	if string(restored) != string(old) {
		t.Fatal("rollback lost original")
	}
}

func TestSetupLockPreservesHooks(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "hooks.json")
	original := `{"hooks":{}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude-notifications-setup.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{CodexHome: home, PluginRoot: fakeBundle(t)}); err == nil {
		t.Fatal("concurrent setup accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Fatal("hooks changed")
	}
}

func TestRelativeCodexHomeIsAbsolute(t *testing.T) {
	t.Setenv("CODEX_HOME", "relative-fixture")
	for _, override := range []string{"", "explicit-fixture"} {
		got, err := ResolveCodexHome(override)
		if err != nil || !filepath.IsAbs(got) {
			t.Fatalf("%q: %v", got, err)
		}
	}
}

func TestHooksSnapshotRejectsLostUpdate(t *testing.T) {
	for _, existed := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "hooks.json")
		expected := []byte(nil)
		if existed {
			expected = []byte(`{"hooks":{}}`)
		}
		changed := []byte(`{"hooks":{},"foreign":"new edit"}`)
		if err := os.WriteFile(path, changed, 0600); err != nil {
			t.Fatal(err)
		}
		_, err := writeHooksFile(path, hooksFile{Hooks: map[string][]hookGroup{}}, expected, existed)
		if err == nil {
			t.Fatal("overwrote concurrent edit")
		}
		actual, _ := os.ReadFile(path)
		if string(actual) != string(changed) {
			t.Fatal("foreign edit lost")
		}
	}
}

func TestSymlinkAssetsCannotEscapeOrRecurse(t *testing.T) {
	for _, target := range []string{"..", filepath.Join(t.TempDir(), "private-fixture")} {
		src := fakeBundle(t)
		if filepath.IsAbs(target) {
			if err := os.WriteFile(target, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(target, filepath.Join(src, "sounds", "link")); err != nil {
			t.Skip(err)
		}
		if err := copyBundle(src, t.TempDir()); err == nil {
			t.Fatal("unsafe symlink copied")
		}
	}
}

func TestLegacyGeneratedRegistrationMigrates(t *testing.T) {
	dir := t.TempDir()
	posix, _ := HookCommands(dir, "Stop")
	h := hookHandler{Type: "command", Command: posix, CommandWindows: "cmd.exe /d /s /c call " + windowsQuote(filepath.Join(dir, "bin", "codex-hook-wrapper.cmd")) + " handle-hook Stop --product codex"}
	if !ownsHandler(h, dir, "Stop") {
		t.Fatal("legacy registration not recognized")
	}
	if ownsHandler(h, t.TempDir(), "Stop") {
		t.Fatal("foreign location claimed")
	}
}

func TestDestinationRootSymlinkThirdTree(t *testing.T) {
	source, foreign, home := fakeBundle(t), fakeBundle(t), t.TempDir()
	canaries := map[string]string{"bin/bootstrap.sh": "foreign bootstrap", ".claude-plugin/marketplace.json": "foreign marketplace", "config/config.json": "custom config"}
	for rel, value := range canaries {
		path := filepath.Join(foreign, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(home, InstallDirName)
	if err := os.Symlink(foreign, dest); err != nil {
		t.Skip(err)
	}
	hooks := filepath.Join(home, "hooks.json")
	original := []byte(`{"hooks":{},"foreign":true}`)
	if err := os.WriteFile(hooks, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{PluginRoot: source, CodexHome: home}); err == nil {
		t.Fatal("accepted third-tree symlink")
	}
	for rel, value := range canaries {
		got, err := os.ReadFile(filepath.Join(foreign, rel))
		if err != nil || string(got) != value {
			t.Fatalf("canary %s changed: %q %v", rel, got, err)
		}
	}
	got, err := os.ReadFile(hooks)
	if err != nil || string(got) != string(original) {
		t.Fatal("hooks changed")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 2 {
		t.Fatalf("setup wrote before rejection: %v %v", entries, err)
	}
	// Registering the target itself is non-destructive and remains supported.
	if _, err := Run(Options{PluginRoot: foreign, CodexHome: home}); err != nil {
		t.Fatal(err)
	}
}

func TestDestinationSymlinkParentHome(t *testing.T) {
	home := t.TempDir()
	alias := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, alias); err != nil {
		t.Skip(err)
	}
	if _, err := Run(Options{PluginRoot: fakeBundle(t), CodexHome: alias}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, InstallDirName, "bin", "codex-hook-wrapper.sh")); err != nil {
		t.Fatal(err)
	}
}
