//go:build windows

package opencodeinstall

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestWindowsCurrentProfileUsesKnownProgramsFolder(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := windowsShortcutPath(home)
	if err != nil {
		t.Fatal(err)
	}
	programs, err := windows.KnownFolderPath(windows.FOLDERID_Programs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !sameWindowsPath(got, filepath.Join(programs, shortcutName)) {
		t.Fatalf("shortcut path = %q, Programs = %q", got, programs)
	}
}

func TestWindowsShortcutContainsTargetAndAppUserModelID(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("released Windows architecture only")
	}
	base := t.TempDir()
	target, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := renderWindowsShortcut(target)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, shortcutName)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	gotTarget, gotAppID, gotArgs, err := inspectWindowsShortcut(path)
	if err != nil {
		t.Fatal(err)
	}
	if !sameWindowsFile(gotTarget, target) || gotAppID != OpenCodeToastAppID || gotArgs != "--help" {
		t.Fatalf("shortcut target=%q executable=%q appID=%q args=%q", gotTarget, target, gotAppID, gotArgs)
	}
}

func TestWindowsShortcutForeignPathIsPreserved(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("released Windows architecture only")
	}
	base := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := Request{Action: Install, ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"),
		BinarySource: filepath.Join(base, "source.exe"), HomeDir: filepath.Join(base, "home"), OpenCodeConfigDir: filepath.Join(base, "opencode"),
		Desktop: true, GOOS: "windows", GOARCH: "amd64"}
	if err := os.WriteFile(r.BinarySource, fixtureBinary("windows", "amd64", "v1"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := windowsShortcutPath(r.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	foreign := []byte("foreign shortcut")
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err == nil || !strings.Contains(err.Error(), "shortcut path is foreign") {
		t.Fatalf("foreign shortcut was not rejected by ownership check: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(foreign) {
		t.Fatalf("foreign shortcut changed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(r.OpenCodeConfigDir, "plugins", pluginName)); !os.IsNotExist(err) {
		t.Fatalf("plugin published before shortcut conflict: %v", err)
	}
}

func TestWindowsWebhookOnlyDoesNotCreateShortcut(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("released Windows architecture only")
	}
	base := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := Request{Action: Install, ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"),
		BinarySource: filepath.Join(base, "source.exe"), HomeDir: filepath.Join(base, "home"), OpenCodeConfigDir: filepath.Join(base, "opencode"),
		Webhook: true, GOOS: "windows", GOARCH: "amd64"}
	if err := os.WriteFile(r.BinarySource, fixtureBinary("windows", "amd64", "v1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	path, err := windowsShortcutPath(r.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("webhook-only created shortcut: %v", err)
	}
	ledger, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := installruntime.OwnedFile(ledger, path); ok {
		t.Fatal("webhook-only owns shortcut")
	}
}

func TestWindowsSharedRemovalUsesOwnedShortcutPathAfterHomeChange(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("released Windows architecture only")
	}
	base := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := Request{Action: Install, ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"),
		BinarySource: filepath.Join(base, "source.exe"), HomeDir: filepath.Join(base, "home-a"), OpenCodeConfigDir: filepath.Join(base, "opencode"),
		Desktop: true, GOOS: "windows", GOARCH: "amd64"}
	if err := os.WriteFile(r.BinarySource, fixtureBinary("windows", "amd64", "v1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	shortcut, err := windowsShortcutPath(r.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	ledger, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	gen := ledger.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: "other-test-consumer", Consumer: installruntime.Consumer{Registration: filepath.Join(r.RuntimeRoot, "other")}, ExpectedGeneration: &gen})
	if err != nil {
		t.Fatal(err)
	}
	r.Action, r.HomeDir = Remove, filepath.Join(base, "home-b")
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(shortcut); !os.IsNotExist(err) {
		t.Fatalf("old shortcut retained: %v", err)
	}
	ledger, _, err = installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ledger.Consumers[consumerID]; ok {
		t.Fatal("OpenCode consumer retained")
	}
	if _, ok := ledger.Consumers["other-test-consumer"]; !ok {
		t.Fatal("other consumer removed")
	}
}
