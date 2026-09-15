package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func storeEnv(t *testing.T) EnvSnapshot {
	t.Helper()
	root := t.TempDir()
	setTestHome(t, root)
	return SnapshotEnv()
}
func TestStoreInitEditsCAS(t *testing.T) {
	env := storeEnv(t)
	r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed {
		t.Fatal("not created")
	}
	before, err := os.Stat(r.Selection.Path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(r.Selection.Path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if err != nil || second.Changed {
		t.Fatalf("noop %v", err)
	}
	after, _ := os.Stat(r.Selection.Path)
	if before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("no-op metadata changed")
	}
	edit := EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/notifications/desktop/volume": json.RawMessage("0.25")}}}
	changed, err := ApplyEdits(context.Background(), edit)
	if err != nil || !changed.Changed {
		t.Fatalf("edit %v", err)
	}
	backup, err := os.ReadFile(changed.BackupPath)
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatal("backup is not raw original")
	}
	backupsBeforeNoop, _ := filepath.Glob(r.Selection.Path + ".backup-*")
	if len(backupsBeforeNoop) == 0 {
		t.Fatal("missing recovery backup")
	}
	if _, err = ApplyEdits(context.Background(), edit); err == nil {
		t.Fatal("stale CAS succeeded")
	}
	edit.ExpectRevision = changed.Revision
	noop, err := ApplyEdits(context.Background(), edit)
	if err != nil || noop.Changed || noop.BackupPath != "" {
		t.Fatalf("edit noop %v", err)
	}
	entries, _ := filepath.Glob(r.Selection.Path + ".backup-*")
	if len(entries) != len(backupsBeforeNoop) {
		t.Fatal("noop backup")
	}
}
func TestStoreImportLinksAndRecovery(t *testing.T) {
	env := storeEnv(t)
	source := filepath.Join(t.TempDir(), "source.json")
	raw := []byte(" {\"future\":900719925474099312345,\"statuses\":{\"question\":{\"sound\":\"${CLAUDE_PLUGIN_ROOT}/sound\"}}} \n")
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := EnsureInitialized(context.Background(), InitRequest{Env: env, From: source})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(r.Selection.Path)
	if !bytes.Equal(got, raw) {
		t.Fatal("import normalized")
	}
	link := filepath.Join(filepath.Dir(r.Selection.Path), "alias.json")
	if err = os.Link(r.Selection.Path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = EnsureInitialized(context.Background(), InitRequest{Env: env}); err == nil {
		t.Fatal("hardlink accepted")
	}
	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(r.Selection.Path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(r.Selection.Path+".tmp-crash", raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = EnsureInitialized(context.Background(), InitRequest{Env: env})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigRecoveryRequired {
		t.Fatalf("recovery %v", err)
	}
}
func TestStoreConcurrentInit(t *testing.T) {
	rounds := 1
	if SnapshotEnv().GOOS == "windows" {
		rounds = 20
	}
	for _, mode := range []string{"automatic", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			for round := 0; round < rounds; round++ {
				t.Run(fmt.Sprintf("round-%02d", round), func(t *testing.T) {
					testStoreConcurrentInit(t, mode == "explicit")
				})
			}
		})
	}
}

func testStoreConcurrentInit(t *testing.T, explicit bool) {
	env := storeEnv(t)
	if explicit {
		// Keep the explicit target in the shared HOME: its absent-target
		// resolution enumerates the same directory in which automatic writers
		// publish private directories on Windows.
		env.Vars[OverrideEnv] = filepath.Join(env.Vars["USERPROFILE"], "explicit.json")
	}
	start := make(chan struct{})
	errs := make(chan error, 20)
	changed := make(chan bool, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
			errs <- e
			changed <- r.Changed
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(changed)
	for e := range errs {
		if e != nil {
			var ce *Error
			if errors.As(e, &ce) {
				t.Errorf("%v [stage=%s win32=%d]", e, ce.causeStage, ce.causeErrno)
				continue
			}
			t.Errorf("%v", e)
		}
	}
	n := 0
	for c := range changed {
		if c {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d creators", n)
	}
}
func TestRawEditsPreservation(t *testing.T) {
	raw := []byte(`{"future":{"big":900719925474099312345,"null":null},"notifications":{"webhook":{"url":"${SECRET}"}},"statuses":{"question":{"sound":"${CLAUDE_PLUGIN_ROOT}/old","unknown":[1,null]}}}`)
	d, err := ParseDocument(raw, "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	next, err := ApplyRawEdits(d, Edits{Set: map[string]json.RawMessage{"/statuses/question/sound": json.RawMessage(`"${CLAUDE_PLUGIN_ROOT}/new"`)}}, AssetContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(next.Bytes(), []byte("900719925474099312345")) || !bytes.Contains(next.Bytes(), []byte("${SECRET}")) || bytes.Contains(next.Bytes(), []byte("schemaVersion")) {
		t.Fatal("raw data changed")
	}
	for _, p := range []string{"", "/schemaVersion", "/future/big", "/notifications/suppressFilters/0", "/statuses/question"} {
		if _, err = ApplyRawEdits(d, Edits{Set: map[string]json.RawMessage{p: json.RawMessage("null")}}, AssetContext{}); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	noop, err := ApplyRawEdits(d, Edits{Remove: []string{"/debug/benchmark"}}, AssetContext{})
	if err != nil || !bytes.Equal(noop.Bytes(), raw) {
		t.Fatal("remove no-op")
	}
}

func TestStoreNullRemoveAndSafeDiagnostics(t *testing.T) {
	env := storeEnv(t)
	source := filepath.Join(t.TempDir(), "source.json")
	secret := "synthetic-secret-must-not-appear"
	raw := []byte(`{"notifications":{"desktop":{"terminalBell":null},"webhook":{"url":"${SECRET}","headers":{"Token":"` + secret + `"}}},"future":{"` + secret + `":900719925474099312345}}`)
	if e := os.WriteFile(source, raw, 0600); e != nil {
		t.Fatal(e)
	}
	r, e := EnsureInitialized(context.Background(), InitRequest{Env: env, From: source})
	if e != nil {
		t.Fatal(e)
	}
	noop, e := ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/notifications/desktop/terminalBell": json.RawMessage("null")}}})
	if e != nil || noop.Changed {
		t.Fatal("null no-op failed")
	}
	removed, e := ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Remove: []string{"/notifications/desktop/terminalBell"}}})
	if e != nil || !removed.Changed {
		t.Fatal("null removal failed")
	}
	safe, e := json.Marshal(removed)
	if e != nil || bytes.Contains(safe, []byte(secret)) {
		t.Fatal("result leaked raw secret")
	}
	got, e := os.ReadFile(r.Selection.Path)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(got, []byte(secret)) || !bytes.Contains(got, []byte("${SECRET}")) || bytes.Contains(got, []byte("terminalBell")) {
		t.Fatal("raw preservation failed")
	}
	_, e = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: removed.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/notifications/desktop/volume": json.RawMessage(`"` + secret + `"`)}}})
	if e == nil || bytes.Contains([]byte(e.Error()), []byte(secret)) {
		t.Fatal("unsafe error diagnostic")
	}
}

func TestStoreInitNoopDetectsSelectionChange(t *testing.T) {
	env := storeEnv(t)
	r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	home := env.Vars["HOME"]
	if env.GOOS == "windows" {
		home = env.Vars["USERPROFILE"]
	}
	legacy := filepath.Join(home, ".claude", "claude-notifications-go", "config.json")
	if err = os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	original := env.Lstat
	injected := false
	env.Lstat = func(path string) (os.FileInfo, error) {
		info, e := original(path)
		if path == r.Selection.Path && !injected {
			injected = true
			if e := os.WriteFile(legacy, []byte("{}"), 0600); e != nil {
				t.Fatal(e)
			}
		}
		return info, e
	}
	result, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	var ce *Error
	if !injected || result.Changed || !errors.As(err, &ce) || ce.Code != ConfigChanged {
		t.Fatalf("selection change accepted: %v", err)
	}
}

func TestRawEditsOptionalNullObject(t *testing.T) {
	raw := []byte(" {\"statuses\": {\"question\": {\"desktop\": null}}, \"future\":900719925474099312345} \n")
	d, e := ParseDocument(raw, "/fixture", true)
	if e != nil {
		t.Fatal(e)
	}
	noop, e := ApplyRawEdits(d, Edits{Remove: []string{"/statuses/question/desktop/enabled"}}, AssetContext{})
	if e != nil || !bytes.Equal(noop.Bytes(), raw) {
		t.Fatalf("absent descendant remove changed null: %v", e)
	}
	next, e := ApplyRawEdits(d, Edits{Set: map[string]json.RawMessage{"/statuses/question/desktop/enabled": json.RawMessage("false")}}, AssetContext{})
	if e != nil {
		t.Fatal(e)
	}
	c, e := next.Effective(AssetContext{})
	if e != nil || c.Statuses["question"].Desktop.Enabled == nil || *c.Statuses["question"].Desktop.Enabled || !bytes.Contains(next.Bytes(), []byte("900719925474099312345")) {
		t.Fatalf("supported leaf not materialized: %v", e)
	}
}

func TestRawEditsNumericNoopAndOperationContract(t *testing.T) {
	raw := []byte("{\"notifications\":{\"desktop\":{\"volume\":1.0},\"webhook\":{\"payloadFields\":{\"value\":{\"big\":900719925474099312345,\"exp\":1e999999999999999999999}}}}}\n")
	d, e := ParseDocument(raw, "/fixture", true)
	if e != nil {
		t.Fatal(e)
	}
	edits := Edits{Set: map[string]json.RawMessage{
		"/notifications/desktop/volume":              json.RawMessage("1e0"),
		"/notifications/webhook/payloadFields/value": json.RawMessage(`{"exp":10e999999999999999999998,"big":900719925474099312345.0}`),
	}}
	same, e := ApplyRawEdits(d, edits, AssetContext{})
	if e != nil || !bytes.Equal(raw, same.Bytes()) {
		t.Fatalf("equal decimal values rewritten: %v", e)
	}
	for _, edits := range []Edits{
		{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}, Remove: []string{"/debug/benchmark"}},
		{Remove: []string{"/debug/benchmark", "/debug/benchmark"}},
		{Set: map[string]json.RawMessage{"/notifications/webhook/payloadFields/value": json.RawMessage(`{"duplicate":1,"duplicate":2}`)}},
	} {
		if _, e := ApplyRawEdits(d, edits, AssetContext{}); e == nil {
			t.Fatal("invalid operation accepted")
		}
	}
	// Adjacent large integers must remain different even beyond float64 precision.
	if sameJSON([]byte("900719925474099312345"), []byte("900719925474099312346")) {
		t.Fatal("numeric comparison rounded")
	}
}

