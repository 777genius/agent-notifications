//go:build linux || darwin

package clientsetup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

type fixture struct {
	r   Request
	ctx context.Context
}

func put(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func get(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func fresh(t *testing.T, p registration.Provider) fixture {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	runtime := filepath.Join(root, "runtime with spaces")
	control := filepath.Join(root, "control")
	client := filepath.Join(root, "client")
	for _, p := range []string{runtime, control, client} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := filepath.Join(runtime, "server")
	l, e := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: Managed, RuntimeRoot: runtime, ConsumerID: "legacy-hooks", Consumer: installruntime.Consumer{Registration: filepath.Join(client, "hooks.json"), Commands: []string{"legacy hook"}}, Files: []installruntime.File{{Path: cmd, Data: []byte("fixture executable never launched"), Mode: 0700}}})
	if e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(client, "hooks.json"), []byte("legacy hooks remain"))
	return fixture{Request{ControlRoot: control, RuntimeRoot: runtime, Command: cmd, ConfigPath: filepath.Join(client, "config"), Provider: p, Mode: Managed, ExpectedGeneration: l.Generation}, ctx}
}
func (f *fixture) apply(t *testing.T) Result {
	t.Helper()
	x, e := Apply(f.ctx, f.r)
	if e != nil {
		t.Fatal(e)
	}
	f.r.ExpectedGeneration = x.Ledger.Generation
	return x
}
func (f *fixture) updateCommand(t *testing.T) {
	t.Helper()
	cmd := filepath.Join(f.r.RuntimeRoot, "new executable")
	l, e := installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RefreshOnly: true, ExpectedGeneration: &f.r.ExpectedGeneration, Files: []installruntime.File{{Path: cmd, Data: []byte("new fixture executable"), Mode: 0700}}})
	if e != nil {
		t.Fatal(e)
	}
	f.r.Command = cmd
	f.r.ExpectedGeneration = l.Generation
}
func TestLifecycle(t *testing.T) {
	for _, p := range []registration.Provider{registration.Codex, registration.Claude} {
		t.Run(string(p), func(t *testing.T) {
			f := fresh(t, p)
			x := f.apply(t)
			if !x.Changed {
				t.Fatal("create unchanged")
			}
			original := get(t, f.r.ConfigPath)
			state := get(t, statePath(f.r))
			generation := x.Ledger.Generation
			if x = f.apply(t); x.Changed || x.Ledger.Generation != generation || !bytes.Equal(original, get(t, f.r.ConfigPath)) || !bytes.Equal(state, get(t, statePath(f.r))) {
				t.Fatal("no-op changed bytes/generation")
			}
			if p == registration.Codex {
				put(t, f.r.ConfigPath, append(original, []byte("enabled = false\ndisabled_tools = ['agent_notify']\nstartup_timeout_sec = 17\ncustom = 'keep'\n[mcp_servers.agent_notifications.env]\nKEY = 'value'\n")...))
			} else {
				b := bytes.Replace(original, []byte(`"command":`), []byte(`"enabled": false, "disabled_tools": ["agent_notify"], "timeout": 17, "custom": "keep", "env": {"KEY":"value"}, "command":`), 1)
				put(t, f.r.ConfigPath, b)
			}
			if p == registration.Codex {
				put(t, f.r.ConfigPath, append([]byte("title = 'preserved'\n"), append(get(t, f.r.ConfigPath), []byte("[mcp_servers.foreign]\nurl = 'https://example.invalid'\n")...)...))
			} else {
				put(t, f.r.ConfigPath, bytes.Replace(get(t, f.r.ConfigPath), []byte(`"mcpServers": {`), []byte(`"title":"preserved", "mcpServers": {"foreign":{"url":"https://example.invalid"},`), 1))
			}
			restricted := get(t, f.r.ConfigPath)
			if f.apply(t).Changed || !bytes.Equal(restricted, get(t, f.r.ConfigPath)) {
				t.Fatal("restriction no-op")
			}
			f.updateCommand(t)
			if !f.apply(t).Changed {
				t.Fatal("path update unchanged")
			}
			b := get(t, f.r.ConfigPath)
			for _, s := range []string{"enabled", "false", "disabled_tools", "agent_notify", "17", "custom", "keep", "KEY", "value"} {
				if !bytes.Contains(b, []byte(s)) {
					t.Fatalf("lost restriction %s: %s", s, b)
				}
			}
			other := f.r
			other.Provider = registration.Claude
			other.ConfigPath = filepath.Join(filepath.Dir(f.r.ConfigPath), "other.json")
			x, e := Apply(f.ctx, other)
			if e != nil {
				t.Fatal(e)
			}
			f.r.ExpectedGeneration = x.Ledger.Generation
			f.r.Remove = true
			x = f.apply(t)
			if !bytes.Contains(get(t, f.r.ConfigPath), []byte("preserved")) || !bytes.Contains(get(t, f.r.ConfigPath), []byte("https://example.invalid")) {
				t.Fatal("lost unrelated configuration")
			}
			if len(x.Ledger.Consumers) != 2 || x.Ledger.Consumers["legacy-hooks"].Commands[0] != "legacy hook" || x.Ledger.Owner != Managed || x.Ledger.RuntimeRoot != f.r.RuntimeRoot {
				t.Fatal("lost existing consumers/owner")
			}
			if _, e := os.Stat(statePath(f.r)); !os.IsNotExist(e) {
				t.Fatal("state remains", e)
			}
			if string(get(t, filepath.Join(filepath.Dir(f.r.ConfigPath), "hooks.json"))) != "legacy hooks remain" {
				t.Fatal("hooks changed")
			}
			if !bytes.Contains(get(t, other.ConfigPath), []byte(registration.Server)) {
				t.Fatal("other client removed")
			}
			f.apply(t)
		})
	}
}
func TestConflictsAndInvalidInputs(t *testing.T) {
	for _, provider := range []registration.Provider{registration.Codex, registration.Claude} {
		for _, name := range []string{"unowned", "modified", "missing-state", "bad-state", "generation", "mode", "command", "runtime", "malformed", "oversized", "symlink", "missing-runtime", "empty"} {
			t.Run(string(provider)+"/"+name, func(t *testing.T) {
				f := fresh(t, provider)
				switch name {
				case "unowned":
					b, e := registration.Apply(registration.Request{Provider: f.r.Provider, Command: f.r.Command, Args: args(f.r.Provider)})
					if e != nil {
						t.Fatal(e)
					}
					put(t, f.r.ConfigPath, b.Bytes)
				case "modified":
					f.apply(t)
					put(t, f.r.ConfigPath, bytes.ReplaceAll(get(t, f.r.ConfigPath), []byte("mcp-server"), []byte("different")))
				case "missing-state":
					f.apply(t)
					if e := os.Remove(statePath(f.r)); e != nil {
						t.Fatal(e)
					}
				case "bad-state":
					f.apply(t)
					put(t, statePath(f.r), []byte(`{"Schema":99}`))
				case "generation":
					f.r.ExpectedGeneration++
				case "mode":
					f.r.Mode = "portable"
				case "command":
					f.r.Command = filepath.Join(f.r.RuntimeRoot, "unmanaged")
				case "runtime":
					f.r.RuntimeRoot = filepath.Dir(f.r.RuntimeRoot)
				case "malformed":
					put(t, f.r.ConfigPath, []byte("[bad"))
				case "oversized":
					put(t, f.r.ConfigPath, bytes.Repeat([]byte("x"), registration.MaxBytes+1))
				case "symlink":
					target := filepath.Join(filepath.Dir(f.r.ConfigPath), "foreign")
					put(t, target, []byte("foreign"))
					if e := os.Symlink(target, f.r.ConfigPath); e != nil {
						t.Fatal(e)
					}
				case "missing-runtime":
					f.r.ControlRoot = filepath.Join(filepath.Dir(f.r.ControlRoot), "absent")
				case "empty":
					put(t, f.r.ConfigPath, nil)
				}
				before := tree(t, filepath.Dir(f.r.ControlRoot))
				if _, e := Apply(f.ctx, f.r); e == nil {
					t.Fatal("accepted invalid input")
				}
				if !reflect.DeepEqual(before, tree(t, filepath.Dir(f.r.ControlRoot))) {
					t.Fatal("invalid input mutated files")
				}
			})
		}
	}
}
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			s, e := os.Readlink(p)
			m[p] = "link:" + s
			return e
		}
		b, e := os.ReadFile(p)
		m[p] = string(b)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func recoverKernel(f fixture, rollback bool) (installruntime.Ledger, error) {
	return installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RefreshOnly: true, RollbackPending: rollback, Prepare: func() ([]installruntime.File, error) { return nil, unchanged }})
}
func TestRecovery(t *testing.T) {
	for _, boundary := range []string{"transaction", "config", "state", "ledger"} {
		for _, rollback := range []bool{false, true} {
			t.Run(boundary+map[bool]string{true: "-rollback", false: "-redo"}[rollback], func(t *testing.T) {
				f := fresh(t, registration.Claude)
				stop := boundary
				if boundary == "config" {
					stop = "promotion:" + f.r.ConfigPath
				}
				if boundary == "state" {
					stop = "promotion:" + statePath(f.r)
				}
				if _, e := apply(f.ctx, f.r, func(s string) error {
					if s == stop {
						return errors.New("interrupted")
					}
					return nil
				}); e == nil {
					t.Fatal("fault not reached")
				}
				if _, e := Apply(f.ctx, f.r); !errors.Is(e, ErrRecovery) {
					t.Fatal("did not require recovery", e)
				}
				_, e := recoverKernel(f, rollback)
				if e != nil && !errors.Is(e, unchanged) {
					t.Fatal(e)
				}
				s, e := installruntime.ReadInstalledSnapshot(f.r.ControlRoot)
				if e != nil || s.Recovery {
					t.Fatal(s, e)
				}
				_, installed := s.Ledger.Consumers[consumerID(f.r.Provider, f.r.ConfigPath)]
				if installed == rollback {
					t.Fatal("consumer recovery mismatch")
				}
				_, e = os.Stat(statePath(f.r))
				if rollback && !os.IsNotExist(e) || !rollback && e != nil {
					t.Fatal("state recovery mismatch", e)
				}
				f.r.ExpectedGeneration = s.Ledger.Generation
				f.apply(t)
			})
		}
	}
}
func TestForeignEditsSurviveRecovery(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{true: "rollback", false: "redo"}[rollback], func(t *testing.T) {
			f := fresh(t, registration.Codex)
			_, e := apply(f.ctx, f.r, func(s string) error {
				if s == "promotion:"+f.r.ConfigPath {
					return errors.New("crash")
				}
				return nil
			})
			if e == nil {
				t.Fatal("expected fault")
			}
			foreign := append(get(t, f.r.ConfigPath), []byte("# later foreign edit\n")...)
			put(t, f.r.ConfigPath, foreign)
			before := tree(t, filepath.Dir(f.r.ControlRoot))
			if _, e = recoverKernel(f, rollback); e == nil {
				t.Fatal("accepted foreign edit")
			}
			if !bytes.Equal(foreign, get(t, f.r.ConfigPath)) {
				t.Fatal("overwrote foreign edit")
			}
			// Rollback may replace only the recovery marker before its CAS preflight.
			after := tree(t, filepath.Dir(f.r.ControlRoot))
			delete(before, filepath.Join(f.r.ControlRoot, "transaction.json"))
			delete(after, filepath.Join(f.r.ControlRoot, "transaction.json"))
			if !reflect.DeepEqual(before, after) {
				t.Fatal("recovery changed live files")
			}
		})
	}
}
func TestConcurrentForeignEdit(t *testing.T) {
	f := fresh(t, registration.Claude)
	foreign := []byte(`{"foreign":true}`)
	_, e := apply(f.ctx, f.r, func(s string) error {
		if s == "transaction" {
			put(t, f.r.ConfigPath, foreign)
		}
		return nil
	})
	if e == nil || !bytes.Equal(foreign, get(t, f.r.ConfigPath)) {
		t.Fatal("concurrent foreign edit lost", e)
	}
}
func TestStateStrictSchemaAndIdentity(t *testing.T) {
	for _, change := range []string{"schema", "unknown", "duplicate", "identity", "args", "oversize", "case-alias", "missing-field", "mode"} {
		t.Run(change, func(t *testing.T) {
			f := fresh(t, registration.Codex)
			f.apply(t)
			path := statePath(f.r)
			b := get(t, path)
			switch change {
			case "schema":
				b = bytes.Replace(b, []byte(`"Schema":1`), []byte(`"Schema":2`), 1)
			case "unknown":
				b = append([]byte(`{"Unknown":1,`), b[1:]...)
			case "duplicate":
				b = append([]byte(`{"Schema":1,`), b[1:]...)
			case "identity":
				b = bytes.Replace(b, []byte(`"Mode":"existing-installer"`), []byte(`"Mode":"portable"`), 1)
			case "args":
				b = bytes.Replace(b, []byte("mcp-server"), []byte("other-call"), 1)
			case "case-alias":
				b = append([]byte(`{"schema":1,`), b[1:]...)
			case "missing-field":
				b = bytes.Replace(b, []byte(`,"TypePresent":false`), nil, 1)
			case "mode":
			// Tested below with a valid schema but non-private state mode.
			case "oversize":
				b = []byte(strings.Repeat(" ", maxState+1))
			}
			// Use real kernel CAS to make the invalid state ledger-owned, so schema
			// validation is exercised independently of fingerprint tamper detection.
			before, e := installruntime.Fingerprint(path)
			if e != nil {
				t.Fatal(e)
			}
			l, e := installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RefreshOnly: true, ExpectedGeneration: &f.r.ExpectedGeneration, Files: []installruntime.File{{Path: path, Before: before, Data: b, Mode: map[bool]uint32{true: 0644, false: 0600}[change == "mode"]}}})
			if e != nil {
				t.Fatal(e)
			}
			f.r.ExpectedGeneration = l.Generation
			original := get(t, f.r.ConfigPath)
			if _, e := Apply(f.ctx, f.r); e == nil {
				t.Fatal("accepted invalid ownership")
			}
			if !bytes.Equal(original, get(t, f.r.ConfigPath)) {
				t.Fatal("config changed")
			}
		})
	}
}
func TestNonrootReadDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires nonroot; root is not permission qualification")
	}
	f := fresh(t, registration.Codex)
	put(t, f.r.ConfigPath, []byte("# private\n"))
	if e := os.Chmod(f.r.ConfigPath, 0000); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(f.r.ConfigPath, 0600)
	if _, e := Apply(f.ctx, f.r); e == nil {
		t.Fatal("read denied ignored")
	}
}

