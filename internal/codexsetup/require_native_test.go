package codexsetup

import (
	"os"
	"path/filepath"
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
