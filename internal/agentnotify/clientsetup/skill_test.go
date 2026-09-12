//go:build linux || darwin

package clientsetup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func skillFixture(t *testing.T) fixture {
	t.Helper()
	f := fresh(t, registration.Codex)
	source := filepath.Join(f.r.RuntimeRoot, "skills", "agent-notify", "SKILL.md")
	destination := filepath.Join(filepath.Dir(f.r.ConfigPath), "skills", "agent-notify", "SKILL.md")
	for _, p := range []string{source, destination} {
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
	}
	f.r.SkillProjection = &SkillProjection{source, destination}
	f.updateSkill(t, []byte("canonical managed skill v1\n"))
	return f
}
func (f *fixture) updateSkill(t *testing.T, b []byte) {
	t.Helper()
	path := f.r.SkillProjection.SourcePath
	before, e := installruntime.Fingerprint(path)
	if e != nil {
		t.Fatal(e)
	}
	l, e := installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RefreshOnly: true, ExpectedGeneration: &f.r.ExpectedGeneration, Files: []installruntime.File{{Path: path, Before: before, Data: b, Mode: 0600}}})
	if e != nil {
		t.Fatal(e)
	}
	f.r.ExpectedGeneration = l.Generation
}
func absent(t *testing.T, p string) {
	t.Helper()
	if _, e := os.Lstat(p); !os.IsNotExist(e) {
		t.Fatalf("expected absent %s: %v", p, e)
	}
}
func skillConflict(t *testing.T, f fixture) {
	t.Helper()
	before := tree(t, filepath.Dir(f.r.ControlRoot))
	if _, e := Apply(f.ctx, f.r); e == nil {
		t.Fatal("accepted conflict")
	}
	if !reflect.DeepEqual(before, tree(t, filepath.Dir(f.r.ControlRoot))) {
		t.Fatal("conflict mutated files")
	}
}
func TestSkillLifecycle(t *testing.T) {
	f := skillFixture(t)
	selected := *f.r.SkillProjection
	// Exact schema 1 without skill is retained until explicit projection.
	f.r.SkillProjection = nil
	f.apply(t)
	oldState := get(t, statePath(f.r))
	if !bytes.Contains(oldState, []byte(`"Schema":1`)) || bytes.Contains(oldState, []byte(`"Skill"`)) {
		t.Fatal("legacy schema changed")
	}
	f.r.SkillProjection = &selected
	x := f.apply(t)
	if !x.Changed || !bytes.Equal(get(t, selected.SourcePath), get(t, selected.DestinationPath)) {
		t.Fatal("initial projection")
	}
	if _, tracked := x.Ledger.Files[selected.DestinationPath]; tracked {
		t.Fatal("external skill in generic ledger")
	}
	state := get(t, statePath(f.r))
	if !bytes.Contains(state, []byte(`"Schema":2`)) {
		t.Fatal("missing version fence")
	}
	info, e := os.Stat(selected.DestinationPath)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private mode", e)
	}
	generation := f.r.ExpectedGeneration
	if f.apply(t).Changed || f.r.ExpectedGeneration != generation || !bytes.Equal(state, get(t, statePath(f.r))) {
		t.Fatal("no-op changed")
	}
	f.updateSkill(t, []byte("canonical managed skill v2\n"))
	f.r.SkillProjection = nil
	generation = f.r.ExpectedGeneration
	if f.apply(t).Changed || f.r.ExpectedGeneration != generation || !bytes.Equal(state, get(t, statePath(f.r))) || string(get(t, selected.DestinationPath)) != "canonical managed skill v1\n" {
		t.Fatal("omission did not preserve")
	}
	f.r.SkillProjection = &selected
	if !f.apply(t).Changed || !bytes.Equal(get(t, selected.SourcePath), get(t, selected.DestinationPath)) {
		t.Fatal("refresh failed")
	}
	other := f.r
	other.Provider = registration.Claude
	other.ConfigPath = filepath.Join(filepath.Dir(f.r.ConfigPath), "other.json")
	other.SkillProjection = nil
	otherResult, e := Apply(f.ctx, other)
	if e != nil {
		t.Fatal(e)
	}
	f.r.ExpectedGeneration = otherResult.Ledger.Generation
	otherState := get(t, statePath(other))
	otherConfig := get(t, other.ConfigPath)
	f.r.Remove = true
	f.r.SkillProjection = nil
	x = f.apply(t)
	absent(t, selected.DestinationPath)
	absent(t, statePath(f.r))
	if len(x.Ledger.Consumers) != 2 || !bytes.Equal(otherState, get(t, statePath(other))) || !bytes.Equal(otherConfig, get(t, other.ConfigPath)) || string(get(t, filepath.Join(filepath.Dir(f.r.ConfigPath), "hooks.json"))) != "legacy hooks remain" {
		t.Fatal("other consumer changed")
	}
}
func TestSkillConflicts(t *testing.T) {
	for _, name := range []string{"foreign-same", "foreign-different", "tampered-source", "untracked-source", "oversized-source", "source-link", "source-parent-link", "destination-link", "destination-parent-link", "runtime-destination", "control-destination", "config-destination", "relative", "unclean", "wrong-name", "claude", "source-cache"} {
		t.Run(name, func(t *testing.T) {
			f := skillFixture(t)
			s := f.r.SkillProjection
			switch name {
			case "foreign-same":
				put(t, s.DestinationPath, get(t, s.SourcePath))
			case "foreign-different":
				put(t, s.DestinationPath, []byte("foreign"))
			case "tampered-source":
				put(t, s.SourcePath, []byte("tampered"))
			case "untracked-source":
				s.SourcePath = filepath.Join(f.r.RuntimeRoot, "untracked")
				put(t, s.SourcePath, []byte("untracked"))
			case "oversized-source":
				f.updateSkill(t, bytes.Repeat([]byte("x"), maxSkill+1))
			case "source-link":
				if e := os.Remove(s.SourcePath); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(f.r.Command, s.SourcePath); e != nil {
					t.Fatal(e)
				}
			case "source-parent-link", "destination-parent-link":
				p := filepath.Dir(s.SourcePath)
				if name == "destination-parent-link" {
					p = filepath.Dir(s.DestinationPath)
				}
				if e := os.Rename(p, p+"-real"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(p+"-real", p); e != nil {
					t.Fatal(e)
				}
			case "destination-link":
				if e := os.Symlink(s.SourcePath, s.DestinationPath); e != nil {
					t.Fatal(e)
				}
			case "runtime-destination":
				s.DestinationPath = s.SourcePath
			case "control-destination":
				s.DestinationPath = filepath.Join(f.r.ControlRoot, "agent-notify", "SKILL.md")
			case "config-destination":
				f.r.ConfigPath = s.DestinationPath
			case "relative":
				s.DestinationPath = "agent-notify/SKILL.md"
			case "unclean":
				s.DestinationPath = filepath.Dir(s.DestinationPath) + "/../agent-notify/SKILL.md"
			case "wrong-name":
				s.DestinationPath = filepath.Join(filepath.Dir(s.DestinationPath), "OTHER.md")
			case "claude":
				f.r.Provider = registration.Claude
			case "source-cache":
				s.SourcePath = filepath.Join(filepath.Dir(f.r.ConfigPath), "cache.md")
				put(t, s.SourcePath, []byte("cache"))
			}
			skillConflict(t, f)
		})
	}
}

func TestSkillCreatesMissingParents(t *testing.T) {
	f := skillFixture(t)
	destination := filepath.Join(filepath.Dir(f.r.ConfigPath), "absent", "agent-notify", "SKILL.md")
	f.r.SkillProjection.DestinationPath = destination
	x := f.apply(t)
	if !x.Changed || !bytes.Equal(get(t, f.r.SkillProjection.SourcePath), get(t, destination)) {
		t.Fatal("missing parents were not created")
	}
}

func TestSkillForeignReplacement(t *testing.T) {
	for _, action := range []string{"refresh", "remove", "omitted", "relocate"} {
		for _, change := range []string{"bytes", "missing", "mode", "symlink"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				f := skillFixture(t)
				f.apply(t)
				destination := f.r.SkillProjection.DestinationPath
				switch change {
				case "bytes":
					put(t, destination, []byte("foreign replacement"))
				case "missing":
					if e := os.Remove(destination); e != nil {
						t.Fatal(e)
					}
				case "mode":
					if e := os.Chmod(destination, 0644); e != nil {
						t.Fatal(e)
					}
				case "symlink":
					if e := os.Remove(destination); e != nil {
						t.Fatal(e)
					}
					if e := os.Symlink(f.r.SkillProjection.SourcePath, destination); e != nil {
						t.Fatal(e)
					}
				}
				switch action {
				case "remove":
					f.r.Remove = true
				case "omitted":
					f.r.SkillProjection = nil
				case "relocate":
					f.r.SkillProjection.DestinationPath = relocation(t, f)
				}
				skillConflict(t, f)
			})
		}
	}
}
func relocation(t *testing.T, f fixture) string {
	t.Helper()
	p := filepath.Join(filepath.Dir(f.r.ConfigPath), "other-skills", "agent-notify", "SKILL.md")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestSkillRelocation(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "foreign-new"}[conflict], func(t *testing.T) {
			f := skillFixture(t)
			f.apply(t)
			old := f.r.SkillProjection.DestinationPath
			next := relocation(t, f)
			f.r.SkillProjection.DestinationPath = next
			if conflict {
				put(t, next, get(t, old))
				skillConflict(t, f)
				return
			}
			if !f.apply(t).Changed {
				t.Fatal("relocation unchanged")
			}
			absent(t, old)
			if !bytes.Equal(get(t, next), get(t, f.r.SkillProjection.SourcePath)) {
				t.Fatal("relocation bytes")
			}
			f.r.SkillProjection = nil
			if f.apply(t).Changed {
				t.Fatal("relocation omission changed")
			}
			f.r.Remove = true
			f.apply(t)
			absent(t, next)
		})
	}
}
func TestSkillRecovery(t *testing.T) {
	for _, action := range []string{"create", "refresh", "remove", "relocate"} {
		for _, boundary := range []string{"config", "skill"} {
			for _, rollback := range []bool{false, true} {
				t.Run(action+"/"+boundary+map[bool]string{true: "/rollback", false: "/redo"}[rollback], func(t *testing.T) {
					f := skillFixture(t)
					original := f.r.SkillProjection.DestinationPath
					if action != "create" {
						f.apply(t)
					}
					before := tree(t, filepath.Dir(f.r.ControlRoot))
					switch action {
					case "refresh":
						f.updateSkill(t, []byte("refreshed"))
						f.updateCommand(t)
					case "remove":
						f.r.Remove = true
					case "relocate":
						f.r.SkillProjection.DestinationPath = relocation(t, f)
						f.updateCommand(t)
					}
					stop := f.r.ConfigPath
					if boundary == "skill" {
						stop = f.r.SkillProjection.DestinationPath
					}
					if _, e := apply(f.ctx, f.r, func(s string) error {
						if s == "promotion:"+stop {
							return errors.New("crash")
						}
						return nil
					}); e == nil {
						t.Fatal("fault missed")
					}
					if _, e := Apply(f.ctx, f.r); !errors.Is(e, ErrRecovery) {
						t.Fatal("recovery not required", e)
					}
					if _, e := recoverKernel(f, rollback); e != nil && !errors.Is(e, unchanged) {
						t.Fatal(e)
					}
					snapshot, e := installruntime.ReadInstalledSnapshot(f.r.ControlRoot)
					if e != nil || snapshot.Recovery {
						t.Fatal(e)
					}
					f.r.ExpectedGeneration = snapshot.Ledger.Generation
					if rollback {
						if action == "create" {
							absent(t, original)
							absent(t, statePath(f.r))
						} else {
							if string(get(t, original)) != before[original] || string(get(t, statePath(f.r))) != before[statePath(f.r)] {
								t.Fatal("rollback lost owned files")
							}
						}
					} else if action == "remove" {
						absent(t, original)
						absent(t, statePath(f.r))
					} else {
						if !bytes.Equal(get(t, f.r.SkillProjection.DestinationPath), get(t, f.r.SkillProjection.SourcePath)) {
							t.Fatal("redo bytes")
						}
						if action == "relocate" {
							absent(t, original)
						}
					}
					f.apply(t)
				})
			}
		}
	}
}

