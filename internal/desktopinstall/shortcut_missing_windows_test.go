//go:build windows

package desktopinstall

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

// Missing owned shortcuts can be repaired or retired through the actual
// kernel transaction. A replacement after staging is still foreign to CAS.
func TestWindowsPortableMissingShortcutLifecycle(t *testing.T) {
	for _, integration := range []portable.Integration{portable.Cursor, portable.CopilotVSCode} {
		for _, repair := range []bool{false, true} {
			operation := "remove"
			if repair {
				operation = "repair"
			}
			t.Run(string(integration)+"/"+operation, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				base, err := installruntime.CanonicalPath(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				root, run, home := filepath.Join(base, "control"), filepath.Join(base, "runtime"), filepath.Join(base, "TEST-home")
				ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run,
					Owner: "existing-installer", ConsumerID: "sibling", Consumer: installruntime.Consumer{Registration: "TEST sibling"}})
				if err != nil {
					t.Fatal(err)
				}
				primary := filepath.Join(run, "primary.exe")
				binding := portable.Binding{Version: 1, Integration: integration, InstallationID: "TEST-install", BindingID: "TEST-binding",
					ScopeID: "TEST-scope", ComponentID: ledger.ID, Owner: ledger.Owner, ScopeRoot: base, DataRoot: base,
					ControlRoot: root, GlobalConfig: filepath.Join(root, "config.json"), RuntimeRoot: run, Primary: "primary.exe"}
				key, consumer, _, err := binding.Registration()
				if err != nil {
					t.Fatal(err)
				}
				ledger, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: ledger.Owner,
					ConsumerID: key, Consumer: consumer, ExpectedGeneration: &ledger.Generation,
					Files: []installruntime.File{{Path: primary, Data: []byte("inert TEST " + installruntime.LocalWriterProtocolMarker), Mode: 0700}}})
				if err != nil {
					t.Fatal(err)
				}
				stage, product := StageCursorWindowsShortcut, CursorDesktop
				if integration == portable.CopilotVSCode {
					stage, product = StageLocalWindowsShortcut, CopilotVSCodeDesktop
				}
				file, err := stage(key, home, primary, true, ledger)
				if err != nil || file == nil {
					t.Fatalf("initial COM shortcut: %+v %v", file, err)
				}
				path := file.Path
				ledger, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: ledger.Owner,
					ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &ledger.Generation, Files: []installruntime.File{*file}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				file, err = stage(key, home, primary, repair, ledger)
				if err != nil || file == nil || file.Path != path || file.Before.Exists || file.Remove == repair {
					t.Fatalf("missing owned shortcut %s: %+v %v", operation, file, err)
				}
				request := installruntime.Request{ControlRoot: root, RuntimeRoot: run, Owner: ledger.Owner, ConsumerID: key,
					RefreshOnly: repair, RemoveConsumer: !repair, ExpectedGeneration: &ledger.Generation, Files: []installruntime.File{*file}}
				foreign := []byte("TEST concurrent foreign replacement")
				if err := os.WriteFile(path, foreign, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := installruntime.Commit(ctx, request); err == nil {
					t.Fatal("absent preimage adopted a concurrent replacement")
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, foreign) {
					t.Fatalf("CAS rejection changed replacement: %q %v", got, err)
				}
				// The foreign replacement intentionally fails installed readiness.
				// Compare durable ownership and journal state without admitting it.
				recorded, recovery, err := installruntime.ReadOwnership(root)
				if err != nil || recovery || !reflect.DeepEqual(recorded, ledger) {
					t.Fatalf("CAS rejection changed ownership: %+v recovery=%v %v", recorded, recovery, err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				after, err := installruntime.Commit(ctx, request)
				if err != nil {
					t.Fatalf("missing shortcut kernel %s: %v", operation, err)
				}
				if !reflect.DeepEqual(after.Consumers["sibling"], ledger.Consumers["sibling"]) {
					t.Fatal("shortcut lifecycle changed sibling")
				}
				if repair {
					id, err := portableWindowsIdentity(product, integration, key, after)
					if err != nil {
						t.Fatal(err)
					}
					if err := windowsShortcutReadyIdentity(id, root, primary, home); err != nil {
						t.Fatalf("repaired COM identity/readiness: %v", err)
					}
					if !reflect.DeepEqual(after.Consumers[key], ledger.Consumers[key]) {
						t.Fatal("repair changed exact consumer")
					}
				} else {
					if _, ok := after.Consumers[key]; ok {
						t.Fatal("missing shortcut prevented consumer removal")
					}
					if _, ok := installruntime.OwnedFile(after, path); ok {
						t.Fatal("missing shortcut ownership was not retired")
					}
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("removal recreated shortcut: %v", err)
					}
				}
			})
		}
	}
}
