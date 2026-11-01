//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/pievent"
)

// The real Cursor runtime writer and common setup must refresh a surviving
// Pi binding without running Pi setup again. A metadata-only retainer would
// miss the Cursor receipt and policy boundaries exercised here.
func TestPiSurvivesCursorOnlySharedRuntimeUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// Go 1.27's testing.TempDir follows GOTMPDIR, which may be build scratch
	// on a removable volume. Product profiles use the native private TEST temp.
	temp, err := os.MkdirTemp("", "TEST-pi-cursor-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temp) })
	root, err := filepath.EvalSymlinks(temp)
	if err != nil {
		t.Fatal(err)
	}
	control, run, stage := filepath.Join(root, "control"), filepath.Join(root, "runtime"), filepath.Join(root, "stage")
	global, profile := filepath.Join(root, "global", "config.json"), filepath.Join(root, "TEST-pi-agent")
	for _, dir := range []string{stage, filepath.Dir(global), filepath.Join(profile, "extensions"), filepath.Join(root, ".cursor")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := cursorQualifiedNamespace(filepath.Join(root, ".cursor")); err != nil {
		t.Skip("real Cursor SDK lifecycle requires a supported native filesystem/host tuple")
	}
	if err := os.WriteFile(global, []byte(`{"schemaVersion":2,"notifications":{"desktop":{"enabled":true},"webhook":{"enabled":true,"url":"http://127.0.0.1:1/TEST-pi","preset":"custom"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	entry := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	primary := filepath.Join(run, "bin", entry)
	writeImage := func(version string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(stage, entry), []byte("TEST inert "+version+" "+installruntime.WriterProtocolMarker+" "+installruntime.LocalWriterProtocolMarker+" "+installruntime.OpenCodeWriterProtocolMarker), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeImage("A")
	installerNativeFixture(t, stage)
	writeRuntime := func(consumer string) error {
		return installRuntime([]string{"--stage", stage, "--target", filepath.Join(run, "bin"), "--entry", entry, "--control-root", control, "--consumer", consumer}, io.Discard)
	}
	if err := writeRuntime(piauthority.PreparationConsumer); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "package")
	if _, err := portableasset.Build(portableasset.BuildRequest{Version: "1.50.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: primary, OutputRoot: pkg}); err != nil {
		t.Fatal(err)
	}
	wizard := func(agent, action string) {
		t.Helper()
		autoUpdate := agent == "cursor" && action == "update"
		if autoUpdate {
			action = "install"
		}
		args := []string{"--action", action, "--agents", agent, "--hooks", "false", "--agent-notify", "true", "--package", pkg, "--control-root", control, "--runtime-root", run, "--global-config", global, "--yes", "--json"}
		if autoUpdate {
			args = append(args, "--install-or-update")
		}
		if agent == "pi" {
			args = append(args, "--pi-agent-dir", profile)
			if action != "uninstall" {
				args = append(args, "--pi-executable", primary, "--pi-desktop", "true", "--pi-webhook", "true")
			}
		} else {
			// The common caller needs an executable path; native Cursor itself
			// is outside this filesystem/SDK lifecycle regression.
			args = append(args, "--scope-root", filepath.Join(root, ".cursor"), "--client-executable", primary)
		}
		var out bytes.Buffer
		if code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false); code != 0 {
			t.Fatalf("%s %s: %d %s", agent, action, code, out.String())
		}
	}
	wizard("pi", "install")
	wizard("cursor", "install")
	readSnapshot := func() installruntime.InstalledSnapshot {
		t.Helper()
		s, err := installruntime.ReadInstalledSnapshot(control)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := readSnapshot()
	var pi portable.Binding
	for _, c := range before.Ledger.Consumers {
		var b portable.Binding
		if json.Unmarshal([]byte(c.Registration), &b) == nil && b.Integration == portable.Pi {
			pi = b
		}
	}
	if pi.BindingID == "" {
		t.Fatal("real common Pi consumer missing")
	}
	name, _ := pi.Filename()
	selector, derived := filepath.Join(pi.DataRoot, name), filepath.Join(pi.DataRoot, piauthority.RuntimeFile)
	readRuntime := func() piauthority.Runtime {
		t.Helper()
		var r piauthority.Runtime
		body, err := os.ReadFile(derived)
		if err != nil || json.Unmarshal(body, &r) != nil {
			t.Fatal("derived runtime unreadable", err)
		}
		return r
	}
	old := readRuntime()
	staleRevision := old.LoadedRevision
	gate, binding, _, _, err := piauthority.Open(ctx, selector, old.LoadedRevision)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile(filepath.Join(control, "agent-notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeImage("B")
	// This is prepare_cursor_runtime's exact existing writer route, followed
	// by Cursor's ordinary integration update. Pi setup is never repeated.
	if err := writeRuntime("claude-hooks"); err != nil {
		t.Fatal(err)
	}
	current := readRuntime()
	if current.PrimarySHA256 == old.PrimarySHA256 || current.LoadedRevision.PrimarySHA256 != current.PrimarySHA256 || readSnapshot().Ledger.Files[primary].SHA256 != current.PrimarySHA256 {
		t.Fatal("Cursor writer left Pi discovery stale")
	}
	old.PrimarySHA256, old.LoadedRevision.PrimarySHA256 = current.PrimarySHA256, current.PrimarySHA256
	if old != current {
		t.Fatal("runtime refresh changed Pi package or binding identity")
	}
	// A release update supplies its new package through Cursor's normal
	// install-or-update caller; Pi keeps its own original package receipt.
	skill := filepath.Join(pkg, "skills", "agent-notifications", "SKILL.md")
	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, append(body, []byte("\nTEST Cursor release B\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	wizard("cursor", "update")
	arbitraryRevision := current.LoadedRevision
	arbitraryRevision.PrimarySHA256 = strings.Repeat("0", 64)
	if _, _, _, _, err := piauthority.Open(ctx, selector, arbitraryRevision); err == nil {
		t.Fatal("arbitrary runtime hash admitted")
	}
	if channels := gate.Channels(ctx, binding, staleRevision); channels != (pievent.Channels{}) {
		t.Fatal("stale observer retains channels")
	}
	if release, err := gate.Acquire(ctx, binding, staleRevision, pievent.DesktopChannel); err == nil {
		release()
		t.Fatal("stale observer effect admitted")
	}
	assertFresh := func(facts piauthority.Runtime) {
		t.Helper()
		for _, channel := range []pievent.Channel{pievent.DesktopChannel, pievent.WebhookChannel} {
			fresh, b, _, _, err := piauthority.Open(ctx, selector, facts.LoadedRevision)
			if err != nil {
				t.Fatal("fresh Pi denied after shared-runtime update/repair", err)
			}
			release, err := fresh.Acquire(ctx, b, facts.LoadedRevision, channel)
			if err != nil {
				t.Fatal("refreshed Pi effect denied", channel, err)
			}
			release()
		}
	}
	assertFresh(current)
	// Only an absent exact owned primary may be repaired from the trusted
	// stage. The old ledger/metadata still prove its previous revision.
	previous := current
	if err := os.Remove(primary); err != nil {
		t.Fatal(err)
	}
	writeImage("C")
	if err := writeRuntime("claude-hooks"); err != nil {
		t.Fatal("missing owned primary repair refused", err)
	}
	current = readRuntime()
	if current.PrimarySHA256 == previous.PrimarySHA256 || readSnapshot().Ledger.Files[primary].SHA256 != current.PrimarySHA256 {
		t.Fatal("missing-primary repair did not refresh exact owned metadata")
	}
	if _, _, _, _, err := piauthority.Open(ctx, selector, previous.LoadedRevision); err == nil {
		t.Fatal("old revision admitted after missing-primary replacement")
	}
	assertFresh(current)
	after := readSnapshot()
	for key, consumer := range before.Ledger.Consumers {
		if !reflect.DeepEqual(after.Ledger.Consumers[key], consumer) {
			t.Fatal("shared update changed sibling consumer", key)
		}
	}
	if got, err := os.ReadFile(filepath.Join(control, "agent-notifications.json")); err != nil || !bytes.Equal(got, policy) {
		t.Fatal("Cursor update changed existing channel consent", err)
	}
	image, err := os.ReadFile(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary, []byte("TEST foreign edited primary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeRuntime("claude-hooks"); err == nil {
		t.Fatal("edited primary was accepted as a missing-file repair")
	}
	if got, err := os.ReadFile(primary); err != nil || string(got) != "TEST foreign edited primary" {
		t.Fatal("refused primary edit did not preserve bytes", err)
	}
	if err := os.WriteFile(primary, image, 0700); err != nil {
		t.Fatal(err)
	}
	// An edited discovery document must refuse before executable promotion.
	if err := os.WriteFile(derived, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	writeImage("C")
	if err := writeRuntime("claude-hooks"); err == nil {
		t.Fatal("user-edited Pi discovery overwritten")
	}
	if got := readSnapshot(); !reflect.DeepEqual(got, after) {
		t.Fatal("refused metadata conflict changed managed installation")
	}
	if got, err := os.ReadFile(derived); err != nil || string(got) != "{}" {
		t.Fatal("refused metadata conflict did not preserve user bytes", err)
	}
	body, _ = json.Marshal(current)
	if err := os.WriteFile(derived, body, 0600); err != nil {
		t.Fatal(err)
	}
	cursorFiles := func() map[string][]byte {
		t.Helper()
		files := map[string][]byte{}
		err := filepath.WalkDir(filepath.Join(root, ".cursor"), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			body, err := os.ReadFile(path)
			files[path] = body
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	filesBefore := cursorFiles()
	beforeRemove := readSnapshot()
	wizard("pi", "uninstall")
	remaining := readSnapshot()
	for key, consumer := range beforeRemove.Ledger.Consumers {
		var b portable.Binding
		if json.Unmarshal([]byte(consumer.Registration), &b) == nil && b.Integration == portable.Cursor {
			if !reflect.DeepEqual(remaining.Ledger.Consumers[key], consumer) {
				t.Fatal("Pi-only removal changed Cursor binding")
			}
		}
	}
	if !reflect.DeepEqual(cursorFiles(), filesBefore) {
		t.Fatal("Pi-only removal changed Cursor files")
	}
	if _, _, _, _, err := piauthority.Open(ctx, selector, current.LoadedRevision); err == nil {
		t.Fatal("removed binding admitted")
	}
	writeImage("D")
	if err := writeRuntime("claude-hooks"); err != nil {
		t.Fatal("removed Pi metadata blocked retained Cursor update", err)
	}
	if _, _, _, _, err := piauthority.Open(ctx, selector, current.LoadedRevision); err == nil {
		t.Fatal("later shared-runtime update revived removed Pi binding")
	}
	wizard("cursor", "uninstall")
}