func TestSkillFinalRemovalNativeRetention(t *testing.T) {
	f := skillFixture(t)
	source := filepath.Join(filepath.Dir(f.r.ControlRoot), "inert-native")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(source, "fixture.txt"), []byte("inert retained native fixture"))
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
	retained := l.Native
	f.apply(t)
	destination := f.r.SkillProjection.DestinationPath
	f.r.Remove = true
	x := f.apply(t)
	if !x.Ledger.Enabled || !reflect.DeepEqual(x.Ledger.Native, retained) {
		t.Fatal("independent removal changed native/policy")
	}
	absent(t, destination)
	f.r.Remove = false
	f.apply(t)
	l, e = installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RemoveConsumer: true, ExpectedGeneration: &f.r.ExpectedGeneration})
	if e != nil {
		t.Fatal(e)
	}
	f.r.ExpectedGeneration = l.Generation
	// Final removal must refuse a foreign replacement before runtime cleanup.
	owned := get(t, destination)
	put(t, destination, []byte("foreign"))
	f.r.Remove = true
	skillConflict(t, f)
	put(t, destination, owned)
	f.r.SkillProjection = nil
	x = f.apply(t)
	if x.Ledger.Enabled || len(x.Ledger.Consumers) != 0 || !reflect.DeepEqual(x.Ledger.Native, retained) {
		t.Fatal("final cleanup native/policy")
	}
	absent(t, destination)
	absent(t, statePath(f.r))
	absent(t, f.r.Command)
	if _, e := os.Stat(native.After.Path); e != nil {
		t.Fatal(e)
	}
}

