//go:build windows

package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsReplacementOutcomes(t *testing.T) {
	for _, state := range []string{"1175-intact", "1176-missing", "1177-mismatch", "1177-visible", "sharing", "sharing-changed"} {
		t.Run(state, func(t *testing.T) {
			env := storeEnv(t)
			r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
			if e != nil {
				t.Fatal(e)
			}
			m, e := beginMutation(context.Background(), env)
			if e != nil {
				t.Fatal(e)
			}
			defer m.close()
			p := m.parent.(*storeParent)
			name := filepath.Base(r.Selection.Path)
			old, e := currentDocument(m, AssetContext{})
			if e != nil {
				t.Fatal(e)
			}
			replacement := []byte(`{"debug":{"benchmark":true}}`)
			temp, e := p.writeArtifact(name, "tmp", replacement)
			if e != nil {
				t.Fatal(e)
			}
			backup, e := uniqueStoreName(name, "backup")
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			e = p.replaceWithRecovery(context.Background(), temp, name, backup, old.Bytes(), replacement, func() error {
				calls++
				switch state {
				case "1175-intact":
					return windows.Errno(1175)
				case "1176-missing":
					if e := os.Rename(r.Selection.Path, filepath.Join(p.path, backup)); e != nil {
						t.Fatal(e)
					}
					return windows.Errno(1176)
				case "1177-mismatch", "sharing-changed":
					if e := os.WriteFile(r.Selection.Path, []byte(`{}`), 0600); e != nil {
						t.Fatal(e)
					}
					if state == "sharing-changed" {
						return windows.ERROR_SHARING_VIOLATION
					}
					return windows.Errno(1177)
				case "1177-visible":
					if e := os.WriteFile(r.Selection.Path, replacement, 0600); e != nil {
						t.Fatal(e)
					}
					return windows.Errno(1177)
				default:
					return windows.ERROR_SHARING_VIOLATION
				}
			})
			if e == nil {
				t.Fatal("error hidden")
			}
			var ce *Error
			switch state {
			case "1176-missing", "1177-mismatch", "sharing-changed":
				if calls != 1 {
					t.Fatal("retried an uncertain replacement")
				}
				if !errors.As(e, &ce) || ce.Code != ConfigRecoveryRequired {
					t.Fatal(e)
				}
			case "1177-visible":
				if !errors.As(e, &ce) || ce.Code != ConfigCommitUncertain {
					t.Fatal(e)
				}
			case "sharing":
				if calls != 3 {
					t.Fatalf("attempts %d", calls)
				}
			default:
				if !errors.Is(e, windows.Errno(1175)) {
					t.Fatal(e)
				}
			}
			got, e := os.ReadFile(filepath.Join(p.path, temp))
			if e != nil || !bytes.Equal(got, replacement) {
				t.Fatal("uncertain temp removed")
			}
		})
	}
}
func TestWindowsManagedReaderWaits(t *testing.T) {
	env := storeEnv(t)
	r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil {
		t.Fatal(e)
	}
	m, e := beginMutation(context.Background(), env)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := ReadFileSnapshot(r.Selection.Path, MaxDocumentBytes); done <- e }()
	select {
	case e := <-done:
		m.close()
		t.Fatalf("reader bypassed lock: %v", e)
	case <-time.After(50 * time.Millisecond):
	}
	m.close()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not resume")
	}
}

func TestWindowsInitRetriesResolveSharingViolationBeforeGuard(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "automatic"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			env := storeEnv(t)
			if explicit {
				env.Vars[OverrideEnv] = filepath.Join(env.Vars["USERPROFILE"], "explicit.json")
			}
			originalReadDir := env.ReadDir
			calls := 0
			env.ReadDir = func(path string) ([]os.DirEntry, error) {
				calls++
				if calls == 1 {
					// Model os.ReadDir colliding with the transient DELETE handle held
					// by another initializer's MoveFileEx directory publication.
					return nil, windows.ERROR_SHARING_VIOLATION
				}
				if calls == 2 {
					guardPath := filepath.Join(env.Vars["USERPROFILE"], ".agent-notifications-config.lock")
					if _, err := os.Lstat(guardPath); !os.IsNotExist(err) {
						t.Fatalf("selection guard published before Resolve completed: %v", err)
					}
				}
				return originalReadDir(path)
			}
			_, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
			if err != nil {
				t.Fatal(err)
			}
			if calls < 2 {
				t.Fatal("initial sharing violation was not retried")
			}
		})
	}
}

