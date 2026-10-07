package installruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const nativeProductBundleID = "com.777genius.agent-notifications"
const preferredNativeRegistrationQuery = `ObjC.import("AppKit"); var url = $.NSWorkspace.sharedWorkspace.URLForApplicationWithBundleIdentifier("com.777genius.agent-notifications"); url ? ObjC.unwrap(url.path) : "";`

const launchServicesRegister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

// ReconcileNativeRegistration is a post-success adapter, never transaction redo.
// It leaves bundle bytes and published callback identities intact. Callers must
// report failure as a warning: the committed installation remains successful.
func ReconcileNativeRegistration(ctx context.Context, control string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return reconcileNativeRegistration(ctx, control, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.WaitDelay = time.Second
		return cmd.CombinedOutput()
	})
}

type nativeRegistrationRunner func(context.Context, string, ...string) ([]byte, error)

func reconcileNativeRegistration(ctx context.Context, control string, run nativeRegistrationRunner) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snapshot, err := ReadInstalledSnapshot(control)
	if err != nil {
		return err
	}
	if snapshot.Recovery {
		return fmt.Errorf("native registration deferred during installation recovery")
	}
	native := snapshot.Ledger.Native
	if native == nil {
		return nil
	}
	bundleID := func(path string) (string, error) {
		out, err := run(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", filepath.Join(path, "Contents", "Info.plist"))
		return strings.TrimSpace(string(out)), err
	}
	id, err := bundleID(native.Path)
	if err != nil || id != nativeProductBundleID {
		return fmt.Errorf("active native bundle identity is not the expected product: %s: %w", native.Path, errors.Join(err, fmt.Errorf("bundle ID %q", id)))
	}
	// Protect every retained generation, including conventional symlinks that
	// resolve to one. Never unregister a previously published callback reader.
	protected := map[string]bool{}
	paths := []string{native.Path, native.PreviousPath}
	for _, generation := range native.Published {
		paths = append(paths, generation.Path)
	}
	for _, generationPath := range paths {
		if generationPath == "" {
			continue
		}
		protected[filepath.Clean(generationPath)] = true
		if path, err := filepath.EvalSymlinks(generationPath); err == nil {
			protected[path] = true
		}
	}
	roots := map[string]bool{}
	for _, consumer := range snapshot.Ledger.Consumers {
		if consumer.RuntimeRoot != "" {
			roots[consumer.RuntimeRoot] = true
		}
	}
	var candidates []string
	for root := range roots {
		for _, relative := range []string{"bin/ClaudeNotifier.app", "bin/terminal-notifier.app", "swift-notifier/ClaudeNotifier.app"} {
			candidates = append(candidates, filepath.Join(root, filepath.FromSlash(relative)))
		}
	}
	sort.Strings(candidates)
	var failures []error
	for _, candidate := range candidates {
		path, err := filepath.EvalSymlinks(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if protected[path] {
			continue
		}
		// Conventional aliases pointing outside their registered runtime are
		// foreign. Product bundle ID alone never authorizes touching those apps.
		root := filepath.Dir(filepath.Dir(candidate))
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		id, err := bundleID(candidate)
		if err != nil {
			failures = append(failures, fmt.Errorf("read obsolete native bundle identity %s: %w", candidate, err))
			continue
		}
		if id != nativeProductBundleID {
			continue
		}
		current, err := os.Stat(candidate)
		canonical, resolveErr := filepath.EvalSymlinks(candidate)
		if err != nil || resolveErr != nil || !os.SameFile(info, current) || canonical != path {
			failures = append(failures, fmt.Errorf("obsolete native bundle changed during reconciliation: %s", candidate))
			continue
		}
		if _, err := run(ctx, launchServicesRegister, "-u", candidate); err != nil {
			failures = append(failures, fmt.Errorf("unregister obsolete native bundle %s: %w", candidate, err))
		}
	}
	if _, err := run(ctx, launchServicesRegister, "-f", native.Path); err != nil {
		failures = append(failures, fmt.Errorf("register active native bundle: %w", err))
	}
	active, activeErr := filepath.EvalSymlinks(native.Path)
	// Registration can succeed while a development copy remains preferred.
	// Observe that conflict, but never expand cleanup beyond consumer roots.
	if out, err := run(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", preferredNativeRegistrationQuery); err != nil {
		failures = append(failures, fmt.Errorf("query preferred native registration: %w", err))
	} else {
		preferred := strings.TrimSpace(string(out))
		canonical, err := filepath.EvalSymlinks(preferred)
		if err != nil || activeErr != nil || canonical != active {
			failures = append(failures, fmt.Errorf("LaunchServices still prefers a different callback reader %q; explicitly reconcile that registration", preferred))
		}
	}
	return errors.Join(failures...)
}