func TestReinstallMissingConfig(t *testing.T) {
	f := fresh(t, registration.Claude)
	f.apply(t)
	if e := os.Remove(f.r.ConfigPath); e != nil {
		t.Fatal(e)
	}
	if !f.apply(t).Changed {
		t.Fatal("missing config not repaired from durable identity")
	}
}

func TestNativePolicyAndFinalRemoval(t *testing.T) {
	f := fresh(t, registration.Codex)
	source := filepath.Join(filepath.Dir(f.r.ControlRoot), "inert-native")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(source, "fixture.txt"), []byte("retained data; no executable or probe"))
	native, e := installruntime.StageRetainedNative(f.ctx, f.r.ControlRoot, source)
	if e != nil {
		t.Fatal(e)
	}
	enabled := true
	l, e := installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RefreshOnly: true, ExpectedGeneration: &f.r.ExpectedGeneration, Native: native, PolicyEnabled: &enabled})
	if e != nil {
		t.Fatal(e)
	}
	f.r.ExpectedGeneration = l.Generation
	beforeNative := l.Native
	x := f.apply(t)
	if !x.Ledger.Enabled || !reflect.DeepEqual(beforeNative, x.Ledger.Native) || x.Ledger.DecoderFloor != l.DecoderFloor {
		t.Fatal("registration changed eligibility/native")
	}
	f.r.Remove = true
	x = f.apply(t)
	if !x.Ledger.Enabled || !reflect.DeepEqual(beforeNative, x.Ledger.Native) {
		t.Fatal("independent removal changed eligibility/native")
	}
	// Restore MCP, then let the kernel remove the hook consumer. The MCP removal
	// becomes final; kernel policy is revoked and native remains retained.
	f.r.Remove = false
	f.apply(t)
	l, e = installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RemoveConsumer: true, ExpectedGeneration: &f.r.ExpectedGeneration})
	if e != nil {
		t.Fatal(e)
	}
	f.r.ExpectedGeneration = l.Generation
	f.r.Remove = true
	x = f.apply(t)
	if x.Ledger.Enabled || len(x.Ledger.Consumers) != 0 || !reflect.DeepEqual(beforeNative, x.Ledger.Native) {
		t.Fatal("final cleanup policy/native")
	}
	if _, e = os.Stat(native.After.Path); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(statePath(f.r)); !os.IsNotExist(e) {
		t.Fatal("final state not removed", e)
	}
	if _, e = os.Stat(f.r.ConfigPath); e != nil {
		t.Fatal("client config deleted", e)
	}
}

