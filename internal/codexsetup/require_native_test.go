package codexsetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRequireNativeBeforeHookCommit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	home := filepath.Join(root, "codex")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	hooks := filepath.Join(home, "hooks.json")
	original := `{"hooks":{},"foreign":true}`
	if err := os.WriteFile(hooks, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	opts := Options{ControlRoot: filepath.Join(root, "control"), CodexHome: home, PluginRoot: fakeBundle(t), RequireNative: true}
	if _, err := Run(opts); err == nil {
		t.Fatal("missing native accepted")
	}
	got, err := os.ReadFile(hooks)
	if err != nil || string(got) != original {
		t.Fatal("hooks changed", err)
	}
	for _, path := range []string{opts.ControlRoot, filepath.Join(home, InstallDirName)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("created %s: %v", path, err)
		}
	}
	opts.RequireNative = false
	if _, err := Run(opts); err != nil {
		t.Fatal("ordinary hook installation changed:", err)
	}
}

func TestNativeStagingHonorsSetupContext(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native bundle staging requires a supported native platform")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	home := filepath.Join(root, "codex")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	hooks := filepath.Join(home, "hooks.json")
	original := `{"hooks":{},"foreign":true}`
	if err := os.WriteFile(hooks, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	bundle := fakeBundle(t)
	if err := os.Mkdir(filepath.Join(bundle, "bin", "ClaudeNotifier.app"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(Options{ControlRoot: filepath.Join(root, "control"), CodexHome: home, PluginRoot: bundle, Context: ctx})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context cancellation", err)
	}
	got, err := os.ReadFile(hooks)
	if err != nil || string(got) != original {
		t.Fatalf("hooks changed after cancellation: %v", err)
	}
}
