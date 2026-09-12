package installruntime

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Walk through a held root, never through absolute child paths. Every opened
// leaf must still match its root-relative metadata observation. Root confines
// even concurrent symlink substitutions; static links are rejected outright.
func walkNativeTree(root *os.Root, visit func(string, os.FileInfo, *os.File) error) error {
	return fs.WalkDir(root.FS(), ".", func(rel string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// DirEntry.Info can retain the original pathname on Darwin. Resolve
		// metadata through the held root even after the source is renamed.
		info, err := root.Lstat(rel)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return visit(rel, info, nil)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported native node: %s", rel)
		}
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return fmt.Errorf("native file identity changed: %s", rel)
		}
		return visit(rel, info, f)
	})
}

func copyNativeTree(source, destination string, expectedID ...string) error {
	sourceRoot, err := openNativeRoot(source)
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	return copyOpenedNativeTree(sourceRoot, destination, expectedID...)
}

func copyOpenedNativeTree(sourceRoot *os.Root, destination string, expectedID ...string) error {
	targetRoot, err := openNativeRoot(destination)
	if err != nil {
		return err
	}
	defer targetRoot.Close()
	if len(expectedID) != 0 {
		if err := checkOpenedNativeRoot(targetRoot, []PathAnchor{{Identity: expectedID[0]}}); err != nil {
			return err
		}
	}
	return walkNativeTree(sourceRoot, func(rel string, info os.FileInfo, sourceFile *os.File) error {
		if info.IsDir() {
			if rel == "." {
				return targetRoot.Chmod(".", info.Mode().Perm())
			}
			return targetRoot.Mkdir(rel, info.Mode().Perm())
		}
		target, err := targetRoot.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(target, sourceFile)
		if err == nil {
			err = target.Sync()
		}
		closeErr := target.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

func checkNativeParents(change *NativeChange) error {
	if filepath.Dir(change.Staged) != filepath.Dir(change.After.Path) {
		return fmt.Errorf("native mutation requires sibling paths")
	}
	got, err := pathAnchors(change.After.Path, false)
	if err != nil {
		return err
	}
	return checkAnchors(change.Parents, got)
}

// Unused candidates still require ownership proof. Identical live bytes do not
// authorize deleting an arbitrary Staged pathname supplied by a caller.
func discardNativeCandidate(change *NativeChange) error {
	if err := checkNativeParents(change); err != nil {
		return err
	}
	if !strings.HasPrefix(filepath.Base(change.Staged), ".candidate-") || change.Staged == change.After.Path {
		return fmt.Errorf("unused native candidate path is not private staging")
	}
	id := change.StagedID
	if id == "" {
		id = change.After.DirectoryID
	}
	if err := checkNativeDirectoryID(change.Staged, id); err != nil {
		return err
	}
	digest, err := treeFingerprint(change.Staged)
	if err != nil {
		return err
	}
	if digest == "" {
		return nil
	}
	if digest != change.After.SHA256 {
		return fmt.Errorf("unused native candidate changed")
	}
	entries, err := capturePurgeTree(change.Staged)
	if err != nil {
		return err
	}
	return cleanupPurgeTree(PurgeTree{Path: change.Staged, Entries: entries}, nil)
}

func openNativeRoot(path string) (*os.Root, error) {
	expected, err := pathAnchors(filepath.Join(path, ".identity"), false)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, fmt.Errorf("native root must be a non-link directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		root.Close()
		return nil, fmt.Errorf("native root identity changed")
	}
	if err := checkOpenedNativeRoot(root, expected); err != nil {
		root.Close()
		return nil, err
	}
	current, err := pathAnchors(filepath.Join(path, ".identity"), false)
	if err == nil {
		err = checkAnchors(expected, current)
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func syncNativeTree(path string) error {
	root, err := openNativeRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	var directories []string
	err = walkNativeTree(root, func(rel string, info os.FileInfo, _ *os.File) error {
		if info.IsDir() {
			directories = append(directories, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		f, err := root.Open(directories[i])
		if err != nil {
			return err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Failed copies have no durable per-entry manifest. Only an empty candidate
// with the allocated identity can be removed; preserve partial trees for inspection.
func removeFailedNativeStage(path string, expected []PathAnchor, id string) error {
	return safeRemoveDirectory(path, id, expected)
}

func nativeDirectoryID(path string) (string, error) {
	var err error
	path, err = platformAnchorPath(path)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	anchors, err := pathAnchors(filepath.Join(path, ".identity"), false)
	if err != nil {
		return "", err
	}
	if len(anchors) == 0 || anchors[len(anchors)-1].Path != path {
		return "", nil
	}
	return anchors[len(anchors)-1].Identity, nil
}
func checkNativeDirectoryID(path, expected string) error {
	// Legacy records have byte identities only; new promotions always bind inodes.
	if expected == "" {
		return nil
	}
	got, err := nativeDirectoryID(path)
	if err != nil {
		return err
	}
	if got != expected {
		return fmt.Errorf("native directory inode changed: %s", path)
	}
	return nil
}

func checkOpenedNativeRoot(root *os.Root, expected []PathAnchor) error {
	if len(expected) == 0 {
		return fmt.Errorf("missing native directory anchor")
	}
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	identity, err := openedDirectoryIdentity(f)
	if err != nil {
		return err
	}
	if identity != expected[len(expected)-1].Identity {
		return fmt.Errorf("opened native directory inode changed")
	}
	return nil
}

// The staging lock and candidates use the same held directory. Reopening the
// lock by an absolute pathname would let a substituted parent create a foreign
// lock file even though the candidate itself remained descriptor-confined.
func lockNativeStage(ctx context.Context, root *os.Root) (func(), error) {
	f, err := openNativeStageLock(root)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		held, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if held {
			opened, e1 := f.Stat()
			named, e2 := root.Lstat(".staging.lock")
			if e1 != nil || e2 != nil || !os.SameFile(opened, named) {
				f.Close()
				return nil, fmt.Errorf("native staging lock inode changed")
			}
			return func() { f.Close() }, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