func TestOwnedStableAlias(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{true: "wrong-runtime", false: "managed"}[foreign], func(t *testing.T) {
			f := fresh(t, registration.Claude)
			alias := filepath.Join(f.r.RuntimeRoot, "stable alias")
			target := f.r.Command
			if foreign {
				target = filepath.Join(filepath.Dir(f.r.RuntimeRoot), "foreign executable")
				put(t, target, []byte("foreign"))
				if e := os.Chmod(target, 0700); e != nil {
					t.Fatal(e)
				}
			}
			l, e := installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RefreshOnly: true, ExpectedGeneration: &f.r.ExpectedGeneration, Files: []installruntime.File{{Path: alias, Link: target, Mode: 0700}}})
			if e != nil {
				t.Fatal(e)
			}
			f.r.ExpectedGeneration = l.Generation
			f.r.Command = alias
			if _, e := Apply(f.ctx, f.r); (e != nil) != foreign {
				t.Fatal("alias trust mismatch", e)
			}
		})
	}
}

func TestRemovalRecovery(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{true: "rollback", false: "redo"}[rollback], func(t *testing.T) {
			f := fresh(t, registration.Claude)
			f.apply(t)
			original := get(t, f.r.ConfigPath)
			originalState := get(t, statePath(f.r))
			f.r.Remove = true
			if _, e := apply(f.ctx, f.r, func(s string) error {
				if s == "promotion:"+statePath(f.r) {
					return errors.New("interrupted removal")
				}
				return nil
			}); e == nil {
				t.Fatal("missing removal fault")
			}
			if _, e := recoverKernel(f, rollback); e != nil && !errors.Is(e, unchanged) {
				t.Fatal(e)
			}
			s, e := installruntime.ReadInstalledSnapshot(f.r.ControlRoot)
			if e != nil {
				t.Fatal(e)
			}
			_, installed := s.Ledger.Consumers[consumerID(f.r.Provider, f.r.ConfigPath)]
			if installed != rollback {
				t.Fatal("removal recovery consumer")
			}
			if rollback && (!bytes.Equal(original, get(t, f.r.ConfigPath)) || !bytes.Equal(originalState, get(t, statePath(f.r)))) {
				t.Fatal("removal rollback lost identity")
			}
			if len(s.Ledger.Consumers["legacy-hooks"].Commands) != 1 {
				t.Fatal("hooks removed")
			}
		})
	}
}

