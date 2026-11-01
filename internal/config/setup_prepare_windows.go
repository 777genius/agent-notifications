//go:build windows

package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Setup publishes the same credential-bearing configuration as config init
// and edit. Go's Unix mode arguments do not establish a private Windows DACL.
// Reuse the store's protected creation, ancestry validation and native publish
// operations; never change permissions on an existing destination.
func prepareGlobalConfigParent(path string) error {
	if !validAbsolute("windows", path) || filepath.Clean(path) != path {
		return &Error{Code: ConfigInvalid, Path: path}
	}
	parent, err := openStoreParent(filepath.Dir(path), true)
	if err != nil {
		return pathErrorAt(path, "prepare-parent-open", err)
	}
	defer parent.close()
	if err = parent.verify(); err != nil {
		return pathErrorAt(path, "prepare-parent-verify", err)
	}
	return nil
}

// Preflight must reject an exposed canonical file before interpreting its
// configuration as user intent. Historical bundle/default sources are read-only
// inputs and do not inherit the canonical destination's private-file policy.
func inspectPreparedCanonical(path string) error {
	if !validAbsolute("windows", path) {
		return &Error{Code: ConfigInvalid, Path: path}
	}
	parent, err := openStoreParent(filepath.Dir(path), false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return pathErrorAt(path, "prepare-inspect-parent-open", err)
	}
	defer parent.close()
	_, err = parent.read(filepath.Base(path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return pathErrorAt(path, "prepare-inspect-read", err)
	}
	if err = parent.verify(); err != nil {
		return pathErrorAt(path, "prepare-inspect-parent-verify", err)
	}
	return nil
}

func prepareGlobalConfig(ctx context.Context, canonicalPath, legacyPath, defaultsPath string) (result PrepareGlobalResult, err error) {
	if !validAbsolute("windows", canonicalPath) {
		return result, &Error{Code: ConfigInvalid, Path: canonicalPath}
	}
	if err = validateStoreTarget(canonicalPath); err != nil {
		return result, err
	}
	var parent *storeParent
	// Another cooperating preparer can still be publishing this directory
	// before its permanent target lock exists. Retry only the native sharing
	// gap, within the caller's original deadline, repeating the strict walk.
	err = checkInitEntryContext(ctx, canonicalPath, func(string) error {
		var e error
		parent, e = openStoreParent(filepath.Dir(canonicalPath), true)
		return e
	})
	if err != nil {
		return result, pathErrorAt(canonicalPath, "prepare-open-parent", err)
	}
	defer parent.close()
	name := filepath.Base(canonicalPath)
	// Validate existing files before creating a lock or staging new bytes.
	// Keep the pre-lock security probe. A file publication can temporarily
	// conflict with its open too; each retry repeats ACL/link/reparse checks.
	err = checkInitEntryContext(ctx, canonicalPath, func(string) error {
		_, e := parent.read(name)
		return e
	})
	if err != nil && !os.IsNotExist(err) {
		return result, pathErrorAt(canonicalPath, "prepare-prelock-read", err)
	}
	unlock, err := parent.lock(ctx, name+".lock", false, true)
	if err != nil {
		return result, pathErrorAt(canonicalPath, "prepare-lock", err)
	}
	defer unlock()
	if err = parent.verify(); err != nil {
		return result, pathErrorAt(canonicalPath, "prepare-locked-parent-verify", err)
	}
	before, err := parent.read(name)
	absent := os.IsNotExist(err)
	result.Source = "canonical"
	raw := before.Bytes
	if absent {
		for i, path := range []string{legacyPath, defaultsPath} {
			result.Source = []string{"legacy", "defaults"}[i]
			source, e := physicalParent(path, false)
			if e == nil {
				raw, _, e = readPrepared(source, filepath.Base(path))
				if e == nil {
					e = samePreparedParent(source, path)
				}
				_ = source.Close()
			}
			if i == 0 && os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return result, e
			}
			break
		}
	} else if err != nil {
		return result, pathErrorAt(canonicalPath, "prepare-locked-read", err)
	}
	out, _, changed, err := completeDesktop(raw, true)
	if err != nil {
		return result, err
	}
	if !absent && !changed {
		err = parent.verify()
		result.Ready = err == nil
		if err != nil {
			return result, pathErrorAt(canonicalPath, "prepare-noop-parent-verify", err)
		}
		return result, nil
	}
	temp, err := parent.writeArtifact(name, "tmp", out)
	if err != nil {
		return result, pathErrorAt(canonicalPath, "prepare-write-artifact", err)
	}
	discard := true
	defer func() {
		if discard {
			parent.discard(temp)
		}
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if !absent {
		current, e := parent.read(name)
		if e != nil {
			return result, pathErrorAt(canonicalPath, "prepare-preimage-read", e)
		}
		if !bytes.Equal(before.Bytes, current.Bytes) {
			return result, &Error{Code: ConfigConflict, Path: canonicalPath}
		}
	}
	err = parent.publish(ctx, temp, name, !absent, before.Bytes, out)
	if err != nil {
		// An absent-target race may have selected a complete canonical winner.
		// Never report success for uncertain or recovery-required publication.
		var ce *Error
		if errors.As(err, &ce) && (ce.Code == ConfigCommitUncertain || ce.Code == ConfigRecoveryRequired) {
			discard = false
			result.Changed = true
			return result, pathErrorAt(canonicalPath, "prepare-publish", err)
		}
		if !absent || !errors.As(err, &ce) || ce.Code != ConfigConflict {
			return result, pathErrorAt(canonicalPath, "prepare-publish", err)
		}
		result.Source = "canonical"
	} else {
		result.Changed = true
		if err = parent.sync(); err != nil {
			return result, sanitizedError(ConfigCommitUncertain, canonicalPath, 0, "prepare-sync", err)
		}
	}
	stage := "prepare-post-read"
	persisted, err := parent.read(name)
	if err == nil {
		stage = "prepare-post-parent-verify"
		err = parent.verify()
	}
	if err == nil {
		stage = "prepare-post-validate"
		_, _, _, err = ValidateGlobalDesktop(persisted.Bytes)
	}
	result.Ready = err == nil
	if err != nil {
		return result, pathErrorAt(canonicalPath, stage, err)
	}
	return result, nil
}

// Windows cannot FlushFileBuffers a directory handle. The temp file is already
// synced before Link/Rename; flush the published file the same way the Windows
// store does after ReplaceFileW.
func syncPreparedPublication(root *os.Root, name string) error {
	// FlushFileBuffers requires GENERIC_WRITE; Root.Open is read-only.
	f, err := root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
