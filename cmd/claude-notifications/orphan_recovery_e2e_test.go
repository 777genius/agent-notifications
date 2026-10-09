//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/777genius/agent-notifications/internal/codexsetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Every subprocess receives this finite environment, never os.Environ(). Native
// qualification is a distinct Lane A fixture; actual setup-codex runs native-free
// here so it cannot reconcile a product native record through LaunchServices.
func orphanCLIEnvironment(t *testing.T, root string) []string {
	t.Helper()
	values := map[string]string{"PATH": "/usr/bin:/bin", "LANG": "C", "LC_ALL": "C"}
	for key, leaf := range map[string]string{
		"HOME": "home", "USERPROFILE": "home", "CODEX_HOME": "codex", "CLAUDE_HOME": "claude", "CLAUDE_CONFIG_DIR": "claude",
		"XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state", "XDG_RUNTIME_DIR": "run",
		"XDG_CONFIG_DIRS": "config-dirs", "XDG_DATA_DIRS": "data-dirs", "APPDATA": "appdata", "LOCALAPPDATA": "localappdata", "TMPDIR": "tmp", "TMP": "tmp", "TEMP": "tmp",
	} {
		path := filepath.Join(root, leaf)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		values[key] = path
		t.Setenv(key, path)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", "")
	if err := os.Unsetenv("AGENT_NOTIFICATIONS_CONFIG"); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	sort.Strings(env)
	return env
}

func orphanCLIWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

type orphanCLITreeEntry struct {
	Mode         fs.FileMode
	SHA256, Link string
}

func orphanCLITree(t *testing.T, root string) map[string]orphanCLITreeEntry {
	t.Helper()
	result := map[string]orphanCLITreeEntry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		e := orphanCLITreeEntry{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			e.SHA256 = fmt.Sprintf("%x", sha256.Sum256(b))
		} else if info.Mode()&os.ModeSymlink != 0 {
			e.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		result[rel] = e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func orphanCLIArtifact(t *testing.T, dir, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	embeddedPut(t, filepath.Join(dir, name), append(data, '\n'), 0600)
}

var orphanCLIBuild struct {
	sync.Once
	Binary, Evidence string
	Err              error
}

func orphanCLIValidateEvidence(root, evidence string) error {
	if !filepath.IsAbs(evidence) || filepath.Clean(evidence) != evidence {
		return fmt.Errorf("evidence requires a clean absolute path")
	}
	physical, err := installruntime.CanonicalPath(evidence)
	if err != nil || physical != evidence {
		return fmt.Errorf("evidence requires a physical path: %v", err)
	}
	if evidence == root || orphanCLIWithin(root, evidence) || orphanCLIWithin(evidence, root) {
		return fmt.Errorf("evidence must be separate from TEST fixture and its control/consumer roots")
	}
	return nil
}

func orphanCLIRealBinary(t *testing.T, env []string, root string) (string, string) {
	t.Helper()
	orphanCLIBuild.Do(func() {
		evidence := os.Getenv("ORPHAN_CLI_EVIDENCE_DIR")
		if evidence != "" {
			if err := orphanCLIValidateEvidence(root, evidence); err != nil {
				orphanCLIBuild.Err = err
				return
			}
			if err := os.MkdirAll(evidence, 0700); err != nil {
				orphanCLIBuild.Err = err
				return
			}
			evidence, orphanCLIBuild.Err = os.MkdirTemp(evidence, "run-")
		} else {
			evidence, orphanCLIBuild.Err = os.MkdirTemp(filepath.Dir(root), "TEST-orphan-cli-evidence-")
		}
		if orphanCLIBuild.Err != nil {
			return
		}
		evidence, orphanCLIBuild.Err = filepath.EvalSymlinks(evidence)
		if orphanCLIBuild.Err != nil {
			return
		}
		orphanCLIBuild.Evidence = evidence
		if err := os.MkdirAll(evidence, 0700); err != nil {
			orphanCLIBuild.Err = err
			return
		}
		orphanCLIBuild.Binary = filepath.Join(evidence, "claude-notifications")
		goPath, err := exec.LookPath("go")
		if err != nil {
			orphanCLIBuild.Err = err
			return
		}
		buildEnv := append([]string(nil), env...)
		// Only the job's prepared compiler/cache configuration crosses this boundary.
		for _, key := range []string{"GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE", "GOTMPDIR", "GOMAXPROCS", "CGO_ENABLED", "CC", "CXX", "CGO_CFLAGS", "CGO_LDFLAGS", "PKG_CONFIG_PATH", "SDKROOT", "DEVELOPER_DIR"} {
			if value := os.Getenv(key); value != "" {
				buildEnv = append(buildEnv, key+"="+value)
			}
		}
		buildEnv = append(buildEnv, "GOTOOLCHAIN=local", "GOTELEMETRY=off", "GOPROXY=off", "GOSUMDB=off")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, goPath, "build", "-mod=readonly", "-trimpath", "-ldflags=-s -w", "-o", orphanCLIBuild.Binary, ".")
		cmd.Env = buildEnv
		cmd.WaitDelay = time.Second
		output, err := cmd.CombinedOutput()
		exit := -1
		if cmd.ProcessState != nil {
			exit = cmd.ProcessState.ExitCode()
		}
		log := fmt.Sprintf("command: %s build -mod=readonly -trimpath '-ldflags=-s -w' -o %s .\nexit: %d\n%s", goPath, orphanCLIBuild.Binary, exit, output)
		if e := os.WriteFile(filepath.Join(evidence, "build.log"), []byte(log), 0600); e != nil {
			orphanCLIBuild.Err = e
			return
		}
		orphanCLIBuild.Err = err
	})
	if orphanCLIBuild.Err != nil {
		t.Fatalf("real sender build: %v; evidence %s", orphanCLIBuild.Err, orphanCLIBuild.Evidence)
	}
	if err := orphanCLIValidateEvidence(root, orphanCLIBuild.Evidence); err != nil {
		t.Fatal(err)
	}
	image := embeddedRead(t, orphanCLIBuild.Binary)
	t.Logf("REAL sender binary=%s sha256=%x evidence=%s", orphanCLIBuild.Binary, sha256.Sum256(image), orphanCLIBuild.Evidence)
	return orphanCLIBuild.Binary, orphanCLIBuild.Evidence
}

type orphanCLIProcess struct {
	Args   []string
	Exit   int
	Output string
}

func orphanCLIRun(t *testing.T, binary string, env []string, evidence string, wantExit int, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			exit = e.ExitCode()
		} else {
			t.Fatalf("execute %v: %v", args, err)
		}
	}
	record := orphanCLIProcess{Args: args, Exit: exit, Output: string(output)}
	file, err := os.OpenFile(filepath.Join(evidence, "commands.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewEncoder(file).Encode(record)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	t.Logf("REAL CLI %q exit=%d", args, exit)
	if exit != wantExit {
		t.Fatalf("exit=%d want=%d for %v:\n%s", exit, wantExit, args, output)
	}
	return output
}

// This is a real CLI test, independently checking a metadata-only delta after
// setup-codex generated the sibling hooks.json and all eight opaque commands.
// Kernel fault replay and native APFS qualification remain in their owned lanes.
func TestOrphanRecoveryRealCLI21Absent28Retained(t *testing.T) {
	parent := os.Getenv("TMPDIR")
	root, err := os.MkdirTemp(parent, "TEST-orphan-real-cli-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Logf("TEST fixture=%s (preserved on failure)", root)
	// Capture cache settings before isolating runtime HOME/config in this process.
	env := orphanCLIEnvironment(t, root)
	binary, evidence := orphanCLIRealBinary(t, env, root)
	control, err := installruntime.ControlRoot()
	if err != nil {
		t.Fatal(err)
	}
	control, err = installruntime.CanonicalPath(control)
	if err != nil || !orphanCLIWithin(root, control) {
		t.Fatalf("default control escaped TEST: %s %v", control, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	primary := filepath.Join(root, "primary")
	runtimeRoot := filepath.Join(root, "codex", codexsetup.InstallDirName)
	registration := filepath.Join(root, "codex", "hooks.json")
	consumer := "codex:" + registration
	// Fake external user state is outside this fixture and all consumer roots.
	sentinelRoot := filepath.Join(evidence, "fake-external-user", "control")
	embeddedPut(t, filepath.Join(sentinelRoot, "ownership-sentinel"), []byte("outside fixture; must remain byte-identical"), 0600)
	sentinelBefore := orphanCLITree(t, sentinelRoot)
	orphanCLIArtifact(t, evidence, "outside-before.json", sentinelBefore)
	retained := make([]installruntime.File, 28)
	for i := range retained {
		retained[i] = installruntime.File{Path: filepath.Join(primary, fmt.Sprintf("retained-%02d", i)), Data: []byte(fmt.Sprintf("retained content %d", i)), Mode: 0640}
	}
	// The real sender remains an owned retained asset without ever sending.
	retained[0].Path = filepath.Join(primary, "bin", "claude-notifications-"+runtime.GOOS+"-"+runtime.GOARCH)
	retained[0].Data = embeddedRead(t, binary)
	retained[0].Mode = 0755
	cleanupReady := false
	t.Cleanup(func() {
		if t.Failed() || !cleanupReady {
			t.Logf("PRESERVED fixture=%s evidence=%s; ownership must be inspected before deletion", root, evidence)
			return
		}
		// Unregister while every retained managed asset still exists.
		for _, file := range retained {
			got, e := installruntime.Fingerprint(file.Path)
			if e != nil || !got.Exists {
				t.Errorf("teardown assets unavailable: %s %v", file.Path, e)
				return
			}
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cleanupCancel()
		if _, e := installruntime.Commit(cleanupCtx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: primary, ConsumerID: "retained", RemoveConsumer: true}); e != nil {
			t.Errorf("teardown unregister failed; preserved %s: %v", root, e)
			return
		}
		final, pending, e := installruntime.ReadOwnership(control)
		if e != nil || pending || len(final.Consumers) != 0 {
			t.Errorf("ownership leak; preserved %s: %v", root, e)
			return
		}
		orphanCLIArtifact(t, evidence, "teardown.json", map[string]any{"fixture": root, "consumers_remaining": len(final.Consumers), "pending": pending, "unregistered_before_fixture_delete": true})
		if e = os.RemoveAll(root); e != nil {
			t.Errorf("fixture deletion: %v", e)
			return
		}
		if _, e = os.Lstat(root); !os.IsNotExist(e) {
			t.Errorf("fixture still exists: %v", e)
		}
		if !reflect.DeepEqual(sentinelBefore, orphanCLITree(t, sentinelRoot)) {
			t.Error("external sentinel changed after fixture teardown")
		}
		for _, name := range []string{"claude-notifications", "build.log", "commands.jsonl", "teardown.json", "outside-before.json", "before-ledger.json", "after-ledger.json", "before-control.json", "after-control.json", "hostile-json.json", "hostile-human.json"} {
			if _, e = os.Stat(filepath.Join(evidence, name)); e != nil {
				t.Errorf("successful evidence lost after teardown: %s: %v", name, e)
			}
		}
		orphanCLIArtifact(t, evidence, "outside-after-teardown.json", orphanCLITree(t, sentinelRoot))
	})
	bootstrap, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: primary, ConsumerID: "retained", Files: retained})
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "bundle")
	for _, name := range []string{"codex-hook-wrapper.sh", "codex-hook-wrapper.cmd"} {
		// Real shipped wrapper bytes, never invoked during registration/recovery.
		embeddedPut(t, filepath.Join(bundle, "bin", name), embeddedRead(t, filepath.Join("..", "..", "bin", name)), 0755)
	}
	// No native or executable in this bootstrap bundle: ordinary setup-codex's
	// postcommit reconcile observes no native record and cannot invoke lsregister.
	orphanCLIRun(t, binary, env, evidence, 0, "setup-codex", "--codex-home", filepath.Join(root, "codex"), "--plugin-root", bundle, "--skip-agent-notify")
	before, pending, err := installruntime.ReadOwnership(control)
	if err != nil || pending {
		t.Fatal(err)
	}
	actual, ok := before.Consumers[consumer]
	if !ok || before.ID != bootstrap.ID || before.RuntimeRoot != primary || len(before.Consumers) != 2 || actual.RuntimeRoot != runtimeRoot || actual.Registration != registration || len(actual.Commands) != 8 || before.Native != nil {
		t.Fatalf("actual setup shape/shared installation wrong: %+v", before)
	}
	expectedCommands := []string{}
	for _, event := range []string{"PreToolUse", "Stop", "SubagentStop", "PermissionRequest"} {
		posix, windows := codexsetup.HookCommands(runtimeRoot, event)
		expectedCommands = append(expectedCommands, posix, windows)
	}
	if !reflect.DeepEqual(actual.Commands, expectedCommands) {
		t.Fatal("actual eight generated commands differ from production codec")
	}
	// Independently decode actual registration, not just the returned ledger.
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Windows string `json:"commandWindows"`
			}
		} `json:"hooks"`
	}
	if err = json.Unmarshal(embeddedRead(t, registration), &hooks); err != nil {
		t.Fatal(err)
	}
	for i, event := range []string{"PreToolUse", "Stop", "SubagentStop", "PermissionRequest"} {
		groups := hooks.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Command != expectedCommands[2*i] || groups[0].Hooks[0].Windows != expectedCommands[2*i+1] {
			t.Fatalf("actual sibling registration wrong for %s", event)
		}
	}
	if _, tracked := before.Files[registration]; tracked {
		t.Fatal("external sibling registration is tracked as runtime Files")
	}
	selected := []string{}
	for path := range before.Files {
		if orphanCLIWithin(runtimeRoot, path) {
			selected = append(selected, path)
		}
	}
	extra := []installruntime.File{}
	for i := len(selected); i < 21; i++ {
		extra = append(extra, installruntime.File{Path: filepath.Join(runtimeRoot, "payload", fmt.Sprintf("extra-%02d", i)), Data: []byte(fmt.Sprintf("legitimate extension %d", i)), Mode: 0644})
	}
	before, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: runtimeRoot, ConsumerID: consumer, RefreshOnly: true, Files: extra})
	if err != nil {
		t.Fatal(err)
	}
	selected = nil
	for path := range before.Files {
		if orphanCLIWithin(runtimeRoot, path) {
			selected = append(selected, path)
		}
	}
	sort.Strings(selected)
	if len(selected) != 21 || len(before.Files) != 49 || len(before.Consumers) != 2 || before.ID == "" {
		t.Fatalf("expected one installation 21/28 split, selected=%d total=%d", len(selected), len(before.Files))
	}
	if before.ID != bootstrap.ID || !reflect.DeepEqual(before.Consumers[consumer], actual) || !reflect.DeepEqual(before.Consumers["retained"], bootstrap.Consumers["retained"]) {
		t.Fatal("legitimate extension changed installation ID or actual consumer records")
	}
	rawPolicy := []byte("{\n  \"schemaVersion\":1, \"enabled\":false, \"unknownOperatorField\":{\"keep\":true},\n  \"route\":{\"navigation\":false}, \"rates\":{\"custom\":7}, \"setupState\":{\"foreignConsent\":false}\n}\n")
	embeddedPut(t, filepath.Join(control, "agent-notifications.json"), rawPolicy, 0600)
	if _, err = installruntime.ReadInstalledSnapshot(control); err != nil {
		t.Fatalf("bootstrap snapshot invalid: %v", err)
	}
	controlInfo, err := os.Stat(control)
	if err != nil || controlInfo.Mode().Perm() != 0700 {
		t.Fatal("control not physically private", err)
	}
	policyBefore := orphanCLITree(t, control)
	retainedBefore := orphanCLITree(t, primary)
	orphanCLIArtifact(t, evidence, "before-ledger.json", before)
	orphanCLIArtifact(t, evidence, "before-control.json", policyBefore)
	orphanCLIArtifact(t, evidence, "before-retained.json", retainedBefore)
	orphanCLIArtifact(t, evidence, "before-orphan.json", orphanCLITree(t, runtimeRoot))
	embeddedPut(t, filepath.Join(evidence, "before-policy.json"), embeddedRead(t, filepath.Join(control, "agent-notifications.json")), 0600)
	embeddedPut(t, filepath.Join(evidence, "before-hooks.json"), embeddedRead(t, registration), 0600)
	orphanCLIArtifact(t, evidence, "fixture.json", map[string]any{"fixture_root": root, "physical_default_control": control, "installation_id": before.ID, "absent_count": 21, "retained_count": 28, "binary_sha256": fmt.Sprintf("%x", sha256.Sum256(embeddedRead(t, binary))), "platform": runtime.GOOS, "native_bootstrap": "native-free; Lane A independently qualifies retained native/APFS"})
	args := []string{"internal-install-runtime", "--recover-orphan-consumer", "--consumer", consumer, "--runtime-root", runtimeRoot, "--control-root", control, "--expected-installation-id", before.ID, "--expected-generation", strconv.FormatUint(before.Generation, 10), "--json"}
	// The only fixture deletion that simulates abandonment; recovery never owns it.
	for _, path := range selected {
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.RemoveAll(runtimeRoot); err != nil {
		t.Fatal(err)
	}
	rejectUnchanged := func(extra []string) {
		t.Helper()
		image := orphanCLITree(t, control)
		outside := orphanCLITree(t, filepath.Dir(runtimeRoot))
		orphanCLIRun(t, binary, env, evidence, 1, append(append([]string(nil), args...), extra...)...)
		if !reflect.DeepEqual(image, orphanCLITree(t, control)) || !reflect.DeepEqual(outside, orphanCLITree(t, filepath.Dir(runtimeRoot))) {
			t.Fatal("rejected CLI changed bytes/modes/paths")
		}
	}
	// Exercise main's separate stdout/stderr rendering, including --json. A
	// strict kernel rejection must not print attacker-controlled ledger keys.
	ledgerPath := filepath.Join(control, "ownership.json")
	ledgerPreimage := embeddedRead(t, ledgerPath)
	key := "INJECTED-OWNERSHIP-MARKER\n\x1b[31m" + strings.Repeat("hostile", 3000)
	encodedKey, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	hostile := append([]byte("{"), encodedKey...)
	hostile = append(hostile, []byte(":true,")...)
	hostile = append(hostile, ledgerPreimage[1:]...)
	embeddedPut(t, ledgerPath, hostile, 0600)
	hostileTree := orphanCLITree(t, root)
	for _, jsonMode := range []bool{false, true} {
		hostileArgs := append(append([]string(nil), args[:len(args)-1]...), "--dry-run")
		if jsonMode {
			hostileArgs = append(hostileArgs, "--json")
		}
		processCtx, processCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		cmd := exec.CommandContext(processCtx, binary, hostileArgs...)
		cmd.Env = env
		cmd.WaitDelay = time.Second
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		processCancel()
		label := "hostile-human"
		if jsonMode {
			label = "hostile-json"
		}
		exit := -1
		if cmd.ProcessState != nil {
			exit = cmd.ProcessState.ExitCode()
		}
		orphanCLIArtifact(t, evidence, label+".json", map[string]any{"args": hostileArgs, "stdout": stdout.String(), "stderr": stderr.String(), "exit": exit})
		if runErr == nil || exit != 1 {
			t.Fatalf("hostile unknown key admission/exit: %v %d", runErr, exit)
		}
		for _, stream := range []string{stdout.String(), stderr.String()} {
			if len(stream) > 4096 || strings.Contains(stream, "INJECTED-OWNERSHIP-MARKER") || strings.ContainsFunc(strings.TrimSuffix(stream, "\n"), unicode.IsControl) {
				t.Fatalf("unsafe main output: %q", stream)
			}
		}
		if jsonMode {
			var result runtimeRecoveryResult
			if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Admissible {
				t.Fatal("hostile JSON rejection missing")
			}
		}
		if !reflect.DeepEqual(hostileTree, orphanCLITree(t, root)) {
			t.Fatal("hostile rejection changed tree or preimages")
		}
	}
	embeddedPut(t, ledgerPath, ledgerPreimage, 0600)

	// All 21 absent, but sibling registration still present: must reject.
	registrationBefore := embeddedRead(t, registration)
	rejectUnchanged(nil)
	if !bytes.Equal(registrationBefore, embeddedRead(t, registration)) {
		t.Fatal("recovery touched surviving registration")
	}
	if err = os.Remove(registration); err != nil {
		t.Fatal(err)
	}
	statusBefore := orphanCLITree(t, control)
	status := orphanCLIRun(t, binary, env, evidence, 1, "setup-notifications", "status", "--control-root", control, "--json")
	var diagnostic struct {
		Reason     string                              `json:"reason"`
		Generation uint64                              `json:"generation"`
		Diagnostic struct{ Code, Path, Action string } `json:"diagnostic"`
	}
	if err = json.Unmarshal(status, &diagnostic); err != nil || diagnostic.Reason != "installation_invalid" || diagnostic.Generation != before.Generation || diagnostic.Diagnostic.Code != "managed_file_missing" || diagnostic.Diagnostic.Path != selected[0] || !strings.Contains(diagnostic.Diagnostic.Action, "preview") {
		t.Fatalf("deterministic before-status: %s %v", status, err)
	}
	if !reflect.DeepEqual(statusBefore, orphanCLITree(t, control)) {
		t.Fatal("invalid status mutated control")
	}
	// The existing ordinary removal remains strict: observable pre-recovery red.
	orphanCLIRun(t, binary, env, evidence, 1, "internal-install-runtime", "--remove", "--consumer", consumer, "--target", filepath.Join(runtimeRoot, "bin"), "--control-root", control)
	if !reflect.DeepEqual(statusBefore, orphanCLITree(t, control)) {
		t.Fatal("ordinary removal mutated invalid state")
	}
	previewImage := orphanCLITree(t, control)
	preview := orphanCLIRun(t, binary, env, evidence, 0, append(append([]string(nil), args...), "--dry-run")...)
	var projected runtimeRecoveryResult
	if err = json.Unmarshal(preview, &projected); err != nil || !projected.Admissible || projected.SelectedCount != 21 || !reflect.DeepEqual(projected.SelectedPaths, selected) || projected.Generation != before.Generation {
		t.Fatalf("preview=%s err=%v", preview, err)
	}
	if !reflect.DeepEqual(previewImage, orphanCLITree(t, control)) {
		t.Fatal("preview took a lock or mutated control")
	}
	embeddedAbsent(t, runtimeRoot)
	embeddedAbsent(t, registration)
	rejectUnchanged([]string{"--refresh"})
	rejectUnchanged([]string{"--expected-generation", strconv.FormatUint(before.Generation+1, 10)})
	// Retained corruption and reappeared orphan bytes must remain untouched.
	original := embeddedRead(t, retained[1].Path)
	embeddedPut(t, retained[1].Path, []byte("foreign retained corruption"), 0640)
	rejectUnchanged(nil)
	if string(embeddedRead(t, retained[1].Path)) != "foreign retained corruption" {
		t.Fatal("foreign retained bytes removed")
	}
	embeddedPut(t, retained[1].Path, original, 0640)
	embeddedPut(t, selected[0], []byte("foreign reappearance"), 0600)
	rejectUnchanged(nil)
	if string(embeddedRead(t, selected[0])) != "foreign reappearance" {
		t.Fatal("foreign orphan bytes removed")
	}
	if err = os.RemoveAll(runtimeRoot); err != nil {
		t.Fatal(err)
	}
	parentBefore, err := os.Stat(filepath.Dir(registration))
	if err != nil {
		t.Fatal(err)
	}
	orphanCLIRun(t, binary, env, evidence, 0, args...)
	parentAfter, err := os.Stat(filepath.Dir(registration))
	if err != nil || !os.SameFile(parentBefore, parentAfter) || !parentBefore.ModTime().Equal(parentAfter.ModTime()) {
		t.Fatal("recovery substituted or wrote the registration/runtime parent directory", err)
	}
	orphanCLIArtifact(t, evidence, "registration-parent.json", map[string]any{"same_inode": true, "before_mtime_ns": parentBefore.ModTime().UnixNano(), "after_mtime_ns": parentAfter.ModTime().UnixNano()})
	after, pending, err := installruntime.ReadOwnership(control)
	if err != nil || pending {
		t.Fatal(err)
	}
	// Independently reconstruct the exact permitted ledger delta.
	data, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var expected installruntime.Ledger
	if err = json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	delete(expected.Consumers, consumer)
	for _, path := range selected {
		delete(expected.Files, path)
	}
	expected.Generation++
	expected.PolicyGeneration++
	if !reflect.DeepEqual(after, expected) {
		t.Fatalf("unexpected ownership delta: after=%+v expected=%+v", after, expected)
	}
	if len(after.Files) != 28 || !bytes.Equal(rawPolicy, embeddedRead(t, filepath.Join(control, "agent-notifications.json"))) || !reflect.DeepEqual(retainedBefore, orphanCLITree(t, primary)) {
		t.Fatal("retained content/modes/policy changed")
	}
	afterControl := orphanCLITree(t, control)
	if len(afterControl) != len(policyBefore) {
		t.Fatal("recovery created or removed control paths")
	}
	for path, entry := range policyBefore {
		got, exists := afterControl[path]
		if !exists || entry.Mode != got.Mode {
			t.Fatalf("control path/mode changed: %s", path)
		}
		if path == "ownership.json" || path == "policy-generation.json" {
			continue
		}
		if entry != got {
			t.Fatalf("unexpected control mutation: %s", path)
		}
	}
	embeddedAbsent(t, runtimeRoot)
	embeddedAbsent(t, registration)
	healthy := orphanCLIRun(t, binary, env, evidence, 0, "setup-notifications", "status", "--control-root", control, "--json")
	var statusAfter struct {
		Reason     string `json:"reason"`
		Generation uint64 `json:"generation"`
	}
	if err = json.Unmarshal(healthy, &statusAfter); err != nil || statusAfter.Reason == "installation_invalid" || statusAfter.Generation != after.Generation {
		t.Fatalf("healthy status=%s %v", healthy, err)
	}
	rejectUnchanged(nil) // stale repeat cannot increment generation a second time
	if _, err = installruntime.ReadInstalledSnapshot(control); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sentinelBefore, orphanCLITree(t, sentinelRoot)) {
		t.Fatal("external fake-user control changed")
	}
	orphanCLIArtifact(t, evidence, "after-ledger.json", after)
	orphanCLIArtifact(t, evidence, "after-control.json", orphanCLITree(t, control))
	orphanCLIArtifact(t, evidence, "after-retained.json", orphanCLITree(t, primary))
	embeddedPut(t, filepath.Join(evidence, "after-policy.json"), embeddedRead(t, filepath.Join(control, "agent-notifications.json")), 0600)
	orphanCLIArtifact(t, evidence, "outside-after.json", orphanCLITree(t, sentinelRoot))
	// Hash actual supplied sources instead of inventing an inaccessible Git SHA.
	hashes := map[string]string{}
	for _, path := range []string{"install_runtime.go", "orphan_recovery_cli_test.go", "orphan_recovery_e2e_test.go", "agent_notify_setup.go", "../../internal/installruntime/orphan_recovery.go", "../../internal/installruntime/orphan_codec.go", "../../internal/installruntime/transaction.go", "../../internal/installruntime/recovery.go", "../../internal/installruntime/snapshot.go", "../../go.mod", "../../go.sum", "../../docs/INSTALLATION.md"} {
		hashes[path] = fmt.Sprintf("%x", sha256.Sum256(embeddedRead(t, path)))
	}
	orphanCLIArtifact(t, evidence, "source-sha256.json", hashes)
	cleanupReady = true
	t.Logf("PROVED actual sibling registration/eight commands; ID=%s generation=%d->%d policy generation=%d->%d exact21/28 metadata delta; fixture=%s evidence=%s", before.ID, before.Generation, after.Generation, before.PolicyGeneration, after.PolicyGeneration, root, evidence)
}
