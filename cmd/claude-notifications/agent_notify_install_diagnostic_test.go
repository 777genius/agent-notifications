//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func installDiagnosticIsolation(t *testing.T) string {
	t.Helper()
	root := setupCommandRoot(t)
	for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "CLAUDE_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "APPDATA", "LOCALAPPDATA", "TMPDIR"} {
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
	installDiagnosticIsolation(t)
	root := setupCommandRoot(t)
	runtime := filepath.Join(root, "runtime")
	// Real tracked path with terminal controls and a length over the diagnostic
	// budget; short components keep the fixture below the filesystem path limit.
	path := runtime
	for i := 0; i < 6; i++ {
		path = filepath.Join(path, strings.Repeat("x", 180))
	}
	path = filepath.Join(path, "quoted\"\n\x1bfile")
	control := filepath.Join(root, "control")
	global := filepath.Join(root, "global.json")
	ledger, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "hooks", Files: []installruntime.File{{Path: path, Data: []byte("private"), Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	composition := agentNotifySetupComposition{globalConfigPath: func() (string, error) { return global, nil }}
	var out bytes.Buffer
	if code := agentNotifySetupExecute(context.Background(), []string{"status", "--control-root", control, "--json"}, &out, composition); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	var result struct {
		Generation uint64                            `json:"generation"`
		Diagnostic installruntime.SnapshotDiagnostic `json:"diagnostic"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Generation != ledger.Generation || result.Diagnostic.Code != "managed_file_missing" || len(result.Diagnostic.Path) > 1024 || !strings.HasSuffix(result.Diagnostic.Path, "...") {
		t.Fatalf("bad result: %+v", result)
	}
	// Short path preserves controls for JSON escaping and quoted human output.
	short := filepath.Join(runtime, "quoted\"\n\x1bfile")
	// A second fresh fixture avoids repairing the intentionally invalid one.
	control2 := filepath.Join(root, "control2")
	if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: control2, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "hooks", Files: []installruntime.File{{Path: short, Data: []byte("private"), Mode: 0600}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(short); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := agentNotifySetupExecute(context.Background(), []string{"status", "--control-root", control2, "--json"}, &out, composition); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Diagnostic.Path != short || strings.Contains(out.String(), "\x1b") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("unsafe JSON: %q", out.String())
	}
	out.Reset()
	if code := agentNotifySetupExecute(context.Background(), []string{"status", "--control-root", control2}, &out, composition); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	if strings.Contains(out.String(), "\x1b") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("unsafe human output: %q", out.String())
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
