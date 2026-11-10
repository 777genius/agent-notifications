package installruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func validateNative(change *NativeChange) error {
	if change == nil {
		return nil
	}
	if change.Purge && change.Before.Path != change.After.Path {
		return fmt.Errorf("native purge paths must match")
	}
	if err := checkNativeParents(change); err != nil {
		return err
	}
	current, err := treeFingerprint(change.After.Path)
	if err != nil {
		return err
	}
	if change.Purge {
		if current != "" && current != change.Before.SHA256 {
			return fmt.Errorf("native purge fingerprint conflict")
		}
		if current != "" {
			return checkNativeDirectoryID(change.Before.Path, change.Before.DirectoryID)
		}
		return nil
	}
	if current == change.After.SHA256 {
		return checkNativeDirectoryID(change.After.Path, change.After.DirectoryID)
	}
	if change.Before.SHA256 != "" && change.Before.Path != change.After.Path {
		before, err := treeFingerprint(change.Before.Path)
		if err != nil {
			return err
		}
		if before != change.Before.SHA256 {
			return fmt.Errorf("native live fingerprint conflict")
		}
		if err := checkNativeDirectoryID(change.Before.Path, change.Before.DirectoryID); err != nil {
			return err
		}
	} else if current != "" && current != change.Before.SHA256 {
		return fmt.Errorf("native live fingerprint conflict")
	} else if current != "" {
		if err := checkNativeDirectoryID(change.Before.Path, change.Before.DirectoryID); err != nil {
			return err
		}
	}
	staged, err := treeFingerprint(change.Staged)
	if err != nil {
		return err
	}
	if staged != change.After.SHA256 {
		return fmt.Errorf("native staged fingerprint conflict")
	}
	id := change.StagedID
	if id == "" {
		id = change.After.DirectoryID
	}
	return checkNativeDirectoryID(change.Staged, id)
}
func promoteNative(change *NativeChange) error {
	if change == nil {
		return nil
	}
	if err := validateNative(change); err != nil {
		return err
	}
	current, err := treeFingerprint(change.After.Path)
	if err != nil {
		return err
	}
	if change.Purge {
		if current == "" {
			return nil
		}
		// Remove only the explicitly purged, fingerprint-checked owned callback.
		if err := renameNative(change.Before.Path, change.Staged, change.Parents, change.Before.DirectoryID); err != nil {
			return err
		}
		return syncDir(filepath.Dir(change.Before.Path))
	}
	if current == change.After.SHA256 {
		return nil
	}
	if current == "" {
		err = renameNative(change.Staged, change.After.Path, change.Parents, change.After.DirectoryID)
	} else if change.Before.Path != "" && change.Before.Path != change.After.Path {
		// Publish the new generation beside the previous one. Never exchange
		// the already-sent callback identity.
		err = renameNative(change.Staged, change.After.Path, change.Parents, change.After.DirectoryID)
	} else {
		return fmt.Errorf("new native bytes reused live callback identity")
	}
	if err != nil {
		return err
	}
	return syncDir(filepath.Dir(change.After.Path))
}

// NativeAlias publishes a required product-owned discovery alias while retaining
// pre-existing concrete conventional bundles and every queued callback inode.
func NativeAlias(change *NativeChange, bin string) ([]File, error) {
	if change == nil {
		return nil, nil
	}
	var files []File
	for _, alias := range []struct {
		name     string
		optional bool
		managed  bool
	}{
		{name: "AgentNotifications.app", managed: true},
		{name: "ClaudeNotifier.app"},
		{name: "terminal-notifier.app", optional: true},
	} {
		next, err := nativeHookAlias(change, filepath.Join(bin, alias.name), alias.optional, alias.managed)
		if err != nil {
			return nil, err
		}
		files = append(files, next...)
	}
	return files, nil
}

