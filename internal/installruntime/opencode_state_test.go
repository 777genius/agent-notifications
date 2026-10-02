package installruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func privateRegistrationRequest(t *testing.T) (context.Context, Request) {
	t.Helper()
	ctx, r := request(t)
	root, err := CanonicalPath(r.RuntimeRoot)
	if err != nil {
		t.Fatal("canonical private runtime root", err)
	}
	r.RuntimeRoot = root
	r.ConsumerID = openCodeConsumer
	bundle := []byte("inert private origin " + strings.Repeat("1", 64))
	r.Consumer = Consumer{Registration: filepath.Join(r.RuntimeRoot, "agent-notifications.js"),
		OpenCode: &OpenCodeRegistration{Origin: strings.Repeat("1", 64), Salt: strings.Repeat("2", 64), Namespace: strings.Repeat("3", 64), BundleSHA256: identity(bundle, 0600).SHA256, OriginBound: true}}
	r.Files = []File{{Path: r.Consumer.Registration, Data: bundle, Mode: 0600}}
	return ctx, r
}

// Catches schema/floor omissions that allow old writers to discard private
// fields, and binary rollback that would install an unaware writer over them.
func TestOpenCodePersistedCompatibilityFence(t *testing.T) {
	ctx, r := privateRegistrationRequest(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(r.ControlRoot, "ownership.json"))
	// Independent frozen pre-E1 decoder contract, not current helper functions.
	var old struct{ Schema, WriterFloor int }
	if json.Unmarshal(before, &old) != nil || (old.Schema <= 3 && old.WriterFloor <= 2) {
		t.Fatal("older writer accepts new persisted protocol")
	}
	gen := l.Generation
	r.ExpectedGeneration, r.RefreshOnly = &gen, true
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "claude-notifications-linux-amd64"), Data: []byte(WriterProtocolMarker), Mode: 0700}}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("unaware binary promoted")
	}
	after, _ := os.ReadFile(filepath.Join(r.ControlRoot, "ownership.json"))
	if string(before) != string(after) {
		t.Fatal("refusal mutated ledger")
	}
	r.Files = nil
	r.RefreshOnly = false
	r.Consumer.OpenCode.Origin = strings.Repeat("4", 64)
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("live incarnation rotated")
	}
}

