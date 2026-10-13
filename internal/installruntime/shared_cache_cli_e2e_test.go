package installruntime

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/opencodeplugin"
)

// Opt in with authenticated release assets and the locally built candidate.
// Child processes use a disposable HOME; no user registration is rewritten.
func TestSharedCacheInstallerCLIOnMac(t *testing.T) {
	baseline, candidate, native, old := os.Getenv("TEST_AN_BASELINE"), os.Getenv("TEST_AN_CANDIDATE"), os.Getenv("TEST_AN_NATIVE"), os.Getenv("TEST_AN_OLD")
	if runtime.GOOS != "darwin" || baseline == "" || candidate == "" || native == "" || old == "" {
		t.Skip("requires explicit Mac release assets and candidate")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// The installer reconciles LaunchServices after committing. Restore the
	// pre-test preferred app and unregister the disposable generation afterwards.
	preferred, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", preferredNativeRegistrationQuery).Output()
	if err != nil {
		t.Fatalf("snapshot native registration: %v", err)
	}
	var testNative string
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if testNative != "" {
			if out, err := exec.CommandContext(cleanup, launchServicesRegister, "-u", testNative).CombinedOutput(); err != nil {
				t.Errorf("unregister TEST native helper: %v %s", err, out)
			}
		}
		if path := string(bytes.TrimSpace(preferred)); path != "" {
			if out, err := exec.CommandContext(cleanup, launchServicesRegister, "-f", path).CombinedOutput(); err != nil {
				t.Errorf("restore native registration: %v %s", err, out)
			}
		}
	})
	home := filepath.Join(root, "TEST-profile")
	control := filepath.Join(home, "Library", "Application Support", "agent-notifications")
	cache := filepath.Join(home, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	oldRoot, newRoot := filepath.Join(cache, "1.46.1"), filepath.Join(cache, "1.49.1")
	entry := "claude-notifications-darwin-" + runtime.GOARCH
	portable, openCode := filepath.Join(oldRoot, "bin", entry), filepath.Join(oldRoot, entry)
	plugin := filepath.Join(home, ".config", "opencode", "plugins", "agent-notifications.js")
	oldBytes, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	pluginBytes, err := opencodeplugin.Render(openCode, control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "claude-hooks", Files: []File{{Path: portable, Data: oldBytes, Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "opencode-notifications", Consumer: Consumer{Registration: plugin, Commands: []string{openCode, "opencode-event", "--protocol", "1"}}, Files: []File{{Path: openCode, Data: oldBytes, Mode: 0700}, {Path: plugin, Data: pluginBytes, Mode: 0600}}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"portable:claude", "portable:codex"} {
		if _, err = Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: id, Consumer: Consumer{Registration: id + " TEST binding", Commands: []string{portable}}}); err != nil {
			t.Fatal(err)
		}
	}
	before, _, err := ReadOwnership(control)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "stage")
	if err = os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	newBytes, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, entry), newBytes, 0700); err != nil {
		t.Fatal(err)
	}
	copyNative := exec.CommandContext(ctx, "/usr/bin/ditto", native, filepath.Join(stage, "ClaudeNotifier.app"))
	if out, err := copyNative.CombinedOutput(); err != nil {
		t.Fatalf("copy helper: %v %s", err, out)
	}
	attestation, err := os.ReadFile(native + ".managed-runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "ClaudeNotifier.app.managed-runtime.json"), attestation, 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + root, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "AGENT_NOTIFICATIONS_CONTROL_ROOT=" + control}
	args := []string{"internal-install-runtime", "--stage", stage, "--target", filepath.Join(newRoot, "bin"), "--entry", entry, "--control-root", control, "--relocate-versioned-cache"}
	run := func(path string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env, cmd.Dir = env, home
		return cmd.CombinedOutput()
	}
	output, err := run(baseline, args...)
	if err == nil || !bytes.Contains(output, []byte("shared cache consumer requires explicit migration: opencode-notifications")) {
		t.Fatalf("baseline did not reproduce failure: %v %s", err, output)
	}
	unchanged, pending, err := ReadOwnership(control)
	if err != nil || pending || !reflect.DeepEqual(before, unchanged) {
		t.Fatalf("failed installer changed ownership: %v", err)
	}
	t.Logf("released installer reproduced shared-cache failure; ownership unchanged")
	output, err = run(candidate, args...)
	if err != nil {
		t.Fatalf("candidate installer: %v %s", err, output)
	}
	after, pending, err := ReadOwnership(control)
	if err != nil || pending || after.Native == nil {
		t.Fatalf("candidate did not commit clean native runtime: %v", err)
	}
	testNative = after.Native.Path
	for _, id := range []string{"opencode-notifications", "portable:claude", "portable:codex"} {
		if !reflect.DeepEqual(before.Consumers[id], after.Consumers[id]) {
			t.Fatalf("changed registration %s", id)
		}
	}
	expectedVersion, err := run(candidate, "--version")
	if err != nil || len(bytes.TrimSpace(expectedVersion)) == 0 {
		t.Fatalf("candidate version: %v %s", err, expectedVersion)
	}
	for _, path := range []string{portable, openCode, filepath.Join(newRoot, "bin", entry)} {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, newBytes) {
			t.Fatalf("binary not updated: %s %v", path, err)
		}
		identity, err := Fingerprint(path)
		if err != nil || identity != after.Files[path] {
			t.Fatalf("wrong fingerprint: %s %v", path, err)
		}
		version, err := run(path, "--version")
		if err != nil || !bytes.Equal(version, expectedVersion) {
			t.Fatalf("updated executable failed: %v %s", err, version)
		}
	}
	gotPlugin, err := os.ReadFile(plugin)
	if err != nil || !bytes.Equal(gotPlugin, pluginBytes) {
		t.Fatal("OpenCode plugin changed")
	}
	t.Logf("candidate CLI committed signed native helper and updated all three executable locations; retained registrations unchanged")
}
