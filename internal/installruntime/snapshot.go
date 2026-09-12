package installruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// InstalledSnapshot is a read-only observation, not an admission lease. Missing,
// corrupt or interrupted state never creates files or performs recovery.
// Delivery must revalidate with WithInstalledLease immediately before handoff,
// without holding a journal lock. The callback must be bounded by ctx.
type InstalledSnapshot struct {
	Ledger   Ledger
	Recovery bool
	Enabled  bool
}

func ReadInstalledSnapshot(root string) (InstalledSnapshot, error) {
	return readInstalledSnapshot(root, nil)
}

func readInstalledSnapshot(root string, requestPolicy *UserPolicy) (InstalledSnapshot, error) {
	var s InstalledSnapshot
	if root == "" {
		var err error
		root, err = ControlRoot()
		if err != nil {
			return s, err
		}
	}
	if err := privateDirectory(root); err != nil && !os.IsNotExist(err) {
		return s, err
	}
	l, err := readLedger(root)
	s.Ledger = l
	if err != nil {
		return s, err
	}
	if _, err = os.Lstat(filepath.Join(root, "transaction.json")); err == nil {
		s.Recovery = true
		return s, nil
	} else if !os.IsNotExist(err) {
		return s, err
	}
	if err = checkPolicyGeneration(root, l); err != nil {
		return s, err
	}
	for path, want := range l.Files {
		got, e := Fingerprint(path)
		if e != nil {
			return s, e
		}
		if got != want {
			return s, fmt.Errorf("installed file fingerprint mismatch: %s", path)
		}
	}
	if l.Native != nil {
		if err := validateNativeRecord(l.Native); err != nil {
			return s, err
		}
		if err := checkNativeDirectoryID(l.Native.Path, l.Native.DirectoryID); err != nil {
			return s, err
		}
		got, e := treeFingerprint(l.Native.Path)
		if e != nil {
			return s, e
		}
		if got != l.Native.SHA256 {
			return s, fmt.Errorf("installed native fingerprint mismatch")
		}
	}
	// Only an explicit policy transaction can enable admission.
	var policy UserPolicy
	if requestPolicy != nil {
		policy = *requestPolicy
	} else {
		policy, err = ReadUserPolicy(root)
		if err != nil {
			return s, err
		}
	}
	s.Enabled = l.Enabled && policy.Enabled
	return s, nil
}

// WithInstalledLease fences generation, owner, fingerprints and recovery until
// the bounded handoff returns. It never changes policy. Missing state
// is rejected without creating a control directory or lock.
func WithInstalledLease(ctx context.Context, root string, expected InstalledSnapshot, handoff func(InstalledSnapshot) error) error {
	current, release, err := AcquireInstalledLease(ctx, root, expected)
	if err != nil {
		return err
	}
	defer release()
	return handoff(current)
}

// AcquireInstalledLease revalidates the snapshot under the existing component
// lock. The caller must release it after bounded handoff (or readiness probing),
// and must not hold a journal lock. Errors release the lease automatically.
// Missing state never creates a directory or a replacement lock inode.
func AcquireInstalledLease(ctx context.Context, root string, expected InstalledSnapshot) (InstalledSnapshot, func(), error) {
	return acquireInstalledLease(ctx, root, expected, true)
}

// AcquireSetupLease pins an existing, unchanged installation for explicit setup
// probes. It does not authorize notification delivery or enable policy. The caller
// must qualify native protocol support before executing the retained helper.
func AcquireSetupLease(ctx context.Context, root string, expected InstalledSnapshot) (InstalledSnapshot, func(), error) {
	return acquireInstalledLease(ctx, root, expected, false)
}

func acquireInstalledLease(ctx context.Context, root string, expected InstalledSnapshot, requireEnabled bool) (InstalledSnapshot, func(), error) {
	var zero InstalledSnapshot
	if root == "" {
		var err error
		root, err = ControlRoot()
		if err != nil {
			return zero, nil, err
		}
	}
	if err := privateDirectory(root); err != nil {
		return zero, nil, err
	}
	path := filepath.Join(root, ".component-install.lock")
	release, err := LockExisting(ctx, path)
	if err != nil {
		return zero, nil, err
	}
	success := false
	defer func() {
		if !success {
			release()
		}
	}()
	current, err := ReadInstalledSnapshot(root)
	if err != nil {
		return zero, nil, err
	}
	if current.Recovery || current.Ledger.ID == "" || !reflect.DeepEqual(current, expected) {
		return zero, nil, fmt.Errorf("installed snapshot changed or recovery required")
	}
	if requireEnabled && !current.Enabled {
		return zero, nil, fmt.Errorf("explicit delivery is disabled")
	}
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	success = true
	return current, release, nil
}

// CheckPrivateControlRoot is read-only and shared by installed-state consumers.
// Missing roots are errors; it never creates or migrates state.
func CheckPrivateControlRoot(root string) error { return privateDirectory(root) }
