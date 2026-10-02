//go:build linux || darwin

package clientsetup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func legacySkillFixture(t *testing.T) (fixture, SkillProjection) {
	t.Helper()
	f := skillFixture(t)
	canonical := *f.r.SkillProjection
	f.r.SkillProjection = &SkillProjection{
		SourcePath:      filepath.Join(f.r.RuntimeRoot, "skills", "agent-notify", "SKILL.md"),
		DestinationPath: filepath.Join(filepath.Dir(f.r.ConfigPath), "skills", "agent-notify", "SKILL.md"),
	}
	f.updateSkill(t, []byte("historical owned skill\n"))
	f.apply(t)
	return f, canonical
}

// This fails if old ownership is rejected after its runtime source is retired,
// or if explicit rename leaves two active projections.
func TestSkillRenamePreservesOmissionAndMovesOwnedProjection(t *testing.T) {
	f, canonical := legacySkillFixture(t)
	legacy := *f.r.SkillProjection
	before, err := installruntime.Fingerprint(legacy.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := installruntime.Commit(f.ctx, installruntime.Request{ControlRoot: f.r.ControlRoot, RuntimeRoot: f.r.RuntimeRoot, Owner: Managed, ConsumerID: "legacy-hooks", RefreshOnly: true, ExpectedGeneration: &f.r.ExpectedGeneration, Files: []installruntime.File{{Path: legacy.SourcePath, Before: before, Remove: true}}})
	if err != nil {
		t.Fatal(err)
	}
	f.r.ExpectedGeneration = ledger.Generation
	state := get(t, statePath(f.r))
	f.r.SkillProjection = nil
	if f.apply(t).Changed || !bytes.Equal(state, get(t, statePath(f.r))) || string(get(t, legacy.DestinationPath)) != "historical owned skill\n" {
		t.Fatal("omission changed historical projection")
	}
	f.r.SkillProjection = &canonical
	if !f.apply(t).Changed || !bytes.Equal(get(t, canonical.SourcePath), get(t, canonical.DestinationPath)) {
		t.Fatal("canonical rename failed")
	}
	absent(t, legacy.DestinationPath)
	if !bytes.Contains(get(t, statePath(f.r)), []byte(canonical.SourcePath)) || bytes.Contains(get(t, statePath(f.r)), []byte(legacy.SourcePath)) {
		t.Fatal("state still points to historical source")
	}
}

// This fails if migration adopts a foreign target or overwrites an edited old copy.
func TestSkillRenameRefusesForeignCopies(t *testing.T) {
	for _, change := range []string{"edited-legacy", "foreign-canonical", "unowned-legacy"} {
		t.Run(change, func(t *testing.T) {
			f, canonical := legacySkillFixture(t)
			legacy := *f.r.SkillProjection
			switch change {
			case "edited-legacy":
				put(t, legacy.DestinationPath, []byte("user edit"))
			case "foreign-canonical":
				put(t, canonical.DestinationPath, get(t, canonical.SourcePath))
			case "unowned-legacy":
				f.r.Remove = true
				f.apply(t)
				f.r.Remove = false
				put(t, legacy.DestinationPath, []byte("foreign old skill"))
			}
			f.r.SkillProjection = &canonical
			skillConflict(t, f)
		})
	}
}

// Removal uses the recorded destination identity even when the caller now names
// the renamed canonical source, which may not exist in a historical runtime.
func TestSkillRenameRemovesHistoricalProjection(t *testing.T) {
	f, canonical := legacySkillFixture(t)
	legacy := *f.r.SkillProjection
	f.r.SkillProjection = &SkillProjection{SourcePath: canonical.SourcePath, DestinationPath: legacy.DestinationPath}
	f.r.Remove = true
	f.apply(t)
	absent(t, legacy.DestinationPath)
	if _, err := os.Stat(canonical.DestinationPath); !os.IsNotExist(err) {
		t.Fatal("removal created canonical projection", err)
	}
}

// A mixed selection could publish canonical frontmatter in the historical skill
// folder, or historical frontmatter in the canonical folder.
func TestSkillRenameRejectsMixedSelection(t *testing.T) {
	for _, sourceLegacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical-source", true: "historical-source"}[sourceLegacy], func(t *testing.T) {
			f, canonical := legacySkillFixture(t)
			legacy := *f.r.SkillProjection
			if sourceLegacy {
				f.r.SkillProjection = &SkillProjection{SourcePath: legacy.SourcePath, DestinationPath: canonical.DestinationPath}
			} else {
				f.r.SkillProjection = &SkillProjection{SourcePath: canonical.SourcePath, DestinationPath: legacy.DestinationPath}
			}
			skillConflict(t, f)
		})
	}
}
