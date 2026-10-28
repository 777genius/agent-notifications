package qoderinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/installruntime"
	processadapter "github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/process"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/qoder"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

func qoderProfile(root string) (string, error) { return qoder.New().ResolveProfileRoot(root) }
func uapRoot(control string) string            { return filepath.Join(filepath.Dir(control), "uap", "qoder") }
func newEngine(r Request) (*uapinstaller.Engine, error) {
	registry, err := clients.NewRegistry(qoder.New())
	if err != nil {
		return nil, err
	}
	root := uapRoot(r.ControlRoot)
	runner := r.Runner
	if runner == nil {
		runner = processadapter.OS{}
	}
	helper := r.BinarySource
	if helper == "" && r.RuntimeRoot != "" {
		if name, ok := sharedBinaryName(r.GOOS, r.GOARCH); ok {
			helper = filepath.Join(r.RuntimeRoot, name)
		}
	}
	return uapinstaller.New(uapinstaller.Config{HelperExecutable: helper, StateRoot: filepath.Join(root, "state"), ManagedRoot: filepath.Join(root, "managed"), PluginDataBase: filepath.Join(root, "plugin-data"), Registry: registry, Runner: runner, TrustedLocalPackages: true, EnableNativeObserver: true, ClientExecutables: map[string]string{"qoder": r.QoderExecutable}})
}
func hasUAPBinding(ctx context.Context, eng *uapinstaller.Engine, id string) (bool, error) {
	view, err := eng.Inspect(ctx)
	if err != nil {
		return false, err
	}
	if view.Recovery.Required {
		return false, uapinstaller.ErrRecoveryRequired
	}
	for _, i := range view.Installations {
		for _, b := range i.Bindings {
			if b.BindingID == id {
				return true, nil
			}
		}
	}
	return false, nil
}
func checkUAPState(control string, r receipt) error {
	state, err := (statev2.Store{Path: filepath.Join(uapRoot(control), "state", "state-v2.json")}).Load()
	if err != nil {
		return err
	}
	for _, i := range state.Installations {
		if i.InstallationID != r.InstallationID {
			continue
		}
		b, ok := i.Clients[r.UAPBindingID]
		if !ok || b.ClientID != "qoder" || b.Scope != "user" || b.NativeProfileRoot != r.ConfigRoot || b.TargetLocator != r.ManagedRoot || b.PackageRevision == nil || b.PackageRevision.TreeDigest != r.TreeDigest || b.Materialization != domain.MaterializationMaterialized || b.Activation != domain.ActivationActive || b.Verification != domain.VerificationInstalled || b.PendingNativeIntent != nil || b.NativeActivationAttempt != "" {
			return errors.New("qoder UAP binding differs from the host receipt or native activation is incomplete")
		}

		projectionMatches := false
		for _, object := range b.NativeObjects {
			if object.Path == r.ManagedRoot && object.ManagedDigest == r.ProjectionDigest && object.Kind == "managed_package_directory" {
				projectionMatches = true
			}
		}
		if !projectionMatches {
			return errors.New("qoder UAP projection differs from the host receipt")
		}
		return nil
	}
	return errors.New("qoder UAP installation missing")
}
func checkUAP(ctx context.Context, r Request, previous receipt) error {
	if err := checkUAPState(r.ControlRoot, previous); err != nil {
		return err
	}
	eng, err := newEngine(r)
	if err != nil {
		return err
	}
	view, err := eng.Inspect(ctx)
	if err != nil {
		return err
	}
	if view.Recovery.Required {
		return uapinstaller.ErrRecoveryRequired
	}
	for _, i := range view.Installations {
		if i.InstallationID == previous.InstallationID {
			for _, b := range i.Bindings {
				if b.BindingID == previous.UAPBindingID {
					if b.ProjectionStatus != "" {
						return fmt.Errorf("qoder managed package is %s", b.ProjectionStatus)
					}
					return nil
				}
			}
		}
	}
	return errors.New("qoder UAP binding missing")
}

func nativeRegistration(profile string) (json.RawMessage, json.RawMessage, string, error) {
	reg, err := readDocument(filepath.Join(profile, filepath.FromSlash(registryName)))
	if err != nil {
		return nil, nil, "", err
	}
	entry, err := nativeEntry(reg.data)
	if err != nil {
		return nil, nil, "", err
	}
	var rows []struct{ Scope, InstallPath, Version string }
	if json.Unmarshal(entry, &rows) != nil || len(rows) != 1 || rows[0].Scope != "user" || !inside(filepath.Join(profile, "plugins", "cache", "local", "agent-notifications-qoder"), rows[0].InstallPath) {
		return nil, nil, "", errors.New("qoder native registration has no exact user package identity")
	}
	settings, err := readDocument(filepath.Join(profile, "settings.json"))
	if err != nil {
		return nil, nil, "", err
	}
	enabled, err := nativeEnabled(settings.data)
	if err != nil || compact(enabled) != "true" {
		return nil, nil, "", errors.New("qoder native activation is not enabled")
	}
	return entry, enabled, rows[0].InstallPath, nil
}

