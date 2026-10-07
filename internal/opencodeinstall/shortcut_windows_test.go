//go:build windows

package opencodeinstall

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Reusing the OpenCode renderer previously meant a Gemini shortcut carried the
// wrong AUMID and registration checks. Exercise actual COM bytes and kernel CAS,
// including an owned file with the wrong identity and independent cleanup.
func TestWindowsGeminiShortcutLifecycleKeepsOpenCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, root, runtimeRoot := filepath.Join(base, "profile with spaces"), filepath.Join(base, "control"), filepath.Join(base, "runtime")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	l := installruntime.Ledger{}
	var geminiPath, openCodePath string
	for _, product := range []DesktopProduct{GeminiDesktop, OpenCodeDesktop} {
		id, _ := identityFor(product)
		if file, err := StageWindowsShortcut(product, home, executable, false, l); err != nil || file != nil {
			t.Fatalf("webhook-only staged a shortcut: %+v %v", file, err)
		}
		file, err := StageWindowsShortcut(product, home, executable, true, l)
		if err != nil || file == nil {
			t.Fatalf("shortcut staging: %+v %v", file, err)
		}
		if product == GeminiDesktop {
			geminiPath = file.Path
		} else {
			openCodePath = file.Path
		}
		receipt := filepath.Join(root, id.consumer+".json")
		l, err = installruntime.Commit(ctx, installruntime.Request{
			ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: id.consumer,
			Consumer: installruntime.Consumer{Registration: receipt, Commands: []string{executable}},
			Files:    []installruntime.File{*file, {Path: receipt, Data: []byte("inert registered test receipt"), Mode: 0600}},
		})
		if err != nil {
			t.Fatal(err)
		}
		target, appID, arguments, err := inspectWindowsShortcut(file.Path)
		if err != nil || !sameWindowsFile(target, executable) || appID != id.appID || arguments != "--help" {
			t.Fatalf("actual COM identity for %s: target=%q appID=%q args=%q err=%v", id.label, target, appID, arguments, err)
		}
		if err := windowsShortcutReadyFor(product, root, executable, home); err != nil {
			t.Fatal(err)
		}
	}
	if sameWindowsPath(geminiPath, openCodePath) {
		t.Fatal("Gemini and OpenCode share a shortcut path")
	}
	// Even ledger-owned and correctly fingerprinted bytes must match the
	// selected product's AUMID; ownership alone is insufficient readiness.
	wrong, err := renderWindowsShortcut(executable)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := installruntime.OwnedFile(l, geminiPath)
	l, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "gemini-notifications", RefreshOnly: true,
		Files: []installruntime.File{{Path: geminiPath, Before: before, Data: wrong, Mode: 0600}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := windowsShortcutReadyFor(GeminiDesktop, root, executable, home); err == nil {
		t.Fatal("Gemini accepted OpenCode's AUMID")
	}
	if err := windowsShortcutReady(root, executable, home); err != nil {
		t.Fatalf("Gemini mutation changed OpenCode readiness: %v", err)
	}
	runtimeRoot = l.Consumers[GeminiConsumerID].RuntimeRoot
	gen := l.Generation
	l, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "gemini-notifications",
		RefreshOnly: true, PolicyOnly: true, RevokeGemini: true, ExpectedGeneration: &gen,
		ExpectedPolicy: &installruntime.Identity{},
		PolicyFields:   map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	remove, err := StageWindowsShortcut(GeminiDesktop, filepath.Join(base, "changed profile"), "", false, l)
	if err != nil || remove == nil || remove.Path != geminiPath || !remove.Remove {
		t.Fatalf("removal did not use only registered Gemini's shortcut: %+v %v", remove, err)
	}
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "gemini-notifications", RefreshOnly: true,
		Files: []installruntime.File{*remove},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(geminiPath); !os.IsNotExist(err) {
		t.Fatal("Gemini shortcut survived owned removal")
	}
	if err := windowsShortcutReady(root, executable, home); err != nil {
		t.Fatalf("Gemini cleanup changed OpenCode readiness: %v", err)
	}
}

