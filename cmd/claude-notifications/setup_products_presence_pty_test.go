//go:build linux || darwin

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// Reads of credential/config FIFOs block the real child; relative PATH, links
// and special executable files must never turn into ordinary selected defaults.
func TestSetupProductsScopedPresence(t *testing.T) {
	for _, name := range []string{"regular", "missing", "symlink", "broken-link", "cyclic-link", "directory", "fifo", "not-executable", "relative-PATH", "invalid-override"} {
		t.Run(name, func(t *testing.T) {
			f := newBootstrapFixture(t)
			candidate := filepath.Join(f.tools, "codex")
			original := filepath.Join(filepath.Dir(f.tools), "TEST codex executable")
			if err := os.Rename(candidate, original); err != nil {
				t.Fatal(err)
			}
			args := ""
			want := "unavailable"
			switch name {
			case "regular":
				if err := os.Rename(original, candidate); err != nil {
					t.Fatal(err)
				}
				want = "present"
			case "missing":
				want = "absent"
			case "symlink", "broken-link", "cyclic-link":
				target := original
				switch name {
				case "broken-link":
					target += " missing"
					want = "absent"
				case "cyclic-link":
					target = candidate
				default:
					want = "present"
				}
				if err := os.Symlink(target, candidate); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(candidate, 0700); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(candidate, 0600); err != nil {
					t.Fatal(err)
				}
			case "not-executable":
				f.write(candidate, []byte("TEST never executable"), 0600)
			case "relative-PATH":
				relativeDir := filepath.Join(f.project, "relative agents")
				if err := os.Mkdir(relativeDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(original, filepath.Join(relativeDir, "codex")); err != nil {
					t.Fatal(err)
				}
				for i, value := range f.env {
					if strings.HasPrefix(value, "PATH=") {
						f.env[i] = "PATH=relative agents" + string(os.PathListSeparator) + f.tools
					}
				}
			case "invalid-override":
				args = " --codex-executable " + shellQuote(candidate)
			}
			// These are the selected authorities, not unrelated secret files.
			for _, leaf := range []string{".env", "claude/settings.json", "codex/auth.json", "gemini/.gemini/settings.json"} {
				path := filepath.Join(f.home, leaf)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			inventory := func() map[string]fs.FileMode {
				t.Helper()
				paths := map[string]fs.FileMode{}
				if err := filepath.WalkDir(f.home, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					info, err := entry.Info()
					if err == nil {
						paths[path] = info.Mode()
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				return paths
			}
			beforePaths, beforeFiles := inventory(), f.snapshot()
			f.script = "exec " + shellQuote(f.binary) + " setup-products select --plain" + args + " < /dev/tty\n"
			var result bootstrapResult
			if name == "invalid-override" {
				result = f.terminal(nil)
				if result.code != 2 || !strings.Contains(result.screen, "codex override:") || strings.Contains(result.screen, "[*]") {
					t.Fatalf("invalid authority fell back: %+v", result)
				}
			} else {
				result = f.terminal(nil, promptStep{"comma-separated", "cancel\n", nil})
				if result.code != 0 {
					t.Fatalf("cancel: %+v", result)
				}
				for _, label := range []string{"Claude Code", "OpenCode", "Gemini CLI"} {
					if !strings.Contains(result.screen, "[*] "+label+" (CLI present)") {
						t.Fatalf("unrelated CLI lost its default: %s", result.screen)
					}
				}
				wantDefaults := 3
				if want == "present" {
					wantDefaults = 4
				}
				if strings.Count(result.screen, "[*]") != wantDefaults {
					t.Fatalf("wrong observed default set: %s", result.screen)
				}
				switch want {
				case "present":
					if !strings.Contains(result.screen, "[*] Codex (CLI present)") {
						t.Fatalf("usable executable not present: %s", result.screen)
					}
				case "absent":
					if !strings.Contains(result.screen, "[ ] Codex (CLI not found in selected PATH)") {
						t.Fatalf("absence gained authority: %s", result.screen)
					}
				default:
					if !strings.Contains(result.screen, "codex:") || strings.Contains(result.screen, "Codex (CLI") {
						t.Fatalf("unavailable executable became selectable: %s", result.screen)
					}
					reason := "PATH entry is not a usable regular executable"
					switch name {
					case "relative-PATH":
						reason = "cannot run executable found relative to current directory"
					case "cyclic-link":
						reason = "too many levels"
					}
					if !strings.Contains(result.screen, reason) {
						t.Fatalf("unavailable reason lost: %s", result.screen)
					}
				}
			}
			if result.out != "" || !reflect.DeepEqual(beforePaths, inventory()) {
				t.Fatalf("discovery emitted a selection or changed profile entries: %+v", result)
			}
			f.unchanged(beforeFiles)
			f.noAcquisition()
		})
	}
}