// Catches crash recovery losing the initialization decision, publishing usable
// mismatched registration, or unlinking a permanent lock during partial purge.
func TestOpenCodeCrashForwardRecovery(t *testing.T) {
	if root := os.Getenv("E1_PRIVATE_CRASH_ROOT"); root != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mode := os.Getenv("E1_PRIVATE_CRASH_MODE")
		l, _, err := ReadOwnership(root)
		if err != nil {
			t.Fatal(err)
		}
		r := Request{ControlRoot: root, RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: openCodeConsumer, ExpectedGeneration: &l.Generation}
		if mode == "init" {
			bundle := []byte("inert crash origin")
			r.Consumer = Consumer{Registration: filepath.Join(l.RuntimeRoot, "agent-notifications.js"), OpenCode: &OpenCodeRegistration{Origin: strings.Repeat("a", 64), Salt: strings.Repeat("b", 64), Namespace: strings.Repeat("c", 64), BundleSHA256: identity(bundle, 0600).SHA256, OriginBound: true}}
			r.Files = []File{{Path: r.Consumer.Registration, Data: bundle, Mode: 0600}}
		} else {
			r.RemoveConsumer = true
			path := l.Consumers[openCodeConsumer].Registration
			r.Files = []File{{Path: path, Before: l.Files[path], Remove: true}}
		}
		r.Fault = func(phase string) error {
			if mode == "init" && phase == "opencode-init" || mode == "purge" && strings.HasPrefix(phase, "purge-entry:") {
				os.Exit(73)
			}
			return nil
		}
		if _, err := Commit(ctx, r); err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash point missed")
	}
	for _, mode := range []string{"init", "purge"} {
		t.Run(mode, func(t *testing.T) {
			ctx, r := privateRegistrationRequest(t)
			other := Consumer{Registration: filepath.Join(r.RuntimeRoot, "other-fixture"), Commands: []string{"retained-inert-command"}, RuntimeRoot: r.RuntimeRoot}
			base := r
			base.ConsumerID = "other-fixture"
			base.Consumer = other
			base.Files = nil
			l, err := Commit(ctx, base)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "purge" {
				if l, err = Commit(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			// Closed removal requires both explicit channel consents false.
			patch := Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner, ConsumerID: "other-fixture", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &l.Generation, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":false,"webhook":false}}`)}}
			if l, err = Commit(ctx, patch); err != nil {
				t.Fatal(err)
			}
			coverage, err := os.MkdirTemp(r.ControlRoot, "crash-cover-")
			if err != nil {
				t.Fatal("owned crash-helper coverage directory", err)
			}
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOpenCodeCrashForwardRecovery$")
			cmd.Dir = r.ControlRoot
			cmd.Env = []string{"E1_PRIVATE_CRASH_ROOT=" + r.ControlRoot, "E1_PRIVATE_CRASH_MODE=" + mode, "GOCOVERDIR=" + coverage}
			out, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 73 {
				t.Fatalf("crash process: %v %s", err, out)
			}
			lock, err := os.Lstat(filepath.Join(r.ControlRoot, OpenCodeStoreLock))
			if err != nil {
				t.Fatal(err)
			}
			s, err := ReadPolicySnapshot(ctx, r.ControlRoot)
			if err != nil || !s.Installation.Recovery {
				t.Fatal("interrupted private decision usable", err)
			}
			if _, err = Commit(ctx, Request{ControlRoot: r.ControlRoot, RollbackPending: true}); err == nil {
				t.Fatal("private decision rolled back")
			}
			after, err := Recover(ctx, r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			if after.ID != l.ID || !reflect.DeepEqual(after.Consumers["other-fixture"], other) {
				t.Fatal("shared metadata lost")
			}
			if mode == "init" {
				reg := after.Consumers[openCodeConsumer].OpenCode
				store, err := AcquireOpenCodeStore(ctx, r.ControlRoot, *reg)
				if err != nil {
					t.Fatal(err)
				}
				payload, err := store.Read()
				store.Close()
				if err != nil || string(payload) != "{}" {
					t.Fatal("initial state mismatched", err)
				}
			} else {
				if _, exists := after.Consumers[openCodeConsumer]; exists {
					t.Fatal("purge recovered active consumer")
				}
				if _, err = os.Lstat(filepath.Dir(openCodeStatePath(r.ControlRoot, *r.Consumer.OpenCode))); !os.IsNotExist(err) {
					t.Fatal("partial purge not finished")
				}
			}
			named, _ := os.Lstat(filepath.Join(r.ControlRoot, OpenCodeStoreLock))
			if !os.SameFile(lock, named) {
				t.Fatal("private lock inode replaced")
			}
			if _, err = Recover(ctx, r.ControlRoot); err != nil {
				t.Fatal("repeated recovery", err)
			}
		})
	}
}

// Catches forward recovery deleting replacement data or allowing reinstall
// after failed removal. Captured inode identities survive partial deletion.
func TestOpenCodePartialPurgePreservesReplacement(t *testing.T) {
	ctx, r := privateRegistrationRequest(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	gen := l.Generation
	patch := Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner, ConsumerID: openCodeConsumer, RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":false,"webhook":false}}`)}}
	l, err = Commit(ctx, patch)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedGeneration = &l.Generation
	r.RemoveConsumer = true
	r.Files = nil
	r.Fault = func(phase string) error {
		if strings.HasPrefix(phase, "purge-entry:") {
			return fmt.Errorf("interrupted deletion")
		}
		return nil
	}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("purge fault missed")
	}
	path := openCodeStatePath(r.ControlRoot, *r.Consumer.OpenCode)
	if err = os.WriteFile(path, []byte("replacement evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Recover(ctx, r.ControlRoot); err == nil {
		t.Fatal("replacement erased")
	}
	r.Fault = nil
	r.RemoveConsumer = false
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("failed removal allowed reinstall")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "replacement evidence" {
		t.Fatal("replacement changed")
	}
}
