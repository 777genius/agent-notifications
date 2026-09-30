package opencodeinstall

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

const linuxTestBinaryName = "claude-notifications-linux-amd64"

func fixture(t *testing.T) (context.Context, Request, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Linux setup fixture needs executable file mode, which Windows does not expose")
	}
	base := t.TempDir()
	source := filepath.Join(base, "source-binary")
	if err := os.WriteFile(source, fixtureBinary("linux", "amd64", "v1"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	r := Request{Action: Install, ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"),
		BinarySource: source, HomeDir: filepath.Join(base, "home"), Desktop: true, GOOS: "linux", GOARCH: "amd64"}
	return ctx, r, filepath.Join(r.HomeDir, ".config", "opencode", "plugins", pluginName)
}

func fixtureBinary(goos, goarch, version string) []byte {
	p, _ := targetPlatform(goos, goarch)
	data := make([]byte, 128)
	switch p.format {
	case "elf":
		copy(data, "\x7fELF")
		data[4], data[5] = 2, 1
		binary.LittleEndian.PutUint16(data[18:20], uint16(p.machine))
	case "macho":
		copy(data, "\xcf\xfa\xed\xfe")
		binary.LittleEndian.PutUint32(data[4:8], p.machine)
	case "pe":
		copy(data, "MZ")
		binary.LittleEndian.PutUint32(data[0x3c:0x40], 0x40)
		copy(data[0x40:], "PE\x00\x00")
		binary.LittleEndian.PutUint16(data[0x44:0x46], uint16(p.machine))
	}
	return append(data, []byte(version+installruntime.WriterProtocolMarker)...)
}

func TestSupportedPlatformBinariesAndValidation(t *testing.T) {
	for _, p := range supportedPlatforms {
		t.Run(p.goos+"-"+p.goarch, func(t *testing.T) {
			got, ok := targetPlatform(p.goos, p.goarch)
			if !ok || got.binary != p.binary || !binaryMatchesPlatform(fixtureBinary(p.goos, p.goarch, "v1"), p) {
				t.Fatalf("target not admitted: %+v", p)
			}
			if p.goos == "windows" && filepath.Ext(p.binary) != ".exe" {
				t.Fatal("Windows binary missing .exe")
			}
			for _, other := range supportedPlatforms {
				if other == p {
					continue
				}
				if binaryMatchesPlatform(fixtureBinary(other.goos, other.goarch, "v1"), p) {
					t.Fatalf("accepted %s/%s binary", other.goos, other.goarch)
				}
			}
		})
	}
	if _, ok := targetPlatform("windows", "arm64"); ok {
		t.Fatal("unreleased Windows arm64 admitted")
	}
}

func TestWindowsBinaryDoesNotRequireUnixExecuteBit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.exe")
	if err := os.WriteFile(path, fixtureBinary("windows", "amd64", "v1"), 0600); err != nil {
		t.Fatal(err)
	}
	p, _ := targetPlatform("windows", "amd64")
	if _, mode, err := readBinary(path, p); err != nil || mode != 0600 {
		t.Fatalf("Windows binary validation: mode %o, err %v", mode, err)
	}
	linux, _ := targetPlatform("linux", "amd64")
	if _, _, err := readBinary(path, linux); err == nil {
		t.Fatal("Windows binary admitted as Linux")
	}
}

