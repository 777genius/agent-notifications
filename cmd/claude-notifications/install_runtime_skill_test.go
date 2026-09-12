package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/skills"
)

type embeddedFixture struct{ stage, bin, control, skill, entry string }

func embeddedFresh(t *testing.T) embeddedFixture {
	t.Helper()
	root := t.TempDir()
	f := embeddedFixture{stage: filepath.Join(root, "stage"), bin: filepath.Join(root, "runtime", "bin"), control: filepath.Join(root, "control"), entry: "claude-notifications-linux-amd64"}
	f.skill = filepath.Join(filepath.Dir(f.bin), "skills", "agent-notify", "SKILL.md")
	embeddedPut(t, filepath.Join(f.stage, f.entry), []byte("inert "+installruntime.WriterProtocolMarker), 0755)
	return f
}
func embeddedPut(t *testing.T, p string, b []byte, m os.FileMode) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, m); e != nil {
		t.Fatal(e)
	}
}
func embeddedRead(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (f embeddedFixture) run(extra ...string) error {
	return installRuntime(append([]string{"--stage", f.stage, "--target", f.bin, "--control-root", f.control}, extra...), io.Discard)
}
func (f embeddedFixture) snapshot(t *testing.T) installruntime.InstalledSnapshot {
	t.Helper()
	s, e := installruntime.ReadInstalledSnapshot(f.control)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func embeddedAbsent(t *testing.T, p string) {
	t.Helper()
	if _, e := os.Lstat(p); !os.IsNotExist(e) {
		t.Fatalf("expected absent %s: %v", p, e)
	}
}

func TestEmbeddedSkillLifecycleProjection(t *testing.T) {
	f := embeddedQualified(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The stage has an inert sender and, on Darwin, an inert signed capability
	// fixture; it has no skill or package cache.
	if e := f.run("--entry", f.entry); e != nil {
		t.Fatal(e)
	}
	s := f.snapshot(t)
	if !bytes.Equal(embeddedRead(t, f.skill), skills.AgentNotify()) {
		t.Fatal("canonical bytes differ")
	}
	id, e := installruntime.Fingerprint(f.skill)
	if e != nil {
		t.Fatal(e)
	}
	canonicalSkill, e := installruntime.CanonicalPath(f.skill)
	if e != nil {
		t.Fatal(e)
	}
	if s.Ledger.Files[canonicalSkill] != id || id.Mode != 0600 || id.Link != "" {
		t.Fatal("skill not owned at exact regular-file identity")
	}
	embeddedAbsent(t, filepath.Join(f.bin, "skills"))
	runtimeRoot, e := installruntime.CanonicalPath(filepath.Dir(f.bin))
	if e != nil {
		t.Fatal(e)
	}
	// Model an older legitimately owned release through the existing transaction seam.
	_, e = installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, Owner: "existing-installer", RuntimeRoot: runtimeRoot, ConsumerID: "claude-hooks", RefreshOnly: true, Files: []installruntime.File{{Path: canonicalSkill, Before: id, Data: []byte("older owned release"), Mode: 0600}}})
	if e != nil {
		t.Fatal(e)
	}
	if e := f.run("--refresh", "--entry", f.entry); e != nil {
		t.Fatal(e)
	}
	if len(f.snapshot(t).Ledger.Consumers) != 1 {
		t.Fatal("refresh added consumer")
	}
	canonicalBin, e := installruntime.CanonicalPath(f.bin)
	if e != nil {
		t.Fatal(e)
	}
	client := filepath.Join(filepath.Dir(f.control), "client")
	projection := filepath.Join(client, "skills", "agent-notify", "SKILL.md")
	if e := os.MkdirAll(filepath.Dir(projection), 0700); e != nil {
		t.Fatal(e)
	}
	r := clientsetup.Request{ControlRoot: f.control, RuntimeRoot: runtimeRoot, Command: filepath.Join(canonicalBin, f.entry), ConfigPath: filepath.Join(client, "config.toml"), Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: f.snapshot(t).Ledger.Generation, SkillProjection: &clientsetup.SkillProjection{SourcePath: canonicalSkill, DestinationPath: projection}}
	if _, e := clientsetup.Apply(ctx, r); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(embeddedRead(t, projection), skills.AgentNotify()) {
		t.Fatal("consumer projection differs")
	}
	if e := f.run("--remove"); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(embeddedRead(t, f.skill), skills.AgentNotify()) {
		t.Fatal("independent removal deleted shared skill")
	}
	r.Remove = true
	r.ExpectedGeneration = f.snapshot(t).Ledger.Generation
	if _, e := clientsetup.Apply(ctx, r); e != nil {
		t.Fatal(e)
	}
	embeddedAbsent(t, f.skill)
	embeddedAbsent(t, projection)
}

func TestEmbeddedSkillRefusesForeignAndTampered(t *testing.T) {
	for _, kind := range []string{"foreign-different", "tampered", "symlink", "corrupt-ledger", "missing-ledger"} {
		t.Run(kind, func(t *testing.T) {
			f := embeddedQualified(t)
			if kind == "tampered" || kind == "corrupt-ledger" || kind == "missing-ledger" {
				if e := f.run("--entry", f.entry); e != nil {
					t.Fatal(e)
				}
			}
			switch kind {
			case "foreign-different", "tampered":
				embeddedPut(t, f.skill, []byte("foreign"), 0600)
			case "symlink":
				embeddedPut(t, f.skill+".foreign", skills.AgentNotify(), 0600)
				if e := os.Symlink(f.skill+".foreign", f.skill); e != nil {
					t.Fatal(e)
				}
			case "corrupt-ledger":
				embeddedPut(t, filepath.Join(f.control, "ownership.json"), []byte("{"), 0600)
			case "missing-ledger":
				if e := os.Remove(filepath.Join(f.control, "ownership.json")); e != nil {
					t.Fatal(e)
				}
			}
			before := embeddedRead(t, f.skill)
			binary, _ := os.ReadFile(filepath.Join(f.bin, f.entry))
			ledger, _ := os.ReadFile(filepath.Join(f.control, "ownership.json"))
			embeddedPut(t, filepath.Join(f.stage, f.entry), []byte("new inert "+installruntime.WriterProtocolMarker), 0755)
			if e := f.run("--entry", f.entry); e == nil {
				t.Fatal("accepted conflict")
			}
			afterBinary, _ := os.ReadFile(filepath.Join(f.bin, f.entry))
			afterLedger, _ := os.ReadFile(filepath.Join(f.control, "ownership.json"))
			if !bytes.Equal(before, embeddedRead(t, f.skill)) || !bytes.Equal(binary, afterBinary) || !bytes.Equal(ledger, afterLedger) {
				t.Fatal("refusal mutated assets or ledger")
			}
			embeddedAbsent(t, filepath.Join(f.control, "transaction.json"))
		})
	}
}

func TestEmbeddedSkillAdoptsMatchingUnmanagedSkill(t *testing.T) {
	f := embeddedQualified(t)
	embeddedPut(t, f.skill, skills.AgentNotify(), 0600)
	if e := f.run("--entry", f.entry); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(embeddedRead(t, f.skill), skills.AgentNotify()) {
		t.Fatal("canonical bytes differ")
	}
	id, e := installruntime.Fingerprint(f.skill)
	if e != nil {
		t.Fatal(e)
	}
	s := f.snapshot(t)
	if s.Ledger.Files[f.skill] != id {
		canonical, e := installruntime.CanonicalPath(f.skill)
		if e != nil || s.Ledger.Files[canonical] != id {
			t.Fatal("matching unmanaged skill was not adopted")
		}
	}
}

func TestEmbeddedSkillNoEntryAndStageAllowlist(t *testing.T) {
	f := embeddedQualified(t)
	embeddedPut(t, filepath.Join(f.stage, "skills", "agent-notify", "SKILL.md"), []byte("untrusted"), 0600)
	embeddedPut(t, filepath.Join(f.stage, "skills", "other", "SKILL.md"), []byte("untrusted"), 0600)
	embeddedPut(t, filepath.Join(f.stage, "skills", "agent-notify", "OTHER.md"), []byte("untrusted"), 0600)
	if e := f.run(); e != nil {
		t.Fatal(e)
	}
	embeddedAbsent(t, f.skill)
	if e := os.Remove(filepath.Join(f.stage, f.entry)); e != nil {
		t.Fatal(e)
	}
	embeddedPut(t, filepath.Join(f.stage, "list-sounds"), []byte("inert utility"), 0755)
	if e := f.run("--refresh"); e != nil {
		t.Fatal(e)
	}
	embeddedAbsent(t, f.skill)
	embeddedPut(t, filepath.Join(f.stage, f.entry), []byte("inert "+installruntime.WriterProtocolMarker), 0755)
	if e := f.run("--entry", f.entry); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(embeddedRead(t, f.skill), skills.AgentNotify()) {
		t.Fatal("stage supplied skill")
	}
	embeddedAbsent(t, filepath.Join(filepath.Dir(f.bin), "skills", "other", "SKILL.md"))
	embeddedAbsent(t, filepath.Join(f.bin, "skills"))
	embeddedAbsent(t, filepath.Join(filepath.Dir(f.bin), "skills", "agent-notify", "OTHER.md"))
}

func TestEmbeddedSkillAdmissionBoundaries(t *testing.T) {
	for _, args := range [][]string{{"--entry", "invalid"}, {"--entry", "claude-notifications-windows-amd64.exe"}, {"--entry", "claude-notifications-linux-amd64", "--require-native"}, {"--entry", "claude-notifications-linux-amd64", "--refresh"}} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			f := embeddedFresh(t)
			if e := f.run(args...); e == nil {
				t.Fatal("accepted invalid/unqualified install")
			}
			embeddedAbsent(t, f.skill)
			embeddedAbsent(t, filepath.Join(f.bin, f.entry))
		})
	}
	t.Run("parent-symlink", func(t *testing.T) {
		f := embeddedQualified(t)
		outside := filepath.Join(filepath.Dir(f.control), "outside")
		if e := os.MkdirAll(outside, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.MkdirAll(filepath.Dir(f.bin), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(outside, filepath.Join(filepath.Dir(f.bin), "skills")); e != nil {
			t.Fatal(e)
		}
		if e := f.run("--entry", f.entry); e == nil {
			t.Fatal("accepted symlink parent")
		}
		embeddedAbsent(t, filepath.Join(outside, "agent-notify", "SKILL.md"))
		embeddedAbsent(t, filepath.Join(f.bin, f.entry))
	})
}

func TestEmbeddedSkillWindowsComposition(t *testing.T) {
	f := embeddedQualified(t)
	if e := os.Remove(filepath.Join(f.stage, f.entry)); e != nil {
		t.Fatal(e)
	}
	f.entry = "claude-notifications-windows-amd64.exe"
	embeddedPut(t, filepath.Join(f.stage, f.entry), []byte("inert "+installruntime.WriterProtocolMarker), 0755)
	hooks := filepath.Join(filepath.Dir(f.bin), "hooks", "hooks.json")
	embeddedPut(t, hooks, []byte(`{"foreign":true,"hooks":{}}`), 0600)
	if e := f.run("--entry", f.entry); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(embeddedRead(t, f.skill), skills.AgentNotify()) || !bytes.Contains(embeddedRead(t, hooks), []byte(f.entry)) {
		t.Fatal("preparations not composed")
	}
	if e := f.run("--refresh", "--entry", f.entry); e != nil {
		t.Fatal(e)
	}
	if e := f.run("--remove", "--entry", f.entry); e != nil {
		t.Fatal(e)
	}
	embeddedAbsent(t, f.skill)
	if !bytes.Contains(embeddedRead(t, hooks), []byte(`"foreign": true`)) {
		t.Fatal("foreign hook field lost")
	}
}

func TestEmbeddedSkillBrokenNativePreserved(t *testing.T) {
	f := embeddedFresh(t)
	native := filepath.Join(f.bin, "ClaudeNotifier.app", "Contents", "MacOS", "terminal-notifier-modern")
	embeddedPut(t, native, []byte("unusable inert callback"), 0600)
	if e := f.run("--entry", f.entry); e == nil {
		t.Fatal("accepted broken native")
	}
	if string(embeddedRead(t, native)) != "unusable inert callback" {
		t.Fatal("native changed")
	}
	embeddedAbsent(t, f.skill)
	embeddedAbsent(t, filepath.Join(f.bin, f.entry))
}
