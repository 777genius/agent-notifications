package installruntime

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// StageFiles snapshots bytes and target identities before taking the component
// lock. The caller supplies its existing runtime allowlist. Callback bundles
// require the separate native promotion path and are never flattened here.
func StageFiles(source, destination string, allow func(string) bool) ([]File, error) {
	source, err := CanonicalPath(source)
	if err != nil {
		return nil, err
	}
	destination, err = CanonicalPath(destination)
	if err != nil {
		return nil, err
	}
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return nil, err
	}
	defer sourceRoot.Close()
	var files []File
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if !allow(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 && filepath.Dir(resolved) != parent {
			return fmt.Errorf("asset symlink escapes directory: %s", path)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular staged file: %s", path)
		}
		if info.Size() < 0 || info.Size() > maxManagedFile {
			return fmt.Errorf("managed input exceeds size limit")
		}
		target := filepath.Join(destination, rel)
		anchors, err := pathAnchors(target, false)
		if err != nil {
			return err
		}
		before, err := Fingerprint(target)
		if err != nil {
			return err
		}
		f, err := sourceRoot.Open(rel)
		if err != nil {
			return err
		}
		opened, err := f.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			f.Close()
			return fmt.Errorf("staged source identity changed: %s", path)
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(maxManagedFile)+1))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > maxManagedFile {
			return fmt.Errorf("managed input exceeds size limit")
		}
		files = append(files, File{Parents: anchors, Path: target, Before: before, Data: data, Mode: uint32(info.Mode().Perm())})
		return nil
	})
	return files, err
}

// CanonicalPath resolves directory roots, including a not-yet-created suffix.
// File CAS leaves must not use it: a final symlink is itself the owned identity.
func CanonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return "", err
	}
	resolved, err = CanonicalPath(parent)
	return filepath.Join(resolved, filepath.Base(absolute)), err
}
