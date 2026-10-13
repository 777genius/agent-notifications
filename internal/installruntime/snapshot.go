package installruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

// ReadOwnership returns the durable ledger and whether a recovery marker is
// present. Unlike ReadInstalledSnapshot it does not fingerprint unrelated
// published files, so a single-path Prepare callback can still repair a
// missing sibling under the component lock.
func ReadOwnership(root string) (Ledger, bool, error) {
	var l Ledger
	if root == "" {
		var err error
		root, err = ControlRoot()
		if err != nil {
			return l, false, err
		}
	}
	if err := privateDirectory(root); err != nil && !os.IsNotExist(err) {
		return l, false, err
	}
	l, err := readLedger(root)
	if err != nil {
		return l, false, err
	}
	_, err = os.Lstat(filepath.Join(root, "transaction.json"))
	if err == nil {
		return l, true, nil
	}
	if !os.IsNotExist(err) {
		return l, false, err
	}
	return l, false, nil
}

// OwnedFile looks up a ledger identity by the published path or its canonical form.
func OwnedFile(l Ledger, path string) (Identity, bool) {
	if owned, ok := l.Files[path]; ok {
		return owned, true
	}
	canonical, err := CanonicalPath(path)
	if err != nil || canonical == path {
		return Identity{}, false
	}
	owned, ok := l.Files[canonical]
	return owned, ok
}

func readInstalledSnapshot(root string, requestPolicy *UserPolicy) (InstalledSnapshot, error) {
	var s InstalledSnapshot
	if root == "" {
		var err error
		root, err = ControlRoot()
		if err != nil {
			return s, snapshotFailure("control_invalid", root, err)
		}
	}
	if err := privateDirectory(root); err != nil && !os.IsNotExist(err) {
		return s, snapshotFailure("control_invalid", root, err)
	}
	l, err := readLedger(root)
	if err != nil {
		return s, snapshotFailure("ledger_invalid", filepath.Join(root, "ownership.json"), err)
	}
	s.Ledger = l
	if _, err = os.Lstat(filepath.Join(root, "transaction.json")); err == nil {
		s.Recovery = true
		return s, nil
	} else if !os.IsNotExist(err) {
		return s, snapshotFailure("control_invalid", filepath.Join(root, "transaction.json"), err)
	}
	if err = checkPolicyGeneration(root, l); err != nil {
		return s, snapshotFailure("policy_invalid", filepath.Join(root, "policy-generation.json"), err)
	}
	paths := make([]string, 0, len(l.Files))
	for path := range l.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		want := l.Files[path]
		got, e := Fingerprint(path)
		if e != nil {
			return s, snapshotFailure("managed_file_unreadable", path, e)
		}
		if got != want {
			cause := fmt.Errorf("installed file fingerprint mismatch: %s", path)
			code := "managed_file_changed"
			if want.Exists && !got.Exists {
				code = "managed_file_missing"
				// Fingerprint represents absence as a zero identity, not an error.
				// Retain the old mismatch text while making absence discoverable.
				return s, snapshotFailure(code, path, &snapshotMissingFileError{message: cause.Error()})
			}
			return s, snapshotFailure(code, path, cause)
		}
	}
	if l.Native != nil {
		if err := checkSnapshotNative(l.Native); err != nil {
			return s, err
		}
	}
	// Only an explicit policy transaction can enable admission.
	var policy UserPolicy
	if requestPolicy != nil {
		policy = *requestPolicy
	} else {
		policy, err = ReadUserPolicy(root)
		if err != nil {
			return s, snapshotFailure("policy_invalid", filepath.Join(root, "agent-notifications.json"), err)
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

// AcquirePolicyLease pins the exact observed policy and installation through a
// consumer's bounded handoff. Unlike AcquireInstalledLease, portable enablement
// is not an admission condition: the consumer must check its own registration
// and consent against the returned snapshot before using the retained native
// bundle. Both existing locks are retained until release.
func AcquirePolicyLease(ctx context.Context, root string, expected PolicySnapshot) (PolicySnapshot, func(), error) {
	var zero PolicySnapshot
	if root == "" {
		return zero, nil, fmt.Errorf("managed control root required")
	}
	if err := privateDirectory(root); err != nil {
		return zero, nil, err
	}
	componentRelease, err := LockExisting(ctx, filepath.Join(root, ".component-install.lock"))
	if err != nil {
		return zero, nil, err
	}
	success := false
	var configRelease func()
	defer func() {
		if !success {
			if configRelease != nil {
				configRelease()
			}
			componentRelease()
		}
	}()
	configRelease, err = LockExisting(ctx, filepath.Join(root, "agent-notifications.json.lock"))
	if err != nil {
		return zero, nil, err
	}
	current := PolicySnapshot{}
	current.Policy, current.Fields, current.Preimage, err = readPolicyForUpdate(root)
	if err != nil {
		return zero, nil, err
	}
	current.Installation, err = readInstalledSnapshot(root, &current.Policy)
	if err != nil {
		return zero, nil, err
	}
	if current.Installation.Recovery || current.Installation.Ledger.ID == "" ||
		current.Preimage != expected.Preimage || !reflect.DeepEqual(current.Installation, expected.Installation) {
		return zero, nil, fmt.Errorf("consumer policy or installation snapshot changed")
	}
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	success = true
	return current, func() { configRelease(); componentRelease() }, nil
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
