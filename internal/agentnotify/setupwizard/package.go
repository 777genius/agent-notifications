package setupwizard

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/packagedigest"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// acquiredExtractName matches portableasset's unpacked directory name.
const acquiredExtractName = "root"

// Pending candidates are addressed by the already frozen identity, never by
// the acknowledged predecessor or a search for the latest acquisition.
func pendingAcquireParent(req Request) (string, error) {
	return durableAcquireParent(req, strings.Join([]string{"confirmed", req.ReleaseVersion, req.PackageSHA256, req.TreeDigest, req.HelperDigest, req.HelperVersion}, "\n"))
}

// Capture only bounded package bytes for public143c's existing digest framing.
// This preflight neither snapshots nor creates, publishes or cleans a path.
func pendingPackageEntries(ctx context.Context, req Request) ([]packagedigest.CapturedEntry, error) {
	var source fs.FS = os.DirFS(req.PackageRoot)
	archive := strings.EqualFold(filepath.Ext(req.PackageRoot), ".zip")
	if archive {
		reader, err := zip.OpenReader(req.PackageRoot)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		seen := map[string]bool{}
		for _, file := range reader.File {
			name := strings.TrimSuffix(file.Name, "/")
			if seen[name] || !fs.ValidPath(name) || strings.Contains(name, "..") || strings.Contains(name, `\`) || file.Mode()&os.ModeSymlink != 0 || !file.Mode().IsRegular() && !file.FileInfo().IsDir() {
				return nil, portablesetup.ErrIntentConflict
			}
			seen[name] = true
		}
		source = &reader.Reader
	}
	var entries []packagedigest.CapturedEntry
	var total int64
	err := fs.WalkDir(source, ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if relative == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if relative == ".plugin-kit-ai.lock" && !entry.IsDir() {
			return nil
		}
		if len(entries) >= domain.DefaultMaxFiles {
			return portablesetup.ErrIntentConflict
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		captured := packagedigest.CapturedEntry{Path: relative}
		switch {
		case info.IsDir():
			captured.Kind = "directory"
		case info.Mode()&os.ModeSymlink != 0 && !archive:
			captured.Kind = "symlink"
			filename := filepath.Join(req.PackageRoot, filepath.FromSlash(relative))
			captured.Target, err = os.Readlink(filename)
			if err != nil {
				return err
			}
			resolved, err := filepath.EvalSymlinks(filename)
			if err != nil {
				return err
			}
			contained, err := filepath.Rel(req.PackageRoot, resolved)
			if err != nil || !filepath.IsLocal(contained) {
				return portablesetup.ErrIntentConflict
			}
			if int64(len(captured.Target)) > domain.DefaultMaxFileBytes {
				return portablesetup.ErrIntentConflict
			}
			total += int64(len(captured.Target))
		case info.Mode().IsRegular():
			captured.Kind = "file"
			if info.Size() > domain.DefaultMaxFileBytes {
				return portablesetup.ErrIntentConflict
			}
			file, err := source.Open(relative)
			if err != nil {
				return err
			}
			opened, statErr := file.Stat()
			if statErr != nil || !opened.Mode().IsRegular() || opened.Size() != info.Size() || !archive && !os.SameFile(info, opened) {
				_ = file.Close()
				return portablesetup.ErrIntentConflict
			}
			captured.Content, err = io.ReadAll(io.LimitReader(file, domain.DefaultMaxFileBytes+1))
			closeErr := file.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if int64(len(captured.Content)) != info.Size() {
				return portablesetup.ErrIntentConflict
			}
			total += int64(len(captured.Content))
		default:
			return portablesetup.ErrIntentConflict
		}
		if total > domain.DefaultMaxTreeBytes {
			return portablesetup.ErrIntentConflict
		}
		entries = append(entries, captured)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Match installer.declaredPackageExecutables: regular immediate bin entries
	// and existing regular mcp commands override host/ZIP executable permissions.
	executable := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind == "file" && path.Dir(entry.Path) == "bin" {
			executable[entry.Path] = true
		}
		if entry.Path == "mcp.json" && entry.Kind == "file" {
			var mcp struct {
				Servers map[string]struct {
					Command string `json:"command"`
				} `json:"mcpServers"`
			}
			if json.Unmarshal(entry.Content, &mcp) == nil {
				for _, server := range mcp.Servers {
					executable[path.Clean(strings.TrimPrefix(filepath.ToSlash(server.Command), "./"))] = true
				}
			}
		}
	}
	for index := range entries {
		entries[index].Executable = entries[index].Kind == "file" && executable[entries[index].Path]
	}
	return entries, nil
}

func pendingPackageTree(ctx context.Context, req Request) (string, error) {
	entries, err := pendingPackageEntries(ctx, req)
	if err != nil {
		return "", err
	}
	return packagedigest.DigestCaptured(ctx, entries)
}

// ResolvePendingPackage checks the admitted intent's exact candidate without
// fetching, consulting the predecessor, or removing any source.
func ResolvePendingPackage(ctx context.Context, req Request, intent portablesetup.Intent) (Request, error) {
	for _, pair := range [][2]string{{req.ReleaseVersion, intent.SourceRevision}, {req.TreeDigest, intent.TreeDigest}, {req.HelperDigest, intent.HelperDigest}, {req.HelperVersion, intent.HelperVersion}, {req.PackageSHA256, intent.SourceDigest}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return req, portablesetup.ErrIntentConflict
		}
	}
	req.ReleaseVersion, req.TreeDigest, req.HelperDigest, req.HelperVersion, req.PackageSHA256 = intent.SourceRevision, intent.TreeDigest, intent.HelperDigest, intent.HelperVersion, intent.SourceDigest
	if req.PackageRoot == "" {
		parent, err := pendingAcquireParent(req)
		if err != nil {
			return req, err
		}
		req.PackageRoot = filepath.Join(parent, acquiredExtractName)
	}
	if err := validatePendingPackage(ctx, req); err != nil {
		return req, err
	}
	if strings.EqualFold(filepath.Ext(req.PackageRoot), ".zip") {
		// Retry archives use exclusive pre-effect extraction. A conflicting
		// archive cannot remove or replace an existing acquisition directory.
		digest, err := archiveChecksum(req.PackageRoot, req.PackageSHA256)
		if err != nil {
			return req, err
		}
		parent, err := pendingAcquireParent(req)
		if err != nil {
			return req, err
		}
		if err := os.MkdirAll(filepath.Dir(parent), 0700); err != nil {
			return req, err
		}
		stage, err := os.MkdirTemp(filepath.Dir(parent), "composition-")
		if err != nil {
			return req, err
		}
		defer func() { _ = os.RemoveAll(stage) }()
		req.PackageRoot, err = portableasset.OpenArchive(req.PackageRoot, stage, digest)
		if err != nil {
			return req, err
		}
		if err := validatePendingPackage(ctx, req); err != nil {
			return req, err
		}
		return RetainCurrentReleasePackage(ctx, req)
	}
	return req, nil
}

func validatePendingPackage(ctx context.Context, req Request) error {
	if req.ReleaseVersion == "" || req.TreeDigest == "" || req.HelperDigest == "" || req.HelperVersion != "agent-notify-portable-v1" || !packageStillUsable(req.PackageRoot) {
		return portablesetup.ErrIntentConflict
	}
	if _, err := archiveChecksum(req.Helper, req.HelperDigest); err != nil {
		return portablesetup.ErrIntentConflict
	}
	entries, err := pendingPackageEntries(ctx, req)
	if err != nil {
		return portablesetup.ErrIntentConflict
	}
	var manifest struct{ Name, Version string }
	for _, entry := range entries {
		if entry.Path == "plugin.json" && entry.Kind == "file" {
			if json.Unmarshal(entry.Content, &manifest) != nil {
				return portablesetup.ErrIntentConflict
			}
		}
	}
	if manifest.Name != "agent-notify" || manifest.Version != strings.TrimPrefix(req.ReleaseVersion, "v") {
		return portablesetup.ErrIntentConflict
	}
	digest, err := packagedigest.DigestCaptured(ctx, entries)
	if err != nil || digest != req.TreeDigest {
		return portablesetup.ErrIntentConflict
	}
	return nil
}

// RetainCurrentReleasePackage publishes only an exclusive composition stage
// after caller admission, before Plan/Run. Its existing identity fields become
// the intent's address. Published sources are shared and never cleanup-owned.
func RetainCurrentReleasePackage(ctx context.Context, req Request) (Request, error) {
	parent, err := pendingAcquireParent(req)
	if err != nil {
		return req, err
	}
	stage := filepath.Dir(req.PackageRoot)
	if filepath.Base(req.PackageRoot) != acquiredExtractName || filepath.Dir(stage) != filepath.Dir(parent) {
		return req, nil
	}
	digest, err := pendingPackageTree(ctx, req)
	if err != nil || req.TreeDigest != "" && req.TreeDigest != digest {
		return req, portablesetup.ErrIntentConflict
	}
	req.TreeDigest = digest
	if req.HelperDigest == "" {
		req.HelperDigest, err = archiveChecksum(req.Helper, "")
		if err != nil {
			return req, err
		}
	}
	if req.HelperVersion == "" {
		req.HelperVersion = "agent-notify-portable-v1"
	}
	parent, err = pendingAcquireParent(req)
	if err != nil {
		return req, err
	}
	if err := validatePendingPackage(ctx, req); err != nil {
		return req, err
	}
	if stage == parent {
		return req, nil
	}
	if !strings.HasPrefix(filepath.Base(stage), "composition-") {
		// A usable legacy acquisition is shared. Publish an exclusive copy;
		// never move it or lend its cleanup to a new caller.
		stage, err = os.MkdirTemp(filepath.Dir(parent), "composition-")
		if err != nil {
			return req, err
		}
		defer func() { _ = os.RemoveAll(stage) }()
		if err := os.CopyFS(filepath.Join(stage, acquiredExtractName), os.DirFS(req.PackageRoot)); err != nil {
			return req, err
		}
		copied := req
		copied.PackageRoot = filepath.Join(stage, acquiredExtractName)
		if err := validatePendingPackage(ctx, copied); err != nil {
			return req, err
		}
	}
	if err := os.Rename(stage, parent); err != nil {
		// A concurrent publisher may already have retained the exact candidate.
		shared := req
		shared.PackageRoot = filepath.Join(parent, acquiredExtractName)
		if validatePendingPackage(ctx, shared) != nil {
			return req, err
		}
	}
	req.PackageRoot = filepath.Join(parent, acquiredExtractName)
	return req, nil
}

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
	key := strings.Join([]string{"fetch", version, runtime.GOOS, runtime.GOARCH, req.ReleaseDownloadRoot}, "\n")
	parent, err := durableAcquireParent(req, key)
	if err != nil {
		return "", cleanup, err
	}
	extracted := filepath.Join(parent, acquiredExtractName)
	if packageStillUsable(extracted) {
		return extracted, cleanup, nil
	}
	_ = os.RemoveAll(parent)
	root, err := portableasset.Fetch(ctx, portableasset.FetchRequest{
		Version: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		DestParent: parent, DownloadRoot: req.ReleaseDownloadRoot, Get: req.PackageFetcher,
	})
	if err != nil {
		_ = os.RemoveAll(parent)
		return "", cleanup, err
	}
	return root, cleanup, nil
}

// PinCurrentReleasePackage is used by the public bootstrap mode when its
// caller did not provide a verified ZIP. It deliberately ignores the old
// recorded package, so updating the runtime also updates the portable skill.
// Pending intents are checked before this function is called and keep their
// recorded source revision for resume.
func PinCurrentReleasePackage(ctx context.Context, req Request) (Request, error) {
	if req.PackageRoot != "" {
		return req, nil
	}
	if req.ReleaseVersion == "" {
		req.ReleaseVersion = req.DefaultReleaseVersion
	}
	root, _, err := resolvePackageRoot(ctx, req, "", "")
	if err != nil {
		return req, err
	}
	req.PackageRoot = root
	return req, nil
}

// StageCurrentReleasePackage lends only a newly owned acquisition to read-only
// caller composition. On success the caller retains it for Plan/Run and pending
// resume; cleanup is permitted only before either can begin an effect. Existing
// explicit/cached sources are shared and never belong to this cleanup.
func StageCurrentReleasePackage(ctx context.Context, req Request) (Request, func(), error) {
	cleanup := func() {}
	if req.PackageRoot != "" {
		return req, cleanup, nil
	}
	if req.ReleaseVersion == "" {
		req.ReleaseVersion = req.DefaultReleaseVersion
	}
	ledger, recovery, err := installruntime.ReadOwnership(req.ControlRoot)
	if err != nil || recovery || ledger.PendingMutation != nil || req.ReleaseVersion == "" || (req.ReleaseDownloadRoot == "" && req.PackageFetcher == nil) {
		return req, cleanup, ErrRefused
	}
	key := strings.Join([]string{"fetch", req.ReleaseVersion, runtime.GOOS, runtime.GOARCH, req.ReleaseDownloadRoot}, "\n")
	parent, err := durableAcquireParent(req, key)
	if err != nil {
		return req, cleanup, err
	}
	if cached := filepath.Join(parent, acquiredExtractName); packageStillUsable(cached) {
		req.PackageRoot = cached
		return req, cleanup, nil
	}
	// Exclusive staging cannot be observed as a shared deterministic cache hit.
	base := filepath.Dir(parent)
	uap := filepath.Dir(base)
	_, uapErr := os.Lstat(uap)
	_, baseErr := os.Lstat(base)
	removeEmpty := func() {
		if os.IsNotExist(baseErr) {
			_ = os.Remove(base)
		}
		if os.IsNotExist(uapErr) {
			_ = os.Remove(uap)
		}
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		removeEmpty()
		return req, cleanup, err
	}
	stage, err := os.MkdirTemp(base, "composition-")
	if err != nil {
		removeEmpty()
		return req, cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(stage); removeEmpty() }
	root, err := portableasset.Fetch(ctx, portableasset.FetchRequest{Version: req.ReleaseVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, DestParent: stage, DownloadRoot: req.ReleaseDownloadRoot, Get: req.PackageFetcher})
	if err != nil {
		cleanup()
		return req, func() {}, err
	}
	req.PackageRoot = root
	return req, cleanup, nil
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
	digest, err := archiveChecksum(source, req.PackageSHA256)
	if err != nil {
		return "", cleanup, err
	}
	parent, err := durableAcquireParent(req, source+"\n"+digest)
	if err != nil {
		return "", cleanup, err
	}
	extracted := filepath.Join(parent, acquiredExtractName)
	if packageStillUsable(extracted) {
		return extracted, cleanup, nil
	}
	_ = os.RemoveAll(parent)
	root, err := portableasset.OpenArchive(source, parent, digest)
	if err != nil {
		_ = os.RemoveAll(parent)
		return "", cleanup, err
	}
	return root, cleanup, nil
}

func durableAcquireParent(req Request, key string) (string, error) {
	if !explicitAbs(req.ControlRoot) {
		return "", fmt.Errorf("%w: control_root_required", ErrRefused)
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(filepath.Dir(req.ControlRoot), "uap", "acquired-source", hex.EncodeToString(sum[:])), nil
}

func archiveChecksum(path, expected string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	got := hex.EncodeToString(sum.Sum(nil))
	if expected != "" && !strings.EqualFold(got, expected) {
		return "", fmt.Errorf("%w: got %s", portableasset.ErrChecksumMismatch, got)
	}
	return got, nil
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
