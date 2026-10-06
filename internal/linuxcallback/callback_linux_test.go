//go:build linux

package linuxcallback

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/777genius/agent-notifications/internal/notification"
)

type testClock struct{ bits atomic.Uint64 }

func (c *testClock) Now() (string, float64, error) {
	return "test-boot", math.Float64frombits(c.bits.Load()), nil
}
func callbackFixture(t *testing.T) (*Handler, string, *testClock, *int) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	key := strings.Repeat("a", 64)
	s := Snapshot{Version: 1, InstallationID: strings.Repeat("b", 64), ApplicationID: "org.agentnotifications.Callback" + strings.Repeat("b", 64), Launcher: "/usr/lib/chatgpt/codex-launcher", Executable: "/usr/lib/chatgpt/ChatGPT", LauncherSHA256: strings.Repeat("c", 64), ExecutableSHA256: strings.Repeat("d", 64), ReleaseSHA256: strings.Repeat("e", 64), ManifestSHA256: strings.Repeat("f", 64), Reader: filepath.Join(dir, "reader"), ReaderSHA256: strings.Repeat("0", 64), DataRoot: filepath.Join(dir, "data"), Records: filepath.Join(dir, "records")}
	b, _ := json.Marshal(s)
	binding := notification.LinuxBinding{SnapshotPath: filepath.Join(dir, "snapshot.json"), SHA256: Digest(b)}
	if e := os.WriteFile(binding.SnapshotPath, b, 0400); e != nil {
		t.Fatal(e)
	}
	owner := Owners{"test-guid", ":1.2", ":1.3", ":1.4"}
	if e := writeRecord(s, Record{1, key, s.InstallationID, binding.SHA256, "opaque/id%?#", owner}); e != nil {
		t.Fatal(e)
	}
	clock := &testClock{}
	clock.bits.Store(math.Float64bits(100))
	launches := new(int)
	h := &Handler{Snapshot: s, Binding: binding, Clock: clock, ReadOwners: func(context.Context) (Owners, error) { return owner, nil }, Verify: func(context.Context, Snapshot) error { return nil }, Launch: func(_ context.Context, selected Snapshot, uri, token string) error {
		if selected != s || uri != "codex://threads/opaque%2Fid%25%3F%23" || token != "native-token" {
			t.Fatalf("wrong selected handoff: %+v %q %q", selected, uri, token)
		}
		*launches++
		return nil
	}}
	return h, key, clock, launches
}

// Red if caller-controlled action data can substitute for the real sender, or
// if unknown action keys create an effect without an owned durable record.
func TestForgedCallerAndUnknownKeyHaveZeroEffects(t *testing.T) {
	for _, tc := range []struct{ sender, key string }{{":1.99", strings.Repeat("a", 64)}, {":1.2", strings.Repeat("0", 64)}, {":1.2", "../record"}} {
		h, _, _, effects := callbackFixture(t)
		h.ReadOwners = func(context.Context) (Owners, error) {
			t.Fatal("forged callback reached owner probing")
			return Owners{}, nil
		}
		h.Verify = func(context.Context, Snapshot) error { t.Fatal("forged callback reached verifier"); return nil }
		if e := h.Handle(context.Background(), tc.sender, tc.key, "native-token"); e == nil || *effects != 0 {
			t.Fatal(e, *effects)
		}
	}
}

// Red if GTK, frontend, notification daemon or bus generation can restart and
// an old action is launched, including a restart during vendor verification.
func TestEveryOwnerGenerationIsRecheckedBeforeEffect(t *testing.T) {
	for _, phase := range []string{"before", "during"} {
		for _, kind := range []string{"guid", "gtk", "frontend", "notifications"} {
			t.Run(phase+kind, func(t *testing.T) {
				h, key, _, effects := callbackFixture(t)
				read := h.ReadOwners
				changed := phase == "before"
				h.ReadOwners = func(ctx context.Context) (Owners, error) {
					o, e := read(ctx)
					if changed {
						switch kind {
						case "guid":
							o.BusGUID = "new-bus"
						case "gtk":
							o.GTK = ":2.2"
						case "frontend":
							o.Frontend = ":2.3"
						case "notifications":
							o.Notifications = ":2.4"
						}
					}
					return o, e
				}
				h.Verify = func(context.Context, Snapshot) error { changed = true; return nil }
				if e := h.Handle(context.Background(), ":1.2", key, "native-token"); e == nil || *effects != 0 {
					t.Fatal(e, *effects)
				}
			})
		}
	}
}

// Red if old submission time expires a genuine late click, or if suspended
// time grants a verifier another launch budget. No submission clock is read.
func TestLateClickGetsFreshContinuousBudget(t *testing.T) {
	h, key, clock, effects := callbackFixture(t)
	clock.bits.Store(math.Float64bits(1000000))
	if e := h.Handle(context.Background(), ":1.2", key, "native-token"); e != nil || *effects != 1 {
		t.Fatal(e, *effects)
	}
	h, key, clock, effects = callbackFixture(t)
	h.Verify = func(context.Context, Snapshot) error { clock.bits.Store(math.Float64bits(104)); return nil }
	if e := h.Handle(context.Background(), ":1.2", key, "native-token"); e == nil || *effects != 0 {
		t.Fatal(e, *effects)
	}
}

// Red if an uncertain opener effect is replayed or a fallback is attempted.
func TestUnknownLaunchIsInvokedOnce(t *testing.T) {
	h, key, _, effects := callbackFixture(t)
	h.Launch = func(context.Context, Snapshot, string, string) error {
		*effects++
		return errors.New("unknown after handoff")
	}
	if e := h.Handle(context.Background(), ":1.2", key, "native-token"); e == nil || *effects != 1 {
		t.Fatal(e, *effects)
	}
}

// Red if setup B or mutable producer files redirect an A record. The cold
// reader only uses A's digest-addressed snapshot and retained durable records.
func TestSnapshotASurvivesSetupBAndReaderRollback(t *testing.T) {
	h, key, _, effects := callbackFixture(t)
	b := h.Snapshot
	b.InstallationID = strings.Repeat("0", 64)
	b.ApplicationID = "org.agentnotifications.Callback" + b.InstallationID
	raw, _ := json.Marshal(b)
	if e := os.WriteFile(filepath.Join(filepath.Dir(h.Binding.SnapshotPath), "current-policy.json"), append([]byte(`{"enabled":false,"clickToFocus":false,"setupB":`), append(raw, '}')...), 0600); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(h.Binding)
	if e != nil || loaded != h.Snapshot {
		t.Fatal(loaded, e)
	}
	h.Snapshot = loaded
	if e = h.Handle(context.Background(), ":1.2", key, "native-token"); e != nil || *effects != 1 {
		t.Fatal(e, *effects)
	}
	// The compatible v1 decoder reads the immutable A bytes, not current policy.
	var rollback Snapshot
	original, e := ReadOwned(h.Binding.SnapshotPath, 16384)
	if e != nil || decode(original, &rollback) != nil || rollback != loaded {
		t.Fatal("v1 snapshot incompatible", e)
	}
}
func TestThreadURIIsOneOpaqueSegment(t *testing.T) {
	for id, want := range map[string]string{"a/b": "codex://threads/a%2Fb", "x?y#z%": "codex://threads/x%3Fy%23z%25", ".": "codex://threads/%2E", "..": "codex://threads/%2E%2E", " space ": "codex://threads/%20space%20"} {
		got, e := ThreadURI(id)
		if e != nil || got != want {
			t.Fatal(id, got, e)
		}
	}
}