func nativeHookAlias(change *NativeChange, path string, optional, managed bool) ([]File, error) {
	info, err := os.Lstat(path)
	if err == nil && info.IsDir() {
		if managed {
			return nil, fmt.Errorf("managed native alias is occupied by a concrete bundle: %s", path)
		}
		return nil, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	before, err := Fingerprint(path)
	if err != nil {
		return nil, err
	}
	if before.Exists && before.Link != change.After.Path && !ownedNativeAlias(before.Link, change) {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("foreign native alias cannot be replaced")
	}
	return []File{{Path: path, Before: before, Link: change.After.Path}}, nil
}

// DiscardNative removes only this invocation's unused, unchanged candidate.
// The component lock and recovery references prevent deleting either an
// interrupted transaction's input or the previous callback retained by a swap.
// This is best-effort staging cleanup, never the transaction's rollback path.
func DiscardNative(ctx context.Context, control string, change *NativeChange) error {
	if change == nil {
		return nil
	}
	if control == "" {
		var err error
		control, err = ControlRoot()
		if err != nil {
			return err
		}
	}
	unlock, err := Lock(ctx, filepath.Join(control, ".component-install.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	parent, err := filepath.EvalSymlinks(filepath.Join(control, "native"))
	if err != nil {
		return err
	}
	if filepath.Dir(change.Staged) != parent || !strings.HasPrefix(filepath.Base(change.Staged), ".candidate-") {
		return fmt.Errorf("candidate cleanup path is not owned staging")
	}
	ledger, err := readLedger(control)
	if err != nil {
		return err
	}
	if ledger.Native != nil && nativeRecordContainsPath(*ledger.Native, change.Staged) {
		return nil
	}
	data, err := readRegularFile(filepath.Join(control, "transaction.json"))
	if err == nil {
		tx, err := decodeTransactionBlobs(data, transactionBlobDir(filepath.Join(control, "transaction.json")))
		if err != nil {
			return err
		}
		if tx.Native != nil && tx.Native.Staged == change.Staged {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	digest, err := treeFingerprint(change.Staged)
	if err != nil || digest == "" {
		return err
	}
	if digest != change.After.SHA256 {
		return fmt.Errorf("candidate changed; preserving for inspection")
	}
	if err := discardNativeCandidate(change); err != nil {
		return err
	}
	return syncDir(parent)
}

// cleanupPurgedNative is replayable while the transaction marker still exists.
// Explicit purge authorizes removal of only these exact recorded identities;
// unknown files and the journal namespace never participate.
func cleanupPurgedNative(change *NativeChange, fault func(string) error) error {
	if change == nil || (!change.Purge && !change.Retire) {
		return nil
	}
	if len(change.PurgeTrees) == 0 {
		return fmt.Errorf("legacy purge marker lacks per-entry ownership; preserve for inspection")
	}
	for _, tree := range change.PurgeTrees {
		if filepath.Dir(tree.Path) != filepath.Dir(change.Before.Path) || !nativeRecordContainsPath(change.Before, tree.Path) && tree.Path != change.Staged {
			return fmt.Errorf("invalid purge tree ownership")
		}
		if err := cleanupPurgeTree(tree, fault); err != nil {
			return err
		}
	}
	return syncDir(filepath.Dir(change.Before.Path))
}

func allocateNativeStage(ctx context.Context, parent string, expected []PathAnchor) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	root, err := openNativeRoot(parent)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = root.Close() }()
	if err = checkOpenedNativeRoot(root, expected); err != nil {
		return "", "", err
	}
	release, err := lockNativeStage(ctx, root)
	if err != nil {
		return "", "", err
	}
	defer release()
	directory, err := root.Open(".")
	if err != nil {
		return "", "", err
	}
	entries, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return "", "", err
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".candidate-") {
			count++
		}
	}
	if count >= 16 {
		return "", "", fmt.Errorf("native staging capacity reached; inspect orphaned candidates")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", "", err
	}
	// The exchanged predecessor keeps this name; retain an app bundle suffix.
	name := ".candidate-" + hex.EncodeToString(random[:]) + ".app"
	if err = root.Mkdir(name, 0700); err != nil {
		return "", "", err
	}
	child, err := root.Open(name)
	if err != nil {
		_ = root.Remove(name)
		return "", "", err
	}
	id, err := openedDirectoryIdentity(child)
	_ = child.Close()
	if err != nil {
		_ = root.Remove(name)
		return "", "", err
	}
	return filepath.Join(parent, name), id, nil
}
