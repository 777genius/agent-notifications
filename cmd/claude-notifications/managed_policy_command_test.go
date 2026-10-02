package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/opencodeplugin"
	"github.com/777genius/agent-notifications/internal/testenv"
)

// This is persisted metadata in a private TEST directory, not an installation:
// no executable is launched and no installer or event consumer is called.
func managedPolicyFixture(t *testing.T, custom ...string) (string, installruntime.Ledger) {
	t.Helper()
	testenv.Set(t, t.TempDir())
	root, err := installruntime.ControlRoot()
	if err != nil {
		t.Fatal(err)
	}
	if len(custom) > 0 {
		root = filepath.Join(filepath.Dir(root), custom[0])
	}
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	runtimeRoot := filepath.Join(root, "runtime")
	binary := filepath.Join(runtimeRoot, "claude-notifications-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	write(binary, []byte("private inert TEST metadata file; never executed"))
	r := installruntime.OpenCodeRegistration{Origin: strings.Repeat("a", 64), Salt: strings.Repeat("b", 64), Namespace: strings.Repeat("c", 64), OriginBound: true}
	bundle, err := (opencodeplugin.RegistrationRenderer{}).RenderRegistration(binary, root, r.Origin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bundle)
	r.BundleSHA256 = hex.EncodeToString(sum[:])
	plugin := filepath.Join(filepath.Dir(root), "opencode", "plugins", "agent-notifications.js")
	write(plugin, bundle)
	files := map[string]installruntime.Identity{}
	for _, p := range []string{binary, plugin} {
		id, err := installruntime.Fingerprint(p)
		if err != nil {
			t.Fatal(err)
		}
		files[p] = id
	}
	l := installruntime.Ledger{Schema: 4, WriterFloor: 3, ID: "TEST-managed-policy", Owner: "existing-installer", RuntimeRoot: runtimeRoot, Generation: 7, PolicyGeneration: 11,
		Consumers: map[string]installruntime.Consumer{"opencode-notifications": {RuntimeRoot: runtimeRoot, Registration: plugin, Commands: []string{binary, "opencode-event", "--protocol", "1"}, OpenCode: &r}, "unrelated-agent": {RuntimeRoot: runtimeRoot, Registration: plugin}}, Files: files}
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "ownership.json"), data)
	write(filepath.Join(root, "policy-generation.json"), []byte(`{"Generation":11,"Enabled":false}`))
	write(filepath.Join(root, "agent-notifications.json"), []byte(`{"schemaVersion":1,"enabled":false,"route":{"openCodeNotifications":{"desktop":false,"webhook":true,"future":99},"unrelatedAgent":{"enabled":true}},"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":true,"url":"${TEST_ENDPOINT}"}},"statuses":{"task_complete":{"enabled":true},"foreign_status":{"enabled":false}},"future":{"integer":9007199254740993}}`))
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock", installruntime.OpenCodeStoreLock} {
		write(filepath.Join(root, name), nil)
	}
	payloadSum := sha256.Sum256([]byte(`{}`))
	claims, err := json.Marshal(map[string]any{"Schema": 1, "Origin": r.Origin, "Namespace": r.Namespace, "SHA256": hex.EncodeToString(payloadSum[:]), "Payload": json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "opencode-admission", r.Namespace, "claims.json"), claims)
	return root, l
}

