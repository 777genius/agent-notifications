//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func installDiagnosticIsolation(t *testing.T) string {
	t.Helper()
	root := setupCommandRoot(t)
	for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "CLAUDE_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "APPDATA", "LOCALAPPDATA", "TMPDIR", "TMP", "TEMP"} {
		dir := filepath.Join(root, key)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", "")
	if err := os.Unsetenv("AGENT_NOTIFICATIONS_CONFIG"); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInstallDiagnosticStatus(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing", false: "changed"}[missing], func(t *testing.T) {
			fakeUser := installDiagnosticIsolation(t)
			sentinel := filepath.Join(fakeUser, "HOME", "sentinel")
			setupCommandWrite(t, sentinel, "fake user secret", 0600)
			f := newSetupCommandFixture(t)
			s, err := installruntime.ReadInstalledSnapshot(f.control)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.runtime, "hook")
			code := "managed_file_changed"
			if missing {
				code = "managed_file_missing"
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				setupCommandWrite(t, path, "private payload never printed", 0600)
			}
			before := setupCommandTree(t, f.root)
			userBefore := setupCommandTree(t, fakeUser)
			var out bytes.Buffer
			if got := agentNotifySetupExecute(context.Background(), []string{"status", "--control-root", f.control, "--json"}, &out, f.composition); got != 1 {
				t.Fatalf("exit=%d: %s", got, &out)
			}
			var result struct {
				Reason     string                              `json:"reason"`
				Generation uint64                              `json:"generation"`
				Diagnostic struct{ Code, Path, Action string } `json:"diagnostic"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Reason != "installation_invalid" || result.Generation != s.Ledger.Generation || result.Diagnostic.Code != code || result.Diagnostic.Path != path || result.Diagnostic.Action == "" {
				t.Fatalf("result=%+v output=%s", result, &out)
			}
			if strings.Contains(out.String(), "private payload") || strings.Contains(out.String(), "fake user secret") {
				t.Fatal("content leaked")
			}
			out.Reset()
			if got := agentNotifySetupExecute(context.Background(), []string{"status", "--control-root", f.control}, &out, f.composition); got != 1 {
				t.Fatalf("human exit=%d", got)
			}
			for _, text := range []string{code, path, "read-only", "trusted"} {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("missing %q: %s", text, &out)
				}
			}
			if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) || !reflect.DeepEqual(userBefore, setupCommandTree(t, fakeUser)) {
				t.Fatal("status mutated installation or fake user")
			}
		})
	}
}

func TestInstallDiagnosticEscapedBoundedPath(t *testing.T) {
	fakeUser := installDiagnosticIsolation(t)
	sentinel := filepath.Join(fakeUser, "HOME", "sentinel")
	setupCommandWrite(t, sentinel, "fake outside secret", 0600)
	root := setupCommandRoot(t)
	runtime := filepath.Join(root, "runtime")
	// This short native filename is valid on Linux and Darwin. Long and invalid
	// UTF-8 diagnostic strings are exercised without filesystem IO in installruntime.
	path := filepath.Join(runtime, "quoted\"\n\x1b-hook")
	control := filepath.Join(root, "control")
	ledger, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "hooks",
		Files: []installruntime.File{{Path: path, Data: []byte("private payload"), Mode: 0600}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	before, userBefore := setupCommandTree(t, root), setupCommandTree(t, fakeUser)
	composition := agentNotifySetupComposition{globalConfigPath: func() (string, error) { return filepath.Join(root, "global.json"), nil }}
	const action = "Read the installer's read-only recovery preview, or reinstall from a trusted source; then reread status."
	for _, format := range []string{"json", "human"} {
		t.Run(format, func(t *testing.T) {
			args := []string{"status", "--control-root", control}
			if format == "json" {
				args = append(args, "--json")
			}
			var out bytes.Buffer
			if code := agentNotifySetupExecute(context.Background(), args, &out, composition); code != 1 {
				t.Fatalf("exit=%d output=%q", code, out.String())
			}
			output := out.String()
			if strings.Count(output, "\n") != 1 || !strings.HasSuffix(output, "\n") || strings.Contains(output, "\x1b") || strings.Contains(output, "private payload") || strings.Contains(output, "fake outside secret") {
				t.Fatalf("unsafe output: %q", output)
			}
			if format == "json" {
				var result agentNotifySetupResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Generation != ledger.Generation || result.Reason != "installation_invalid" || result.Diagnostic == nil || result.Diagnostic.Code != "managed_file_missing" || result.Diagnostic.Path != path || result.Diagnostic.Action != action {
					t.Fatalf("bad result: %+v output=%q", result, output)
				}
				if !strings.Contains(output, `quoted\"\n\u001b-hook`) {
					t.Fatalf("missing escaped JSON path: %q", output)
				}
			} else {
				// Independent literal escaping of the hostile basename proves the
				// production formatter quotes the real snapshot diagnostic path.
				escapedPath := "\"" + runtime + `/quoted\"\n\x1b-hook` + "\""
				want := "installation_invalid; generation=" + strconv.FormatUint(ledger.Generation, 10) + "; diagnostic=managed_file_missing; path=" + escapedPath + ". " + action + "\n"
				if output != want {
					t.Fatalf("human output=%q want=%q", output, want)
				}
			}
			if !reflect.DeepEqual(before, setupCommandTree(t, root)) || !reflect.DeepEqual(userBefore, setupCommandTree(t, fakeUser)) {
				t.Fatal("status mutated installation or fake outside sentinel tree")
			}
		})
	}
}

func TestInstallDiagnosticOmittedWithoutSnapshotFailure(t *testing.T) {
	installDiagnosticIsolation(t)
	root := setupCommandRoot(t)
	composition := agentNotifySetupComposition{globalConfigPath: func() (string, error) { return filepath.Join(root, "global.json"), nil }}
	var out bytes.Buffer
	if code := agentNotifySetupExecute(context.Background(), []string{"status", "--control-root", filepath.Join(root, "missing"), "--json"}, &out, composition); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if _, ok := result["diagnostic"]; ok {
		t.Fatalf("invented diagnostic: %s", &out)
	}
	if _, ok := result["generation"]; ok {
		t.Fatalf("invented generation: %s", &out)
	}
}

func TestInstallDiagnosticInvalidLedgerOmitsGeneration(t *testing.T) {
	installDiagnosticIsolation(t)
	f := newSetupCommandFixture(t)
	if err := os.Truncate(filepath.Join(f.control, "ownership.json"), 1); err != nil {
		t.Fatal(err)
	}
	before := setupCommandTree(t, f.root)
	var out bytes.Buffer
	if code := agentNotifySetupExecute(context.Background(), []string{"status", "--control-root", f.control, "--json"}, &out, f.composition); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if _, ok := result["generation"]; ok {
		t.Fatalf("invalid ledger generation exposed: %s", &out)
	}
	if !strings.Contains(string(result["diagnostic"]), "ledger_invalid") {
		t.Fatalf("missing ledger diagnostic: %s", &out)
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("status mutated corrupt ledger")
	}
}