func TestWindowsGeminiShortcutRejectsForeignAndUntrustedIdentity(t *testing.T) {
	home := filepath.Join(t.TempDir(), "profile")
	id, _ := identityFor(GeminiDesktop)
	path, err := windowsShortcutPathFor(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign shortcut"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StageWindowsShortcut(GeminiDesktop, home, executable, true, installruntime.Ledger{}); err == nil {
		t.Fatal("Gemini adopted a foreign shortcut")
	}
	if _, err := StageWindowsShortcut(DesktopProduct(255), home, executable, true, installruntime.Ledger{}); err == nil {
		t.Fatal("staging accepted an untrusted product identity")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "foreign shortcut" {
		t.Fatalf("foreign shortcut changed: %q %v", got, err)
	}
}

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

// Shell profile lookup runs in a fresh process, using only a new TEST profile.
// Missing Programs must not prevent planning, and staging must not create it.
func TestWindowsCurrentProfileStagesMissingPrograms(t *testing.T) {
	const child = "AN_TEST_MISSING_PROGRAMS_CHILD"
	if os.Getenv(child) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWindowsCurrentProfileStagesMissingPrograms$")
		cmd.Env = append(os.Environ(), child+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fresh-profile shortcut staging: %v\n%s", err, output)
		}
		return
	}
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	for key, name := range map[string]string{
		"USERPROFILE": "home", "APPDATA": "appdata", "LOCALAPPDATA": "localappdata", "TEMP": "tmp", "TMP": "tmp",
	} {
		path := filepath.Join(base, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	if _, err := windows.KnownFolderPath(windows.FOLDERID_Programs, 0); err == nil {
		t.Fatal("fresh-profile fixture unexpectedly passed existing-folder verification")
	}
	programs, err := windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(base, programs)
	if err != nil || !filepath.IsAbs(programs) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		t.Fatal("native Programs metadata is outside the fresh TEST profile")
	}
	if _, err := os.Lstat(programs); !os.IsNotExist(err) {
		t.Fatalf("fixture Programs was not absent: %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := StageWindowsShortcut(OpenCodeDesktop, home, executable, true, installruntime.Ledger{})
	if err != nil || file == nil {
		t.Fatalf("missing Programs rejected shortcut staging: %v", err)
	}
	if !sameWindowsPath(file.Path, filepath.Join(programs, shortcutName)) || file.Before.Exists || len(file.Data) == 0 {
		t.Fatal("staging did not preserve the native planned path and absent preimage")
	}
	if _, err := os.Lstat(programs); !os.IsNotExist(err) {
		t.Fatalf("path planning or staging created Programs: %v", err)
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

// RED: Local stage/readiness consults a fixed consumer instead of the verified
// portable key, creates a second identity, or accepts a copied/wrong binding.
func TestWindowsLocalShortcutUsesRecordedPortableConsumer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, run, home := filepath.Join(base, "control"), filepath.Join(base, "runtime"), filepath.Join(base, "TEST-profile")
	l, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: "existing-installer", ConsumerID: "sibling", Consumer: installruntime.Consumer{Registration: "sibling"}})
	if err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(run, "primary.exe")
	b := portable.Binding{Version: 1, Integration: portable.CopilotVSCode, InstallationID: "TEST-install", BindingID: "TEST-binding", ScopeID: "TEST-scope", ComponentID: l.ID, Owner: l.Owner, ScopeRoot: base, DataRoot: base, ControlRoot: root, GlobalConfig: filepath.Join(root, "agent-notifications.json"), RuntimeRoot: run, Primary: "primary.exe"}
	key, c, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: key, Consumer: c, ExpectedGeneration: &l.Generation, Files: []installruntime.File{{Path: primary, Data: []byte("inert TEST writer " + installruntime.LocalWriterProtocolMarker), Mode: 0700}}})
	if err != nil {
		t.Fatal(err)
	}
	file, err := StageLocalWindowsShortcut(key, home, primary, true, l)
	if err != nil || file == nil {
		t.Fatalf("Local stage: %+v %v", file, err)
	}
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &l.Generation, Files: []installruntime.File{*file}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := localWindowsIdentity(key, l)
	if err != nil {
		t.Fatal(err)
	}
	if err := windowsShortcutReadyIdentity(id, root, primary, home); err != nil {
		t.Fatal(err)
	}
	if len(l.Consumers) != 2 || l.Consumers[key].Registration != c.Registration {
		t.Fatal("Local shortcut minted another registration")
	}
	if _, err := StageWindowsShortcut(CopilotVSCodeDesktop, home, primary, true, l); err == nil {
		t.Fatal("Local fixed-key wrapper admitted shortcut")
	}
	if _, err := StageLocalWindowsShortcut("portable:wrong", home, primary, true, l); err == nil {
		t.Fatal("wrong portable consumer admitted shortcut")
	}
	file, err = StageLocalWindowsShortcut(key, home, primary, false, l)
	if err != nil || file == nil || !file.Remove {
		t.Fatalf("Local owned cleanup stage: %+v %v", file, err)
	}
}