func TestStoreSelectionGuardIsAlsoTargetLock(t *testing.T) {
	env := storeEnv(t)
	home := env.Vars["HOME"]
	if env.GOOS == "windows" {
		home = env.Vars["USERPROFILE"]
	}
	env.Vars["AGENT_NOTIFICATIONS_CONFIG"] = filepath.Join(home, ".agent-notifications-config")
	r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if err != nil || !r.Changed {
		t.Fatalf("shared lock init: %v", err)
	}
	guardPath := env.Vars["AGENT_NOTIFICATIONS_CONFIG"] + ".lock"
	before, err := os.Stat(guardPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(guardPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("persistent guard replaced")
	}
	env.Vars["AGENT_NOTIFICATIONS_CONFIG"] = guardPath
	_, err = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigUnsafeTarget {
		t.Fatalf("protocol guard accepted: %v", err)
	}
}

func TestStoreRejectsAmbiguousResultBeforeBackup(t *testing.T) {
	env := storeEnv(t)
	r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(r.Selection.Path)
	result, err := ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/notifications/suppressFilters": json.RawMessage(`[{"status":"question","Status":"api_error"}]`)}}})
	if err == nil || result.Changed || result.BackupPath != "" {
		t.Fatalf("ambiguous result published: %v", err)
	}
	after, _ := os.ReadFile(r.Selection.Path)
	backups, _ := filepath.Glob(r.Selection.Path + ".backup-*")
	if !bytes.Equal(before, after) || len(backups) != 0 {
		t.Fatal("rejected edit modified storage")
	}
	_, err = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/notifications/suppressFilters": json.RawMessage(`[{"Status":"question"}]`)}}})
	if err != nil {
		t.Fatalf("unambiguous historical case rejected: %v", err)
	}
}

func TestStoreRejectsProtocolArtifactTargets(t *testing.T) {
	for _, name := range []string{"other.json.lock", "other.json.tmp-0123456789abcdef0123456789abcdef", "other.json.backup-0123456789abcdef0123456789abcdef"} {
		t.Run(name, func(t *testing.T) {
			env := storeEnv(t)
			target := filepath.Join(t.TempDir(), name)
			env.Vars[OverrideEnv] = target
			if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.Stat(target)
			r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
			var ce *Error
			if !errors.As(err, &ce) || ce.Code != ConfigUnsafeTarget || r.Changed {
				t.Fatalf("metadata target accepted: %v", err)
			}
			after, _ := os.Stat(target)
			if !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("metadata touched")
			}
		})
	}
}