func TestConfigParentSymlinkAndWriteDenial(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{true: "write-denied", false: "parent-symlink"}[denied], func(t *testing.T) {
			f := fresh(t, registration.Claude)
			parent := filepath.Dir(f.r.ConfigPath)
			if denied {
				if os.Geteuid() == 0 {
					t.Skip("requires nonroot")
				}
				if e := os.Chmod(parent, 0500); e != nil {
					t.Fatal(e)
				}
				defer os.Chmod(parent, 0700)
			} else {
				alias := filepath.Join(filepath.Dir(parent), "client-alias")
				if e := os.Symlink(parent, alias); e != nil {
					t.Fatal(e)
				}
				f.r.ConfigPath = filepath.Join(alias, "config")
			}
			if _, e := Apply(f.ctx, f.r); e == nil {
				t.Fatal("unsafe parent accepted")
			}
			if _, e := os.Stat(filepath.Join(parent, "config")); !os.IsNotExist(e) {
				t.Fatal("config created", e)
			}
		})
	}
}

func TestConcurrentRemovalStillFencesGeneration(t *testing.T) {
	f := fresh(t, registration.Codex)
	f.apply(t)
	f.r.Remove = true
	var after map[string]string
	_, e := apply(f.ctx, f.r, func(boundary string) error {
		if boundary == "before-commit" {
			// A second installer removes this consumer between our read-only preflight
			// and kernel lock acquisition. The kernel's absent-removal fast path skips
			// Prepare and its own generation check; the adapter must reject stale success.
			if _, e := Apply(f.ctx, f.r); e != nil {
				t.Fatal(e)
			}
			after = tree(t, filepath.Dir(f.r.ControlRoot))
		}
		return nil
	})
	if !errors.Is(e, ErrConflict) {
		t.Fatal("stale absent-removal accepted", e)
	}
	if !reflect.DeepEqual(after, tree(t, filepath.Dir(f.r.ControlRoot))) {
		t.Fatal("stale removal mutated state")
	}
}
