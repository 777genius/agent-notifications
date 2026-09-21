//go:build linux

package setup

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func linuxNoneFixture(t *testing.T) (Options, Request) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	o := Options{
		ControlRoot: filepath.Join(root, "control"),
		RuntimeRoot: filepath.Join(root, "runtime"),
		Owner:       "existing-installer", ConsumerID: "codex",
		GlobalConfig: filepath.Join(root, "global.json"), Platform: "linux",
		JournalClock: journal.DefaultClock(),
	}
	hook := filepath.Join(o.RuntimeRoot, "hook")
	l, err := installruntime.Commit(contextFor(t), installruntime.Request{
		ControlRoot: o.ControlRoot, RuntimeRoot: o.RuntimeRoot, Owner: o.Owner, ConsumerID: o.ConsumerID,
		Files: []installruntime.File{{Path: hook, Data: []byte("unchanged hook fixture"), Mode: 0700}},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, o.GlobalConfig, globalConfig, 0600)
	return o, Request{ExpectedGeneration: l.Generation, Enabled: ptr(true), Route: &Route{}}
}

func TestLinuxNoneSetupProvisionsJournalWithoutNative(t *testing.T) {
	o, r := linuxNoneFixture(t)
	result, err := Apply(contextFor(t), o, r)
	if err != nil || !result.Enabled || result.Namespace == "" {
		t.Fatal(result, err)
	}
	s := snapshot(t, o)
	if s.Installation.Ledger.Native != nil {
		t.Fatal("linux none setup installed native helper")
	}
	assertNone(t, o)
	store, err := journal.Open(contextFor(t), journal.Options{Root: filepath.Join(o.ControlRoot, "state", "journal"), Clock: o.JournalClock})
	if err != nil {
		t.Fatal(err)
	}
	a := journal.Admission{Key: journal.Key{Source: "codex/local", Session: "session", Kind: journal.Explicit, Request: "linux-none"}, Digest: sha256.Sum256([]byte("payload")), TrackingID: "tracking"}
	admitted, err := store.Admit(contextFor(t), a)
	if err != nil || !admitted.Fresh {
		t.Fatal(admitted, err)
	}
}

func TestLinuxLocalRoutingDoesNotMutateState(t *testing.T) {
	o, r := linuxNoneFixture(t)
	r.Route = &Route{LocalRouting: true, ApplicationPath: "/Applications/Chosen.app", TeamID: "TEAM123456"}
	before := tree(t, filepath.Dir(o.ControlRoot))
	_, err := Apply(contextFor(t), o, r)
	if err == nil {
		t.Fatal("linux local routing accepted")
	}
	wantReason(t, err, "unsupported_platform")
	if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
		t.Fatal("failure mutated state")
	}
}
