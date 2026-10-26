package installruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// CacheRepairPlan is a read-only, exact observation. It authorizes no delivery
// and carries no replacement bytes. RepairCache revalidates it under the locks.
type CacheRepairPlan struct {
	ID, LedgerSHA256 string
	Generation       uint64
	Policy           Identity
	Paths            []string
}

// PreviewCacheRepair never creates locks, directories, payloads or journals.
// Only absent owned files in recorded Claude version caches are repairable.
func PreviewCacheRepair(ctx context.Context, root string) (CacheRepairPlan, error) {
	var zero CacheRepairPlan
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if root == "" {
		var err error
		root, err = ControlRoot()
		if err != nil {
			return zero, err
		}
	}
	if err := privateDirectory(root); os.IsNotExist(err) {
		return zero, nil
	} else if err != nil {
		return zero, err
	}
	release, err := LockExisting(ctx, filepath.Join(root, ".component-install.lock"))
	if err != nil {
		return zero, err
	}
	defer release()
	releasePolicy, err := LockExisting(ctx, filepath.Join(root, "agent-notifications.json.lock"))
	if err != nil {
		return zero, err
	}
	defer releasePolicy()
	plan, _, _, err := previewCacheRepairLocked(ctx, root)
	return plan, err
}

// RepairCache restores exact identities, preserving consumers, native records
// and policy bytes. Normal transaction redo protects interrupted publication.
func RepairCache(ctx context.Context, root string, expected CacheRepairPlan) (Ledger, error) {
	return repairCache(ctx, root, expected, nil)
}

func repairCache(ctx context.Context, root string, expected CacheRepairPlan, fault func(string) error) (Ledger, error) {
	if root == "" {
		var err error
		root, err = ControlRoot()
		if err != nil {
			return Ledger{}, err
		}
	}
	plan, err := PreviewCacheRepair(ctx, root)
	if err != nil {
		return Ledger{}, err
	}
	if !reflect.DeepEqual(plan, expected) {
		return Ledger{}, fmt.Errorf("cache repair observation changed")
	}
	if len(plan.Paths) == 0 {
		l, _, err := ReadOwnership(root)
		return l, err
	}
	l, recovery, err := ReadOwnership(root)
	if err != nil || recovery {
		return Ledger{}, fmt.Errorf("cache repair ownership unavailable")
	}
	ids := make([]string, 0, len(l.Consumers))
	for id := range l.Consumers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return l, fmt.Errorf("cache repair requires an existing consumer")
	}
	r := Request{ControlRoot: root, RuntimeRoot: l.Consumers[ids[0]].RuntimeRoot,
		Owner: "existing-installer", ConsumerID: ids[0], RefreshOnly: true,
		ExpectedGeneration: &expected.Generation, ExpectedPolicy: &expected.Policy, cacheRepair: true, Fault: fault}
	r.Prepare = func() ([]File, error) {
		current, _, files, err := previewCacheRepairLocked(ctx, root)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(current, expected) {
			return nil, fmt.Errorf("cache repair observation changed")
		}
		return files, nil
	}
	return Commit(ctx, r)
}

func cacheRuntimeRoot(root string) bool {
	// Reuse the same version and package grammar as explicit cache relocation.
	other := filepath.Join(filepath.Dir(root), "0.0.0")
	if other == root {
		other = filepath.Join(filepath.Dir(root), "0.0.1")
	}
	return filepath.IsAbs(root) && filepath.Clean(root) == root && versionedClaudeCachePeers(root, other)
}

func cacheOwnedPath(l Ledger, path string) bool {
	for _, c := range l.Consumers {
		if cacheRuntimeRoot(c.RuntimeRoot) && pathWithinRoot(c.RuntimeRoot, path) {
			return true
		}
	}
	return cacheRuntimeRoot(l.RuntimeRoot) && pathWithinRoot(l.RuntimeRoot, path)
}

