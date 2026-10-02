package codexsetup

import (
	"os"
	"path/filepath"
	"testing"
)

// This fails if canonical bundle updates retain a second legacy runtime skill,
// historical bundles stop installing, or an edited/foreign old copy is removed.
func TestCodexBundleSkillRename(t *testing.T) {
	for _, change := range []string{"owned", "edited", "unowned", "dual-source"} {
		t.Run(change, func(t *testing.T) {
			source := fakeBundle(t)
			home := t.TempDir()
			control := filepath.Join(t.TempDir(), "control")
			write := func(path, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			legacySource := filepath.Join(source, "skills", "agent-notify", "SKILL.md")
			legacy := filepath.Join(home, InstallDirName, "skills", "agent-notify", "SKILL.md")
			canonical := filepath.Join(home, InstallDirName, "skills", "agent-notifications", "SKILL.md")
			opts := Options{CodexHome: home, PluginRoot: source, ControlRoot: control}
			if change == "owned" || change == "edited" {
				write(legacySource, "historical skill")
				if _, err := Run(opts); err != nil {
					t.Fatal("historical bundle install failed", err)
				}
				if err := os.Remove(legacySource); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(source, "skills", "agent-notifications", "SKILL.md"), "canonical skill")
			if change == "edited" || change == "unowned" {
				write(legacy, "user legacy skill")
			}
			if change == "dual-source" {
				write(legacySource, "extra old skill")
			}
			_, err := Run(opts)
			if change == "owned" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
					t.Fatal("legacy runtime skill remains", err)
				}
				body, err := os.ReadFile(canonical)
				if err != nil || string(body) != "canonical skill" {
					t.Fatal("canonical runtime skill not installed", string(body), err)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe bundle update accepted")
				}
				if _, err := os.Lstat(canonical); !os.IsNotExist(err) {
					t.Fatal("conflict published canonical skill", err)
				}
				if change != "dual-source" {
					body, err := os.ReadFile(legacy)
					if err != nil || string(body) != "user legacy skill" {
						t.Fatal("user legacy skill changed", string(body), err)
					}
				}
			}
		})
	}
}
