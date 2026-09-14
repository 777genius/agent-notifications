package codexsetup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSetupRejectsConfigOverlappingRegistration(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "existing"}[existing], func(t *testing.T) {
			source := fakeBundle(t)
			home := t.TempDir()
			hooks := filepath.Join(home, "hooks.json")
			before := []byte(`{"foreign":true}`)
			if existing {
				if err := os.WriteFile(hooks, before, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("AGENT_NOTIFICATIONS_CONFIG", hooks)
			_, err := Run(Options{CodexHome: home, PluginRoot: source})
			if err == nil || !strings.Contains(err.Error(), "ConfigUnsafeTarget") {
				t.Fatalf("overlap accepted: %v", err)
			}
			after, readErr := os.ReadFile(hooks)
			if existing && (readErr != nil || string(after) != string(before)) {
				t.Fatal("registration changed")
			}
			if !existing && !os.IsNotExist(readErr) {
				t.Fatalf("registration created: %v", readErr)
			}
			if _, statErr := os.Stat(filepath.Join(home, InstallDirName)); !os.IsNotExist(statErr) {
				t.Fatal("runtime assets changed")
			}
		})
	}
}

func TestInitializationRetryUsesConcreteInstalledBinary(t *testing.T) {
	source := fakeBundle(t)
	parent := filepath.Join(t.TempDir(), "missing-parent")
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", filepath.Join(parent, "config.json"))
	retry := filepath.Join(t.TempDir(), "bundle with spaces", "bin", "claude-notifications-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		retry += ".exe"
	}
	// A non-directory ancestor deterministically makes initialization fail.
	if err := os.WriteFile(parent, nil, 0600); err != nil {
		t.Fatal(err)
	}
	err := initializeConfig(source, retry)
	if err == nil || !strings.Contains(err.Error(), retry) || strings.Contains(err.Error(), "retry only: claude-notifications ") {
		t.Fatalf("retry command is not concrete: %v", err)
	}
}

func TestPowerShellRetryCommandEscapesLiteralPath(t *testing.T) {
	got := powershellCommand(`C:\Users\O'Brien\bin\notify.exe`)
	want := `& 'C:\Users\O''Brien\bin\notify.exe' config init`
	if got != want {
		t.Fatalf("PowerShell retry = %q, want %q", got, want)
	}
}

func TestSetupPublishesConventionalNotifierAliases(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native bundle promotion requires a supported native platform")
	}
	for _, tc := range []struct {
		name, bundle, helper string
	}{
		{"modern", "ClaudeNotifier.app", "terminal-notifier-modern"},
		{"legacy", "terminal-notifier.app", "terminal-notifier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fakeBundle(t)
			helper := filepath.Join(source, "bin", tc.bundle, "Contents", "MacOS", tc.helper)
			if err := os.MkdirAll(filepath.Dir(helper), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0755); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			home := filepath.Join(root, "codex")
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			res, err := Run(Options{ControlRoot: filepath.Join(root, "control"), CodexHome: home, PluginRoot: source})
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(res.InstallDir, "bin")
			var target string
			for _, name := range []string{"ClaudeNotifier.app", "terminal-notifier.app"} {
				got, err := os.Readlink(filepath.Join(bin, name))
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if !strings.HasPrefix(filepath.Base(got), "generation-") || !strings.HasSuffix(got, ".app") {
					t.Fatalf("%s -> %s", name, got)
				}
				if target == "" {
					target = got
				} else if got != target {
					t.Fatalf("aliases diverged: %s vs %s", target, got)
				}
			}
			if _, err := os.Stat(filepath.Join(bin, tc.bundle, "Contents", "MacOS", tc.helper)); err != nil {
				t.Fatalf("conventional helper missing: %v", err)
			}
		})
	}
}
