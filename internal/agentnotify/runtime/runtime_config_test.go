package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
)

func TestNewUsesCanonicalGlobalConfigResolver(t *testing.T) {
	for mode, wantSource := range map[string]string{"fresh": "universal", "legacy": "legacy", "explicit": "explicit"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("APPDATA", filepath.Join(home, "appdata"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			oldOverride, hadOverride := os.LookupEnv(config.OverrideEnv)
			if err := os.Unsetenv(config.OverrideEnv); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if hadOverride {
					_ = os.Setenv(config.OverrideEnv, oldOverride)
				} else {
					_ = os.Unsetenv(config.OverrideEnv)
				}
			})
			if mode == "legacy" {
				legacy := filepath.Join(home, ".claude", "claude-notifications-go", "config.json")
				if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(legacy, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "explicit" {
				t.Setenv(config.OverrideEnv, filepath.Join(home, "selected", "config.json"))
			}
			selected, err := config.Resolve(config.SnapshotEnv())
			if err != nil {
				t.Fatal(err)
			}
			if selected.Source != wantSource {
				t.Fatalf("source = %q, want %q", selected.Source, wantSource)
			}
			backend, err := New(Options{ControlRoot: filepath.Join(home, "control")})
			if err != nil {
				t.Fatal(err)
			}
			if backend.opts.GlobalConfig != selected.Path {
				t.Fatalf("GlobalConfig = %q, want %q (%s)", backend.opts.GlobalConfig, selected.Path, selected.Source)
			}
		})
	}
}
