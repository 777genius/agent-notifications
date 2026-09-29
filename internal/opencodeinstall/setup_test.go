package opencodeinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func fixture(t *testing.T) (context.Context, Request, string) {
	t.Helper()
	base := t.TempDir()
	source := filepath.Join(base, "source-binary")
	if err := os.WriteFile(source, []byte("inert Linux test fixture v1 "+installruntime.WriterProtocolMarker), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	r := Request{Action: Install, ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"),
		BinarySource: source, HomeDir: filepath.Join(base, "home"), Desktop: true, GOOS: "linux", GOARCH: "amd64"}
	return ctx, r, filepath.Join(r.HomeDir, ".config", "opencode", "plugins", pluginName)
}

func TestInstallUpdateAndRemovePreserveSeparateChannelConsent(t *testing.T) {
	ctx, r, plugin := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(r.RuntimeRoot, binaryName)
	gate := CurrentGate{ControlRoot: r.ControlRoot, Executable: binary, GOOS: "linux", GOARCH: "amd64"}
	if desktop, webhook := gate.Channels(ctx); !desktop || webhook {
		t.Fatalf("initial channels = %v %v", desktop, webhook)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if s.Policy.Enabled || s.Installation.Enabled {
		t.Fatal("OpenCode opt-in changed global MCP policy")
	}
	first, err := os.ReadFile(plugin)
	if err != nil || !strings.Contains(string(first), binary) {
		t.Fatalf("plugin missing owned executable: %v", err)
	}
	r.Action, r.Desktop, r.Webhook = Update, false, true
	if err := os.WriteFile(r.BinarySource, []byte("inert Linux test fixture v2 "+installruntime.WriterProtocolMarker), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if desktop, webhook := gate.Channels(ctx); desktop || !webhook {
		t.Fatalf("updated channels = %v %v", desktop, webhook)
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if gate.Enabled(ctx) {
		t.Fatal("loaded old plugin still authorized after remove")
	}
	if _, err := os.Lstat(plugin); !os.IsNotExist(err) {
		t.Fatalf("plugin remains: %v", err)
	}
	if _, err := os.Lstat(binary); !os.IsNotExist(err) {
		t.Fatalf("last-consumer binary remains: %v", err)
	}
}

func TestForeignAndModifiedPluginArePreserved(t *testing.T) {
	ctx, r, plugin := fixture(t)
	if err := os.MkdirAll(filepath.Dir(plugin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("adopted foreign JS")
	}
	if got, _ := os.ReadFile(plugin); string(got) != "foreign" {
		t.Fatal("foreign JS changed")
	}
	if err := os.Remove(plugin); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte("user edited JS"), 0600); err != nil {
		t.Fatal(err)
	}
	gate := CurrentGate{ControlRoot: r.ControlRoot, Executable: filepath.Join(r.RuntimeRoot, binaryName), GOOS: "linux", GOARCH: "amd64"}
	if gate.Enabled(ctx) {
		t.Fatal("edited plugin passed current gate")
	}
	for _, action := range []Action{Update, Remove} {
		r.Action = action
		if err := Apply(ctx, r); err == nil {
			t.Fatalf("%s accepted edited JS", action)
		}
		if got, _ := os.ReadFile(plugin); string(got) != "user edited JS" {
			t.Fatal("edited JS overwritten")
		}
	}
}

func TestSharedConsumerRemovalKeepsOtherRuntime(t *testing.T) {
	ctx, r, plugin := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	gen := l.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: "other-test-consumer", Consumer: installruntime.Consumer{Registration: filepath.Join(r.RuntimeRoot, "other")}, ExpectedGeneration: &gen})
	if err != nil {
		t.Fatal(err)
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, _, err = installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Consumers[consumerID]; ok {
		t.Fatal("OpenCode consumer remains")
	}
	if _, ok := l.Consumers["other-test-consumer"]; !ok {
		t.Fatal("other consumer removed")
	}
	if _, err := os.Lstat(plugin); !os.IsNotExist(err) {
		t.Fatalf("OpenCode plugin retained: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(r.RuntimeRoot, binaryName)); err != nil {
		t.Fatalf("shared binary removed: %v", err)
	}
}

func TestExplicitRecoveryDoesNotEnableIncompleteInstall(t *testing.T) {
	ctx, r, _ := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	gen := l.Generation
	marker := filepath.Join(r.RuntimeRoot, "recovery-test")
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen,
		Files: []installruntime.File{{Path: marker, Data: []byte("marker"), Mode: 0600}},
		Fault: func(string) error { return errors.New("simulated interruption") }})
	if err == nil {
		t.Fatal("fault not reached")
	}
	gate := CurrentGate{ControlRoot: r.ControlRoot, Executable: filepath.Join(r.RuntimeRoot, binaryName), GOOS: "linux", GOARCH: "amd64"}
	if gate.Enabled(ctx) {
		t.Fatal("pending transaction passed gate")
	}
	r.Action = Recover
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if !gate.Enabled(ctx) {
		t.Fatal("recovered state not admitted")
	}
}

func TestStaleConsentCannotActivateFirstRegistration(t *testing.T) {
	ctx, r, plugin := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(r.ControlRoot, "agent-notifications.json")
	if err := os.WriteFile(policy, []byte(`{"schemaVersion":1,"enabled":false,"route":{"openCodeNotifications":{"desktop":true,"webhook":false}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	r.Action = Install
	if err := Apply(ctx, r); err == nil {
		t.Fatal("stale consent activated absent registration")
	}
	if _, err := os.Lstat(plugin); !os.IsNotExist(err) {
		t.Fatalf("plugin published despite stale consent: %v", err)
	}
}

func TestGlobalPlacementAndChangedRootRemovalRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Request, string) string
	}{
		{"xdg", func(r *Request, base string) string {
			r.XDGConfigHome = filepath.Join(base, "xdg")
			return filepath.Join(r.XDGConfigHome, "opencode", "plugins", pluginName)
		}},
		{"override", func(r *Request, base string) string {
			r.XDGConfigHome = filepath.Join(base, "xdg")
			r.OpenCodeConfigDir = filepath.Join(base, "selected-opencode")
			return filepath.Join(r.OpenCodeConfigDir, "plugins", pluginName)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, r, _ := fixture(t)
			plugin := tc.change(&r, t.TempDir())
			if err := Apply(ctx, r); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(plugin); err != nil {
				t.Fatalf("planned global plugin absent: %v", err)
			}
			r.Action = Remove
			r.OpenCodeConfigDir = filepath.Join(t.TempDir(), "different-opencode")
			if err := Apply(ctx, r); err == nil {
				t.Fatal("changed root removed original registration")
			}
			if _, err := os.Stat(plugin); err != nil {
				t.Fatalf("original plugin deleted: %v", err)
			}
		})
	}
}

func TestExistingComponentSuppliesRuntimeRoot(t *testing.T) {
	ctx, r, _ := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Action, r.RuntimeRoot = Install, ""
	if err := Apply(ctx, r); err != nil {
		t.Fatalf("recorded runtime root not reused: %v", err)
	}
}

func TestRemoveResumesAfterSharedPluginCAS(t *testing.T) {
	ctx, r, plugin := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	gen := l.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: "other-test-consumer", Consumer: installruntime.Consumer{Registration: filepath.Join(r.RuntimeRoot, "other")}, ExpectedGeneration: &gen})
	if err != nil {
		t.Fatal(err)
	}
	if err := setChannels(ctx, r.ControlRoot, r.RuntimeRoot, false, false); err != nil {
		t.Fatal(err)
	}
	l, _, err = installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	owned, ok := installruntime.OwnedFile(l, plugin)
	if !ok {
		t.Fatal("fixture plugin not owned")
	}
	gen = l.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen,
		Files: []installruntime.File{{Path: plugin, Before: owned, Remove: true}}})
	if err != nil {
		t.Fatal(err)
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatalf("remove did not resume: %v", err)
	}
	l, _, err = installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Consumers[consumerID]; ok {
		t.Fatal("registration remains")
	}
}
