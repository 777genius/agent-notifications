package qoderpackage

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeBundleContract(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "final binary")
			body := []byte("exact final platform bytes")
			if err := os.WriteFile(source, body, 0700); err != nil {
				t.Fatal(err)
			}
			req := BuildRequest{Version: "1.49.5", GOOS: goos, GOARCH: "amd64", Executable: source, OutputRoot: filepath.Join(dir, "package with spaces"), Archive: filepath.Join(dir, "bundle.zip")}
			got, err := Build(req)
			if err != nil {
				t.Fatal(err)
			}
			var hooks struct {
				Hooks map[string][]struct {
					Async bool
					Hooks []struct {
						Type, Command, Name string
						Args                []string
						Timeout             int
					}
				}
			}
			raw, err := os.ReadFile(filepath.Join(got.Root, "hooks/hooks.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &hooks); err != nil {
				t.Fatal(err)
			}
			groups := hooks.Hooks["Stop"]
			if len(hooks.Hooks) != 1 || len(groups) != 1 || !groups[0].Async || len(groups[0].Hooks) != 1 {
				t.Fatalf("unexpected native hook: %s", raw)
			}
			hook := groups[0].Hooks[0]
			binary := "claude-notifications"
			if goos == "windows" {
				binary += ".exe"
			}
			if hook.Timeout < 35 || hook.Type != "command" || hook.Command != "${QODER_PLUGIN_ROOT}/bin/"+binary || !reflect.DeepEqual(hook.Args, []string{"handle-hook", "Stop", "--product", "qoder"}) {
				t.Fatalf("unsafe/incorrect command contract: %+v", hook)
			}
			// Native Qoder substitutes its root in the executable field. Arguments stay
			// separate, so a path with spaces must never acquire shell quotes.
			resolved := strings.ReplaceAll(hook.Command, "${QODER_PLUGIN_ROOT}", got.Root)
			actual, err := os.ReadFile(resolved)
			if err != nil || string(actual) != string(body) {
				t.Fatalf("binary substitution: %q %v", actual, err)
			}
			archive, err := zip.OpenReader(got.Archive)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			seen := map[string]bool{}
			for _, file := range archive.File {
				if !file.FileInfo().IsDir() {
					seen[file.Name] = true
				}
				if file.Name == "bin/"+binary && file.Mode().Perm()&0100 == 0 {
					t.Fatal("lost executable permission")
				}
			}
			for _, name := range []string{"plugin.json", ".qoder-plugin/plugin.json", "hooks/hooks.json", "README.md", "THIRD_PARTY_NOTICES.txt", "bin/" + binary} {
				if !seen[name] {
					t.Fatalf("missing %s", name)
				}
			}
			if len(seen) != 6 {
				t.Fatalf("unexpected extra runtime/observer: %v", seen)
			}
			if _, err := Build(req); err == nil {
				t.Fatal("overwrote existing package")
			}
		})
	}
}