func TestSkillStrictOwnership(t *testing.T) {
	for _, change := range []string{"v1-extended", "unknown", "alias", "missing", "null", "identity", "source", "destination"} {
		t.Run(change, func(t *testing.T) {
			f := skillFixture(t)
			f.apply(t)
			path := statePath(f.r)
			b := get(t, path)
			switch change {
			case "v1-extended":
				b = bytes.Replace(b, []byte(`"Schema":2`), []byte(`"Schema":1`), 1)
			case "unknown":
				b = bytes.Replace(b, []byte(`"Skill":{`), []byte(`"Skill":{"Unknown":true,`), 1)
			case "alias":
				b = bytes.Replace(b, []byte(`"DestinationPath":`), []byte(`"destinationpath":`), 1)
			case "missing":
				b = bytes.Replace(b, []byte(`"Link":"",`), nil, 1)
			case "null":
				start := bytes.Index(b, []byte(`,"Skill":`))
				b = append(b[:start], []byte(`,"Skill":null}`)...)
			case "identity":
				b = bytes.Replace(b, []byte(`"Exists":true`), []byte(`"Exists":false`), 1)
			case "source":
				b = bytes.Replace(b, []byte(f.r.SkillProjection.SourcePath), []byte(f.r.Command), 1)
			case "destination":
				b = bytes.Replace(b, []byte(f.r.SkillProjection.DestinationPath), []byte(f.r.ConfigPath), 1)
			}
			if bytes.Equal(b, get(t, path)) {
				t.Fatal("mutation did not change fixture")
			}
			before, e := installruntime.Fingerprint(path)
			if e != nil {
				t.Fatal(e)
			}
			l, e := installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RefreshOnly: true, ExpectedGeneration: &f.r.ExpectedGeneration, Files: []installruntime.File{{Path: path, Before: before, Data: b, Mode: 0600}}})
			if e != nil {
				t.Fatal(e)
			}
			f.r.ExpectedGeneration = l.Generation
			skillConflict(t, f)
		})
	}
}