func snapshotFiles(root string) (map[string]installruntime.Identity, error) {
	files := map[string]installruntime.Identity{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("qoder native package contains a nonregular file")
		}
		id, err := installruntime.Fingerprint(path)
		if err != nil {
			return err
		}
		if !id.Exists || id.Link != "" {
			return errors.New("qoder native package changed during observation")
		}
		files[path] = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("qoder native package is empty")
	}
	return files, nil
}
func attestNativePackage(ctx context.Context, r Request, eng *uapinstaller.Engine, next *receipt) error {
	view, err := eng.Inspect(ctx)
	if err != nil {
		return err
	}
	if view.Recovery.Required {
		return uapinstaller.ErrRecoveryRequired
	}
	for _, i := range view.Installations {
		if i.InstallationID == next.InstallationID {
			for _, b := range i.Bindings {
				if b.BindingID == next.UAPBindingID {
					next.ProjectionDigest = b.ProjectionDigest
				}
			}
		}
	}
	if next.ProjectionDigest == "" {
		return errors.New("qoder UAP projection receipt missing")
	}
	entry, enabled, path, err := nativeRegistration(r.ConfigRoot)
	if err != nil {
		return err
	}
	if path != filepath.Join(r.ConfigRoot, "plugins", "cache", "local", "agent-notifications-qoder", r.Version) {
		return errors.New("qoder native cache path differs from the selected release")
	}
	files, err := compareNativeCopy(next.ManagedRoot, path)
	if err != nil {
		return err
	}
	next.PackageRoot, next.NativeEntry, next.NativeEnabled, next.Files = path, entry, enabled, files
	return checkUAPState(r.ControlRoot, *next)
}
func checkNativeSnapshot(r receipt) error {
	if r.Pending || !filepath.IsAbs(r.PackageRoot) || len(r.Files) == 0 {
		return errors.New("qoder native snapshot missing")
	}
	entry, enabled, path, err := nativeRegistration(r.ConfigRoot)
	if err != nil {
		return err
	}
	if path != r.PackageRoot || compact(entry) != compact(r.NativeEntry) || compact(enabled) != compact(r.NativeEnabled) {
		return errors.New("qoder native registration or activation was edited")
	}
	current, err := snapshotFiles(r.PackageRoot)
	if err != nil {
		return err
	}
	if len(current) != len(r.Files) {
		return errors.New("qoder native package contains an unowned file")
	}
	for p, want := range r.Files {
		if !inside(r.PackageRoot, p) || current[p] != want {
			return errors.New("qoder native package was edited or removed")
		}
	}
	return nil
}
func removeInstallation(ctx context.Context, r Request, eng *uapinstaller.Engine, previous receipt) error {
	if err := setChannels(ctx, r.ControlRoot, r.RuntimeRoot, false, false, r.ChannelPreimage); err != nil {
		return err
	}
	if !previous.Removing {
		previous.Pending, previous.Removing = true, true
		if err := publishReceipt(ctx, r, previous); err != nil {
			return err
		}
	}
	present, err := hasUAPBinding(ctx, eng, previous.UAPBindingID)
	if err != nil {
		return err
	}
	if present {
		p, err := eng.Prepare(ctx, uapinstaller.Request{Operation: uapinstaller.OpRemove, ClientID: "qoder", ClientConfigRoot: r.ConfigRoot, ClientExecutable: r.QoderExecutable, InstallationID: previous.InstallationID})
		if err != nil {
			return err
		}
		defer func() { _ = p.Close() }()
		if _, err = eng.Apply(ctx, p, uapinstaller.Decision{Confirmed: true}); err != nil {
			return fmt.Errorf("qoder UAP removal incomplete; consent remains off: %w", err)
		}
	}
	if r.Fault != nil {
		if err := r.Fault("qoder_uap_removed"); err != nil {
			return err
		}
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return err
	}
	path := filepath.Join(r.ControlRoot, receiptName)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	req := installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: ConsumerID, RemoveConsumer: true, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage}
	if len(s.Installation.Ledger.Consumers) > 1 {
		req.Prepare = func() ([]installruntime.File, error) {
			return []installruntime.File{{Path: path, Before: before, Remove: true}}, nil
		}
	}
	_, err = installruntime.Commit(ctx, req)
	return err
}
func recoverInstallation(ctx context.Context, r Request) error {
	if _, err := installruntime.Recover(ctx, r.ControlRoot); err != nil {
		return err
	}
	eng, err := newEngine(r)
	if err != nil {
		return err
	}
	view, err := eng.Inspect(ctx)
	if err != nil {
		return err
	}
	if view.Recovery.Required {
		if _, err = eng.Recover(ctx, view); err != nil {
			return err
		}
	}
	l, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		return err
	}
	if recovery {
		return errors.New("runtime recovery remains pending")
	}
	if _, ok := l.Consumers[ConsumerID]; ok {
		previous, err := readReceipt(r.ControlRoot, l)
		if err != nil {
			return err
		}
		if previous.Pending {
			return errors.New("qoder UAP/runtime recovery completed; installation remains incomplete with consent off; retry install or remove")
		}
		return checkUAP(ctx, r, previous)
	}
	return nil
}

