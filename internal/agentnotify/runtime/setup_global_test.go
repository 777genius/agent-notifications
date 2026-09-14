package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupGlobalInspectionStrict(t *testing.T) {
	for _, raw := range []string{"{}", `{"notifications":{"desktop":{"enabled":true,"enabled":false,"sound":true,"clickToFocus":true}}}`, strings.Repeat(" ", 65537)} {
		t.Run("invalid", func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "global.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			b, err := New(Options{ControlRoot: root, GlobalConfig: path})
			if err != nil {
				t.Fatal(err)
			}
			got := b.InspectGlobalConfiguration()
			if got.Configuration != "configuration_invalid" || got.DesktopEnabled != nil {
				t.Fatal(got)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != raw {
				t.Fatal("inspection changed config", err)
			}
		})
	}
}
