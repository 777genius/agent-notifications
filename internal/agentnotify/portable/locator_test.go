//go:build linux || darwin

package portable

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func fixture(t *testing.T) (Binding, string, installruntime.Request) {
	return fixtureWithPrimaryFile(t, "primary")
}

func fixtureWithPrimaryFile(t *testing.T, file string) (Binding, string, installruntime.Request) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{Version: 1, Integration: Codex, InstallationID: "uap-install", BindingID: "binding", ScopeID: "user", Owner: "existing-installer", ScopeRoot: filepath.Join(root, "scope with spaces"), DataRoot: filepath.Join(root, "shared data"), ControlRoot: filepath.Join(root, "control"), GlobalConfig: filepath.Join(root, "global", "config.json"), RuntimeRoot: filepath.Join(root, "primary runtime"), Primary: "primary"}
	for _, p := range []string{b.ScopeRoot, b.DataRoot, filepath.Dir(b.GlobalConfig)} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte("inert primary")
	if file != "primary" {
		data = []byte(installruntime.WriterProtocolMarker)
	}
	r := installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: "existing", Files: []installruntime.File{{Path: primaryPath(b.RuntimeRoot, file), Data: data, Mode: 0700}}}
	l, err := installruntime.Commit(testContext(t), r)
	if err != nil {
		t.Fatal(err)
	}
	b.ComponentID = l.ID
	key, consumer, raw, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	r.Files = nil
	r.ConsumerID = key
	r.Consumer = consumer
	r.ExpectedGeneration = &l.Generation
	if _, err = installruntime.Commit(testContext(t), r); err != nil {
		t.Fatal(err)
	}
	name, _ := b.Filename()
	if err = os.WriteFile(filepath.Join(b.DataRoot, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return b, name, r
}

func TestPrimaryPathValidation(t *testing.T) {
	b, _, _ := fixture(t)
	for _, primary := range []string{PlatformPrimary(), "primary", "bin/helper-v1"} {
		b.Primary = primary
		if _, _, _, err := b.Registration(); err != nil {
			t.Errorf("valid primary %q rejected: %v", primary, err)
		}
	}
	for _, primary := range []string{"", "/bin/helper", "../helper", "bin/../helper", "bin/./helper", "bin//helper", `bin\helper`, "bin/C:helper"} {
		b.Primary = primary
		if _, _, _, err := b.Registration(); err == nil {
			t.Errorf("unsafe primary %q accepted", primary)
		}
	}
}

func TestLegacyMissingPrimaryResolvesOnlyOwnedPlatformBinary(t *testing.T) {
	b, name, _ := fixtureWithPrimaryFile(t, PlatformPrimary())
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	primary, found, err := InstalledPrimary(snapshot.Ledger, b.InstallationID, b.ControlRoot)
	if err != nil || !found || primary != "primary" {
		t.Fatalf("legacy binding identity lost: %q %v %v", primary, found, err)
	}
	lease, err := Acquire(testContext(t), b.DataRoot, name)
	if err != nil {
		t.Fatal(err)
	}
	if want := primaryPath(b.RuntimeRoot, PlatformPrimary()); lease.Executable != want {
		t.Errorf("legacy primary resolved to %q, want %q", lease.Executable, want)
	}
	lease.Release()
	if err := os.WriteFile(primaryPath(b.RuntimeRoot, "primary"), []byte("unexpected file"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(testContext(t), b.DataRoot, name); err == nil {
		t.Fatal("unexpected legacy primary file was ignored")
	}
}
func TestBindingLeaseAndRevocation(t *testing.T) {
	b, name, r := fixture(t)
	lease, err := Acquire(testContext(t), b.DataRoot, name)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Executable != filepath.Join(b.RuntimeRoot, b.Primary) {
		t.Fatal("wrong runtime")
	}
	// A real kernel mutation cannot pass the live launcher lease.
	ctx, cancel := context.WithTimeout(testContext(t), 20*time.Millisecond)
	r.ExpectedGeneration = nil
	r.RemoveConsumer = true
	if _, err = installruntime.Commit(ctx, r); err == nil {
		t.Fatal("mutation bypassed lease")
	}
	cancel()
	lease.Release()
	if _, err = installruntime.Commit(testContext(t), r); err != nil {
		t.Fatal(err)
	}
	if _, err = Acquire(testContext(t), b.DataRoot, name); err == nil {
		t.Fatal("revoked binding accepted")
	}
}
func TestSharedDataIndependentBindings(t *testing.T) {
	b, name, r := fixture(t)
	other := b
	other.Integration = Claude
	other.BindingID = "claude-binding"
	key, c, raw, _ := other.Registration()
	second, _ := other.Filename()
	if second == name {
		t.Fatal("shared integration slot")
	}
	r.ExpectedGeneration = nil
	r.ConsumerID = key
	r.Consumer = c
	if _, err := installruntime.Commit(testContext(t), r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.DataRoot, second), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{name, second} {
		l, e := Acquire(testContext(t), b.DataRoot, n)
		if e != nil {
			t.Fatal(e)
		}
		l.Release()
	}
	if err := os.Remove(filepath.Join(b.DataRoot, name)); err != nil {
		t.Fatal(err)
	}
	l, e := Acquire(testContext(t), b.DataRoot, second)
	if e != nil {
		t.Fatal(e)
	}
	l.Release()
	if _, e = Acquire(testContext(t), b.DataRoot, name); e == nil {
		t.Fatal("missing locator accepted")
	}
	if _, e = os.Stat(filepath.Join(b.DataRoot, name)); !os.IsNotExist(e) {
		t.Fatal("locator reconstructed")
	}
}
func TestLocatorRefusals(t *testing.T) {
	for _, kind := range []string{"missing", "unknown", "alias", "duplicate", "oversize", "mode", "symlink", "hardlink", "parent-link", "foreign", "owner", "primary", "recovery", "floor", "stale", "scope", "file-owner", "fifo", "ledger-owner", "decoder-floor", "primary-link", "primary-hardlink", "primary-mode", "data-mode", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			b, name, _ := fixture(t)
			path := filepath.Join(b.DataRoot, name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			write := func(data []byte) {
				t.Helper()
				if e := os.WriteFile(path, data, 0600); e != nil {
					t.Fatal(e)
				}
			}
			switch kind {
			case "primary-link":
				err = os.Rename(filepath.Join(b.RuntimeRoot, b.Primary), filepath.Join(b.RuntimeRoot, "real"))
				if err == nil {
					err = os.Symlink("real", filepath.Join(b.RuntimeRoot, b.Primary))
				}
			case "primary-hardlink":
				err = os.Link(filepath.Join(b.RuntimeRoot, b.Primary), filepath.Join(b.RuntimeRoot, "alias"))
			case "primary-mode":
				err = os.Chmod(filepath.Join(b.RuntimeRoot, b.Primary), 0777)
			case "data-mode":
				err = os.Chmod(b.DataRoot, 0755)
			case "trailing":
				write(append(raw, []byte("{}")...))
			case "missing":
				err = os.Remove(path)
			case "unknown":
				write(append([]byte(`{"unknown":1,`), raw[1:]...))
			case "alias":
				write([]byte(strings.Replace(string(raw), `"version"`, `"Version"`, 1)))
			case "duplicate":
				write(append([]byte(`{"version":1,`), raw[1:]...))
			case "oversize":
				write([]byte(strings.Repeat(" ", MaxBytes+1)))
			case "mode":
				err = os.Chmod(path, 0644)
			case "symlink":
				err = os.Rename(path, path+".real")
				if err == nil {
					err = os.Symlink(path+".real", path)
				}
			case "hardlink":
				err = os.Link(path, path+".alias")
			case "parent-link":
				err = os.Rename(b.DataRoot, b.DataRoot+".real")
				if err == nil {
					err = os.Symlink(b.DataRoot+".real", b.DataRoot)
				}
			case "foreign":
				b.BindingID = "foreign"
				_, _, raw, _ = b.Registration()
				write(raw)
			case "owner":
				write([]byte(strings.Replace(string(raw), "existing-installer", "foreign-owner", 1)))
			case "primary":
				err = os.WriteFile(filepath.Join(b.RuntimeRoot, b.Primary), []byte("modified"), 0700)
			case "recovery":
				err = os.WriteFile(filepath.Join(b.ControlRoot, "transaction.json"), []byte("{}"), 0600)
			case "floor", "stale", "ledger-owner", "decoder-floor":
				p := filepath.Join(b.ControlRoot, "ownership.json")
				var l installruntime.Ledger
				data, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal(data, &l); e != nil {
					t.Fatal(e)
				}
				switch kind {
				case "ledger-owner":
					l.Owner = "foreign-owner"
				case "decoder-floor":
					l.DecoderFloor = 999
				case "floor":
					l.WriterFloor = 999
				default:
					l.ID = "another-component"
				}
				data, e = json.Marshal(l)
				if e != nil {
					t.Fatal(e)
				}
				err = os.WriteFile(p, data, 0600)
			case "scope":
				err = os.Remove(b.ScopeRoot)
			case "file-owner":
				if os.Geteuid() != 0 {
					t.Skip("requires synthetic foreign UID")
				}
				err = os.Chown(path, 1, 1)
			case "fifo":
				err = os.Remove(path)
				if err == nil {
					err = makeFIFO(path)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if l, e := Acquire(testContext(t), b.DataRoot, name); e == nil {
				l.Release()
				t.Fatal("unsafe binding accepted")
			}
		})
	}
}
func TestBoundedArgsAndCancellation(t *testing.T) {
	for _, a := range [][]string{nil, {"--locator", "../x"}, {"--locator", "x"}, {"--locator", "agent-notify-" + strings.Repeat("a", 64) + ".json", "--integration", "codex"}} {
		if _, e := ParseArgs(a); e == nil {
			t.Fatal("argv accepted")
		}
	}
	b, name, _ := fixture(t)
	ctx, cancel := context.WithCancel(testContext(t))
	cancel()
	if _, e := Acquire(ctx, b.DataRoot, name); e == nil {
		t.Fatal("cancelled launch")
	}
}

func TestReadLocatorForRecoveryRequiresCanonicalPrivateBytes(t *testing.T) {
	for _, scenario := range []string{"present", "absent", "foreign-bytes", "symlink", "hardlink", "public-mode"} {
		t.Run(scenario, func(t *testing.T) {
			b, name, _ := fixture(t)
			path := filepath.Join(b.DataRoot, name)
			switch scenario {
			case "absent":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "foreign-bytes":
				if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(b.DataRoot, "foreign"), path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(b.DataRoot, "another-link")); err != nil {
					t.Fatal(err)
				}
			case "public-mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			got, found, err := ReadLocatorForRecovery(b.DataRoot, name)
			switch scenario {
			case "present":
				if err != nil || !found || got != b {
					t.Fatalf("valid locator rejected: %+v %v %v", got, found, err)
				}
			case "absent":
				if err != nil || found {
					t.Fatalf("absent locator not distinguished: %v %v", found, err)
				}
			default:
				if err == nil || found {
					t.Fatalf("unsafe locator accepted: %v %v", found, err)
				}
			}
		})
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

// Regression: Cursor cannot produce its recorded key/command while unknown
// integrations must continue to refuse registration.
func TestCursorRegistration(t *testing.T) {
	b, _, _ := fixture(t)
	b.Integration = Cursor
	key, c, raw, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if key != "portable:"+hex.EncodeToString(sum[:]) || c.Registration != string(raw) || c.RuntimeRoot != b.RuntimeRoot || len(c.Commands) != 1 || c.Commands[0] != primaryPath(b.RuntimeRoot, b.Primary) {
		t.Fatal("Cursor registration identity differs")
	}
	decoded, err := decode(raw)
	if err != nil || decoded != b {
		t.Fatal("Cursor binding did not round trip", err)
	}
	b.Integration = Integration("unknown")
	if _, _, _, err := b.Registration(); err != ErrInvalid {
		t.Fatal("unknown integration admitted", err)
	}
}

func publishedCursorFixture(t *testing.T) (Binding, string) {
	t.Helper()
	b, _, _ := fixture(t)
	b.Integration = Cursor
	name, err := Publish(b)
	if err != nil {
		t.Fatal(err)
	}
	return b, filepath.Join(b.DataRoot, name)
}

// Regression: an identity reader takes a setup lease, requires installed
// authority, or writes runtime state instead of only returning published bytes.
func TestReadCursorBindingIdentityOnly(t *testing.T) {
	b, path := publishedCursorFixture(t)
	root := filepath.Dir(b.DataRoot)
	snapshot := func() map[string]string {
		t.Helper()
		state := map[string]string{}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			var raw []byte
			if !entry.IsDir() {
				raw, err = os.ReadFile(path)
				if err != nil {
					return err
				}
			}
			state[path] = fmt.Sprintf("%v %v %x", info.Mode(), info.ModTime(), sha256.Sum256(raw))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	installed, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || b.CheckSnapshot(installed) == nil {
		t.Fatal("TEST Cursor identity unexpectedly has installed authorization", err)
	}
	for _, absentRuntime := range []bool{false, true} {
		if absentRuntime {
			for _, p := range []string{b.ScopeRoot, b.ControlRoot, b.RuntimeRoot, filepath.Dir(b.GlobalConfig)} {
				if err := os.RemoveAll(p); err != nil {
					t.Fatal(err)
				}
			}
		}
		before := snapshot()
		got, err := ReadCursorBinding(path)
		if err != nil || got != b {
			t.Fatalf("published identity differs: %+v %v", got, err)
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("identity read changed files, locks, or runtime state")
		}
	}
}

// Regression: a reader normalizes selectors, accepts a foreign hashed identity
// or directory, ignores Cursor integration/canonical JSON, or bypasses readPrivate.
func TestReadCursorBindingRefusals(t *testing.T) {
	for _, scenario := range []string{"relative", "unclean", "filename", "data-root", "codex", "claude", "copilot", "noncanonical", "duplicate", "missing", "mode", "symlink", "hardlink", "parent-link"} {
		t.Run(scenario, func(t *testing.T) {
			b, path := publishedCursorFixture(t)
			_, _, raw, err := b.Registration()
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "relative":
				cwd, e := os.Getwd()
				if e != nil {
					t.Fatal(e)
				}
				path, err = filepath.Rel(cwd, path)
				if err == nil {
					var selected []byte
					selected, err = os.ReadFile(path)
					if err == nil && string(selected) != string(raw) {
						t.Fatal("relative selector does not reach the published fixture")
					}
				}
			case "unclean":
				path = b.DataRoot + string(os.PathSeparator) + "." + string(os.PathSeparator) + filepath.Base(path)
			case "filename":
				path = filepath.Join(b.DataRoot, "agent-notify-"+strings.Repeat("a", 64)+".json")
				err = os.WriteFile(path, raw, 0600)
			case "data-root":
				other := filepath.Join(filepath.Dir(b.DataRoot), "other data")
				err = os.Mkdir(other, 0700)
				if err == nil {
					path = filepath.Join(other, filepath.Base(path))
					err = os.WriteFile(path, raw, 0600)
				}
			case "codex", "claude", "copilot":
				b.Integration = map[string]Integration{"codex": Codex, "claude": Claude, "copilot": CopilotVSCode}[scenario]
				var name string
				name, err = Publish(b)
				path = filepath.Join(b.DataRoot, name)
			case "noncanonical":
				err = os.WriteFile(path, append(raw, '\n'), 0600)
			case "duplicate":
				err = os.WriteFile(path, append([]byte(`{"version":1,`), raw[1:]...), 0600)
			case "missing":
				err = os.Remove(path)
			case "mode":
				err = os.Chmod(path, 0644)
			case "symlink":
				err = os.Rename(path, path+".real")
				if err == nil {
					err = os.Symlink(path+".real", path)
				}
			case "hardlink":
				err = os.Link(path, path+".alias")
			case "parent-link":
				err = os.Rename(b.DataRoot, b.DataRoot+".real")
				if err == nil {
					err = os.Symlink(b.DataRoot+".real", b.DataRoot)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := ReadCursorBinding(path); err != ErrInvalid || got != (Binding{}) {
				t.Fatalf("%s accepted or leaked identity: %+v %v", scenario, got, err)
			}
		})
	}
}

// Regression: Local borrows Cursor's absolute selector or a path-only locator,
// or loses the historical installed primary. Only the registered opaque ID wins.
func TestReadLocalBindingUsesRegisteredCanonicalLocator(t *testing.T) {
	b, _, request := fixture(t)
	b.Integration = CopilotVSCode
	key, consumer, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	request.ConsumerID, request.Consumer, request.ExpectedGeneration = key, consumer, nil
	if _, err := installruntime.Commit(testContext(t), request); err != nil {
		t.Fatal(err)
	}
	name, err := Publish(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadLocalBinding(b.ControlRoot, b.BindingID)
	if err != nil || got != b {
		t.Fatalf("registered Local: %+v %v", got, err)
	}
	for _, value := range []string{filepath.Join(b.DataRoot, name), "../binding", "..", "binding/child", strings.Repeat("x", 129), "missing"} {
		if _, err := ReadLocalBinding(b.ControlRoot, value); err == nil {
			t.Fatalf("unsafe/unregistered ID %q admitted", value)
		}
	}
	// Even a fully canonical private locator cannot survive consumer revocation.
	request.RemoveConsumer = true
	if _, err := installruntime.Commit(testContext(t), request); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLocalBinding(b.ControlRoot, b.BindingID); err == nil {
		t.Fatal("unregistered private locator admitted")
	}
}

// Regression: map iteration arbitrarily chooses one of two registrations with
// the same Local binding ID, including registrations of different installations.
func TestReadLocalBindingRejectsDuplicateRegistrations(t *testing.T) {
	b, _, request := fixture(t)
	b.Integration = CopilotVSCode
	for index := 0; index < 2; index++ {
		candidate := b
		if index == 1 {
			candidate.InstallationID = "other-installation"
		}
		key, c, _, err := candidate.Registration()
		if err != nil {
			t.Fatal(err)
		}
		request.ConsumerID, request.Consumer, request.ExpectedGeneration = key, c, nil
		if _, err := installruntime.Commit(testContext(t), request); err != nil {
			t.Fatal(err)
		}
		if _, err := Publish(candidate); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ReadLocalBinding(b.ControlRoot, b.BindingID); err == nil {
		t.Fatal("ambiguous Local binding admitted")
	}
}

// Regression: canonical registered identity is weakened to parseable bytes or
// pathname existence, or a changed primary is accepted against stale ledger data.
func TestReadLocalBindingRejectsLocatorAndInstalledIdentityDrift(t *testing.T) {
	for _, change := range []string{"locator", "primary", "symlink"} {
		t.Run(change, func(t *testing.T) {
			b, _, request := fixture(t)
			b.Integration = CopilotVSCode
			key, c, raw, err := b.Registration()
			if err != nil {
				t.Fatal(err)
			}
			request.ConsumerID, request.Consumer, request.ExpectedGeneration = key, c, nil
			if _, err := installruntime.Commit(testContext(t), request); err != nil {
				t.Fatal(err)
			}
			name, err := Publish(b)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadLocalBinding(b.ControlRoot, b.BindingID); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(b.DataRoot, name)
			switch change {
			case "locator":
				if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			case "primary":
				if err := os.WriteFile(filepath.Join(b.RuntimeRoot, b.Primary), []byte("foreign replacement"), 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				original := path + "-original"
				if err := os.Rename(path, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadLocalBinding(b.ControlRoot, b.BindingID); err == nil {
				t.Fatal("changed identity admitted")
			}
		})
	}
}
