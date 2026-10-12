package installruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVersionedClaudeCacheRelocationSharedOpenCode(t *testing.T) {
	for _, scenario := range []string{"success", "recovery", "foreign-primary", "invalid-command", "foreign-after-upgrade"} {
		t.Run(scenario, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
			oldRoot, newRoot := filepath.Join(cache, "1.46.1"), filepath.Join(cache, "1.49.1")
			entry := "claude-notifications-linux-amd64"
			portablePrimary := filepath.Join(oldRoot, "bin", entry)
			openCodePrimary := filepath.Join(oldRoot, entry)
			newPrimary := filepath.Join(newRoot, "bin", entry)
			plugin := filepath.Join(root, "opencode", "plugins", "agent-notifications.js")
			control := filepath.Join(root, "control")
			oldBytes, newBytes := []byte("old"+WriterProtocolMarker), []byte("new"+WriterProtocolMarker)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
				Files: []File{{Path: portablePrimary, Data: oldBytes, Mode: 0700}}}); err != nil {
				t.Fatal(err)
			}
			args := []string{openCodePrimary, "opencode-event", "--protocol", "1"}
			if scenario == "invalid-command" {
				args[3] = "2"
			}
			if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "opencode-notifications",
				Consumer: Consumer{Registration: plugin, Commands: args}, Files: []File{{Path: openCodePrimary, Data: oldBytes, Mode: 0700}, {Path: plugin, Data: []byte("unchanged OpenCode plugin"), Mode: 0600}}}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"portable:claude", "portable:codex"} {
				if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: id,
					Consumer: Consumer{Registration: id + " binding", Commands: []string{portablePrimary}}}); err != nil {
					t.Fatal(err)
				}
			}
			before, _, err := ReadOwnership(control)
			if err != nil {
				t.Fatal(err)
			}
			before, err = Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "opencode-notifications",
				PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &before.Generation,
				PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":false,"webhook":true}}`)}})
			if err != nil {
				t.Fatal(err)
			}
			policyPath := filepath.Join(control, "agent-notifications.json")
			policyBefore, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "foreign-primary" {
				if err := os.WriteFile(openCodePrimary, []byte("foreign edit"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			move := Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: "existing-installer", ConsumerID: "claude-hooks", RelocateVersionedCache: true,
				Files: []File{{Path: newPrimary, Data: newBytes, Mode: 0700}}}
			if scenario == "recovery" {
				move.Fault = func(phase string) error {
					if phase == "transaction" {
						return fmt.Errorf("interrupted shared upgrade")
					}
					return nil
				}
			}
			after, err := Commit(ctx, move)
			if scenario == "foreign-primary" || scenario == "invalid-command" {
				if err == nil {
					t.Fatal("unsafe shared upgrade succeeded")
				}
				if scenario == "foreign-primary" && !strings.Contains(err.Error(), "fingerprint") {
					t.Fatalf("foreign edit was not checked: %v", err)
				}
				unchanged, pending, readErr := ReadOwnership(control)
				if readErr != nil || pending || !reflect.DeepEqual(before, unchanged) {
					t.Fatalf("rejection changed ownership: %v", readErr)
				}
				if _, err := os.Stat(newPrimary); !os.IsNotExist(err) {
					t.Fatal("rejection published new primary")
				}
				return
			}
			if scenario == "recovery" {
				if err == nil || !strings.Contains(err.Error(), "interrupted shared upgrade") {
					t.Fatalf("upgrade never reached its journal: %v", err)
				}
				after, err = Commit(ctx, Request{ControlRoot: control, RecoverOnly: true})
			}
			if err != nil {
				t.Fatalf("shared OpenCode cache relocation failed: %v", err)
			}
			if after.RuntimeRoot != oldRoot || after.Consumers["claude-hooks"].RuntimeRoot != newRoot {
				t.Fatal("wrong retained/moving runtime")
			}
			for _, id := range []string{"opencode-notifications", "portable:claude", "portable:codex"} {
				if !reflect.DeepEqual(before.Consumers[id], after.Consumers[id]) {
					t.Fatalf("consumer changed: %s", id)
				}
			}
			for _, path := range []string{portablePrimary, openCodePrimary, newPrimary} {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, newBytes) || !after.Files[path].Exists {
					t.Fatalf("primary not refreshed/owned: %s: %v", path, err)
				}
			}
			pluginAfter, err := os.ReadFile(plugin)
			if err != nil || string(pluginAfter) != "unchanged OpenCode plugin" {
				t.Fatal("plugin registration changed")
			}
			policyAfter, err := os.ReadFile(policyPath)
			if err != nil || !bytes.Equal(policyBefore, policyAfter) {
				t.Fatal("channel/admission policy changed")
			}
			// A later Claude upgrade must refresh bindings retained at the first root.
			nextRoot := filepath.Join(cache, "1.50.0")
			nextPrimary := filepath.Join(nextRoot, "bin", entry)
			nextBytes := []byte("next" + WriterProtocolMarker)
			move.RuntimeRoot = nextRoot
			move.Files = []File{{Path: nextPrimary, Data: nextBytes, Mode: 0700}}
			firstUpgrade := after
			if scenario == "foreign-after-upgrade" {
				if err := os.WriteFile(openCodePrimary, []byte("foreign edit after upgrade"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			after, err = Commit(ctx, move)
			if scenario == "foreign-after-upgrade" {
				if err == nil || !strings.Contains(err.Error(), "fingerprint") {
					t.Fatalf("second upgrade did not reject foreign edit: %v", err)
				}
				unchanged, pending, readErr := ReadOwnership(control)
				if readErr != nil || pending || !reflect.DeepEqual(firstUpgrade, unchanged) {
					t.Fatalf("second rejection changed ownership: %v", readErr)
				}
				if _, err := os.Stat(nextPrimary); !os.IsNotExist(err) {
					t.Fatal("second rejection published new primary")
				}
				return
			}
			if scenario == "recovery" {
				if err == nil || !strings.Contains(err.Error(), "interrupted shared upgrade") {
					t.Fatalf("second upgrade never reached its journal: %v", err)
				}
				after, err = Commit(ctx, Request{ControlRoot: control, RecoverOnly: true})
			}
			if err != nil {
				t.Fatalf("second shared cache relocation failed: %v", err)
			}
			for _, path := range []string{portablePrimary, openCodePrimary, nextPrimary} {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, nextBytes) || !after.Files[path].Exists {
					t.Fatalf("second upgrade left a stale primary: %s: %v", path, err)
				}
			}
			for _, id := range []string{"opencode-notifications", "portable:claude", "portable:codex"} {
				if !reflect.DeepEqual(before.Consumers[id], after.Consumers[id]) {
					t.Fatalf("second upgrade changed consumer: %s", id)
				}
			}
		})
	}
}
