package codexsetup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestLegacyConfigMutableAdoptionRepairRemoval(t *testing.T) {
	for _, initial := range []string{"", `{"desktop":{"enabled":false},"foreign":7}`, "malformed user bytes"} {
		t.Run(initial, func(t *testing.T) {
			source, home := fakeBundle(t), t.TempDir()
			opts := Options{ControlRoot: filepath.Join(home, "control"), CodexHome: home, PluginRoot: source}
			config := filepath.Join(home, InstallDirName, "config", "config.json")
			if initial != "" {
				if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, []byte(initial), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Run(opts); err != nil {
				t.Fatal(err)
			}
			expected := initial
			if expected == "" {
				expected = `{"notifications":{}}`
			}
			check := func(want string) {
				t.Helper()
				got, err := os.ReadFile(config)
				if err != nil || string(got) != want {
					t.Fatalf("config %q, %v; want %q", got, err, want)
				}
			}
			check(expected)
			edited := `{"desktop":{"enabled":false},"edited":true}`
			if err := os.WriteFile(config, []byte(edited), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(opts); err != nil {
				t.Fatal(err)
			}
			check(edited)
			data, err := os.ReadFile(filepath.Join(opts.ControlRoot, "ownership.json"))
			if err != nil {
				t.Fatal(err)
			}
			var ledger installruntime.Ledger
			if err := json.Unmarshal(data, &ledger); err != nil {
				t.Fatal(err)
			}
			if _, owned := ledger.Files[config]; owned {
				t.Fatal("mutable config is immutable-owned")
			}
			opts.Remove = true
			if _, err := Run(opts); err != nil {
				t.Fatal(err)
			}
			check(edited)
		})
	}
}

func TestLegacyDefaultsWaitForConfigWriterAndPreserveDisable(t *testing.T) {
	source, home := fakeBundle(t), t.TempDir()
	opts := Options{ControlRoot: filepath.Join(home, "control"), CodexHome: home, PluginRoot: source}
	path := filepath.Join(home, InstallDirName, "config", "config.json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := installruntime.Lock(ctx, path+".lock")
	if err != nil {
		t.Fatal(err)
	}
	// This writer holds the permanent config lock before setup can publish
	// defaults. Its global disable must win when setup resumes.
	done := make(chan error, 1)
	go func() { _, err := Run(opts); done <- err }()
	disabled := []byte(`{"desktop":{"enabled":false},"sound":{"enabled":false}}`)
	if err := os.WriteFile(path, disabled, 0600); err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(disabled) {
		t.Fatalf("disable overwritten: %q %v", got, err)
	}
}

func TestPreviouslyImmutableConfigIsNeverRemovedByAdapter(t *testing.T) {
	source, home := fakeBundle(t), t.TempDir()
	opts := Options{ControlRoot: filepath.Join(home, "control"), CodexHome: home, PluginRoot: source}
	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(result.InstallDir, "config", "config.json")
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(opts.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	id := "codex:" + result.HooksPath
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Model precisely the obsolete assembly's immutable ledger row, using the
	// real common transaction. No manual ledger edits or real user state.
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: opts.ControlRoot, Owner: "existing-installer", RuntimeRoot: result.InstallDir, ConsumerID: id, Consumer: snap.Ledger.Consumers[id], Files: []installruntime.File{{Path: path, Before: before, Data: data, Mode: before.Mode}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, remove := range []bool{false, true} {
		opts.Remove = remove
		if _, err := Run(opts); err == nil || !strings.Contains(err.Error(), "ownership to mutable") {
			t.Fatalf("expected actionable migration refusal, got %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(data) {
			t.Fatal("historically owned user config changed")
		}
	}
}
