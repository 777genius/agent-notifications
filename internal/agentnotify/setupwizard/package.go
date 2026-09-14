package setupwizard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
)

func resolvePackageRoot(req Request) (string, func(), error) {
	cleanup := func() {}
	if !explicitAbs(req.PackageRoot) {
		return "", cleanup, fmt.Errorf("%w: package_required", ErrRefused)
	}
	info, err := os.Lstat(req.PackageRoot)
	if err != nil {
		return "", cleanup, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", cleanup, fmt.Errorf("%w: package path must not be a symlink", ErrRefused)
	}
	if info.IsDir() {
		return req.PackageRoot, cleanup, nil
	}
	if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(req.PackageRoot), ".zip") {
		return "", cleanup, fmt.Errorf("%w: package must be a directory or zip archive", ErrRefused)
	}
	parent, err := os.MkdirTemp(filepath.Dir(req.ControlRoot), "acquired-package-")
	if err != nil {
		return "", cleanup, err
	}
	root, err := portableasset.OpenArchive(req.PackageRoot, parent, req.PackageSHA256)
	if err != nil {
		_ = os.RemoveAll(parent)
		return "", cleanup, err
	}
	return root, func() { _ = os.RemoveAll(parent) }, nil
}
