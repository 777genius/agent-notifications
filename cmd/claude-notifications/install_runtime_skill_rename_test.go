package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/skills"
)

// This fails if a refresh leaves a second runtime skill, loses ledger ownership,
// or removes a user-edited/unowned historical skill.
func TestEmbeddedSkillRenameMigration(t *testing.T) {
	for _, change := range []string{"owned", "edited", "unowned"} {
		t.Run(change, func(t *testing.T) {
			f := embeddedQualified(t)
			legacy := filepath.Join(filepath.Dir(f.bin), "skills", "agent-notify", "SKILL.md")
			if change != "unowned" {
				if err := f.run("--entry", f.entry); err != nil {
					t.Fatal(err)
				}
				canonical, err := installruntime.CanonicalPath(f.skill)
				if err != nil {
					t.Fatal(err)
				}
				legacy, err = installruntime.CanonicalPath(legacy)
				if err != nil {
					t.Fatal(err)
				}
				before, err := installruntime.Fingerprint(canonical)
				if err != nil {
					t.Fatal(err)
				}
				runtimeRoot, err := installruntime.CanonicalPath(filepath.Dir(f.bin))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if _, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "claude-hooks", RefreshOnly: true, Files: []installruntime.File{{Path: canonical, Before: before, Remove: true}, {Path: legacy, Data: []byte("historical owned skill"), Mode: 0600}}}); err != nil {
					t.Fatal(err)
				}
			}
			if change != "owned" {
				embeddedPut(t, legacy, []byte("user-owned legacy bytes"), 0600)
			}
			args := []string{"--entry", f.entry}
			if change != "unowned" {
				args = append(args, "--refresh")
			}
			if err := f.run(args...); change == "owned" {
				if err != nil {
					t.Fatal(err)
				}
				embeddedAbsent(t, legacy)
				if !bytes.Equal(embeddedRead(t, f.skill), skills.AgentNotify()) {
					t.Fatal("renamed runtime skill bytes differ")
				}
				for path := range f.snapshot(t).Ledger.Files {
					if path == legacy {
						t.Fatal("retired legacy asset remains owned")
					}
				}
			} else {
				if err == nil {
					t.Fatal("accepted changed/unowned legacy skill")
				}
				if string(embeddedRead(t, legacy)) != "user-owned legacy bytes" {
					t.Fatal("legacy bytes changed")
				}
				embeddedAbsent(t, f.skill)
				if change == "unowned" {
					embeddedAbsent(t, filepath.Join(f.bin, f.entry))
				}
			}
		})
	}
}

func TestEmbeddedSkillRenameRefusesLegacySymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink privileges are separately qualified")
	}
	f := embeddedQualified(t)
	legacy := filepath.Join(filepath.Dir(f.bin), "skills", "agent-notify", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.stage, f.entry), legacy); err != nil {
		t.Fatal(err)
	}
	if err := f.run("--entry", f.entry); err == nil {
		t.Fatal("accepted legacy symlink")
	}
	if _, err := os.Readlink(legacy); err != nil {
		t.Fatal("legacy symlink changed", err)
	}
	embeddedAbsent(t, f.skill)
}
