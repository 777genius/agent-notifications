package codexsetup

import (
	configtemplate "github.com/777genius/agent-notifications/config"
	"github.com/777genius/agent-notifications/internal/config"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigPreflightPreservesOldDestination(t *testing.T) {
	source := fakeBundle(t)
	home := t.TempDir()
	destination := filepath.Join(home, InstallDirName)
	candidate := filepath.Join(destination, "config", "config.json")
	if err := os.MkdirAll(filepath.Dir(candidate), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"unknown":{"CANARY":"secret"}}`)
	if err := os.WriteFile(candidate, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", candidate)
	_, err := Run(Options{CodexHome: home, PluginRoot: source})
	if err == nil || !strings.Contains(err.Error(), "ConfigUnsafeTarget") {
		t.Fatalf("expected overlap rejection: %v", err)
	}
	if strings.Contains(err.Error(), "CANARY") {
		t.Fatal("disclosed config")
	}
	after, e := os.ReadFile(candidate)
	if e != nil || string(after) != string(original) {
		t.Fatal("old destination changed")
	}
	if _, e := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(e) {
		t.Fatal("registered before preflight")
	}
}

func TestConfigDryRunDoesNotInitialize(t *testing.T) {
	source := fakeBundle(t)
	canonical := filepath.Join(t.TempDir(), "missing", "config.json")
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	home := filepath.Join(t.TempDir(), "codex")
	if _, err := Run(Options{CodexHome: home, PluginRoot: source, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{home, filepath.Dir(canonical)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote %s", p)
		}
	}
}

func TestConfigInitPreservesExistingAfterRegistration(t *testing.T) {
	source := fakeBundle(t)
	canonical := os.Getenv("AGENT_NOTIFICATIONS_CONFIG")
	before, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{CodexHome: t.TempDir(), PluginRoot: source}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}" || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("existing config changed")
	}
}

func TestConfigInitFailureLeavesSuccessfulRegistration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix public-directory mutation denial fixture")
	}
	source := fakeBundle(t)
	parent := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0777); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(parent, "config.json")
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	home := t.TempDir()
	result, err := Run(Options{CodexHome: home, PluginRoot: source})
	if err == nil || !strings.Contains(err.Error(), "registration succeeded") || !strings.Contains(err.Error(), "config init") {
		t.Fatalf("expected actionable partial success: %v", err)
	}
	for _, path := range []string{result.HooksPath, filepath.Join(result.InstallDir, "bin", "codex-hook-wrapper.sh")} {
		if _, e := os.Stat(path); e != nil {
			t.Fatalf("successful registration rolled back: %v", e)
		}
	}
	if _, e := os.Stat(canonical); !os.IsNotExist(e) {
		t.Fatal("failed init published config")
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := initializeConfig(source); err != nil {
		t.Fatalf("config-only retry failed: %v", err)
	}
	if _, err := os.Stat(canonical); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPreflightRecognizesOnlyExactVersionDestination(t *testing.T) {
	for _, version := range []string{config.ConsumerVersion, "0.0.1"} {
		t.Run(version, func(t *testing.T) {
			source := fakeBundle(t)
			if err := os.Unsetenv(config.OverrideEnv); err != nil {
				t.Fatal(err)
			} // restored by fakeBundle's Setenv cleanup
			destination := t.TempDir()
			for _, root := range []string{source, destination} {
				if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(root, "config"), 0700); err != nil {
					t.Fatal(err)
				}
				candidateVersion := config.ConsumerVersion
				if root == destination {
					candidateVersion = version
				}
				manifest := `{"name":"claude-notifications-go","version":"` + candidateVersion + `"}`
				if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(manifest), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config", "config.json"), configtemplate.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := preflightConfig(source, destination, true)
			if version == config.ConsumerVersion {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "ConfigLegacyImportRequired") {
					t.Fatalf("old bundle blessed: %v", err)
				}
			}
		})
	}
}
