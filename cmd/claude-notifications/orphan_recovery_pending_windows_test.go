//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// This fixture uses ordinary installer transactions and the existing fault seam.
// It deliberately depends on no Unix-only orphan fixture helpers.
func TestRuntimePendingWindowsReplaysOrdinaryInterruptedCommit(t *testing.T) {
	for _, mode := range []string{"--recover-pending", "--rollback-pending"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp("", "TEST-pending-windows-")
			if err != nil {
				t.Fatal(err)
			}
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("preserved failed fixture: %s", root)
					return
				}
				l, pending, err := installruntime.ReadOwnership(filepath.Join(root, "control"))
				if err != nil || pending || len(l.Consumers) != 0 {
					t.Errorf("ownership leak; preserved %s: %v", root, err)
					return
				}
				if os.Getenv("ORPHAN_CLI_KEEP_FIXTURES") == "1" {
					t.Logf("preserved verified fixture: %s", root)
					return
				}
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			})
			control, runtime := filepath.Join(root, "control"), filepath.Join(root, "runtime")
			payload := filepath.Join(runtime, "payload")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			request := installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: runtime, ConsumerID: "retained", Files: []installruntime.File{{Path: payload, Data: []byte("before"), Mode: 0600}}}
			before, err := installruntime.Commit(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			// A valid installation without a marker must be refused unchanged,
			// including paths, bytes, modes, ownership and generation.
			noJournal := pendingWindowsTree(t, root)
			var out bytes.Buffer
			err = installRuntime([]string{mode, "--control-root", control, "--json"}, &out)
			var result runtimeRecoveryResult
			if err == nil || json.Unmarshal(out.Bytes(), &result) != nil || result.Admissible {
				t.Fatalf("no-journal admitted: %v %s", err, &out)
			}
			observed, pending, err := installruntime.ReadOwnership(control)
			if err != nil || pending || !reflect.DeepEqual(before, observed) || !reflect.DeepEqual(noJournal, pendingWindowsTree(t, root)) {
				t.Fatalf("no-journal refusal mutated installation: %v", err)
			}
			interrupted := errors.New("TEST ordinary transaction interruption")
			identityBefore, err := installruntime.Fingerprint(payload)
			if err != nil || !identityBefore.Exists || before.Files[payload] != identityBefore {
				t.Fatalf("initial ownership/payload disagree: %v", err)
			}
			request.Files[0].Before = identityBefore
			request.Files[0].Data = []byte("after")
			request.Fault = func(phase string) error {
				if phase == "transaction" {
					return interrupted
				}
				return nil
			}
			_, err = installruntime.Commit(ctx, request)
			if !errors.Is(err, interrupted) {
				t.Fatalf("supported fault not reached: %v", err)
			}
			_, pending, err = installruntime.ReadOwnership(control)
			if err != nil || !pending {
				t.Fatalf("pending marker missing: %v", err)
			}
			out.Reset()
			// No orphan consumer/root/generation selectors: the adapter must
			// admit legitimate Windows journals and permanent private locks.
			if err := installRuntime([]string{mode, "--control-root", control, "--json"}, &out); err != nil {
				t.Fatal(err, &out)
			}
			if json.Unmarshal(out.Bytes(), &result) != nil || !result.Admissible || result.Mode != strings.TrimPrefix(mode, "--") {
				t.Fatalf("invalid recovery result: %s", &out)
			}
			observed, pending, err = installruntime.ReadOwnership(control)
			if err != nil || pending || observed.ID != before.ID || observed.Owner != before.Owner || observed.RuntimeRoot != before.RuntimeRoot || !reflect.DeepEqual(observed.Native, before.Native) || !reflect.DeepEqual(observed.Consumers, before.Consumers) {
				t.Fatalf("ownership did not converge: %v", err)
			}
			wantGeneration := before.Generation + 1
			if mode == "--rollback-pending" {
				wantGeneration++
			}
			if observed.Generation != wantGeneration || result.Generation != observed.Generation || result.InstallationID != observed.ID {
				t.Fatalf("unexpected generation/result: %+v %+v", observed, result)
			}
			want := "after"
			if mode == "--rollback-pending" {
				want = "before"
			}
			data, err := os.ReadFile(payload)
			if err != nil || string(data) != want {
				t.Fatalf("payload=%q want=%q err=%v", data, want, err)
			}
			identity, err := installruntime.Fingerprint(payload)
			if err != nil || observed.Files[payload] != identity {
				t.Fatalf("ownership/payload disagree: %v", err)
			}
			if _, err := installruntime.ReadInstalledSnapshot(control); err != nil {
				t.Fatal("recovered snapshot unhealthy", err)
			}
			if _, err := os.Lstat(filepath.Join(control, "transaction.json")); !os.IsNotExist(err) {
				t.Fatal("marker not retired", err)
			}
			request.Files, request.Fault, request.RemoveConsumer = nil, nil, true
			if _, err := installruntime.Commit(ctx, request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type pendingWindowsEntry struct {
	Mode fs.FileMode
	Hash [32]byte
}

func pendingWindowsTree(t *testing.T, root string) map[string]pendingWindowsEntry {
	t.Helper()
	tree := make(map[string]pendingWindowsEntry)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := pendingWindowsEntry{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.Hash = sha256.Sum256(data)
		}
		tree[rel] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
