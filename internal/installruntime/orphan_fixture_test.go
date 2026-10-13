package installruntime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/codexcommand"
)

// All ownership originates from ordinary Commit requests in a new test home.
// No registration flow or native process is run by this kernel fixture.
type orphanFixture struct {
	ctx                                                       context.Context
	root, control, primary, runtime, registration, consumerID string
	before                                                    Ledger
	orphanPaths                                               []string
	sentinel                                                  string
}

func newOrphanFixture(t *testing.T) *orphanFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for key, leaf := range map[string]string{
		"HOME": "home", "USERPROFILE": "home", "CODEX_HOME": "codex-home", "CLAUDE_HOME": "claude", "CLAUDE_CONFIG_DIR": "claude",
		"XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state", "XDG_RUNTIME_DIR": "xdg-runtime",
		"APPDATA": "appdata", "LOCALAPPDATA": "localappdata", "TMPDIR": "tmp", "TMP": "tmp", "TEMP": "tmp",
	} {
		path := filepath.Join(root, leaf)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", "")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	f := &orphanFixture{ctx: ctx, root: root, control: filepath.Join(root, "control"), primary: filepath.Join(root, "primary"), runtime: filepath.Join(root, "o'brien space", "codex", codexcommand.InstallDirName)}
	f.registration = filepath.Join(filepath.Dir(f.runtime), "hooks.json")
	f.consumerID = "codex:" + f.registration
	defaultRoot, err := ControlRoot()
	if err != nil || !pathWithinRoot(root, defaultRoot) {
		t.Fatalf("default control escaped fixture: %s %v", defaultRoot, err)
	}
	f.sentinel = filepath.Join(root, "fake-other-user", "control", "sentinel")
	if err := os.MkdirAll(filepath.Dir(f.sentinel), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.sentinel, []byte("outsider bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b, err := os.ReadFile(f.sentinel)
		if err != nil || string(b) != "outsider bytes" {
			t.Errorf("outside control sentinel changed: %v", err)
		}
	})
	retained := make([]File, 28)
	for i := range retained {
		retained[i] = File{Path: filepath.Join(f.primary, fmt.Sprintf("asset-%02d", i)), Data: []byte(fmt.Sprintf("retained-%d", i)), Mode: 0640}
	}
	if _, err = Commit(ctx, Request{ControlRoot: f.control, RuntimeRoot: f.primary, Owner: "existing-installer", ConsumerID: "retained", Files: retained}); err != nil {
		t.Fatal(err)
	}
	files := []File{
		{Path: filepath.Join(f.runtime, "bin", "codex-hook-wrapper.sh"), Data: []byte("inert POSIX wrapper"), Mode: 0755},
		{Path: filepath.Join(f.runtime, "bin", "codex-hook-wrapper.cmd"), Data: []byte("inert cmd wrapper"), Mode: 0644},
	}
	for i := 0; i < 19; i++ {
		files = append(files, File{Path: filepath.Join(f.runtime, "payload", fmt.Sprintf("asset-%02d", i)), Data: []byte(fmt.Sprintf("orphan-%d", i)), Mode: 0644})
	}
	for _, file := range files {
		f.orphanPaths = append(f.orphanPaths, file.Path)
	}
	sort.Strings(f.orphanPaths)
	// The sibling registration is observation-only, never in the runtime Files.
	if err := os.WriteFile(f.registration, []byte("inert generated hooks registration"), 0600); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(f.registration), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.registration, []byte("inert generated hooks registration"), 0600); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	f.before, err = Commit(ctx, Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: f.consumerID, Consumer: Consumer{Registration: f.registration, Commands: codexcommand.Commands(f.runtime)}, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	f.before, err = Commit(ctx, Request{ControlRoot: f.control, RuntimeRoot: f.primary, Owner: "existing-installer", ConsumerID: "retained", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &f.before.Generation, PolicyEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	// Manual raw-policy fields are legitimate operator edits. Recovery preserves
	// their exact bytes, spacing, unknown fields, consent and navigation intent.
	raw := []byte("{\n  \"schemaVersion\":1, \"enabled\":true, \"unknownOperatorField\":{\"keep\":true},\n  \"route\":{\"navigation\":false}, \"rates\":{\"custom\":7}, \"setupState\":{\"foreignConsent\":false}\n}\n")
	if err := os.WriteFile(filepath.Join(f.control, "agent-notifications.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInstalledSnapshot(f.control); err != nil {
		t.Fatalf("initial fixture unhealthy: %v", err)
	}
	return f
}

func (f *orphanFixture) abandon(t *testing.T, removeRoot bool) {
	t.Helper()
	for _, path := range f.orphanPaths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(f.registration); err != nil {
		t.Fatal(err)
	}
	if removeRoot {
		if err := os.RemoveAll(f.runtime); err != nil {
			t.Fatal(err)
		}
	}
}

func orphanControlImages(t *testing.T, root string) map[string][]byte {
	t.Helper()
	images := map[string][]byte{}
	for _, name := range []string{"ownership.json", "agent-notifications.json", "policy-generation.json", "transaction.json"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			images[path] = nil
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		images[path] = data
	}
	return images
}

func assertOrphanImages(t *testing.T, images map[string][]byte) {
	t.Helper()
	for path, want := range images {
		got, err := os.ReadFile(path)
		if want == nil {
			if !os.IsNotExist(err) {
				t.Errorf("created absent control file %s: %v", path, err)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("changed control preimage %s: %v", path, err)
		}
	}
}

// This test uses only pre-existing APIs and runs unchanged on frozen main.
func TestOrphanOrdinaryRemovalRefusesMissing21Payloads(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, true)
	images := orphanControlImages(t, f.control)
	_, err := Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: f.consumerID, RemoveConsumer: true})
	if err == nil || !strings.Contains(err.Error(), "managed fingerprint changed without transaction") {
		t.Fatalf("ordinary removal no longer reproduces admission blocker: %v", err)
	}
	t.Logf("existing RemoveConsumer rejected 21 absent payloads: %v", err)
	assertOrphanImages(t, images)
}