func TestSkillForeignEditDuringPromotionAndRecovery(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "redo", true: "rollback"}[rollback], func(t *testing.T) {
			f := skillFixture(t)
			destination := f.r.SkillProjection.DestinationPath
			if _, e := apply(f.ctx, f.r, func(s string) error {
				if s == "promotion:"+destination {
					return errors.New("crash")
				}
				return nil
			}); e == nil {
				t.Fatal("fault missed")
			}
			foreign := []byte("foreign after promotion")
			put(t, destination, foreign)
			if _, e := recoverKernel(f, rollback); e == nil {
				t.Fatal("recovery adopted foreign edit")
			}
			if !bytes.Equal(foreign, get(t, destination)) {
				t.Fatal("foreign edit lost")
			}
		})
	}
	t.Run("transaction-CAS", func(t *testing.T) {
		f := skillFixture(t)
		destination := f.r.SkillProjection.DestinationPath
		foreign := []byte("concurrent foreign creation")
		if _, e := apply(f.ctx, f.r, func(s string) error {
			if s == "transaction" {
				put(t, destination, foreign)
			}
			return nil
		}); e == nil {
			t.Fatal("CAS accepted creation")
		}
		if !bytes.Equal(foreign, get(t, destination)) {
			t.Fatal("CAS lost foreign data")
		}
	})
}

