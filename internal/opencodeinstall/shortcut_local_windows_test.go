//go:build windows

package opencodeinstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Native Windows qualification only: a real COM-rendered TEST shortcut shared
// by two exact portable registrations. On old5d the first disable stages Remove.
func TestWindowsLocalShortcutRetainedUntilLastBinding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, run, home := filepath.Join(base, "control"), filepath.Join(base, "runtime"), filepath.Join(base, "TEST-profile")
	l, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: "existing-installer", ConsumerID: "sibling", Consumer: installruntime.Consumer{Registration: "TEST-sibling"}})
	if err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(run, "primary.exe")
	b := portable.Binding{Version: 1, Integration: portable.CopilotVSCode, InstallationID: "TEST-install", BindingID: "TEST-A", ScopeID: "TEST-A", ComponentID: l.ID, Owner: l.Owner, ScopeRoot: base, DataRoot: base, ControlRoot: root, GlobalConfig: filepath.Join(root, "agent-notifications.json"), RuntimeRoot: run, Primary: "primary.exe"}
	keyA, cA, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: keyA, Consumer: cA, ExpectedGeneration: &l.Generation, Files: []installruntime.File{{Path: primary, Data: []byte("inert TEST " + installruntime.LocalWriterProtocolMarker), Mode: 0700}}})
	if err != nil {
		t.Fatal(err)
	}
	b.BindingID, b.ScopeID = "TEST-B", "TEST-B"
	keyB, cB, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: keyB, Consumer: cB, ExpectedGeneration: &l.Generation})
	if err != nil {
		t.Fatal(err)
	}
	file, err := StageLocalWindowsShortcut(keyA, home, primary, true, l)
	if err != nil || file == nil {
		t.Fatalf("COM setup stage: %+v %v", file, err)
	}
	path := file.Path
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: keyA, RefreshOnly: true, ExpectedGeneration: &l.Generation, Files: []installruntime.File{*file}})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	owned, ok := installruntime.OwnedFile(l, path)
	if !ok || !owned.Exists {
		t.Fatal("COM shortcut not recorded owned")
	}
	for _, key := range []string{keyA, keyB} {
		file, err := StageLocalWindowsShortcut(key, home, primary, false, l)
		if err != nil || file != nil {
			t.Fatalf("shared binding disable must retain shortcut: key=%s file=%+v err=%v", key, file, err)
		}
	}
	// A malformed unrelated portable record cannot authorize deletion. Nor may
	// a foreign command copied onto a valid Local peer count as ownership.
	for _, kind := range []string{"malformed-portable", "foreign-command"} {
		bad := l
		bad.Consumers = make(map[string]installruntime.Consumer, len(l.Consumers)+1)
		for key, c := range l.Consumers {
			bad.Consumers[key] = c
		}
		if kind == "malformed-portable" {
			bad.Consumers["portable:TEST-unrelated"] = installruntime.Consumer{Registration: "{}"}
		} else {
			c := bad.Consumers[keyB]
			c.Commands = []string{filepath.Join(base, "foreign.exe")}
			bad.Consumers[keyB] = c
		}
		file, err := StageLocalWindowsShortcut(keyA, home, primary, false, bad)
		if err == nil || file != nil {
			t.Fatalf("%s must refuse/preserve: %+v %v", kind, file, err)
		}
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, current) {
			t.Fatalf("peer refusal changed owned shortcut: %v", err)
		}
	}
	// Remove A using the real kernel ledger; B and the shortcut remain intact.
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: keyA, RemoveConsumer: true, ExpectedGeneration: &l.Generation})
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, current) || !reflect.DeepEqual(l.Consumers[keyB], cB) {
		t.Fatalf("first removal changed shared bytes or surviving registration: %v", err)
	}
	id, err := localWindowsIdentity(keyB, l)
	if err != nil {
		t.Fatal(err)
	}
	if err := windowsShortcutReadyIdentity(id, root, primary, home); err != nil {
		t.Fatal(err)
	}
	actualTarget, appID, args, err := inspectWindowsShortcut(path)
	if err != nil || !sameWindowsFile(actualTarget, primary) || appID != CopilotVSCodeToastAppID || args != "--help" {
		t.Fatalf("surviving COM identity: %q %q %q %v", actualTarget, appID, args, err)
	}
	file, err = StageLocalWindowsShortcut(keyB, home, primary, false, l)
	if err != nil || file == nil || !file.Remove || file.Path != path || file.Before != owned {
		t.Fatalf("last binding must stage exact owned removal: %+v %v", file, err)
	}
	sibling := l.Consumers["sibling"]
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: keyB, RefreshOnly: true, ExpectedGeneration: &l.Generation, Files: []installruntime.File{*file}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l.Consumers[keyB], cB) {
		t.Fatal("shortcut removal changed the last recorded consumer")
	}
	l, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: keyB, RemoveConsumer: true, ExpectedGeneration: &l.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("last removal did not remove owned shortcut: %v", err)
	}
	if _, remains := l.Consumers[keyB]; remains || !reflect.DeepEqual(l.Consumers["sibling"], sibling) {
		t.Fatal("last removal retained the selected binding or changed the legacy sibling")
	}
}