// Called only while holding the component and policy locks, including Prepare.
func previewCacheRepairLocked(ctx context.Context, root string) (CacheRepairPlan, Ledger, []File, error) {
	var p CacheRepairPlan
	l, err := readLedger(root)
	if err != nil {
		return p, l, nil, err
	}
	if _, err = os.Lstat(filepath.Join(root, "transaction.json")); !os.IsNotExist(err) {
		return p, l, nil, fmt.Errorf("cache repair requires no pending transaction")
	}
	if l.PendingMutation != nil || l.WriterFloor < 0 || l.WriterFloor > SupportedWriterFloor || l.DecoderFloor < 0 ||
		(l.Native == nil && l.DecoderFloor != 0) || (l.Native != nil && l.DecoderFloor != l.Native.DecoderFloor) {
		return p, l, nil, fmt.Errorf("cache repair requires compatible unreserved ownership")
	}
	if l.ID != "" && l.Owner != "existing-installer" {
		return p, l, nil, fmt.Errorf("foreign installation owner")
	}
	if err = checkPolicyGeneration(root, l); err != nil {
		return p, l, nil, err
	}
	_, _, policy, err := readPolicyForUpdate(root)
	if err != nil {
		return p, l, nil, err
	}
	if l.Native != nil {
		if err = checkSnapshotNative(l.Native); err != nil {
			return p, l, nil, err
		}
	}
	p.ID, p.Generation, p.Policy = l.ID, l.Generation, policy
	ledgerBytes, err := json.Marshal(l)
	if err != nil {
		return p, l, nil, err
	}
	digest := sha256.Sum256(ledgerBytes)
	p.LedgerSHA256 = hex.EncodeToString(digest[:])
	paths := make([]string, 0, len(l.Files))
	for path := range l.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	present := map[string]Identity{}
	for _, path := range paths {
		if err = ctx.Err(); err != nil {
			return p, l, nil, err
		}
		want := l.Files[path]
		got, e := Fingerprint(path)
		if e != nil {
			return p, l, nil, e
		}
		if got == want {
			present[path] = got
			continue
		}
		if got.Exists || !want.Exists || !cacheOwnedPath(l, path) {
			return p, l, nil, fmt.Errorf("managed fingerprint changed without transaction: %s", path)
		}
		p.Paths = append(p.Paths, path)
	}
	files := make([]File, 0, len(p.Paths))
	for _, path := range p.Paths {
		want := l.Files[path]
		file := File{Path: path, Link: want.Link, Mode: want.Mode}
		if want.Link == "" {
			file.Data, err = cacheReplica(root, paths, present, want)
		} else {
			err = cacheRepairAlias(l, path, want.Link)
		}
		if err != nil {
			return p, l, nil, fmt.Errorf("cannot repair %s: %w", path, err)
		}
		files = append(files, file)
	}
	// Writer aliases require their regular target in the same transaction. An
	// unchanged target may be republished only after its full identity is checked.
	for _, file := range append([]File(nil), files...) {
		if file.Link == "" || !managedWriter(file.Path) {
			continue
		}
		target := filepath.Clean(filepath.Join(filepath.Dir(file.Path), file.Link))
		if filepath.IsAbs(file.Link) {
			target = file.Link
		}
		included := false
		for _, staged := range files {
			if staged.Path == target {
				included = true
			}
		}
		if included {
			continue
		}
		want, ok := present[target]
		if !ok || !want.Exists || want.Link != "" || !managedWriter(target) {
			return p, l, nil, fmt.Errorf("writer alias target unavailable")
		}
		data, e := readRegularFile(target)
		if e != nil || identity(data, want.Mode) != want {
			return p, l, nil, fmt.Errorf("writer alias target changed")
		}
		files = append(files, File{Path: target, Before: want, Data: data, Mode: want.Mode})
	}
	return p, l, files, ctx.Err()
}

