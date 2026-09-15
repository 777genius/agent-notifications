package config

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MutationTimeout = 5 * time.Second

type InitRequest struct {
	Env    EnvSnapshot
	Assets AssetContext
	Legacy LegacyContext
	// From is an explicit raw import source, used only for an absent target.
	From string
}
type EditRequest struct {
	Env            EnvSnapshot
	Assets         AssetContext
	ExpectRevision string
	Edits          Edits
}

// Result contains only safe metadata. Changed with ConfigCommitUncertain means
// a commit may be visible; inspect before deciding whether to retry.
type Result struct {
	Selection  Selection `json:"selection"`
	Revision   string    `json:"revision,omitempty"`
	Changed    bool      `json:"changed"`
	BackupPath string    `json:"backupPath,omitempty"`
}

// mutationFiles is the narrow OS transaction boundary. Test adapters inject
// failures here; production has no fault hooks or environment switches.
type mutationFiles interface {
	read(string) (Snapshot, error)
	writeArtifact(string, string, []byte) (string, error)
	publish(context.Context, string, string, bool, []byte, []byte) error
	sync() error
	discard(string)
	verify() error
	close()
}

type mutation struct {
	parent    mutationFiles
	selection Selection
	unlock    func()
	guard     func()
	ctx       context.Context
}

func (m *mutation) close() { m.unlock(); m.parent.close(); m.guard() }