func TestSkillOwnershipCannotBeSharedOrAdopted(t *testing.T) {
	t.Run("schema1", func(t *testing.T) {
		f := skillFixture(t)
		selected := f.r.SkillProjection
		f.r.SkillProjection = nil
		f.apply(t)
		put(t, selected.DestinationPath, get(t, selected.SourcePath))
		f.r.SkillProjection = selected
		skillConflict(t, f)
	})
	t.Run("other-consumer", func(t *testing.T) {
		f := skillFixture(t)
		f.apply(t)
		f.r.ConfigPath = filepath.Join(filepath.Dir(f.r.ConfigPath), "other-codex.toml")
		skillConflict(t, f)
	})
}
func TestSkillFinalRemovalRecovery(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "redo", true: "rollback"}[rollback], func(t *testing.T) {
			f := skillFixture(t)
			f.apply(t)
			destination := f.r.SkillProjection.DestinationPath
			l, e := installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: "legacy-hooks", RemoveConsumer: true, ExpectedGeneration: &f.r.ExpectedGeneration})
			if e != nil {
				t.Fatal(e)
			}
			f.r.ExpectedGeneration = l.Generation
			f.r.Remove = true
			if _, e := apply(f.ctx, f.r, func(s string) error {
				if s == "promotion:"+destination {
					return errors.New("crash final removal")
				}
				return nil
			}); e == nil {
				t.Fatal("fault missed")
			}
			// Recovery precedes Prepare; stop there without creating a consumer.
			_, _ = installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, Owner: Managed, RuntimeRoot: f.r.RuntimeRoot, ConsumerID: consumerID(f.r.Provider, f.r.ConfigPath), RollbackPending: rollback, Prepare: func() ([]installruntime.File, error) { return nil, unchanged }})
			snapshot, e := installruntime.ReadInstalledSnapshot(f.r.ControlRoot)
			if e != nil || snapshot.Recovery {
				t.Fatal("recovery incomplete", e)
			}
			if rollback {
				if len(snapshot.Ledger.Consumers) != 1 || !bytes.Equal(get(t, destination), get(t, f.r.SkillProjection.SourcePath)) {
					t.Fatal("rollback lost final ownership")
				}
				f.r.ExpectedGeneration = snapshot.Ledger.Generation
				f.apply(t)
			} else if len(snapshot.Ledger.Consumers) != 0 {
				t.Fatal("redo retained consumer")
			}
			absent(t, destination)
			absent(t, statePath(f.r))
			absent(t, f.r.Command)
		})
	}
}
