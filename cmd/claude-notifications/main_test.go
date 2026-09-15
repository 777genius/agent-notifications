package main

import (
	"encoding/json"
	"errors"
	"github.com/777genius/agent-notifications/internal/testenv"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadPluginManifestVersion(t *testing.T) {
	pluginRoot := t.TempDir()
	manifestDir := filepath.Join(pluginRoot, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(`{"version":"9.99.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := readPluginManifestVersion(pluginRoot); got != "9.99.0" {
		t.Fatalf("readPluginManifestVersion() = %q, want %q", got, "9.99.0")
	}
}

func TestMaybeScheduleWindowsLazyUpdateOnMismatch(t *testing.T) {
	pluginRoot := setupLazyUpdateTestPlugin(t, "9.99.0")
	withIsolatedLazyUpdateGlobals(t)

	var called int
	var gotRoot string
	scheduleWindowsLazyUpdate = func(root string) error {
		called++
		gotRoot = root
		return nil
	}

	maybeScheduleWindowsLazyUpdate(pluginRoot)
	maybeScheduleWindowsLazyUpdate(pluginRoot)

	if called != 1 {
		t.Fatalf("scheduleWindowsLazyUpdate called %d times, want 1", called)
	}
	if gotRoot != pluginRoot {
		t.Fatalf("scheduleWindowsLazyUpdate root = %q, want %q", gotRoot, pluginRoot)
	}
}

func TestMaybeScheduleWindowsLazyUpdateSkipsMatchingVersion(t *testing.T) {
	pluginRoot := setupLazyUpdateTestPlugin(t, version)
	withIsolatedLazyUpdateGlobals(t)

	scheduleWindowsLazyUpdate = func(root string) error {
		t.Fatalf("scheduleWindowsLazyUpdate called for matching version at %s", root)
		return nil
	}

	maybeScheduleWindowsLazyUpdate(pluginRoot)
}

func TestMaybeScheduleWindowsLazyUpdateRetriesAfterScheduleFailure(t *testing.T) {
	pluginRoot := setupLazyUpdateTestPlugin(t, "9.99.0")
	withIsolatedLazyUpdateGlobals(t)

	var called int
	scheduleWindowsLazyUpdate = func(root string) error {
		called++
		return errors.New("boom")
	}

	maybeScheduleWindowsLazyUpdate(pluginRoot)
	maybeScheduleWindowsLazyUpdate(pluginRoot)

	if called != 2 {
		t.Fatalf("scheduleWindowsLazyUpdate called %d times, want 2", called)
	}
}

func TestLazyUpdateQuoting(t *testing.T) {
	if got, want := shellSingleQuoted(`C:/Users/O'Brien/bin/install.sh`), `'C:/Users/O'"'"'Brien/bin/install.sh'`; got != want {
		t.Fatalf("shellSingleQuoted() = %q, want %q", got, want)
	}
	if got, want := powershellSingleQuoted(`C:\Users\O'Brien\bash.exe`), `'C:\Users\O''Brien\bash.exe'`; got != want {
		t.Fatalf("powershellSingleQuoted() = %q, want %q", got, want)
	}
}

func TestWindowsLazyUpdateCommandUsesThreadSleep(t *testing.T) {
	command := windowsLazyUpdatePowerShellCommand(`C:\Program Files\Git\bin\bash.exe`, `echo ok`)

	if strings.Contains(command, `Start-Sleep`) {
		t.Fatalf("lazy update command uses Start-Sleep: %s", command)
	}
	for _, want := range []string{
		`[System.Threading.Thread]::Sleep(750)`,
		`for ($i = 0; $i -lt 6; $i++)`,
		`& 'C:\Program Files\Git\bin\bash.exe' -lc 'echo ok'`,
		`if ($LASTEXITCODE -eq 0) { break }`,
		`[System.Threading.Thread]::Sleep(5000)`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("lazy update command missing %q: %s", want, command)
		}
	}
}

func TestNewExecHookUsesArgsWithoutShell(t *testing.T) {
	hook := newExecHook(`C:\Tools\claude-notifications.exe`, "Stop")

	if hook.Command != `C:\Tools\claude-notifications.exe` {
		t.Fatalf("newExecHook command = %q", hook.Command)
	}
	if want := []string{"handle-hook", "Stop"}; !reflect.DeepEqual(hook.Args, want) {
		t.Fatalf("newExecHook args = %#v, want %#v", hook.Args, want)
	}
	if hook.Shell != "" {
		t.Fatalf("newExecHook shell = %q, want empty", hook.Shell)
	}
}

func TestWindowsHookSettingsUseExecFormForAllHooks(t *testing.T) {
	settings := newWindowsHookSettings(`C:\Tools\claude-notifications-windows-amd64.exe`)
	expected := map[string]string{
		"PreToolUse":   "PreToolUse",
		"Notification": "Notification",
		"Stop":         "Stop",
		"SubagentStop": "SubagentStop",
		"TeammateIdle": "TeammateIdle",
	}

	for hookEvent, expectedArg := range expected {
		groups := settings.Hooks[hookEvent]
		if len(groups) != 1 {
			t.Fatalf("%s groups = %d, want 1", hookEvent, len(groups))
		}
		if len(groups[0].Hooks) != 1 {
			t.Fatalf("%s commands = %d, want 1", hookEvent, len(groups[0].Hooks))
		}

		hook := groups[0].Hooks[0]
		if hook.Command != `C:\Tools\claude-notifications-windows-amd64.exe` {
			t.Fatalf("%s command = %q", hookEvent, hook.Command)
		}
		if want := []string{"handle-hook", expectedArg}; !reflect.DeepEqual(hook.Args, want) {
			t.Fatalf("%s args = %#v, want %#v", hookEvent, hook.Args, want)
		}
		if hook.Shell != "" {
			t.Fatalf("%s shell = %q, want empty", hookEvent, hook.Shell)
		}
		if strings.Contains(hook.Command, "|") {
			t.Fatalf("%s command contains shell pipe: %q", hookEvent, hook.Command)
		}
	}
}

func TestWindowsHookSettingsJSONHasNoShellSyntax(t *testing.T) {
	data, err := json.Marshal(newWindowsHookSettings(`C:\Tools\claude-notifications-windows-amd64.exe`))
	if err != nil {
		t.Fatal(err)
	}

	jsonText := string(data)
	for _, forbidden := range []string{
		`"shell"`,
		"$input",
		"powershell",
		"hook-wrapper",
		".bat",
		".cmd",
	} {
		if strings.Contains(jsonText, forbidden) {
			t.Fatalf("windows hooks JSON contains %q: %s", forbidden, jsonText)
		}
	}
}

func TestWindowsHookSettingsPreserveSpecialPathChars(t *testing.T) {
	exePath := `C:\Users\O'Brien\A $pecial Dir\claude-notifications-windows-amd64.exe`
	settings := newWindowsHookSettings(exePath)
	hook := settings.Hooks["Stop"][0].Hooks[0]

	if hook.Command != exePath {
		t.Fatalf("command = %q, want %q", hook.Command, exePath)
	}
	if strings.Contains(hook.Command, "`") || strings.Contains(hook.Command, "\"") {
		t.Fatalf("command should not be shell-quoted: %q", hook.Command)
	}

	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var decoded hookSettings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Hooks["Stop"][0].Hooks[0].Command; got != exePath {
		t.Fatalf("decoded command = %q, want %q", got, exePath)
	}
}

func setupLazyUpdateTestPlugin(t *testing.T, pluginVersion string) string {
	t.Helper()

	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugin")
	for _, dir := range []string{
		filepath.Join(pluginRoot, ".claude-plugin"),
		filepath.Join(pluginRoot, "bin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	manifest := []byte(`{"version":"` + pluginVersion + `"}`)
	if err := os.WriteFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "bin", "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return pluginRoot
}

func withIsolatedLazyUpdateGlobals(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	testenv.Set(t, root)

	oldGOOS := currentGOOS
	oldSchedule := scheduleWindowsLazyUpdate
	currentGOOS = "windows"

	t.Cleanup(func() {
		currentGOOS = oldGOOS
		scheduleWindowsLazyUpdate = oldSchedule
	})
}
