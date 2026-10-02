package portableasset

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// This fails if historical repair rejects the old package, a new release cannot
// be extracted, or a package can activate both spellings of the skill.
func TestSkillRenameArchiveLayouts(t *testing.T) {
	for _, layout := range []string{"agent-notifications", "agent-notify", "both"} {
		t.Run(layout, func(t *testing.T) {
			base := t.TempDir()
			archive := filepath.Join(base, "fixture.zip")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			entries := map[string]string{
				"plugin.json":              `{"name":"agent-notify","version":"historical-fixture"}`,
				"mcp.json":                 `{"mcpServers":{"agent-notify":{"command":"./bin/claude-notifications"}}}`,
				"bin/claude-notifications": "inert executable, never invoked",
			}
			for _, name := range []string{"agent-notifications", "agent-notify"} {
				if layout == name || layout == "both" {
					entries["skills/"+name+"/SKILL.md"] = "---\nname: " + name + "\ndescription: Historical bytes stay unchanged\n---\n"
				}
			}
			for path, body := range entries {
				entry, err := writer.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			parent := filepath.Join(base, "extracted")
			root, err := OpenArchive(archive, parent, "")
			if layout == "both" {
				if !errors.Is(err, ErrInvalidLayout) {
					t.Fatal("dual skill package accepted", err)
				}
				if _, err := os.Lstat(filepath.Join(parent, "root")); !os.IsNotExist(err) {
					t.Fatal("invalid package was retained", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			path := "skills/" + layout + "/SKILL.md"
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil || string(body) != entries[path] {
				t.Fatal("package skill was rewritten", string(body), err)
			}
		})
	}
}
