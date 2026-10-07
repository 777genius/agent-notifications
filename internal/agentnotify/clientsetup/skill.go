package clientsetup

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// SkillProjection selects a canonical managed source and an explicit user skill
// destination. Existing parents must be physical directories; missing parents
// are created under the commit lock. Nil preserves an existing projection;
// Remove removes it using its recorded identity.
type SkillProjection struct {
	SourcePath, DestinationPath string
}

type skillOwnership struct {
	SourcePath, DestinationPath string
	Identity                    installruntime.Identity
}

const maxSkill = 64 * 1024

// Missing destination parents are created by the kernel under commit.
var errDirectoryAbsent = error(directoryAbsentError{})

type directoryAbsentError struct{}

func (directoryAbsentError) Error() string { return "projection parent absent" }

func skillPathsValid(r Request, source, destination string) bool {
	// Historical projections remain removable and may be relocated to the
	// renamed canonical skill using their exact recorded ownership.
	sourceName := filepath.Base(filepath.Dir(source))
	destinationName := filepath.Base(filepath.Dir(destination))
	skillName := func(name string) bool { return name == "agent-notifications" || name == "agent-notify" }
	return r.Provider == registration.Codex &&
		skillName(sourceName) && source == filepath.Join(r.RuntimeRoot, "skills", sourceName, "SKILL.md") && clean(source) &&
		clean(destination) && filepath.Base(destination) == "SKILL.md" &&
		skillName(destinationName) &&
		destination != r.ConfigPath && destination != r.RuntimeRoot && destination != r.ControlRoot &&
		!within(r.RuntimeRoot, destination) && !within(r.ControlRoot, destination)
}

func skillIdentitiesEqual(got, want installruntime.Identity) bool {
	if got.Exists != want.Exists || got.SHA256 != want.SHA256 || got.Link != want.Link {
		return false
	}
	if !got.Exists || got.Link != "" {
		return true
	}
	return installruntime.IdentityMode(got.Mode) == installruntime.IdentityMode(want.Mode)
}

// Called twice: read-only preflight and under the existing kernel locks. External
// skills are config mutations, never generic ledger assets. Exact ownership is
// retained in the consumer state, including across final-consumer cleanup.
func projectSkill(r Request, l installruntime.Ledger, old *skillOwnership, inspect bool) ([]installruntime.File, *skillOwnership, []string, error) {
	var files []installruntime.File
	var paths []string
	var liveOld installruntime.Identity
	if old != nil {
		if !skillPathsValid(r, old.SourcePath, old.DestinationPath) || !old.Identity.Exists || old.Identity.Mode != 0600 || old.Identity.Link != "" {
			return nil, nil, nil, ErrConflict
		}
		if _, tracked := l.Files[old.DestinationPath]; tracked {
			return nil, nil, nil, ErrConflict
		}
		_, got, e := read(old.DestinationPath, maxSkill)
		if e != nil {
			return nil, nil, nil, e
		}
		if !skillIdentitiesEqual(got, old.Identity) {
			return nil, nil, nil, ErrConflict
		}
		liveOld = got
		paths = append(paths, old.DestinationPath)
	}
	selected := r.SkillProjection
	if selected != nil {
		if !skillPathsValid(r, selected.SourcePath, selected.DestinationPath) {
			return nil, nil, nil, ErrConflict
		}
		// A historical repair selects both historical paths. New projection
		// selects both canonical paths; removal relies on recorded ownership.
		if !r.Remove && filepath.Base(filepath.Dir(selected.SourcePath)) != filepath.Base(filepath.Dir(selected.DestinationPath)) {
			return nil, nil, nil, ErrConflict
		}
	}
	if r.Remove {
		if selected != nil && old == nil {
			_, got, e := read(selected.DestinationPath, maxSkill)
			if e != nil {
				return nil, nil, nil, e
			}
			if got.Exists {
				return nil, nil, nil, ErrConflict
			}
			paths = append(paths, selected.DestinationPath)
		}
		if selected != nil && old != nil && selected.DestinationPath != old.DestinationPath {
			return nil, nil, nil, ErrConflict
		}
		if old != nil {
			files = append(files, installruntime.File{Path: old.DestinationPath, Before: liveOld, Remove: true})
		}
		return files, nil, paths, nil
	}
	if selected == nil {
		return nil, old, paths, nil
	}
	// A second spelling in the same skills root would activate two copies.
	// Only the recorded old projection may occupy that sibling during rename.
	alternateName := "agent-notify"
	if filepath.Base(filepath.Dir(selected.DestinationPath)) == alternateName {
		alternateName = "agent-notifications"
	}
	alternate := filepath.Join(filepath.Dir(filepath.Dir(selected.DestinationPath)), alternateName, "SKILL.md")
	if old == nil || old.DestinationPath != alternate {
		_, identity, e := read(alternate, maxSkill)
		if e != nil {
			return nil, nil, nil, e
		}
		if identity.Exists {
			return nil, nil, nil, ErrConflict
		}
		paths = append(paths, alternate)
	}
	if _, tracked := l.Files[selected.DestinationPath]; tracked {
		return nil, nil, nil, ErrConflict
	}
	source, got, e := read(selected.SourcePath, maxSkill)
	if e != nil {
		return nil, nil, nil, e
	}
	want, tracked := l.Files[selected.SourcePath]
	if !tracked || !got.Exists || got.Link != "" || got != want {
		return nil, nil, nil, ErrConflict
	}
	_, before, e := read(selected.DestinationPath, maxSkill)
	if e != nil {
		return nil, nil, nil, e
	}
	same := old != nil && old.DestinationPath == selected.DestinationPath
	if !same {
		if before.Exists {
			return nil, nil, nil, ErrConflict
		}
		// Existing parents must be physical directories. Missing parents are
		// created by the kernel under the commit lock via pathAnchors.
		if !inspect {
			if e := requireDirectory(filepath.Dir(selected.DestinationPath)); e != nil && e != errDirectoryAbsent {
				return nil, nil, nil, ErrConflict
			}
		}
		paths = append(paths, selected.DestinationPath)
		if old != nil {
			files = append(files, installruntime.File{Path: old.DestinationPath, Before: liveOld, Remove: true})
		}
	} else if !skillIdentitiesEqual(before, old.Identity) {
		return nil, nil, nil, ErrConflict
	}
	hash := sha256.Sum256(source)
	identity := installruntime.Identity{Exists: true, SHA256: hex.EncodeToString(hash[:]), Mode: 0600}
	next := &skillOwnership{selected.SourcePath, selected.DestinationPath, identity}
	if !skillIdentitiesEqual(before, identity) {
		files = append(files, installruntime.File{Path: selected.DestinationPath, Before: before, Data: source, Mode: 0600})
	}
	return files, next, paths, nil
}
