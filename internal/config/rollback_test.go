package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/testenv"
)

// Run unchanged in both the final build and a disposable FreshNeutral=false
// bridge snapshot. Existing N/E must remain readable in either rollout phase.
func TestRollbackExistingCanonicalSharedLoader(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "neutral"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			testenv.Set(t, home)
			path, _, err := neutralPath(SnapshotEnv(), home)
			if err != nil {
				t.Fatal(err)
			}
			source := "universal"
			if explicit {
				path = filepath.Join(home, "portable", "chosen.json")
				t.Setenv(OverrideEnv, path)
				source = "explicit"
			}
			raw := []byte(`{"schemaVersion":1,"notifications":{"desktop":{"enabled":false,"volume":0.37,"appIcon":"${CLAUDE_PLUGIN_ROOT}/icon.png"},"webhook":{"enabled":false}},"future":{"large":9007199254740993,"template":"${ROLLBACK_CANARY}","nested":[null,false,{"unknown":true}]}}` + "\n")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(1700000000, 0)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			bundle := filepath.Join(home, "bundle")
			selected, err := Resolve(SnapshotEnv())
			if err != nil || selected.Path != path || selected.Source != source || !selected.Exists {
				t.Fatalf("selection: %+v %v", selected, err)
			}
			cfg, err := LoadFromPluginRootQuiet(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Notifications.Desktop.Volume != 0.37 || cfg.Notifications.Desktop.AppIcon != filepath.Join(bundle, "icon.png") {
				t.Fatal("shared loader failed to read existing canonical or expand runtime template")
			}
			afterRaw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, afterRaw) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("read changed raw fields, template, bytes, mode or mtime")
			}
			for _, absent := range []string{path + ".lock", filepath.Join(home, ".agent-notifications-config.lock"), filepath.Join(home, ".claude"), bundle} {
				if _, err := os.Lstat(absent); !os.IsNotExist(err) {
					t.Fatalf("read created path %s: %v", absent, err)
				}
			}
		})
	}
}
