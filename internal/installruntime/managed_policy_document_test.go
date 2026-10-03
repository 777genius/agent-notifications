package installruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const rawPolicyBefore = `{"schemaVersion":1,"enabled":false,"notifications":{"desktop":{"volume":0.5}},"route":{"openCodeNotifications":{"desktop":false},"otherAgent":{"enabled":true}},"rates":{"burst":2},"setupState":{"origin":"immutable","nested":{"large":9007199254740993}},"foreign":{"large":9007199254740993}}`
const rawPolicyAfter = `{
 "foreign":{"large":9007199254740993}, "enabled":false, "schemaVersion":1,
 "setupState":{"nested":{"large":9007199254740993},"origin":"immutable"},
 "rates":{"burst":2}, "route":{"otherAgent":{"enabled":true},"openCodeNotifications":{"desktop":false}},
 "notifications":{"desktop":{"volume":0.25}}, "debug":true
}`

// Persist inert TEST metadata directly. No installation, executable, host or
// provider runs: these checks exercise only the existing transaction boundary.
func rawPolicyFixture(t *testing.T) (Request, Ledger) {
	t.Helper()
	base, err := CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, run := filepath.Join(base, "control"), filepath.Join(base, "runtime")
	write := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	binary := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	binary = filepath.Join(run, binary)
	plugin := filepath.Join(base, "plugins", "agent-notifications.js")
	write(binary, []byte("inert TEST asset never executed"), 0600)
	write(plugin, []byte("inert TEST registration never evaluated"), 0600)
	files := map[string]Identity{}
	for _, p := range []string{binary, plugin} {
		id, err := Fingerprint(p)
		if err != nil {
			t.Fatal(err)
		}
		files[p] = id
	}
	reg := &OpenCodeRegistration{Origin: strings.Repeat("a", 64), Salt: strings.Repeat("b", 64), Namespace: strings.Repeat("c", 64), BundleSHA256: files[plugin].SHA256, OriginBound: true}
	l := Ledger{Schema: 4, WriterFloor: OpenCodeWriterFloor, ID: "TEST-raw-policy", Owner: "existing-installer", RuntimeRoot: run, Generation: 7, PolicyGeneration: 11, Files: files,
		Consumers: map[string]Consumer{openCodeConsumer: {RuntimeRoot: run, Registration: plugin, Commands: []string{binary, "opencode-event", "--protocol", "1"}, OpenCode: reg}, "other-agent": {RuntimeRoot: run, Registration: plugin}}}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "ownership.json"), b, 0600)
	write(filepath.Join(root, "policy-generation.json"), []byte(`{"Generation":11,"Enabled":false}`), 0600)
	write(filepath.Join(root, "agent-notifications.json"), []byte(rawPolicyBefore), 0640)
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		write(filepath.Join(root, name), nil, 0600)
	}
	preimage, err := Fingerprint(filepath.Join(root, "agent-notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := Request{ControlRoot: root, RuntimeRoot: run, Owner: l.Owner, ConsumerID: openCodeConsumer, PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &l.Generation, ExpectedPolicy: &preimage, PolicyDocument: []byte(rawPolicyAfter)}
	var expected Ledger
	_ = json.Unmarshal(b, &expected)
	r.Prepare = func() ([]File, error) {
		observed, pending, err := ReadOwnership(root)
		if err != nil || pending || !reflect.DeepEqual(observed, expected) {
			return nil, fmt.Errorf("stale full ledger")
		}
		return nil, nil
	}
	return r, l
}

func rawPolicyContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type rawTreeEntry struct {
	Mode  os.FileMode
	Bytes string
}

func rawPolicyTree(t *testing.T, root string) map[string]rawTreeEntry {
	t.Helper()
	entries := map[string]rawTreeEntry{}
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		var data []byte
		if !d.IsDir() {
			data, err = os.ReadFile(p)
			if err != nil {
				return err
			}
		}
		entries[p] = rawTreeEntry{info.Mode(), string(data)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return entries
}
func rawPolicyRefusesWithoutEffects(t *testing.T, r Request, want error) {
	t.Helper()
	before := rawPolicyTree(t, filepath.Dir(r.ControlRoot))
	_, err := Commit(rawPolicyContext(t), r)
	if err == nil || (want != nil && !errors.Is(err, want)) {
		t.Fatalf("expected refusal %v, got %v", want, err)
	}
	if !reflect.DeepEqual(before, rawPolicyTree(t, filepath.Dir(r.ControlRoot))) {
		t.Fatal("refused raw edit changed TEST tree")
	}
}

// Catches recovery of an intervening valid journal before policy CAS. The
// journal stages an unrelated file: the rejected edit must leave all of it alone.
func TestRawPolicyPendingJournalIsNotRecovered(t *testing.T) {
	r, l := rawPolicyFixture(t)
	var next Ledger
	b, _ := json.Marshal(l)
	_ = json.Unmarshal(b, &next)
	next.Generation++
	next.PolicyGeneration++
	path := filepath.Join(r.ControlRoot, "other-agent.json")
	f := File{Path: path, Data: []byte("unrelated pending state"), Mode: 0600}
	var err error
	f.Parents, err = pathAnchors(path, true)
	if err != nil {
		t.Fatal(err)
	}
	next.Files[path] = desired(f)
	tx := transaction{Schema: 4, Before: l, After: next, Files: []File{f}, ConfigPaths: []string{filepath.Join(r.ControlRoot, "agent-notifications.json")}}
	if err = writeTransaction(filepath.Join(r.ControlRoot, "transaction.json"), tx); err != nil {
		t.Fatal(err)
	}
	parsed, err := readTransactionFile(filepath.Join(r.ControlRoot, "transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = validateOpenCodeDecision(r.ControlRoot, parsed); err != nil {
		t.Fatal(err)
	}
	rawPolicyRefusesWithoutEffects(t, r, ErrPolicyRecovery)
}

// Catches recreation of either deleted permanent lock after observation.
func TestRawPolicyMissingLocksAreNotRecreated(t *testing.T) {
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		t.Run(name, func(t *testing.T) {
			r, _ := rawPolicyFixture(t)
			if err := os.Remove(filepath.Join(r.ControlRoot, name)); err != nil {
				t.Fatal(err)
			}
			rawPolicyRefusesWithoutEffects(t, r, ErrPolicyConflict)
		})
	}
}

// Catches byte/CAS and same-generation metadata races, unbounded paths/modes,
// protected nested metadata changes, ambiguous JSON and policy budget bypasses.
func TestRawPolicyRejectsStaleOrUnboundedChanges(t *testing.T) {
	for _, kind := range []string{"stale-generation", "stale-policy", "same-generation-ledger", "protected-route", "protected-setup", "large-integer", "foreign", "enabled", "schema", "duplicate", "case-root", "case-nested", "oversize", "depth", "entries", "files", "config-paths", "fields", "intent", "consumer", "prepare-files", "missing-fence", "general-mode", "unknown-consumer", "unbound-origin", "missing-policy", "symlink-policy"} {
		t.Run(kind, func(t *testing.T) {
			r, l := rawPolicyFixture(t)
			switch kind {
			case "stale-generation":
				n := l.Generation - 1
				r.ExpectedGeneration = &n
			case "stale-policy":
				if err := os.WriteFile(filepath.Join(r.ControlRoot, "agent-notifications.json"), []byte(rawPolicyBefore+" "), 0640); err != nil {
					t.Fatal(err)
				}
			case "same-generation-ledger":
				l.Consumers["other-agent"] = Consumer{RuntimeRoot: r.RuntimeRoot, Registration: "changed"}
				if err := writeJSON(filepath.Join(r.ControlRoot, "ownership.json"), l); err != nil {
					t.Fatal(err)
				}
			case "protected-route":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"desktop":false`), []byte(`"desktop":true`), 1)
			case "protected-setup":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"immutable"`), []byte(`"changed"`), 1)
			case "large-integer":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`9007199254740993`), []byte(`9007199254740992`), 1)
			case "foreign":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"foreign"`), []byte(`"renamed"`), 1)
			case "enabled":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"enabled":false`), []byte(`"enabled":true`), 1)
			case "schema":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":2`), 1)
			case "duplicate":
				r.PolicyDocument = []byte(`{"schemaVersion":1,"enabled":false,"enabled":false}`)
			case "case-root":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"enabled":false`), []byte(`"enabled":false,"Enabled":false`), 1)
			case "case-nested":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"volume":0.25`), []byte(`"volume":0.25,"Volume":0.75`), 1)
			case "oversize":
				r.PolicyDocument = append(r.PolicyDocument, bytes.Repeat([]byte(" "), 65536)...)
			case "depth":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"debug":true`), []byte(`"debug":`+strings.Repeat("[", 18)+"0"+strings.Repeat("]", 18)), 1)
			case "entries":
				r.PolicyDocument = bytes.Replace(r.PolicyDocument, []byte(`"debug":true`), []byte(`"debug":[`+strings.Repeat("0,", 1024)+"0]"), 1)
			case "files":
				r.Files = []File{{Path: filepath.Join(r.ControlRoot, "unexpected"), Data: []byte("bad"), Mode: 0600}}
			case "config-paths":
				r.ConfigPaths = []string{filepath.Join(r.ControlRoot, "unexpected")}
			case "fields":
				r.PolicyFields = map[string]json.RawMessage{"rates": json.RawMessage(`{"burst":9}`)}
			case "intent":
				enabled := true
				r.PolicyEnabled = &enabled
			case "consumer":
				r.Consumer = l.Consumers[openCodeConsumer]
			case "prepare-files":
				r.Prepare = func() ([]File, error) {
					return []File{{Path: filepath.Join(r.ControlRoot, "unexpected"), Data: []byte("bad"), Mode: 0600}}, nil
				}
			case "missing-fence":
				r.Prepare = nil
			case "general-mode":
				r.PolicyOnly = false
			case "unknown-consumer":
				r.ConsumerID = "other-agent"
			case "unbound-origin":
				c := l.Consumers[openCodeConsumer]
				c.OpenCode.OriginBound = false
				if err := writeJSON(filepath.Join(r.ControlRoot, "ownership.json"), l); err != nil {
					t.Fatal(err)
				}
			case "missing-policy":
				if err := os.Remove(filepath.Join(r.ControlRoot, "agent-notifications.json")); err != nil {
					t.Fatal(err)
				}
			case "symlink-policy":
				p := filepath.Join(r.ControlRoot, "agent-notifications.json")
				if err := os.Rename(p, p+".foreign"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(p+".foreign", p); err != nil {
					t.Skip(err)
				}
			}
			rawPolicyRefusesWithoutEffects(t, r, ErrPolicyConflict)
		})
	}
}

// Catches a second staged file, new policy ownership, lost raw formatting/mode,
// changed origin/assets/foreign metadata or missing/double counter publication.
func TestRawPolicyPublishesExactDocumentAndOnlyCounters(t *testing.T) {
	r, l := rawPolicyFixture(t)
	before := rawPolicyTree(t, filepath.Dir(r.ControlRoot))
	next, err := Commit(rawPolicyContext(t), r)
	if err != nil {
		t.Fatal(err)
	}
	want := l
	want.Generation++
	want.PolicyGeneration++
	if !reflect.DeepEqual(want, next) {
		t.Fatal("ledger changed outside counters")
	}
	data, err := os.ReadFile(filepath.Join(r.ControlRoot, "agent-notifications.json"))
	if err != nil || !bytes.Equal(data, r.PolicyDocument) {
		t.Fatalf("raw bytes lost: %v", err)
	}
	after := rawPolicyTree(t, filepath.Dir(r.ControlRoot))
	if len(after) != len(before) {
		t.Fatal("unexpected leftover transaction state")
	}
	for p, entry := range before {
		switch filepath.Base(p) {
		case "agent-notifications.json", "ownership.json", "policy-generation.json":
		default:
			if after[p] != entry {
				t.Fatalf("unrelated TEST entry changed: %s", p)
			}
		}
	}
	got, err := Fingerprint(filepath.Join(r.ControlRoot, "agent-notifications.json"))
	if err != nil || got.Mode != r.ExpectedPolicy.Mode {
		t.Fatal("policy mode lost")
	}
	if err = checkPolicyGeneration(r.ControlRoot, next); err != nil {
		t.Fatal(err)
	}
	rawPolicyRefusesWithoutEffects(t, r, ErrPolicyConflict)
}

// Uses the existing fault seam solely at the pure transaction library boundary:
// input buffers are snapshotted and recovery retains the one-file CAS decision.
func TestRawPolicyCopiesInputAndRetainsRecoverableDecision(t *testing.T) {
	r, l := rawPolicyFixture(t)
	original := append([]byte(nil), r.PolicyDocument...)
	fence := r.Prepare
	r.Prepare = func() ([]File, error) { r.PolicyDocument[0] = '!'; return fence() }
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("TEST interrupted publication")
		}
		return nil
	}
	if _, err := Commit(rawPolicyContext(t), r); err == nil || errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("expected uncertain journal: %v", err)
	}
	tx, err := readTransactionFile(filepath.Join(r.ControlRoot, "transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Files) != 1 || tx.Files[0].Path != filepath.Join(r.ControlRoot, "agent-notifications.json") || !bytes.Equal(tx.Files[0].Data, original) || tx.Files[0].Before != *r.ExpectedPolicy {
		t.Fatal("not one immutable kernel-derived CAS file")
	}
	want := l
	want.Generation++
	want.PolicyGeneration++
	if !reflect.DeepEqual(tx.After, want) {
		t.Fatal("journal metadata changed outside counters")
	}
	if err = recoverTransaction(rawPolicyContext(t), r.ControlRoot, l, tx, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(tx.Files[0].Path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("recovery lost exact raw bytes")
	}
	if err = checkPolicyGeneration(r.ControlRoot, want); err != nil {
		t.Fatal(err)
	}
}
