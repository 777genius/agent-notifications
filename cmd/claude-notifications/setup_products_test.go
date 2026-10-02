package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/clientdetect"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The old top-level single-choice menu could not express a mixed observer and
// portable selection. Verify the real UAP SelectMany and machine-output seam,
// including cancellation without installation/config writes.
func TestSetupProductsSelect(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"AGENT_NOTIFICATIONS_CONTROL_ROOT", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME", "OPENCODE_CONFIG_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	for _, tc := range []struct {
		name, answer, want string
		code               int
	}{
		{"mixed-numbers", "1,3,4\n", "claude,opencode,gemini\n", 0},
		{"mixed-ids", "codex,gemini\n", "codex,gemini\n", 0},
		{"all", "1,2,3,4\n", "claude,codex,opencode,gemini\n", 0},
		{"cancel", "cancel\n", "", 0},
		{"empty", "\n", "", 0},
		{"closed", "", "", 1},
		{"duplicate", "1,claude\n", "", 1},
		{"unknown", "5\n", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var selected, prompts bytes.Buffer
			code := runSetupProducts([]string{"select"}, strings.NewReader(tc.answer), &selected, &prompts)
			if code != tc.code || selected.String() != tc.want {
				t.Fatalf("selection: code=%d output=%q prompts=%q", code, selected.String(), prompts.String())
			}
			for _, label := range []string{"Claude Code", "Codex", "OpenCode", "Gemini CLI", "comma-separated"} {
				if !strings.Contains(prompts.String(), label) {
					t.Fatalf("product multiselect omitted %q: %s", label, prompts.String())
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("selection mutated product roots: %v %v", entries, err)
			}
		})
	}
}

func TestSetupProductsRejectsUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"install"}, {"select", "--product", "gemini"}} {
		var selected, prompts bytes.Buffer
		if code := runSetupProducts(args, strings.NewReader("1\n"), &selected, &prompts); code != 2 || selected.Len() != 0 {
			t.Fatalf("unexpected arguments selected products: %v code=%d output=%q", args, code, selected.String())
		}
	}
}

// Red regression: a capability/feature query reads stdin, polls a profile, or
// pollutes machine output, preventing safe version-gated shell acquisition.
func TestSetupProductsFixedFeatures(t *testing.T) {
	for _, tc := range []struct{ command, want string }{{"capabilities", "setup-products-v1 claude codex opencode gemini\n"}, {"features", "terminal-selector-v1\n"}} {
		var output, prompts bytes.Buffer
		if code := runSetupProducts([]string{tc.command}, strings.NewReader(""), &output, &prompts); code != 0 || output.String() != tc.want || prompts.Len() != 0 {
			t.Fatalf("%s code=%d output=%q prompts=%q", tc.command, code, output.String(), prompts.String())
		}
	}
}

// Red regression: extending the PR283 picker drops a singleton or a mixed
// portable/native subset when the existing public line seam parses real input.
func TestSetupProductsAllSubsets(t *testing.T) {
	for _, ids := range []string{"claude", "codex", "opencode", "gemini", "claude,codex", "claude,opencode", "claude,gemini", "codex,opencode", "codex,gemini", "opencode,gemini", "claude,codex,opencode", "claude,codex,gemini", "claude,opencode,gemini", "codex,opencode,gemini", "claude,codex,opencode,gemini"} {
		var output, prompts bytes.Buffer
		if code := runSetupProducts([]string{"select"}, strings.NewReader(ids+"\n"), &output, &prompts); code != 0 || output.String() != ids+"\n" {
			t.Fatalf("%s code=%d output=%q", ids, code, output.String())
		}
	}
}

// Red regression: read-only presence runs --version or turns config-only
// evidence into an installation default. All surfaces are disposable TEST data.
func TestSetupProductsDiscoveryIsReadOnly(t *testing.T) {
	e, a := setupProductsDiscoveryFixture(t)
	for _, id := range productOrder {
		path := filepath.Join(e.PATH, fixtureProductName(id))
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf executed > \""+filepath.Join(e.Home, "EXECUTED")+"\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Config content deliberately is not valid JSON; mere presence must not
	// parse settings, auth, or .env files during host detection.
	for _, name := range []string{".env", "auth.json", "settings.json"} {
		if err := os.WriteFile(filepath.Join(e.Home, name), []byte("TEST secret canary"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	facts, _, err := discoverProducts(context.Background(), a, e)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if !f.Present || !f.Selectable {
			t.Fatalf("usable TEST CLI excluded: %+v", f)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Home, "EXECUTED")); !os.IsNotExist(err) {
		t.Fatalf("agent stub executed: %v", err)
	}
	if err := os.Remove(filepath.Join(e.PATH, fixtureProductName("codex"))); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(e.Home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	facts, _, err = discoverProducts(context.Background(), a, e)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if f.ID == "codex" && (f.Present || !f.Selectable) {
			t.Fatalf("config-only evidence became CLI default: %+v", f)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Home, "control")); !os.IsNotExist(err) {
		t.Fatalf("discovery created control state: %v", err)
	}
}

func fixtureProductName(id string) string {
	if runtime.GOOS == "windows" {
		return id + ".exe"
	}
	return id
}

func setupProductsDiscoveryFixture(t *testing.T) (productEnvironment, setupProductsArgs) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "TEST-PATH")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	e := productEnvironment{Home: home, PATH: path, DefaultControlRoot: filepath.Join(home, "control"), Values: map[string]string{}}
	a, err := parseSetupProducts([]string{"select"})
	if err != nil {
		t.Fatal(err)
	}
	return e, a
}

// Red regression: a per-client DetectionError returned beside top-level nil
// is offered as a normal absent/default target rather than an unavailable fact.
func TestSetupProductsPerClientDetectionError(t *testing.T) {
	e, a := setupProductsDiscoveryFixture(t)
	if err := os.WriteFile(filepath.Join(e.PATH, "codex"), []byte("TEST never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	scopes, err := productScopes(a, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(scopes["codex-home"], 0700); err != nil {
		t.Fatal(err)
	}
	detector := clientdetect.NewOS(e.Home)
	detector.Lstat = func(path string) (os.FileInfo, error) {
		if path == scopes["codex-home"] {
			return nil, errors.New("TEST inaccessible profile")
		}
		return os.Lstat(path)
	}
	facts, _, err := discoverProductsWithDetector(context.Background(), a, e, scopes, detector)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if f.ID == "codex" && (f.Selectable || !strings.Contains(f.Reason, "inaccessible")) {
			t.Fatalf("per-client error became ordinary absence: %+v", f)
		}
	}
}

// Red regression: an invalid explicit authority falls back to ambient PATH or
// a provider converts a relative override into the caller's working directory.
func TestSetupProductsExplicitAuthorityDoesNotFallback(t *testing.T) {
	e, a := setupProductsDiscoveryFixture(t)
	for _, key := range []string{"claude-executable", "codex-executable", "opencode-executable", "gemini-executable"} {
		a.Scopes = map[string]string{key: filepath.Join(e.Home, "missing")}
		if _, _, err := discoverProducts(context.Background(), a, e); err == nil {
			t.Fatalf("missing %s fell back", key)
		}
	}
	a.Scopes = nil
	e.Values["CODEX_HOME"] = "relative"
	if _, _, err := discoverProducts(context.Background(), a, e); err == nil {
		t.Fatal("relative profile became cwd/default authority")
	}
}