// Sidecars are protocol metadata, never editable configuration targets. Reserving
// their exact namespace prevents an explicit override from replacing another
// writer's persistent lock inode or recovery evidence.
func validateStoreTarget(path string) error {
	base := filepath.Base(path)
	if strings.HasSuffix(strings.ToLower(base), ".lock") {
		return &Error{Code: ConfigUnsafeTarget, Path: path}
	}
	for _, marker := range []string{".tmp-", ".backup-"} {
		if i := strings.LastIndex(base, marker); i >= 0 && len(base[i+len(marker):]) == 32 {
			if _, err := hex.DecodeString(base[i+len(marker):]); err == nil {
				return &Error{Code: ConfigUnsafeTarget, Path: path}
			}
		}
	}
	return nil
}
func sameStorePath(a, b, goos string) bool {
	if goos == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// heldStoreLocks is scoped to one synchronous mutation. Entries come from
// validated, still-open OS lock handles, never from pathname comparisons.
type heldStoreLocks struct{ files []os.FileInfo }
type heldStoreLocksKey struct{}

func reusesStoreLock(ctx context.Context, f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	held, _ := ctx.Value(heldStoreLocksKey{}).(*heldStoreLocks)
	if held != nil {
		for _, other := range held.files {
			if os.SameFile(info, other) {
				return true, nil
			}
		}
	}
	return false, nil
}
func rememberStoreLock(ctx context.Context, f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if held, _ := ctx.Value(heldStoreLocksKey{}).(*heldStoreLocks); held != nil {
		held.files = append(held.files, info)
	}
	return nil
}

// resolveForMutation keeps the public resolver a single, read-only snapshot,
// while allowing a mutation to wait out the brief Windows directory-sharing
// gap created by another initializer's MoveFileEx publication. The private
// errno survives sanitization specifically so no other resolver failure is
// retried.
func resolveForMutation(ctx context.Context, env EnvSnapshot) (Selection, error) {
	for {
		if ctx.Err() != nil {
			return Selection{}, &Error{Code: ConfigLockTimeout}
		}
		selected, err := Resolve(env)
		if ctx.Err() != nil {
			return Selection{}, &Error{Code: ConfigLockTimeout, Path: selected.Path}
		}
		var ce *Error
		if env.GOOS != "windows" || !errors.As(err, &ce) || ce.causeErrno != 32 {
			return selected, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Selection{}, &Error{Code: ConfigLockTimeout, Path: selected.Path}
		case <-timer.C:
		}
	}
}

func beginMutation(ctx context.Context, env EnvSnapshot) (*mutation, error) {
	ctx = context.WithValue(ctx, heldStoreLocksKey{}, &heldStoreLocks{})
	guard := func() {}
	guardHeld := false
	home := env.Vars["HOME"]
	if env.GOOS == "windows" {
		home = env.Vars["USERPROFILE"]
	}
	acquireGuard := func(initial Selection) error {
		if !validAbsolute(env.GOOS, home) {
			return nil
		}
		physical, e := filepath.EvalSymlinks(home)
		if e != nil {
			if initial.Source != "explicit" {
				return pathError(home, e)
			}
			return nil
		}
		guardPath := filepath.Join(physical, ".agent-notifications-config.lock")
		if initial.Path != "" && sameStorePath(initial.Path, guardPath, env.GOOS) {
			return &Error{Code: ConfigInvalid, Path: initial.Path}
		}
		parent, e := openStoreParent(physical, false)
		if e != nil {
			if initial.Source != "explicit" {
				return pathError(home, e)
			}
			return nil
		}
		release, e := parent.lock(ctx, ".agent-notifications-config.lock", false, true)
		if e != nil {
			parent.close()
			_, guardStatErr := os.Lstat(guardPath)
			if initial.Source != "explicit" || !portableGuardUnavailable(e) || !os.IsNotExist(guardStatErr) {
				return pathError(home, e)
			}
			return nil
		}
		guard = func() { release(); parent.close() }
		guardHeld = true
		return nil
	}

	// Resolve before creating either lock. Besides invalid overrides, this keeps
	// an unavailable Windows config base side-effect free. Existing legacy
	// selection still succeeds when APPDATA is unavailable.
	initial, err := resolveForMutation(ctx, env)
	if err != nil {
		var ce *Error
		if !errors.As(err, &ce) || ce.Code != ConfigRecoveryRequired {
			guard()
			return nil, err
		}
		// A live initializer may own these artifacts. Classify only after locks.
	}
	if err := validateStoreTarget(initial.Path); err != nil {
		guard()
		return nil, err
	}
	if !guardHeld {
		if err := acquireGuard(initial); err != nil {
			return nil, err
		}
	}
	fail := func(e error) (*mutation, error) { guard(); return nil, e }
	selected, err := resolveForMutation(ctx, env)
	if err != nil {
		var ce *Error
		if !errors.As(err, &ce) || ce.Code != ConfigRecoveryRequired {
			return fail(err)
		}
	}
	if initial.Exists && (!selected.Exists || initial.Path != selected.Path) {
		return fail(&Error{Code: ConfigConflict, Path: initial.Path})
	}
	// Initialization may adopt a newly selected winner under the selection guard.
	// Edits still compare their path-bound revision before touching bytes.
	parent, err := openStoreParent(filepath.Dir(selected.Path), true)
	if err != nil {
		return fail(pathError(selected.Path, err))
	}
	release, err := parent.lock(ctx, filepath.Base(selected.Path)+".lock", false, true)
	if err != nil {
		parent.close()
		return fail(pathError(selected.Path, err))
	}
	next, err := resolveForMutation(ctx, env)
	if err == nil && next.Path != selected.Path {
		err = &Error{Code: ConfigConflict, Path: selected.Path}
	}
	if err == nil {
		err = parent.verify()
	}
	if err != nil {
		release()
		parent.close()
		return fail(err)
	}
	return &mutation{parent: parent, selection: next, unlock: release, guard: guard, ctx: ctx}, nil
}
func currentDocument(m *mutation, assets AssetContext) (Document, error) {
	snap, err := m.parent.read(filepath.Base(m.selection.Path))
	if err != nil {
		return Document{}, pathError(m.selection.Path, err)
	}
	d, err := ParseDocument(snap.Bytes, snap.PhysicalPath, true)
	if err != nil {
		return Document{}, err
	}
	if _, err = d.Effective(assets); err != nil {
		return Document{}, pathError(m.selection.Path, err)
	}
	return d, nil
}

func EnsureInitialized(ctx context.Context, r InitRequest) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, MutationTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return Result{}, &Error{Code: ConfigLockTimeout}
	}
	// Existing valid initialization is a read-only snapshot. It need not
	// create lock files in a read-only deployment or adjust any permissions.
	if selected, e := resolveForMutation(ctx, r.Env); e == nil && selected.Exists {
		if e := validateStoreTarget(selected.Path); e != nil {
			return Result{Selection: selected}, e
		}

		snap, e := readInitSnapshot(ctx, selected.Path)
		if e == nil {
			d, e := ParseDocument(snap.Bytes, snap.PhysicalPath, true)
			if e == nil {
				_, e = d.Effective(r.Assets)
			}
			if e != nil {
				return Result{Selection: selected}, pathErrorAt(selected.Path, "init-parse-effective", e)
			}
			// Selection can change while this read-only snapshot is taken (for
			// example, a legacy file appears while neutral was selected).
			// Never report initialization of a silently retargeted selection.
			next, e := resolveForMutation(ctx, r.Env)
			if e != nil {
				return Result{Selection: selected}, e
			}
			if !next.Exists || next.Path != selected.Path || snap.PhysicalPath != selected.Path {
				return Result{Selection: selected}, &Error{Code: ConfigChanged, Path: selected.Path}
			}
			return Result{Selection: next, Revision: d.Revision()}, nil
		}
		if os.IsNotExist(e) {
			return Result{Selection: selected}, &Error{Code: ConfigChanged, Path: selected.Path}
		}
		if !os.IsNotExist(e) {
			var ce *Error
			if !errors.As(e, &ce) || ce.Code != ConfigChanged {
				return Result{Selection: selected}, pathErrorAt(selected.Path, "init-read-snapshot", e)
			}
		}
	}
	m, err := beginMutation(ctx, r.Env)
	if err != nil {
		return Result{}, err
	}
	defer m.close()
	result := Result{Selection: m.selection}
	if m.selection.Exists {
		d, e := currentDocument(m, r.Assets)
		if e != nil {
			return result, e
		}
		result.Revision = d.Revision()
		return result, nil
	}
	var d Document
	if r.From != "" {
		if !validAbsolute(r.Env.GOOS, r.From) {
			return result, &Error{Code: ConfigInvalid, Path: r.From}
		}
		snap, e := readSnapshotContext(m.ctx, r.From, MaxDocumentBytes)
		if e != nil {
			return result, pathError(r.From, e)
		}
		d, err = ParseDocument(snap.Bytes, m.selection.Path, false)
	} else {
		if m.selection.Source != "explicit" {
			if err = CheckHistorical(r.Legacy, func(path string, limit int) (Snapshot, error) {
				return readSnapshotContext(m.ctx, path, limit)
			}); err != nil {
				return result, err
			}
		}
		d, err = SeedDocument(m.selection.Path)
	}
	if err != nil {
		return result, err
	}
	if _, err = d.Effective(r.Assets); err != nil {
		return result, err
	}
	return commitDocument(ctx, r.Env, m, Document{}, d, result)
}