func TestWindowsInvalidOverrideDoesNotPublishGuard(t *testing.T) {
	env := storeEnv(t)
	before, err := os.ReadDir(env.Vars["USERPROFILE"])
	if err != nil {
		t.Fatal(err)
	}
	env.Vars[OverrideEnv] = `relative\config.json`
	_, err = EnsureInitialized(context.Background(), InitRequest{Env: env})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigOverrideInvalid {
		t.Fatalf("invalid override: %v", err)
	}
	after, err := os.ReadDir(env.Vars["USERPROFILE"])
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("invalid override changed home entries: before=%v after=%v", before, after)
	}
	for i := range before {
		if before[i].Name() != after[i].Name() || before[i].Type() != after[i].Type() {
			t.Fatalf("invalid override changed home entries: before=%v after=%v", before, after)
		}
	}
}

func TestWindowsPrivateCreationAndACLReplacement(t *testing.T) {
	env := storeEnv(t)
	r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil {
		t.Fatal(e)
	}
	f, e := winOpen(r.Selection.Path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = checkWindowsACL(f, true); e != nil {
		f.Close()
		t.Fatal(e)
	}
	before, e := windowsDACLEvidence(f)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("DACL before ReplaceFileW: SDDL=%q control=%#x protected=%t ACEs=%x", before.sddl, before.control, before.protected, before.aces)
	// The shared cross-platform Store tests exercise actual ReplaceFileW edits;
	// this assertion captures the DACL independently for the native CI gate.
	m, e := beginMutation(context.Background(), env)
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	old, e := currentDocument(m, AssetContext{})
	if e != nil {
		t.Fatal(e)
	}
	next, e := ParseDocument([]byte(`{"debug":{"benchmark":true}}`), r.Selection.Path, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = commitDocument(context.Background(), env, m, old, next, r); e != nil {
		t.Fatal(e)
	}
	f, e = winOpen(r.Selection.Path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	after, e := windowsDACLEvidence(f)
	if e != nil {
		t.Fatalf("read DACL after ReplaceFileW: %v (before SDDL=%q)", e, before.sddl)
	}
	t.Logf("DACL after ReplaceFileW: SDDL=%q control=%#x protected=%t ACEs=%x", after.sddl, after.control, after.protected, after.aces)
	// SECURITY_DESCRIPTOR.String also renders incidental DACL control flags
	// (for example, auto-inheritance bookkeeping). ReplaceFileW may update those
	// without changing either the effective ACE list or inheritance protection.
	if before.protected != after.protected || !equalWindowsACEs(before.aces, after.aces) {
		t.Fatalf("DACL changed: before SDDL=%q control=%#x protected=%t ACEs=%x; after SDDL=%q control=%#x protected=%t ACEs=%x",
			before.sddl, before.control, before.protected, before.aces, after.sddl, after.control, after.protected, after.aces)
	}
}

type windowsDACLSnapshot struct {
	sddl      string
	control   uint16
	protected bool
	aces      [][]byte
}

func windowsDACLEvidence(f *os.File) (windowsDACLSnapshot, error) {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return windowsDACLSnapshot{}, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return windowsDACLSnapshot{}, err
	}
	if dacl == nil {
		return windowsDACLSnapshot{}, errors.New("null DACL")
	}
	control, _, err := sd.Control()
	if err != nil {
		return windowsDACLSnapshot{}, err
	}
	snapshot := windowsDACLSnapshot{
		sddl:      sd.String(),
		control:   uint16(control),
		protected: control&windows.SE_DACL_PROTECTED != 0,
		aces:      make([][]byte, 0, dacl.AceCount),
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return windowsDACLSnapshot{}, err
		}
		size := int(ace.Header.AceSize)
		if size < int(unsafe.Sizeof(ace.Header)) {
			return windowsDACLSnapshot{}, fmt.Errorf("invalid ACE size %d", size)
		}
		raw := unsafe.Slice((*byte)(unsafe.Pointer(ace)), size)
		snapshot.aces = append(snapshot.aces, append([]byte(nil), raw...))
	}
	return snapshot, nil
}

func equalWindowsACEs(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestWindowsInitialLockAppearanceRereads(t *testing.T) {
	root := t.TempDir()
	p, e := openStoreParent(root, false)
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	old := []byte(`{}`)
	next := []byte(`{"debug":{"benchmark":true}}`)
	temp, e := p.writeArtifact("config.json", "tmp", old)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.publish(context.Background(), temp, "config.json", false, nil, old); e != nil {
		t.Fatal(e)
	}
	if e = p.sync(); e != nil {
		t.Fatal(e)
	}
	calls := 0
	snap, e := readWindowsSnapshot(filepath.Join(root, "config.json"), MaxDocumentBytes, func(path string, limit int) (Snapshot, error) {
		calls++
		current, e := readFileSnapshotUnmanaged(path, limit)
		if e != nil {
			return Snapshot{}, e
		}
		if calls == 1 {
			release, e := p.lock(context.Background(), "config.json.lock", false, true)
			if e != nil {
				t.Fatal(e)
			}
			temp, e := p.writeArtifact("config.json", "tmp", next)
			if e != nil {
				release()
				t.Fatal(e)
			}
			e = p.publish(context.Background(), temp, "config.json", true, old, next)
			if e == nil {
				e = p.sync()
			}
			release()
			if e != nil {
				t.Fatal(e)
			}
		}
		return current, nil
	})
	if e != nil || calls != 2 || !bytes.Equal(snap.Bytes, next) {
		t.Fatalf("initial sidecar not reread: calls=%d err=%v", calls, e)
	}
}
func TestWindowsHandlesNotInherited(t *testing.T) {
	root := t.TempDir()
	p, e := openStoreParent(root, false)
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	f, e := winOpen(filepath.Join(root, "private"), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, true)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	query := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	for _, file := range append(p.ancestors, f) {
		var flags uint32
		ok, _, e := query.Call(file.Fd(), uintptr(unsafe.Pointer(&flags)))
		if ok == 0 || flags&windows.HANDLE_FLAG_INHERIT != 0 {
			t.Fatalf("inheritable/invalid handle: %v", e)
		}
	}
}

// Ordinary inherited read grants are valid for reads and init no-ops, but
// publishing secret-bearing edits still requires a private target.
func TestWindowsBroadACLReadOnlyInit(t *testing.T) {
	env := storeEnv(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	raw := []byte("{}\n")
	if e := os.WriteFile(target, raw, 0600); e != nil {
		t.Fatal(e)
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	setACL := func(path, acl string) {
		t.Helper()
		sd, e := windows.SecurityDescriptorFromString("D:P" + acl)
		if e != nil {
			t.Fatal(e)
		}
		dacl, _, e := sd.DACL()
		if e != nil {
			t.Fatal(e)
		}
		if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); e != nil {
			t.Fatal(e)
		}
	}
	full := "(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)"
	setACL(target, full+"(A;;FR;;;WD)")
	// Retain WRITE_DAC for fixture cleanup, but deny directory creation rights
	// by granting this user only read/traverse and security-descriptor access.
	setACL(dir, "(A;;FRFXWD;;;"+user.User.Sid.String()+")(A;;FA;;;SY)")
	t.Cleanup(func() { setACL(dir, full) })
	env.Vars = map[string]string{OverrideEnv: target}
	before, e := os.Stat(target)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = ReadDocument(ReadRequest{Env: env, ReadSnapshot: ReadFileSnapshot}); e != nil {
		t.Fatal(e)
	}
	r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil || r.Changed {
		t.Fatalf("read-only broad ACL init: %v", e)
	}
	foundPublic := false
	for _, diagnostic := range r.Selection.Diagnostics {
		foundPublic = foundPublic || diagnostic.Code == ConfigPublicReadable
	}
	if !foundPublic {
		t.Fatal("broad read ACL was not diagnosed")
	}
	after, e := os.Stat(target)
	got, readErr := os.ReadFile(target)
	entries, dirErr := os.ReadDir(dir)
	if e != nil || readErr != nil || dirErr != nil || !bytes.Equal(got, raw) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() || len(entries) != 1 {
		t.Fatal("no-op changed file or created artifacts")
	}
	portableEnv := env
	portableEnv.Vars = map[string]string{OverrideEnv: filepath.Join(t.TempDir(), "portable.json"), "USERPROFILE": dir}
	if _, e = EnsureInitialized(context.Background(), InitRequest{Env: portableEnv}); e != nil {
		t.Fatalf("portable read-only HOME: %v", e)
	}
	setACL(dir, full)
	_, e = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
	var ce *Error
	if !errors.As(e, &ce) || ce.Code != ConfigPermissionDenied {
		t.Fatalf("broad ACL edit allowed: %v", e)
	}
	got, e = os.ReadFile(target)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("rejected edit changed bytes")
	}
}

func TestWindowsLongUnicodeCaseAliasAndPinnedParent(t *testing.T) {
	env := storeEnv(t)
	root := t.TempDir()
	target := filepath.Join(root, strings.Repeat("long", 45), "配置 with spaces", "Config.JSON")
	env.Vars = map[string]string{OverrideEnv: target}
	r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil {
		t.Fatal(e)
	}
	env.Vars[OverrideEnv] = strings.ToUpper(target)
	alias, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil || alias.Changed || alias.Revision != r.Revision || alias.Selection.Path != r.Selection.Path {
		t.Fatalf("case alias identity: %+v %v", alias, e)
	}
	m, e := beginMutation(context.Background(), env)
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, e = ApplyEdits(ctx, EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
	var ce *Error
	if !errors.As(e, &ce) || ce.Code != ConfigLockTimeout {
		t.Fatalf("alias bypassed target lock: %v", e)
	}
	dir := filepath.Dir(r.Selection.Path)
	if e = os.Rename(dir, dir+"-moved"); e == nil {
		t.Fatal("pinned parent could be moved")
	}
	if e = m.parent.verify(); e != nil {
		t.Fatal(e)
	}
}

func TestWindowsRejectsLinkedLockAndFinalSymlink(t *testing.T) {
	for _, attack := range []string{"lock-hardlink", "final-symlink"} {
		t.Run(attack, func(t *testing.T) {
			env := storeEnv(t)
			r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
			if e != nil {
				t.Fatal(e)
			}
			if attack == "lock-hardlink" {
				if e = os.Link(r.Selection.Path+".lock", r.Selection.Path+".alias-lock"); e != nil {
					t.Fatal(e)
				}
				_, e = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
			} else {
				alias := filepath.Join(filepath.Dir(r.Selection.Path), "linked.json")
				if e = os.Symlink(r.Selection.Path, alias); e != nil {
					t.Skipf("symlink privilege unavailable: %v", e)
				}
				env.Vars[OverrideEnv] = alias
				_, e = EnsureInitialized(context.Background(), InitRequest{Env: env})
			}
			var ce *Error
			if !errors.As(e, &ce) || ce.Code != ConfigLinkedPath {
				t.Fatalf("link accepted: %v", e)
			}
		})
	}
}

// All subsequent test directories inherit this explicitly established private
// ACL, independent of the native runner's inherited temp-directory defaults.
func prepareTestRoot(path string) error {
	sa, err := privateSecurity(true)
	if err != nil {
		return err
	}
	acl, _, err := sa.SecurityDescriptor.DACL()
	if err != nil {
		return err
	}
	owner, _, err := sa.SecurityDescriptor.Owner()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, acl, nil)
	if err != nil {
		return err
	}
	f, err := winOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, false)
	if err != nil {
		return err
	}
	defer f.Close()
	return checkWindowsACL(f, true)
}

func TestWindowsStoreSourceReadsShareDeadline(t *testing.T) {
	for _, historical := range []bool{false, true} {
		t.Run(fmt.Sprint(historical), func(t *testing.T) {
			env := storeEnv(t)
			sourceDir := t.TempDir()
			source := filepath.Join(sourceDir, "source.json")
			if err := os.WriteFile(source, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			parent, err := openStoreParent(sourceDir, false)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.close()
			release, err := parent.lock(context.Background(), "source.json.lock", false, true)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			request := InitRequest{Env: env, From: source}
			if historical {
				request.From = ""
				request.Legacy = LegacyContext{Candidates: []HistoricalCandidate{{Path: source}}}
			}
			guardParent, err := openStoreParent(env.Vars["USERPROFILE"], false)
			if err != nil {
				t.Fatal(err)
			}
			defer guardParent.close()
			releaseGuard, err := guardParent.lock(context.Background(), ".agent-notifications-config.lock", false, true)
			if err != nil {
				t.Fatal(err)
			}
			guardDone := make(chan struct{})
			go func() { time.Sleep(25 * time.Millisecond); releaseGuard(); close(guardDone) }()
			defer func() { <-guardDone }()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			started := time.Now()
			_, err = EnsureInitialized(ctx, request)
			var ce *Error
			if !errors.As(err, &ce) || ce.Code != ConfigLockTimeout || time.Since(started) > time.Second {
				t.Fatalf("source read escaped operation deadline: %v", err)
			}
			selected, err := Resolve(env)
			if err != nil || selected.Exists {
				t.Fatalf("deadline changed destination: %v", err)
			}
			m, err := beginMutation(context.Background(), env)
			if err != nil {
				t.Fatalf("destination locks not released: %v", err)
			}
			m.close()
		})
	}
}

func TestWindowsImportReusesSelectionGuard(t *testing.T) {
	env := storeEnv(t)
	home := env.Vars["USERPROFILE"]
	source := filepath.Join(home, ".agent-notifications-config")
	raw := []byte("{\n  \"future\": 9007199254740993, \"template\": \"${HOME}\"\n}\n")
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	env.Vars[OverrideEnv] = filepath.Join(home, "imported.json")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := EnsureInitialized(ctx, InitRequest{Env: env, From: source})
	if err != nil || !r.Changed {
		t.Fatalf("import: %+v %v", r, err)
	}
	got, err := os.ReadFile(r.Selection.Path)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("raw import changed: %v", err)
	}
	p, err := openStoreParent(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for _, name := range []string{filepath.Base(source) + ".lock", "imported.json.lock"} {
		release, err := p.lock(ctx, name, false, false)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
}