func TestWindowsNativeInstallUpdateRemoveAndForeignProtection(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("native Windows amd64 lifecycle only")
	}
	base := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	p, _ := targetPlatform("windows", "amd64")
	r := Request{
		Action: Install, ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"),
		BinarySource: filepath.Join(base, "source.exe"), HomeDir: filepath.Join(base, "home"),
		OpenCodeConfigDir: filepath.Join(base, "opencode-config"), Desktop: true, GOOS: "windows", GOARCH: "amd64",
	}
	if err := os.WriteFile(r.BinarySource, fixtureBinary("windows", "amd64", "v1"), 0600); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(r.OpenCodeConfigDir, "plugins", pluginName)
	if err := os.MkdirAll(filepath.Dir(plugin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte("foreign OpenCode plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("foreign plugin was overwritten")
	}
	if got, err := os.ReadFile(plugin); err != nil || string(got) != "foreign OpenCode plugin" {
		t.Fatalf("foreign plugin changed: %v", err)
	}
	if err := os.Remove(plugin); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(r.RuntimeRoot, p.binary)
	if got, err := os.ReadFile(installed); err != nil || string(got) != string(fixtureBinary("windows", "amd64", "v1")) {
		t.Fatalf("owned executable missing or changed: %v", err)
	}
	shortcut, err := windowsShortcutPath(r.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	shortcutTarget, shortcutAppID, shortcutArgs, err := inspectWindowsShortcut(shortcut)
	if err != nil {
		t.Fatal(err)
	}
	if !sameWindowsFile(shortcutTarget, installed) || shortcutAppID != OpenCodeToastAppID || shortcutArgs != "--help" {
		t.Fatalf("installed shortcut target=%q installed=%q appID=%q args=%q", shortcutTarget, installed, shortcutAppID, shortcutArgs)
	}
	if err := windowsShortcutReady(r.ControlRoot, installed, r.HomeDir); err != nil {
		t.Fatal(err)
	}
	shortcutBytes, err := os.ReadFile(shortcut)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shortcut, []byte("foreign edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := windowsShortcutReady(r.ControlRoot, installed, r.HomeDir); err == nil {
		t.Fatal("modified shortcut passed readiness")
	}
	if err := os.WriteFile(shortcut, shortcutBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := windowsShortcutReady(r.ControlRoot, installed, filepath.Join(base, "different-home")); err == nil {
		t.Fatal("shortcut outside current Programs folder passed readiness")
	}
	ledger, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery {
		t.Fatalf("read installed ownership: recovery=%v err=%v", recovery, err)
	}
	ownedCommand := ledger.Consumers[consumerID].Commands[0]
	if got, err := os.ReadFile(plugin); err != nil || !strings.Contains(string(got), strconv.Quote(ownedCommand)) {
		t.Fatalf("owned plugin missing executable path: %v", err)
	}
	gate := CurrentGate{ControlRoot: r.ControlRoot, Executable: installed, GOOS: "windows", GOARCH: "amd64"}
	if desktop, webhook := gate.Channels(ctx); !desktop || webhook {
		t.Fatalf("installed channels = %v %v", desktop, webhook)
	}
	r.Action, r.Desktop, r.Webhook = Update, false, true
	r.HomeDir = filepath.Join(base, "different-home")
	if err := os.WriteFile(r.BinarySource, fixtureBinary("windows", "amd64", "v2"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if desktop, webhook := gate.Channels(ctx); desktop || !webhook {
		t.Fatalf("updated channels = %v %v", desktop, webhook)
	}
	if _, err := os.Lstat(shortcut); !os.IsNotExist(err) {
		t.Fatalf("desktop-disabled shortcut retained: %v", err)
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if gate.Enabled(ctx) {
		t.Fatal("loaded plugin remains admitted after removal")
	}
	if _, err := os.Lstat(plugin); !os.IsNotExist(err) {
		t.Fatalf("plugin retained: %v", err)
	}
	if _, err := os.Lstat(installed); !os.IsNotExist(err) {
		t.Fatalf("owned executable retained: %v", err)
	}
}

func TestLinuxArm64InstallGateAndWrongArchitectureRejection(t *testing.T) {
	ctx, r, _ := fixture(t)
	r.GOARCH = "arm64"
	r.BinarySource = filepath.Join(t.TempDir(), "source-arm64")
	if err := os.WriteFile(r.BinarySource, fixtureBinary("linux", "amd64", "wrong"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("installed binary for the wrong Linux architecture")
	}
	if err := os.WriteFile(r.BinarySource, fixtureBinary("linux", "arm64", "right"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	p, _ := targetPlatform("linux", "arm64")
	gate := CurrentGate{ControlRoot: r.ControlRoot, Executable: filepath.Join(r.RuntimeRoot, p.binary), GOOS: "linux", GOARCH: "arm64"}
	if !gate.Enabled(ctx) {
		t.Fatal("installed Linux arm64 consumer denied")
	}
}

func TestInstallUpdateAndRemovePreserveSeparateChannelConsent(t *testing.T) {
	ctx, r, plugin := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(r.RuntimeRoot, linuxTestBinaryName)
	gate := CurrentGate{ControlRoot: r.ControlRoot, Executable: binary, GOOS: "linux", GOARCH: "amd64"}
	if desktop, webhook := gate.Channels(context.Background()); !desktop || webhook {
		t.Fatalf("initial channels = %v %v", desktop, webhook)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONTROL_ROOT", r.ControlRoot)
	fromPlugin := CurrentGate{Executable: binary, GOOS: "linux", GOARCH: "amd64"}
	if desktop, webhook := fromPlugin.Channels(context.Background()); !desktop || webhook {
		t.Fatalf("plugin-selected control root channels = %v %v", desktop, webhook)
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
	if err := os.WriteFile(r.BinarySource, fixtureBinary("linux", "amd64", "v2"), 0700); err != nil {
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

func TestInstallIntoPrecreatedEmptyControlDirectory(t *testing.T) {
	ctx, r, plugin := fixture(t)
	if err := os.MkdirAll(r.ControlRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plugin); err != nil {
		t.Fatalf("plugin was not installed: %v", err)
	}
}

func TestPrecreatedControlWithoutLocksPreservesExistingPolicy(t *testing.T) {
	ctx, r, plugin := fixture(t)
	if err := os.MkdirAll(r.ControlRoot, 0700); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(r.ControlRoot, "agent-notifications.json")
	if err := os.WriteFile(policy, []byte(`{"schemaVersion":1,"enabled":false,"route":{"openCodeNotifications":{"desktop":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("existing policy without locks was adopted")
	}
	if _, err := os.Lstat(plugin); !os.IsNotExist(err) {
		t.Fatalf("plugin published despite untrusted policy: %v", err)
	}
}

func TestPlannedPluginPreimageRejectsLateForeignFile(t *testing.T) {
	ctx, r, plugin := fixture(t)
	placement, before, err := plan(r, installruntime.Ledger{}, []byte("desired plugin"))
	if err != nil {
		t.Fatal(err)
	}
	if placement.Target != plugin || before.Exists {
		t.Fatal("unexpected initial plugin placement")
	}
	if err := os.MkdirAll(filepath.Dir(plugin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte("foreign plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	gen := uint64(0)
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer",
		ConsumerID: consumerID, Consumer: installruntime.Consumer{Registration: plugin},
		ExpectedGeneration: &gen,
		Files:              []installruntime.File{{Path: plugin, Before: before, Data: []byte("desired plugin"), Mode: 0600}},
	})
	if err == nil || !strings.Contains(err.Error(), "staged fingerprint changed") {
		t.Fatalf("late foreign plugin was not rejected by file CAS: %v", err)
	}
	if got, err := os.ReadFile(plugin); err != nil || string(got) != "foreign plugin" {
		t.Fatalf("foreign plugin changed: %q, %v", got, err)
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
	gate := CurrentGate{ControlRoot: r.ControlRoot, Executable: filepath.Join(r.RuntimeRoot, linuxTestBinaryName), GOOS: "linux", GOARCH: "amd64"}
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
	if _, err := os.Lstat(filepath.Join(r.RuntimeRoot, linuxTestBinaryName)); err != nil {
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
	gate := CurrentGate{ControlRoot: r.ControlRoot, Executable: filepath.Join(r.RuntimeRoot, linuxTestBinaryName), GOOS: "linux", GOARCH: "amd64"}
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
