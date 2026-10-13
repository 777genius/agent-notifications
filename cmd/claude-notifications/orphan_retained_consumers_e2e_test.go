//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/codexcommand"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Regression: a deleted TEST Codex installation must be recoverable while the
// shared ledger retains generated Codex commands, legacy OpenCode argv, and
// canonical portable registration JSON. The old retained-reference decoder
// rejects those legitimate records before the metadata-only recovery can run.
func TestOrphanRecoveryRealCLISixKnownConsumers(t *testing.T) {
	compiler, err := orphanCLIObserveCompiler() // Observe caches before HOME isolation.
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(os.Getenv("TMPDIR"), "TEST-orphan-six-consumers-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	env := orphanCLIEnvironment(t, root)
	binary, evidence := orphanCLIRealBinary(t, compiler, env, root)
	control, err := installruntime.ControlRoot()
	if err != nil {
		t.Fatal(err)
	}
	control, err = installruntime.CanonicalPath(control)
	if err != nil || !orphanCLIWithin(root, control) {
		t.Fatalf("control escaped TEST: %s %v", control, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	primary := filepath.Join(root, "primary")
	liveCodex := filepath.Join(root, "live-codex", codexcommand.InstallDirName)
	orphan := filepath.Join(root, "deleted-TEST-codex", codexcommand.InstallDirName)
	registration := filepath.Join(filepath.Dir(orphan), "hooks.json")
	orphanID := "codex:" + registration
	complete := false
	t.Cleanup(func() {
		if t.Failed() || !complete {
			t.Logf("PRESERVED TEST fixture=%s evidence=%s", root, evidence)
			return
		}
		ledger, pending, e := installruntime.ReadOwnership(control)
		if e != nil || pending {
			t.Errorf("teardown ownership: %v", e)
			return
		}
		ids := make([]string, 0, len(ledger.Consumers))
		for id := range ledger.Consumers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cleanupCancel()
		// Revoke every TEST consumer before deleting its fixture, preserving evidence.
		for _, id := range ids {
			if _, e = installruntime.Commit(cleanupCtx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: ledger.Consumers[id].RuntimeRoot, ConsumerID: id, RemoveConsumer: true}); e != nil {
				t.Errorf("teardown %s: %v", id, e)
				return
			}
		}
		ledger, pending, e = installruntime.ReadOwnership(control)
		if e != nil || pending || len(ledger.Consumers) != 0 {
			t.Errorf("teardown ownership leak: %v", e)
			return
		}
		if os.Getenv("ORPHAN_CLI_KEEP_FIXTURES") == "1" {
			t.Logf("preserved verified TEST fixture=%s", root)
			return
		}
		if e = os.RemoveAll(root); e != nil {
			t.Error(e)
		}
	})
	commit := func(runtimeRoot, id string, consumer installruntime.Consumer, files []installruntime.File) installruntime.Ledger {
		t.Helper()
		ledger, e := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: runtimeRoot, ConsumerID: id, Consumer: consumer, Files: files})
		if e != nil {
			t.Fatalf("register TEST consumer %s: %v", id, e)
		}
		return ledger
	}
	// Seven shared runtime assets and two identical 21-file Codex distributions.
	// All payloads are inert; only the separate real CLI binary is executed.
	primaryNames := []string{portable.PlatformPrimary(), filepath.Base(portable.PlatformPrimary()), "hooks/hooks.json", "skills/agent-notifications/SKILL.md", "README.md", "LICENSE", "sounds/default.wav"}
	payloadNames := []string{
		"bin/codex-hook-wrapper.sh", "bin/codex-hook-wrapper.cmd",
		"bin/claude-notifications-darwin-arm64", "bin/claude-notifications-darwin-amd64",
		"bin/claude-notifications-linux-arm64", "bin/claude-notifications-linux-amd64",
		"bin/claude-notifications-windows-amd64.exe", "bin/claude-notifications.sh",
		"hooks/hooks-codex.json", "skills/agent-notifications/SKILL.md",
		"README.md", "LICENSE", "config/default.json",
		"sounds/complete.wav", "sounds/permission.wav", "sounds/error.wav", "sounds/start.wav",
		"docs/INSTALLATION.md", "docs/CODEX.md", "docs/CONFIGURATION.md", "docs/TROUBLESHOOTING.md",
	}
	files := func(runtimeRoot string, names []string) []installruntime.File {
		result := make([]installruntime.File, 0, len(names))
		for _, name := range names {
			mode := uint32(0644)
			if filepath.Dir(name) == "bin" || name == filepath.Base(portable.PlatformPrimary()) {
				mode = 0755
			}
			result = append(result, installruntime.File{Path: filepath.Join(runtimeRoot, filepath.FromSlash(name)), Data: []byte("TEST inert distribution asset: " + name + "\n" + installruntime.WriterProtocolMarker + "\n" + installruntime.LocalWriterProtocolMarker), Mode: mode})
		}
		return result
	}
	primaryCommand := filepath.Join(primary, filepath.FromSlash(portable.PlatformPrimary()))
	commit(primary, "claude-hooks", installruntime.Consumer{Commands: []string{primaryCommand}}, files(primary, primaryNames))
	liveRegistration := filepath.Join(filepath.Dir(liveCodex), "hooks.json")
	embeddedPut(t, liveRegistration, []byte("TEST retained generated Codex registration\n"), 0600)
	commit(liveCodex, "codex:"+liveRegistration, installruntime.Consumer{Registration: liveRegistration, Commands: codexcommand.Commands(liveCodex)}, files(liveCodex, payloadNames))
	embeddedPut(t, registration, []byte("TEST deleted Codex registration\n"), 0600)
	commit(orphan, orphanID, installruntime.Consumer{Registration: registration, Commands: codexcommand.Commands(orphan)}, files(orphan, payloadNames))
	legacyRegistration := filepath.Join(root, "config", "opencode", "plugins", "agent-notifications.js")
	embeddedPut(t, legacyRegistration, []byte("// TEST retained legacy OpenCode registration\n"), 0600)
	before := commit(primary, "opencode-notifications", installruntime.Consumer{Registration: legacyRegistration, Commands: []string{filepath.Join(primary, filepath.Base(portable.PlatformPrimary())), "opencode-event", "--protocol", "1"}}, nil)
	for _, integration := range []portable.Integration{portable.Claude, portable.Codex} {
		binding := portable.Binding{Version: 1, Integration: integration, InstallationID: "TEST-six-consumers", BindingID: "TEST-" + string(integration), ScopeID: "user", ComponentID: before.ID, Owner: before.Owner, ScopeRoot: filepath.Join(root, string(integration)), DataRoot: filepath.Join(root, "data"), ControlRoot: control, GlobalConfig: filepath.Join(control, "agent-notifications.json"), RuntimeRoot: primary, Primary: portable.PlatformPrimary()}
		id, consumer, _, e := binding.Registration()
		if e != nil {
			t.Fatal(e)
		}
		before = commit(primary, id, consumer, nil)
	}
	if len(before.Consumers) != 6 || len(before.Files) != 49 || before.Native != nil {
		t.Fatalf("six native-free consumers/49 files required: %+v", before)
	}
	rawPolicy := []byte("{\n  \"schemaVersion\":1, \"enabled\":false, \"unknownOperatorField\":{\"keep\":true},\n  \"route\":{\"navigation\":false}, \"setupState\":{\"foreignConsent\":false}\n}\n")
	embeddedPut(t, filepath.Join(control, "agent-notifications.json"), rawPolicy, 0600)
	if _, err = installruntime.ReadInstalledSnapshot(control); err != nil {
		t.Fatalf("initial published snapshot: %v", err)
	}
	sentinel := filepath.Join(evidence, "six-consumers-outside", "control")
	embeddedPut(t, filepath.Join(sentinel, "ownership-sentinel"), []byte("TEST outside all consumer roots\n"), 0600)
	outsideBefore := orphanCLITree(t, sentinel)
	retainedPrimary, retainedCodex := orphanCLITree(t, primary), orphanCLITree(t, filepath.Dir(liveCodex))
	legacyBefore := embeddedRead(t, legacyRegistration)
	selected := []string{}
	for path := range before.Files {
		if orphanCLIWithin(orphan, path) {
			selected = append(selected, path)
		}
	}
	sort.Strings(selected)
	if len(selected) != 21 {
		t.Fatalf("selected=%d", len(selected))
	}
	if err = os.RemoveAll(orphan); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(registration); err != nil {
		t.Fatal(err)
	}
	beforeControl := orphanCLITree(t, control)
	orphanCLIArtifact(t, evidence, "six-consumers-before.json", before)
	args := []string{"internal-install-runtime", "--recover-orphan-consumer", "--consumer", orphanID, "--runtime-root", orphan, "--control-root", control, "--expected-installation-id", before.ID, "--expected-generation", strconv.FormatUint(before.Generation, 10), "--json"}
	preview := orphanCLIRun(t, binary, env, evidence, 0, append(append([]string(nil), args...), "--dry-run")...)
	var result runtimeRecoveryResult
	if err = json.Unmarshal(preview, &result); err != nil || !result.Admissible || result.SelectedCount != 21 || !reflect.DeepEqual(result.SelectedPaths, selected) || result.Generation != before.Generation {
		t.Fatalf("six known consumers preview: %s %v", preview, err)
	}
	if !reflect.DeepEqual(beforeControl, orphanCLITree(t, control)) {
		t.Fatal("dry-run mutated control")
	}
	orphanCLIRun(t, binary, env, evidence, 0, args...)
	after, pending, err := installruntime.ReadOwnership(control)
	if err != nil || pending {
		t.Fatalf("read recovered ledger: pending=%v err=%v", pending, err)
	}
	encoded, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var expected installruntime.Ledger
	if err = json.Unmarshal(encoded, &expected); err != nil {
		t.Fatal(err)
	}
	delete(expected.Consumers, orphanID)
	for _, path := range selected {
		delete(expected.Files, path)
	}
	expected.Generation++
	expected.PolicyGeneration++
	if !reflect.DeepEqual(after, expected) || len(after.Files) != 28 || len(after.Consumers) != 5 || after.Native != nil {
		t.Fatalf("unexpected recovery delta: got=%+v want=%+v", after, expected)
	}
	if !bytes.Equal(rawPolicy, embeddedRead(t, filepath.Join(control, "agent-notifications.json"))) || !reflect.DeepEqual(retainedPrimary, orphanCLITree(t, primary)) || !reflect.DeepEqual(retainedCodex, orphanCLITree(t, filepath.Dir(liveCodex))) || !bytes.Equal(legacyBefore, embeddedRead(t, legacyRegistration)) {
		t.Fatal("retained payload/registration/policy changed")
	}
	afterControl := orphanCLITree(t, control)
	if len(beforeControl) != len(afterControl) {
		t.Fatal("recovery created/removed control paths")
	}
	for path, entry := range beforeControl {
		got, exists := afterControl[path]
		if !exists || got.Mode != entry.Mode {
			t.Fatalf("control path/mode changed: %s", path)
		}
		if path != "ownership.json" && path != "policy-generation.json" && entry != got {
			t.Fatalf("unexpected control mutation: %s", path)
		}
	}
	embeddedAbsent(t, filepath.Join(control, "transaction.json"))
	embeddedAbsent(t, orphan)
	embeddedAbsent(t, registration)
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snapshot.Recovery || !reflect.DeepEqual(snapshot.Ledger, after) {
		t.Fatalf("recovered published snapshot unhealthy: %v", err)
	}
	if !reflect.DeepEqual(outsideBefore, orphanCLITree(t, sentinel)) {
		t.Fatal("outside TEST sentinel changed")
	}
	orphanCLIArtifact(t, evidence, "six-consumers-after.json", after)
	orphanCLIArtifact(t, evidence, "six-consumers-proof.json", map[string]any{"initial_consumers": 6, "retained_consumers": 5, "selected_files": 21, "retained_files": 28, "native": nil, "fixture": root})
	complete = true
	t.Logf("PROVED six known consumers: 49->28 files, generation %d->%d; fixture=%s evidence=%s", before.Generation, after.Generation, root, evidence)
}
