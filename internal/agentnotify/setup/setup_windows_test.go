//go:build windows

package setup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	policyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/windows"
)

func windowsContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func windowsWrite(t *testing.T, p, data string) {
	t.Helper()
	if e := os.WriteFile(p, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
}

func windowsRestrictPath(t *testing.T, path string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err = setupRestrictPrivate(h); err != nil {
		t.Fatal(err)
	}
}

func windowsNoneFixture(t *testing.T) (Options, Request) {
	t.Helper()
	root := t.TempDir()
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
	l, e := installruntime.Commit(windowsContext(t), k)
	if e != nil {
		t.Fatal(e)
	}
	windowsRestrictPath(t, o.ControlRoot)
	windowsWrite(t, o.GlobalConfig, `{"foreign":{"keep":true},"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`)
	enabled := true
	return o, Request{ExpectedGeneration: l.Generation, Enabled: &enabled, Route: &Route{}}
}

func TestWindowsNoneSetupProvisionsJournalWithoutNative(t *testing.T) {
	o, r := windowsNoneFixture(t)
	result, e := Apply(windowsContext(t), o, r)
	if e != nil || !result.Enabled || result.Namespace == "" {
		t.Fatal(result, e)
	}
	s, e := installruntime.ReadPolicySnapshot(windowsContext(t), o.ControlRoot)
	if e != nil {
		t.Fatal(e)
	}
	if s.Installation.Ledger.Native != nil {
		t.Fatal("windows none setup installed native helper")
	}
	var route map[string]any
	if e := json.Unmarshal(s.Fields["route"], &route); e != nil {
		t.Fatal(e)
	}
	want := map[string]any{"localRouting": false, "allowUnknownCaller": false, "allowCallerAsserted": false, "applicationPath": "", "teamID": ""}
	if !reflect.DeepEqual(route, want) {
		t.Fatalf("route: %s", s.Fields["route"])
	}
	p, e := policyruntime.ValidateSetupPolicy(s, o.GlobalConfig)
	if e != nil || p.Route != (origin.RoutePolicy{}) {
		t.Fatalf("runtime policy: %+v %v", p, e)
	}
	jroot := filepath.Join(o.ControlRoot, "state", "journal")
	store, e := journal.Open(windowsContext(t), journal.Options{Root: jroot, Clock: o.JournalClock})
	if e != nil {
		t.Fatal(e)
	}
	a := journal.Admission{Key: journal.Key{Source: "codex/local", Session: "session", Kind: journal.Explicit, Request: "windows-none"}, Digest: sha256.Sum256([]byte("payload")), TrackingID: "tracking"}
	admitted, e := store.Admit(windowsContext(t), a)
	if e != nil || !admitted.Fresh {
		t.Fatal(admitted, e)
	}
}

func TestWindowsLocalRoutingRejected(t *testing.T) {
	o, r := windowsNoneFixture(t)
	r.Route = &Route{LocalRouting: true, ApplicationPath: `C:\Chosen.app`, TeamID: "TEAM123456"}
	_, e := Apply(windowsContext(t), o, r)
	if e == nil {
		t.Fatal("windows local routing accepted")
	}
	var se *Error
	if !errors.As(e, &se) || se.Reason != "unsupported_platform" {
		t.Fatalf("wanted unsupported_platform, got %v", e)
	}
}
