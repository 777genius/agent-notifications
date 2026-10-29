package qoderpackage

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
			for _, rel := range []string{"plugin.json", ".qoder-plugin/plugin.json"} {
				raw, err := os.ReadFile(filepath.Join(got.Root, rel))
				if err != nil {
					t.Fatal(err)
				}
				var manifest map[string]string
				if err := json.Unmarshal(raw, &manifest); err != nil {
					t.Fatal(err)
				}
				if manifest["name"] != "agent-notifications-qoder" || manifest["version"] != req.Version {
					t.Fatalf("incorrect manifest identity: %v", manifest)
				}
				if rel == "plugin.json" && manifest["$schema"] != "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json" {
					t.Fatalf("standard schema missing: %v", manifest)
				}
				if rel != "plugin.json" && manifest["$schema"] != "" {
					t.Fatal("native manifest must not claim standard schema")
				}
			}
			var hooks struct {
				Hooks map[string][]struct {
					Matcher string
					Async   *bool
					Hooks   []struct {
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
			binary := "claude-notifications"
			if goos == "windows" {
				binary += ".exe"
			}
			wantEvents := []struct{ event, matcher string }{
				{"Stop", ""}, {"Notification", ""}, {"PermissionRequest", ""}, {"SubagentStop", ""}, {"TeammateIdle", ""}, {"PreToolUse", "ExitPlanMode|AskUserQuestion"},
			}
			if len(hooks.Hooks) != len(wantEvents) {
				t.Fatalf("unexpected native hook set: %s", raw)
			}
			for _, want := range wantEvents {
				groups := hooks.Hooks[want.event]
				if len(groups) != 1 || groups[0].Async != nil || groups[0].Matcher != want.matcher || len(groups[0].Hooks) != 1 {
					t.Fatalf("unexpected native hook for %s: %s", want.event, raw)
				}
				hook := groups[0].Hooks[0]
				if hook.Timeout < 35 || hook.Type != "command" || hook.Command != "${QODER_PLUGIN_ROOT}/bin/"+binary || hook.Name != "agent-notifications-qoder-"+strings.ToLower(want.event) || !reflect.DeepEqual(hook.Args, []string{"handle-hook", want.event, "--product", "qoder"}) {
					t.Fatalf("unsafe/incorrect command contract for %s: %+v", want.event, hook)
				}
			}
			// Native Qoder substitutes its root in the executable field. Arguments stay
			// separate, so a path with spaces must never acquire shell quotes.
			resolved := strings.ReplaceAll(hooks.Hooks["Stop"][0].Hooks[0].Command, "${QODER_PLUGIN_ROOT}", got.Root)
			actual, err := os.ReadFile(resolved)
			if err != nil || string(actual) != string(body) {
				t.Fatalf("binary substitution: %q %v", actual, err)
			}
			archive, err := zip.OpenReader(got.Archive)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := archive.Close(); err != nil {
					t.Error(err)
				}
			}()
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

// Refuse before writing a package: an archive in its own source directory
// would otherwise be walked and copied back into itself.
func TestRejectArchiveInsideOutputRoot(t *testing.T) {
	for _, suffix := range []string{".", "bundle.zip", "nested/bundle.zip"} {
		t.Run(suffix, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "package")
			_, err := Build(BuildRequest{Version: "1.49.5", GOOS: "linux", GOARCH: "amd64", Executable: filepath.Join(base, "unused"), OutputRoot: root, Archive: filepath.Join(root, filepath.FromSlash(suffix))})
			if err == nil || !strings.Contains(err.Error(), "archive must be outside output root") {
				t.Fatalf("inside archive accepted: %v", err)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("package created before refusal: %v", err)
			}
		})
	}
}

func TestRejectArchiveCaseAlias(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("case aliases are refused on Darwin and Windows")
	}
	base := t.TempDir()
	root := filepath.Join(base, "package")
	_, err := Build(BuildRequest{Version: "1.49.5", GOOS: "linux", GOARCH: "amd64", Executable: filepath.Join(base, "unused"), OutputRoot: root, Archive: filepath.Join(base, "PACKAGE", "bundle.zip")})
	if err == nil || !strings.Contains(err.Error(), "archive must be outside output root") {
		t.Fatalf("case-alias archive accepted: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("package created before refusal: %v", err)
	}
}

func TestManagedHooksUseExactOwnedRuntimeEntrypoint(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "verified binary")
	if err := os.WriteFile(source, []byte("TEST final executable"), 0700); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(root, "shared control")
	executable := filepath.Join(root, "shared runtime", "claude-notifications-linux-amd64")
	got, err := Build(BuildRequest{Version: "1.49.6", GOOS: "linux", GOARCH: "amd64", Executable: source, OutputRoot: filepath.Join(root, "native package"), ManagedControlRoot: control, Binding: "TEST-owned-binding", ManagedExecutable: executable})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(got.Root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string
				Args    []string
			}
		}
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Hooks) != 6 {
		t.Fatalf("managed event set differs: %s", raw)
	}
	for event, groups := range doc.Hooks {
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("ambiguous managed hook: %s", event)
		}
		hook := groups[0].Hooks[0]
		if hook.Command != executable || !reflect.DeepEqual(hook.Args, []string{"qoder-event", event, "--control-root", control, "--binding", "TEST-owned-binding"}) {
			t.Fatalf("managed hook bypassed exact runtime receipt: %+v", hook)
		}
	}
}
