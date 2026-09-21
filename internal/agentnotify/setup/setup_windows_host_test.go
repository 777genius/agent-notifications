//go:build linux || darwin

package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func windowsNoneFixture(t *testing.T) (Options, Request) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	o := Options{
		ControlRoot:  filepath.Join(root, "control"),
		RuntimeRoot:  filepath.Join(root, "runtime"),
		Owner:        "existing-installer",
		ConsumerID:   "codex",
		GlobalConfig: filepath.Join(root, "global.json"),
		Platform:     "windows",
		JournalClock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "fixture", Seconds: 100, Available: true} }),
	}
	hook := filepath.Join(o.RuntimeRoot, "hook")
	k := installruntime.Request{ControlRoot: o.ControlRoot, RuntimeRoot: o.RuntimeRoot, Owner: o.Owner, ConsumerID: o.ConsumerID, Files: []installruntime.File{{Path: hook, Data: []byte("unchanged hook fixture"), Mode: 0700}}}
	l, e := installruntime.Commit(contextFor(t), k)
	if e != nil {
		t.Fatal(e)
	}
	write(t, o.GlobalConfig, globalConfig, 0600)
	return o, Request{ExpectedGeneration: l.Generation, Enabled: ptr(true), Route: &Route{}}
}

func TestWindowsNoneSetupProvisionsJournalWithoutNative(t *testing.T) {
	o, r := windowsNoneFixture(t)
	result, e := Apply(contextFor(t), o, r)
	if e != nil || !result.Enabled || result.Namespace == "" {
		t.Fatal(result, e)
	}
	s := snapshot(t, o)
	if s.Installation.Ledger.Native != nil {
		t.Fatal("windows none setup installed native helper")
	}
	assertNone(t, o)
	jroot := filepath.Join(o.ControlRoot, "state", "journal")
	store, e := journal.Open(contextFor(t), journal.Options{Root: jroot, Clock: o.JournalClock})
	if e != nil {
		t.Fatal(e)
	}
	a := journal.Admission{Key: journal.Key{Source: "codex/local", Session: "session", Kind: journal.Explicit, Request: "windows-none"}, Digest: [32]byte{1}, TrackingID: "tracking"}
	admitted, e := store.Admit(contextFor(t), a)
	if e != nil || !admitted.Fresh {
		t.Fatal(admitted, e)
	}
}

func TestWindowsLocalRoutingStillRequiresAppIdentity(t *testing.T) {
	o, r := windowsNoneFixture(t)
	r.Route = &Route{LocalRouting: true, ApplicationPath: "/Applications/Chosen.app", TeamID: "TEAM123456"}
	before := tree(t, filepath.Dir(o.ControlRoot))
	_, e := Apply(contextFor(t), o, r)
	if e == nil {
		t.Fatal("windows local routing accepted")
	}
	wantReason(t, e, "unsupported_platform")
	if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
		t.Fatal("failure mutated state")
	}
}
