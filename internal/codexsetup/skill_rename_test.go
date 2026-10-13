package codexsetup

import (
	"os"
	"path/filepath"
	"testing"
)

// This fails if an update or historical repair leaves both skill names, either
// directly in the runtime or in its portable package template. Edited and
// unowned siblings must conflict without publishing the incoming skill.
func TestCodexBundleSkillRename(t *testing.T) {
	for _, skillRoot := range []string{"skills", "portable-package/skills"} {
		for _, incoming := range []string{"agent-notifications", "agent-notify"} {
			for _, change := range []string{"owned", "edited", "unowned", "dual-source"} {
				t.Run(skillRoot+"/"+incoming+"/"+change, func(t *testing.T) {
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
					sibling := "agent-notify"
					if incoming == sibling {
						sibling = "agent-notifications"
					}
					root := filepath.FromSlash(skillRoot)
					siblingSource := filepath.Join(source, root, sibling, "SKILL.md")
					siblingDestination := filepath.Join(home, InstallDirName, root, sibling, "SKILL.md")
					incomingDestination := filepath.Join(home, InstallDirName, root, incoming, "SKILL.md")
					opts := Options{CodexHome: home, PluginRoot: source, ControlRoot: control}
					if change == "owned" || change == "edited" {
						write(siblingSource, "previous skill: "+sibling)
						if _, err := Run(opts); err != nil {
							t.Fatal("previous bundle install failed", err)
						}
						if err := os.Remove(siblingSource); err != nil {
							t.Fatal(err)
						}
					}
					write(filepath.Join(source, root, incoming, "SKILL.md"), "incoming skill: "+incoming)
					if change == "edited" || change == "unowned" {
						write(siblingDestination, "user sibling skill")
					}
					if change == "dual-source" {
						write(siblingSource, "extra sibling skill")
					}
					_, err := Run(opts)
					if change == "owned" {
						if err != nil {
							t.Fatal(err)
						}
						if _, err := os.Lstat(siblingDestination); !os.IsNotExist(err) {
							t.Fatal("sibling runtime skill remains", err)
						}
						body, err := os.ReadFile(incomingDestination)
						if err != nil || string(body) != "incoming skill: "+incoming {
							t.Fatal("incoming runtime skill not installed", string(body), err)
						}
					} else {
						if err == nil {
							t.Fatal("unsafe bundle update accepted")
						}
						if _, err := os.Lstat(incomingDestination); !os.IsNotExist(err) {
							t.Fatal("conflict published incoming skill", err)
						}
						if change != "dual-source" {
							body, err := os.ReadFile(siblingDestination)
							if err != nil || string(body) != "user sibling skill" {
								t.Fatal("user sibling skill changed", string(body), err)
							}
						}
					}
				})
			}
		}
	}
}