func TestRawEditsNullStatusMap(t *testing.T) {
	raw := []byte(`{"statuses":null,"future":null,"debug":{"logLevel":"info"}}`)
	d, err := ParseDocument(raw, "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	noop, err := ApplyRawEdits(d, Edits{Remove: []string{"/statuses/question/title"}}, AssetContext{})
	if err != nil || !bytes.Equal(noop.Bytes(), raw) {
		t.Fatalf("null removal: %v", err)
	}
	next, err := ApplyRawEdits(d, Edits{Set: map[string]json.RawMessage{"/statuses/question/title": json.RawMessage(`"custom"`)}}, AssetContext{})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(next.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if string(result["future"]) != "null" {
		t.Fatal("unrelated null lost")
	}
	c, err := next.Effective(AssetContext{})
	if err != nil || c.Statuses["question"].Title != "custom" {
		t.Fatalf("null map set failed: %v", err)
	}
}

func TestStoreImportIsCreateOnlyAndValidatesSource(t *testing.T) {
	for _, canonical := range []string{"absent", "valid", "invalid"} {
		t.Run(canonical, func(t *testing.T) {
			env := storeEnv(t)
			dir := t.TempDir()
			target := filepath.Join(dir, "selected.json")
			source := filepath.Join(dir, "source.json")
			env.Vars[OverrideEnv] = target
			sourceRaw := []byte(`{"notifications":{"desktop":{"volume":99}}}`)
			if canonical != "absent" {
				raw := []byte("{}\n")
				if canonical == "invalid" {
					raw = []byte("null\n")
				}
				if err := os.WriteFile(target, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(source, sourceRaw, 0600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(target)
			r, err := EnsureInitialized(context.Background(), InitRequest{Env: env, From: source})
			if canonical == "valid" {
				if err != nil || r.Changed {
					t.Fatalf("existing target consulted invalid import source: %v", err)
				}
			} else if err == nil || r.Changed {
				t.Fatalf("invalid source/canonical accepted: %v", err)
			}
			after, readErr := os.ReadFile(target)
			if !bytes.Equal(before, after) || (canonical == "absent" && !os.IsNotExist(readErr)) {
				t.Fatal("create-only validation changed canonical")
			}
			backups, _ := filepath.Glob(target + ".backup-*")
			if len(backups) != 0 {
				t.Fatal("failed/no-op import created backup")
			}
		})
	}
}

func TestStoreDistinctExplicitTargets(t *testing.T) {
	env := storeEnv(t)
	first, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	targets := []string{filepath.Join(first, "first.json"), filepath.Join(second, "second.json")}
	for _, target := range targets {
		env.Vars[OverrideEnv] = target
		r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
		if err != nil || !r.Changed || r.Selection.Path != target {
			t.Fatalf("distinct explicit target: %v", err)
		}
	}
	for _, target := range targets {
		if _, err := ReadFileSnapshot(target, MaxDocumentBytes); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreHeldLockIdentityAndOtherLockDeadline(t *testing.T) {
	env := storeEnv(t)
	m, err := beginMutation(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	p := m.parent.(*storeParent)
	name := filepath.Base(m.selection.Path) + ".lock"
	release, err := p.lock(m.ctx, name, true, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	other, err := p.lock(context.Background(), "other.lock", false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer other()
	ctx, cancel := context.WithTimeout(m.ctx, 40*time.Millisecond)
	defer cancel()
	if release, err = p.lock(ctx, "other.lock", true, false); err == nil {
		release()
		t.Fatal("distinct lock bypassed deadline")
	}
}
