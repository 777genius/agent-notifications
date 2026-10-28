package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestInstallRuntimeConfirmedSeedPreservesExplicitEnablement(t *testing.T) {
	for _, initial := range []string{"absent", "false", "true"} {
		t.Run(initial, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			root := t.TempDir()
			control, stage, target := filepath.Join(root, "control"), filepath.Join(root, "stage"), filepath.Join(root, "runtime", "bin")
			if err := os.Mkdir(stage, 0o700); err != nil {
				t.Fatal(err)
			}
			entry := "claude-notifications-linux-amd64"
			if runtime.GOOS == "windows" {
				entry = "claude-notifications-windows-" + runtime.GOARCH + ".exe"
			}
			if err := os.WriteFile(filepath.Join(stage, entry), []byte("TEST inert writer "+installruntime.WriterProtocolMarker), 0o700); err != nil {
				t.Fatal(err)
			}
			args := []string{"--stage", stage, "--target", target, "--entry", entry, "--control-root", control}
			if initial != "absent" {
				if err := installRuntime(args, io.Discard); err != nil {
					t.Fatal(err)
				}
				before, err := installruntime.ReadPolicySnapshot(ctx, control)
				if err != nil {
					t.Fatal(err)
				}
				on := initial == "true"
				if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: filepath.Dir(target), Owner: "existing-installer", ConsumerID: "claude-hooks", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &before.Installation.Ledger.Generation, ExpectedPolicy: &before.Preimage, PolicyEnabled: &on}); err != nil {
					t.Fatal(err)
				}
			}
			if err := installRuntime(append(args, "--seed-enabled-if-absent"), io.Discard); err != nil {
				t.Fatal(err)
			}
			after, err := installruntime.ReadPolicySnapshot(ctx, control)
			if err != nil || after.Policy.Enabled != (initial != "false") {
				t.Fatalf("initial %s => enabled=%v: %v", initial, after.Policy.Enabled, err)
			}
		})
	}
}

func TestInstallRuntimeSeedRefusesManualOptOutBetweenCommits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	control, stage, target := filepath.Join(root, "control"), filepath.Join(root, "stage"), filepath.Join(root, "runtime", "bin")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := "claude-notifications-linux-amd64"
	if runtime.GOOS == "windows" {
		entry = "claude-notifications-windows-" + runtime.GOARCH + ".exe"
	}
	if err := os.WriteFile(filepath.Join(stage, entry), []byte("TEST inert writer "+installruntime.WriterProtocolMarker), 0o700); err != nil {
		t.Fatal(err)
	}
	original := installruntime.Identity{}
	if err := installRuntime([]string{"--stage", stage, "--target", target, "--entry", entry, "--control-root", control}, io.Discard); err != nil {
		t.Fatal(err)
	}
	committed, _, err := installruntime.ReadOwnership(control)
	if err != nil {
		t.Fatal(err)
	}
	manual := []byte("{\"schemaVersion\":1,\"enabled\":false}\n")
	path := filepath.Join(control, "agent-notifications.json")
	if err := os.WriteFile(path, manual, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = seedCommittedRuntimeEnablement(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: filepath.Dir(target), Owner: "existing-installer", ConsumerID: "claude-hooks", ExpectedPolicy: &original}, committed)
	if !errors.Is(err, installruntime.ErrPolicyConflict) {
		t.Fatalf("intervening opt-out was adopted: %v", err)
	}
	after, err := installruntime.ReadPolicySnapshot(ctx, control)
	if err != nil || after.Policy.Enabled || after.Installation.Ledger.Generation != committed.Generation {
		t.Fatalf("manual opt-out changed: %+v %v", after, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(manual) {
		t.Fatal("manual policy bytes changed", err)
	}
}