func compareNativeCopy(managed, path string) (map[string]installruntime.Identity, error) {
	files, err := snapshotFiles(path)
	if err != nil {
		return nil, err
	}
	source, err := snapshotFiles(managed)
	if err != nil {
		return nil, err
	}
	if len(files) != len(source) {
		return nil, errors.New("qoder native cache contains an unexpected file inventory")
	}
	for p, want := range source {
		rel, err := filepath.Rel(managed, p)
		if err != nil {
			return nil, err
		}
		got, ok := files[filepath.Join(path, rel)]
		if !ok || want.SHA256 != got.SHA256 || want.Mode&0111 != 0 && got.Mode&0111 == 0 {
			return nil, errors.New("qoder native package differs from UAP's delivered bytes")
		}
	}
	return files, nil
}

// A pending handoff is never a license to overwrite a later user edit. The
// prepared UAP package is the expected copy until the host receipt is complete.
func verifyPendingNative(control string, r receipt) error {
	reg, err := readDocument(filepath.Join(r.ConfigRoot, filepath.FromSlash(registryName)))
	if err != nil {
		return err
	}
	entry, err := nativeEntry(reg.data)
	if err != nil {
		return err
	}
	if len(entry) == 0 {
		return nil
	}
	state, err := (statev2.Store{Path: filepath.Join(uapRoot(control), "state", "state-v2.json")}).Load()
	if err != nil {
		return err
	}
	found := false
	for _, i := range state.Installations {
		if i.InstallationID != r.InstallationID {
			continue
		}
		b, ok := i.Clients[r.UAPBindingID]
		if ok && b.ClientID == "qoder" && b.NativeProfileRoot == r.ConfigRoot && b.TargetLocator == r.ManagedRoot {
			found = true
		}
	}
	// Completed removal no longer addresses the native registry. Host-owned
	// cleanup can finish while preserving any subsequently registered plugin.
	if !found && r.Removing {
		return nil
	}
	if !found {
		return errors.New("pending Qoder native registration has no exact UAP owner")
	}
	if len(r.Files) > 0 && compact(entry) == compact(r.NativeEntry) {
		r.Pending = false
		return checkNativeSnapshot(r)
	}
	_, _, path, err := nativeRegistration(r.ConfigRoot)
	if err != nil {
		return err
	}

	_, err = compareNativeCopy(r.ManagedRoot, path)
	return err
}

// Native CLI copying must not adopt an orphan directory merely because its
// plugin name/version looks right. Only an existing exact host/UAP receipt can
// authorize reuse of an already populated destination.
func checkCacheDestination(r Request, previous *receipt) error {
	path := filepath.Join(r.ConfigRoot, "plugins", "cache", "local", "agent-notifications-qoder", r.Version)
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if previous == nil {
		return errors.New("qoder native cache destination exists without managed ownership")
	}
	if path == previous.PackageRoot && len(previous.Files) > 0 {
		files, err := snapshotFiles(path)
		if err != nil {
			return err
		}
		if len(files) != len(previous.Files) {
			return errors.New("qoder native cache contains an unowned file")
		}
		for p, want := range previous.Files {
			if files[p] != want {
				return errors.New("qoder native cache was edited")
			}
		}
		return nil
	}
	if previous.Pending {
		state, err := (statev2.Store{Path: filepath.Join(uapRoot(r.ControlRoot), "state", "state-v2.json")}).Load()
		if err != nil {
			return err
		}
		for _, i := range state.Installations {
			if i.InstallationID != previous.InstallationID {
				continue
			}
			b, ok := i.Clients[previous.UAPBindingID]
			if ok && b.ClientID == "qoder" && b.NativeProfileRoot == r.ConfigRoot && b.TargetLocator == previous.ManagedRoot {
				_, err = compareNativeCopy(previous.ManagedRoot, path)
				return err
			}
		}
	}
	return errors.New("qoder native cache destination has no exact managed receipt")
}
