//go:build linux || darwin

package portable

import (
	"context"
	"encoding/json"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (Binding, string, installruntime.Request) {
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
	r := installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: "existing", Files: []installruntime.File{{Path: filepath.Join(b.RuntimeRoot, b.Primary), Data: []byte("inert primary"), Mode: 0700}}}
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
				if kind == "ledger-owner" {
					l.Owner = "foreign-owner"
				} else if kind == "decoder-floor" {
					l.DecoderFloor = 999
				} else if kind == "floor" {
					l.WriterFloor = 999
				} else {
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

func testContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}
