package setupwizard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
)

func resolvePackageRoot(ctx context.Context, req Request, recordedPath, recordedVersion string) (string, func(), error) {
	cleanup := func() {}
	source := req.PackageRoot
	if source == "" {
		source = recordedPath
	}
	if explicitAbs(source) {
		return openLocalPackage(req, source)
	}
	version := recordedVersion
	if version == "" {
		version = req.ReleaseVersion
	}
	if version == "" || (req.ReleaseDownloadRoot == "" && req.PackageFetcher == nil) {
		return "", cleanup, fmt.Errorf("%w: package_required", ErrRefused)
	}
	parent, err := os.MkdirTemp(filepath.Dir(req.ControlRoot), "acquired-package-")
	if err != nil {
		return "", cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(parent) }
	root, err := portableasset.Fetch(ctx, portableasset.FetchRequest{
		Version: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		DestParent: parent, DownloadRoot: req.ReleaseDownloadRoot, Get: req.PackageFetcher,
	})
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return root, cleanup, nil
}

func openLocalPackage(req Request, source string) (string, func(), error) {
	cleanup := func() {}
	info, err := os.Lstat(source)
	if err != nil {
		return "", cleanup, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", cleanup, fmt.Errorf("%w: package path must not be a symlink", ErrRefused)
	}
	if info.IsDir() {
		return source, cleanup, nil
	}
	if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(source), ".zip") {
		return "", cleanup, fmt.Errorf("%w: package must be a directory or zip archive", ErrRefused)
	}
	parent, err := os.MkdirTemp(filepath.Dir(req.ControlRoot), "acquired-package-")
	if err != nil {
		return "", cleanup, err
	}
	root, err := portableasset.OpenArchive(source, parent, req.PackageSHA256)
	if err != nil {
		_ = os.RemoveAll(parent)
		return "", cleanup, err
	}
	return root, func() { _ = os.RemoveAll(parent) }, nil
}

func packageDeclaredName(root string) string {
	if !explicitAbs(root) {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &manifest) != nil {
		return ""
	}
	return strings.TrimSpace(manifest.Name)
}

func uapStateFile(controlRoot string) string {
	return filepath.Join(filepath.Dir(controlRoot), "uap", "state", "state-v2.json")
}

func loadUAPState(controlRoot string) (domain.StateFileV2, error) {
	if !explicitAbs(controlRoot) {
		return domain.StateFileV2{}, fmt.Errorf("%w: control_root_required", ErrRefused)
	}
	return statev2.Store{Path: uapStateFile(controlRoot)}.Load()
}

// LiveNotifyClients returns selected agents that already have a non-absent
// portable binding. Missing state is "none"; the TTY adapter does not invent
// an installation from the running master's version.
func LiveNotifyClients(controlRoot string, agents []string) []string {
	state, err := loadUAPState(controlRoot)
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, agent := range agents {
		if agent != "" {
			want[agent] = true
		}
	}
	var found []string
	seen := map[string]bool{}
	for _, installation := range state.Installations {
		for _, binding := range installation.Clients {
			if !want[binding.ClientID] || seen[binding.ClientID] {
				continue
			}
			if binding.Materialization == domain.MaterializationAbsent {
				continue
			}
			seen[binding.ClientID] = true
			found = append(found, binding.ClientID)
		}
	}
	return found
}

func desiredPackage(req Request) (localPath, version string) {
	state, err := loadUAPState(req.ControlRoot)
	if err != nil {
		return "", ""
	}
	installation, ok := pickInstallation(state, req.InstallationID)
	if !ok {
		return "", ""
	}
	version = strings.TrimSpace(installation.Package.Version)
	if version == "" {
		version = strings.TrimPrefix(strings.TrimSpace(installation.Source.ResolvedRevision), "v")
	}
	for _, path := range []string{installation.Source.CanonicalSource, installation.Source.RequestedSource} {
		if packageStillUsable(path) {
			return path, version
		}
	}
	return "", version
}

func pickInstallation(state domain.StateFileV2, id string) (domain.Installation, bool) {
	if id != "" {
		for _, item := range state.Installations {
			if item.InstallationID == id {
				return item, true
			}
		}
		return domain.Installation{}, false
	}
	if len(state.Installations) == 1 {
		return state.Installations[0], true
	}
	return domain.Installation{}, false
}

func packageStillUsable(path string) bool {
	if !explicitAbs(path) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if info.IsDir() {
		plugin, err := os.Lstat(filepath.Join(path, "plugin.json"))
		return err == nil && plugin.Mode().IsRegular() && plugin.Size() > 0
	}
	return info.Mode().IsRegular() && strings.EqualFold(filepath.Ext(path), ".zip")
}