func ApplyEdits(ctx context.Context, r EditRequest) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, MutationTimeout)
	defer cancel()
	if r.ExpectRevision == "" {
		return Result{}, &Error{Code: ConfigConflict}
	}
	m, err := beginMutation(ctx, r.Env)
	if err != nil {
		return Result{}, err
	}
	defer m.close()
	result := Result{Selection: m.selection}
	if !m.selection.Exists {
		return result, &Error{Code: ConfigConflict, Path: m.selection.Path}
	}
	old, err := currentDocument(m, r.Assets)
	if err != nil {
		return result, err
	}
	result.Revision = old.Revision()
	if old.Revision() != r.ExpectRevision {
		return result, &Error{Code: ConfigConflict, Path: m.selection.Path}
	}
	next, err := ApplyRawEdits(old, r.Edits, r.Assets)
	if err != nil {
		return result, err
	}
	if bytes.Equal(old.original, next.original) {
		return result, nil
	}
	return commitDocument(ctx, r.Env, m, old, next, result)
}

func commitDocument(ctx context.Context, env EnvSnapshot, m *mutation, old, next Document, result Result) (Result, error) {
	base := filepath.Base(m.selection.Path)
	check := func() error {
		if ctx.Err() != nil {
			return &Error{Code: ConfigLockTimeout, Path: m.selection.Path}
		}
		s, e := resolveForMutation(ctx, env)
		if e != nil {
			return e
		}
		if s.Path != m.selection.Path || s.Exists != m.selection.Exists {
			return &Error{Code: ConfigConflict, Path: m.selection.Path}
		}
		if e = m.parent.verify(); e != nil {
			return e
		}
		if s.Exists {
			snap, e := m.parent.read(base)
			if e != nil {
				return pathError(s.Path, e)
			}
			if !bytes.Equal(snap.Bytes, old.original) {
				return &Error{Code: ConfigConflict, Path: s.Path}
			}
		}
		return nil
	}
	if err := check(); err != nil {
		return result, err
	}
	if m.selection.Exists {
		backup, err := m.parent.writeArtifact(base, "backup", old.original)
		if err != nil {
			return result, pathError(m.selection.Path, err)
		}
		result.BackupPath = filepath.Join(filepath.Dir(m.selection.Path), backup)
		if err = m.parent.sync(); err != nil {
			return result, pathError(m.selection.Path, err)
		}
	}
	temp, err := m.parent.writeArtifact(base, "tmp", next.original)
	if err != nil {
		return result, pathError(m.selection.Path, err)
	}
	// Our own temp is recovery evidence for a missing canonical. Re-resolving
	// after creating it would reject the in-flight operation; compare the pinned
	// entry directly for fresh creation, whose publication is no-overwrite.
	if m.selection.Exists {
		err = check()
	} else {
		if ctx.Err() != nil {
			err = &Error{Code: ConfigLockTimeout}
		}
		if err == nil {
			err = m.parent.verify()
		}
	}
	if err != nil {
		m.parent.discard(temp)
		return result, err
	}
	err = m.parent.publish(ctx, temp, base, m.selection.Exists, old.original, next.original)
	if err != nil {
		var ce *Error
		if errors.As(err, &ce) && (ce.Code == ConfigCommitUncertain || ce.Code == ConfigRecoveryRequired) {
			result.Changed = true
			result.Revision = ""
			if visible, e := m.parent.read(base); e == nil {
				result.Selection.Exists = true
				if d, e := ParseDocument(visible.Bytes, visible.PhysicalPath, true); e == nil {
					result.Revision = d.Revision()
				}
			} else if os.IsNotExist(e) {
				result.Selection.Exists = false
			}
			return result, err
		}
		m.parent.discard(temp)
		return result, pathError(m.selection.Path, err)
	}
	result.Changed = true
	result.Selection.Exists = true
	committed, e := ParseDocument(next.original, m.selection.Path, true)
	if e == nil {
		result.Revision = committed.Revision()
	}
	if err = m.parent.sync(); err != nil {
		return result, &Error{Code: ConfigCommitUncertain, Path: m.selection.Path}
	}
	return result, nil
}