func policyCLI(t *testing.T, args []string, input string) (int, []byte, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := configCommand(args, strings.NewReader(input), &out, &stderr)
	return code, out.Bytes(), stderr.String()
}
func policyRevision(t *testing.T) string {
	t.Helper()
	code, out, stderr := policyCLI(t, []string{"inspect", "--target", "opencode", "--json"}, "")
	if code != 0 {
		t.Fatalf("managed inspect: %s", stderr)
	}
	var i config.Inspection
	if json.Unmarshal(out, &i) != nil || !i.Valid || i.Revision == "" {
		t.Fatalf("invalid inspection: %s", out)
	}
	return i.Revision
}
func treeBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		result[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// Catches the installed-consumer/shared-config mismatch, lost raw values,
// rewritten setup consent/incarnation, and writes without generation publication.
func TestManagedPolicyCLIEditUsesInstalledPolicy(t *testing.T) {
	root, before := managedPolicyFixture(t)
	ordinary := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(ordinary, []byte(`{"notifications":{"desktop":{"volume":0.8}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.OverrideEnv, ordinary)
	t.Setenv("AGENT_NOTIFICATIONS_CONTROL_ROOT", filepath.Join(t.TempDir(), "untrusted"))
	rev := policyRevision(t)
	code, out, stderr := policyCLI(t, []string{"path", "--target", "opencode", "--json"}, "")
	var selection config.Selection
	if code != 0 || json.Unmarshal(out, &selection) != nil || selection.Path != filepath.Join(root, "agent-notifications.json") {
		t.Fatalf("wrong policy selection: %s %s", out, stderr)
	}
	oldTree := treeBytes(t, root)
	code, _, stderr = policyCLI(t, []string{"edit", "--target", "opencode", "--stdin", "--expect-revision", rev}, `{"set":{"/notifications/desktop/volume":0.25}}`)
	if code != 0 {
		t.Fatalf("managed edit: %s", stderr)
	}
	after, recovery, err := installruntime.ReadOwnership(root)
	if err != nil || recovery || after.Generation != before.Generation+1 || after.PolicyGeneration != before.PolicyGeneration+1 {
		t.Fatalf("generation not committed: %+v %v", after, err)
	}
	if !reflect.DeepEqual(before.Consumers, after.Consumers) || !reflect.DeepEqual(before.Files, after.Files) || before.ID != after.ID || before.Owner != after.Owner {
		t.Fatal("edit changed installation/other agents")
	}
	data, err := os.ReadFile(selection.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{"9007199254740993", "${TEST_ENDPOINT}", "unrelatedAgent", "foreign_status", "\"future\": 99"} {
		if !bytes.Contains(data, []byte(literal)) {
			t.Fatalf("lost raw policy value: %s", literal)
		}
	}
	d, err := config.ParseDocument(data, selection.Path, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := d.Effective(config.ValidationAssets(""))
	if err != nil || cfg.Notifications.Desktop.Volume != 0.25 {
		t.Fatalf("consumer settings unchanged: %v", err)
	}
	newTree := treeBytes(t, root)
	for p, b := range oldTree {
		if p != selection.Path && p != filepath.Join(root, "ownership.json") && p != filepath.Join(root, "policy-generation.json") && newTree[p] != b {
			t.Fatalf("unrelated file changed: %s", p)
		}
	}
	shared, err := os.ReadFile(ordinary)
	if err != nil || string(shared) != `{"notifications":{"desktop":{"volume":0.8}}}` {
		t.Fatal("managed edit changed ordinary target")
	}
	// A repeat retains all bytes and both generations.
	noOpRev := policyRevision(t)
	stable := treeBytes(t, root)
	code, _, stderr = policyCLI(t, []string{"edit", "--target", "opencode", "--stdin", "--expect-revision", noOpRev}, `{"set":{"/notifications/desktop/volume":0.25}}`)
	if code != 0 || !reflect.DeepEqual(stable, treeBytes(t, root)) {
		t.Fatalf("no-op wrote files: %s", stderr)
	}
	// Path-bound CAS refuses the previously displayed revision without side effects.
	code, _, stderr = policyCLI(t, []string{"edit", "--target", "opencode", "--stdin", "--expect-revision", rev}, `{"set":{"/notifications/desktop/volume":0.75}}`)
	if code == 0 || !strings.Contains(stderr, "ConfigConflict") || !reflect.DeepEqual(stable, treeBytes(t, root)) {
		t.Fatal("stale revision wrote policy")
	}
}

// Catches silent fallback/bootstrap on absent, corrupt, stale or unknown targets.
func TestManagedPolicyCLIInvalidMetadataWritesNothing(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt", "duplicate-metadata", "unknown-consumer", "wrong-command", "wrong-bound-root", "generation", "recovery", "missing-policy", "missing-lock", "unknown-target"} {
		t.Run(kind, func(t *testing.T) {
			root, l := managedPolicyFixture(t)
			rev := policyRevision(t)
			args := []string{"edit", "--target", "opencode", "--stdin", "--expect-revision", rev}
			switch kind {
			case "missing":
				if err := os.Remove(filepath.Join(root, "ownership.json")); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(root, "ownership.json"), []byte(`{`), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate-metadata":
				data, err := os.ReadFile(filepath.Join(root, "ownership.json"))
				if err != nil {
					t.Fatal(err)
				}
				data = append([]byte(`{"Generation":7,`), data[1:]...)
				if err = os.WriteFile(filepath.Join(root, "ownership.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown-consumer":
				delete(l.Consumers, "opencode-notifications")
			case "wrong-command":
				c := l.Consumers["opencode-notifications"]
				c.Commands[1] = "another-agent"
				l.Consumers["opencode-notifications"] = c
			case "wrong-bound-root":
				c := l.Consumers["opencode-notifications"]
				bundle, err := (opencodeplugin.RegistrationRenderer{}).RenderRegistration(c.Commands[0], filepath.Join(root, "elsewhere"), c.OpenCode.Origin)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(c.Registration, bundle, 0600); err != nil {
					t.Fatal(err)
				}
				id, err := installruntime.Fingerprint(c.Registration)
				if err != nil {
					t.Fatal(err)
				}
				l.Files[c.Registration] = id
				c.OpenCode.BundleSHA256 = id.SHA256
				l.Consumers["opencode-notifications"] = c
			case "generation":
				l.Generation++ // Bytes unchanged: installation-bound CAS must still fail.
			case "recovery":
				if err := os.WriteFile(filepath.Join(root, "transaction.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-policy":
				if err := os.Remove(filepath.Join(root, "agent-notifications.json")); err != nil {
					t.Fatal(err)
				}
			case "missing-lock":
				if err := os.Remove(filepath.Join(root, "agent-notifications.json.lock")); err != nil {
					t.Fatal(err)
				}
			case "unknown-target":
				args[2] = "unknown"
			}
			if kind == "unknown-consumer" || kind == "wrong-command" || kind == "wrong-bound-root" || kind == "generation" {
				data, err := json.Marshal(l)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(root, "ownership.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := treeBytes(t, root)
			code, _, _ := policyCLI(t, args, `{"set":{"/notifications/desktop/volume":0.25}}`)
			if code == 0 || !reflect.DeepEqual(before, treeBytes(t, root)) {
				t.Fatal("invalid managed target wrote files or succeeded")
			}
		})
	}
}

// Catches bypasses of either permanent component/config lock at the public boundary.
func TestManagedPolicyCLIRespectsExistingLocks(t *testing.T) {
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		t.Run(name, func(t *testing.T) {
			root, _ := managedPolicyFixture(t)
			rev := policyRevision(t)
			before := treeBytes(t, root)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			release, err := installruntime.LockExisting(ctx, filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan int, 1)
			go func() {
				var out, stderr bytes.Buffer
				done <- configCommand([]string{"edit", "--target", "opencode", "--stdin", "--expect-revision", rev}, strings.NewReader(`{"set":{"/notifications/desktop/volume":0.25}}`), &out, &stderr)
			}()
			select {
			case <-done:
				release()
				t.Fatal("edit bypassed held lock")
			case <-time.After(100 * time.Millisecond):
			}
			if !reflect.DeepEqual(before, treeBytes(t, root)) {
				release()
				t.Fatal("files changed while lock held")
			}
			release()
			select {
			case code := <-done:
				if code != 0 {
					t.Fatal("edit failed after lock release")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("edit failed to resume")
			}
		})
	}
}

// Catches setup/config selection drift for an explicitly placed installation,
// arbitrary config-file authority, and changes to the ordinary CLI contract.
func TestManagedPolicyCLIExplicitRootAndSharedCompatibility(t *testing.T) {
	root, _ := managedPolicyFixture(t, "TEST-custom-control")
	home := os.Getenv("HOME")
	before := treeBytes(t, home)
	for _, args := range [][]string{
		{"path", "--target", "opencode"},
		{"path", "--target", "unknown"},
		{"path", "--target", ""},
		{"path", "--target", "shared", "--control-root", root},
		{"init", "--target", "opencode"},
		{"preflight-update", "--target", "opencode", "--json", "--stdin"},
		{"inspect", "--target", "opencode", "--json", "--control-root", filepath.Join(root, "agent-notifications.json")},
		{"inspect", "--target", "opencode", "--json", "--control-root", filepath.Join(root, "missing")},
	} {
		code, _, _ := policyCLI(t, args, `{}`)
		if code == 0 || !reflect.DeepEqual(before, treeBytes(t, home)) {
			t.Fatalf("unsupported selection wrote files: %v", args)
		}
	}
	code, out, stderr := policyCLI(t, []string{"inspect", "--target", "opencode", "--control-root", root, "--json"}, "")
	var inspection config.Inspection
	if code != 0 || json.Unmarshal(out, &inspection) != nil || inspection.Selection.Path != filepath.Join(root, "agent-notifications.json") {
		t.Fatalf("custom route: %s %s", out, stderr)
	}
	code, _, stderr = policyCLI(t, []string{"edit", "--target", "opencode", "--control-root", root, "--stdin", "--expect-revision", inspection.Revision}, `{"set":{"/notifications/webhook/url":"${TEST_OTHER_ENDPOINT}"}}`)
	if code != 0 {
		t.Fatalf("custom edit: %s", stderr)
	}
	edited, err := os.ReadFile(inspection.Selection.Path)
	if err != nil || !bytes.Contains(edited, []byte("${TEST_OTHER_ENDPOINT}")) {
		t.Fatal("custom root edit went elsewhere")
	}
	managedBefore := treeBytes(t, root)
	ordinary := filepath.Join(home, "TEST-shared.json")
	t.Setenv(config.OverrideEnv, ordinary)
	for _, selector := range [][]string{nil, {"--target", "shared"}} {
		initArgs := append([]string{"init", "--json"}, selector...)
		code, _, stderr = policyCLI(t, initArgs, "")
		if code != 0 {
			t.Fatalf("ordinary init: %s", stderr)
		}
		inspectArgs := append([]string{"inspect", "--json"}, selector...)
		code, out, stderr = policyCLI(t, inspectArgs, "")
		if code != 0 || json.Unmarshal(out, &inspection) != nil || inspection.Selection.Path != ordinary {
			t.Fatalf("ordinary selection: %s %s", out, stderr)
		}
		editArgs := append([]string{"edit", "--stdin", "--expect-revision", inspection.Revision}, selector...)
		code, _, stderr = policyCLI(t, editArgs, `{"set":{"/notifications/desktop/volume":0.4}}`)
		if code != 0 {
			t.Fatalf("ordinary edit: %s", stderr)
		}
	}
	if !reflect.DeepEqual(managedBefore, treeBytes(t, root)) {
		t.Fatal("ordinary edit changed managed policy")
	}
}

// Catches valid ordinary config edits that exceed the installed policy budget,
// and attempts to use config editing to change setup-owned consent/origin.
func TestManagedPolicyCLIRejectsUnpublishableEdits(t *testing.T) {
	root, _ := managedPolicyFixture(t)
	rev := policyRevision(t)
	before := treeBytes(t, root)
	oversized, err := json.Marshal(config.Edits{Set: map[string]json.RawMessage{"/notifications/webhook/headers/TEST": json.RawMessage(`"` + strings.Repeat("x", 70*1024) + `"`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{string(oversized), `{"set":{"/route/openCodeNotifications/desktop":true}}`, `{"set":{"/enabled":true}}`, `{"set":{"/schemaVersion":2}}`} {
		code, _, _ := policyCLI(t, []string{"edit", "--target", "opencode", "--stdin", "--expect-revision", rev}, input)
		if code == 0 || !reflect.DeepEqual(before, treeBytes(t, root)) {
			t.Fatal("unpublishable or setup-owned edit wrote files")
		}
	}
}