func cacheReplica(root string, paths []string, present map[string]Identity, want Identity) ([]byte, error) {
	for _, path := range paths {
		if present[path] != want {
			continue
		}
		data, err := readRegularFile(path)
		if err != nil {
			return nil, err
		}
		if identity(data, want.Mode) != want {
			return nil, fmt.Errorf("managed replica changed")
		}
		return data, nil
	}
	if !ownedTransactionBlobName(want.SHA256) {
		return nil, fmt.Errorf("invalid recovery payload identity")
	}
	data, err := readRegularFile(filepath.Join(root, "cache-repair.blobs", want.SHA256))
	if err != nil {
		return nil, fmt.Errorf("exact owned replica unavailable: %w", err)
	}
	if identity(data, want.Mode) != want {
		return nil, fmt.Errorf("recovery payload checksum mismatch")
	}
	return data, nil
}

func cacheRepairAlias(l Ledger, path, link string) error {
	if filepath.IsAbs(link) {
		if l.Native == nil {
			return fmt.Errorf("native alias target unavailable")
		}
		for _, gen := range importNativeGenerations(*l.Native) {
			if gen.Path != link {
				continue
			}
			if err := checkNativeDirectoryID(gen.Path, gen.DirectoryID); err != nil {
				return err
			}
			digest, err := treeFingerprint(gen.Path)
			if err != nil {
				return err
			}
			if digest != gen.SHA256 {
				return fmt.Errorf("native alias target changed")
			}
			return nil
		}
		return fmt.Errorf("unmanaged absolute alias target")
	}
	target := filepath.Clean(filepath.Join(filepath.Dir(path), link))
	want, owned := l.Files[target]
	if !owned || !want.Exists || want.Link != "" || !cacheOwnedPath(l, target) {
		return fmt.Errorf("unmanaged relative alias target")
	}
	return nil // Its present bytes or exact recovery payload were checked above.
}

// Recovery payloads are optional support, never ordinary owned files. Retain
// only validated version-cache content; readers still authenticate every byte
// against the current ledger identity. A missing cache cannot break installation.
func retainCacheRepairPayloads(root string, l Ledger, files []File) {
	staged := map[string]File{}
	for _, file := range files {
		staged[file.Path] = file
	}
	for path, want := range l.Files {
		if !want.Exists || want.Link != "" || !cacheOwnedPath(l, path) || !ownedTransactionBlobName(want.SHA256) {
			continue
		}
		blob := filepath.Join(root, "cache-repair.blobs", want.SHA256)
		// Existing foreign or edited support files are never overwritten.
		if got, err := Fingerprint(blob); err != nil || got.Exists {
			continue
		}
		var data []byte
		if file, ok := staged[path]; ok {
			if desired(file) != want {
				continue
			}
			data = file.Data
		} else {
			got, err := Fingerprint(path)
			if err != nil || got != want {
				continue
			}
			data, err = readRegularFile(path)
			if err != nil {
				continue
			}
		}
		if identity(data, want.Mode) == want {
			_ = safePublish(File{Path: blob, Data: data, Mode: 0600}, true)
		}
	}
}

// Only self-verifying content-addressed children are ours to prune. Unknown
// names, edited bytes, links and directories remain untouched.
func pruneCacheRepairPayloads(root string, l Ledger) {
	dir := filepath.Join(root, "cache-repair.blobs")
	if err := privateDirectory(dir); err != nil {
		return
	}
	keep := map[string]bool{}
	for path, want := range l.Files {
		if cacheOwnedPath(l, path) && want.Exists && want.Link == "" {
			keep[want.SHA256] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if keep[name] || !ownedTransactionBlobName(name) {
			continue
		}
		path := filepath.Join(dir, name)
		anchors, err := pathAnchors(path, false)
		if err != nil {
			continue
		}
		got, err := Fingerprint(path)
		if err != nil || !got.Exists || got.Link != "" || got.SHA256 != name || got.Mode != IdentityMode(0600) {
			continue
		}
		_ = safePublish(File{Path: path, Parents: anchors, Before: got, Remove: true}, true)
	}
}
