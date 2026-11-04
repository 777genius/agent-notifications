package installruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

func orphanControl(root string) error {
	if !cleanPhysicalPath(root) {
		return orphanConflict("control", root, fmt.Errorf("existing physical control root required"))
	}
	if _, err := pathAnchors(filepath.Join(root, ".identity"), false); err != nil {
		return orphanConflict("control", root, err)
	}
	if err := privateDirectory(root); err != nil {
		return orphanConflict("control", root, err)
	}
	// Observe permanent lock inodes without acquiring a lock or creating state.
	// Preview must not claim admission if commit would have to create a lock.
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		path := filepath.Join(root, name)
		f, err := openLock(path, false)
		if err != nil {
			return orphanConflict("control_lock", path, err)
		}
		opened, statErr := f.Stat()
		named, nameErr := os.Lstat(path)
		_ = f.Close()
		if statErr != nil || nameErr != nil || !os.SameFile(opened, named) {
			return orphanConflict("control_lock", path, fmt.Errorf("permanent lock inode changed"))
		}
	}
	return nil
}

func prepareOrphan(root string, req OrphanRecoveryRequest) (Ledger, orphanDecision, error) {
	d := orphanDecision{ControlRoot: root, Request: req}
	var l Ledger
	if err := orphanControl(root); err != nil {
		return l, d, err
	}
	var err error
	l, err = readOrphanLedger(root)
	if err != nil {
		return l, d, orphanConflict("ledger", root, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "transaction.json")); !os.IsNotExist(err) {
		if err == nil {
			err = ErrPolicyRecovery
		}
		return l, d, orphanConflict("pending_transaction", root, err)
	}
	if err := checkPolicyGeneration(root, l); err != nil {
		return l, d, orphanConflict("policy_generation", root, err)
	}
	var wrappers []string
	d.Files, wrappers, err = orphanSelection(root, l, req)
	if err != nil {
		return l, d, err
	}
	d.SourcePolicyGeneration = l.PolicyGeneration
	for _, p := range d.Files {
		d.Identities = append(d.Identities, l.Files[p])
	}
	policy, _, preimage, err := readPolicyForUpdate(root)
	if err != nil || policy.Enabled != l.Enabled {
		if err == nil {
			err = fmt.Errorf("policy intent differs from ledger")
		}
		return l, d, orphanConflict("policy", root, err)
	}
	d.Policy = preimage
	d.ControlParents, d.ControlLocks, err = observeOrphanControl(root)
	if err != nil {
		return l, d, orphanConflict("control", root, err)
	}
	for _, p := range orphanRuntimePaths(d.Files, wrappers) {
		a, err := observeOrphanAbsence(p)
		if err != nil {
			return l, d, orphanConflict("payload_present", p, err)
		}
		d.RuntimeAbsences = append(d.RuntimeAbsences, a)
	}
	if req.Consumer.Registration != "" {
		a, err := observeOrphanAbsence(req.Consumer.Registration)
		if err != nil {
			return l, d, orphanConflict("registration_present", req.Consumer.Registration, err)
		}
		d.RegistrationAbsence = &a
	}
	if err := validateOrphanAssets(l, d); err != nil {
		return l, d, err
	}
	return l, d, nil
}

func readOrphanLedger(root string) (Ledger, error) {
	l, err := readLedger(root)
	if err != nil {
		return l, err
	}
	raw, err := readRegularFile(filepath.Join(root, "ownership.json"))
	if err != nil {
		return l, err
	}
	if err := validateOrphanJSON(raw, reflect.TypeOf(Ledger{}), false); err != nil {
		return l, err
	}
	var again Ledger
	if err := json.Unmarshal(raw, &again); err != nil {
		return l, err
	}
	if !reflect.DeepEqual(l, again) {
		return l, fmt.Errorf("ownership changed while reading orphan preimage")
	}
	return l, nil
}

func observeOrphanControl(root string) ([]PathAnchor, []PathAnchor, error) {
	parents, err := pathAnchors(filepath.Join(root, "agent-notifications.json"), false)
	if err != nil {
		return nil, nil, err
	}
	var locks []PathAnchor
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		path := filepath.Join(root, name)
		id, err := orphanPermanentLockIdentity(path)
		if err != nil {
			return nil, nil, err
		}
		locks = append(locks, PathAnchor{Path: path, Identity: id})
	}
	return parents, locks, nil
}

// PreviewOrphanConsumer never acquires locks, creates directories, executes
// helpers, or writes files. It requires the same existing-installation predicate.
func PreviewOrphanConsumer(controlRoot string, req OrphanRecoveryRequest) (OrphanRecoveryPreview, error) {
	p := OrphanRecoveryPreview{ConsumerID: req.ConsumerID, RuntimeRoot: req.RuntimeRoot}
	l, d, err := prepareOrphan(controlRoot, req)
	p.InstallationID, p.Generation = l.ID, l.Generation
	p.SelectedPaths = append([]string(nil), d.Files...)
	p.SelectedCount, p.ExpectedPolicy = len(p.SelectedPaths), d.Policy
	if err == nil {
		refreshed, e := refreshLedgerIdentities(l)
		err = e
		p.NativeValidated = e == nil
		p.NativeIdentityRefresh = e == nil && !reflect.DeepEqual(l.Native, refreshed.Native)
	}
	p.Admissible = err == nil
	if conflict, ok := err.(*OrphanRecoveryConflict); ok {
		p.ConflictCode, p.ConflictPath = conflict.Code, conflict.Path
	} else if err != nil {
		p.ConflictCode = "native"
	}
	return p, err
}

// RecoverOrphanConsumer removes only absent ownership metadata. The request is
// fenced again under the permanent component and policy CAS locks.
func RecoverOrphanConsumer(ctx context.Context, controlRoot string, req OrphanRecoveryRequest) (Ledger, error) {
	return Commit(ctx, Request{ControlRoot: controlRoot, OrphanRecovery: &req})
}

func commitOrphan(ctx context.Context, r Request) (Ledger, error) {
	allowed := Request{ControlRoot: r.ControlRoot, OrphanRecovery: r.OrphanRecovery, ExpectedPolicy: r.ExpectedPolicy, Fault: r.Fault}
	// Functions are not comparable with DeepEqual; remove the authorized seam.
	actual := r
	actual.Fault, allowed.Fault = nil, nil
	if !reflect.DeepEqual(actual, allowed) {
		return Ledger{}, fmt.Errorf("orphan recovery cannot combine mutation modes")
	}
	encoded, err := json.Marshal(r.OrphanRecovery)
	if err != nil {
		return Ledger{}, err
	}
	var req OrphanRecoveryRequest
	if err := json.Unmarshal(encoded, &req); err != nil {
		return Ledger{}, err
	}
	if err := validateOrphanRequest(req); err != nil {
		return Ledger{}, err
	}
	if err := orphanControl(r.ControlRoot); err != nil {
		return Ledger{}, err
	}
	parents, locks, err := observeOrphanControl(r.ControlRoot)
	if err != nil {
		return Ledger{}, err
	}
	release, err := LockExisting(ctx, filepath.Join(r.ControlRoot, ".component-install.lock"))
	if err != nil {
		return Ledger{}, err
	}
	defer release()
	releasePolicy, err := LockExisting(ctx, filepath.Join(r.ControlRoot, "agent-notifications.json.lock"))
	if err != nil {
		return Ledger{}, err
	}
	defer releasePolicy()
	l, d, err := prepareOrphan(r.ControlRoot, req)
	if err != nil {
		return l, err
	}
	if err := checkPersistedAnchors(parents, d.ControlParents); err != nil {
		return l, err
	}
	parentDev := objectDevice(d.ControlParents[len(d.ControlParents)-1].Identity)
	for i, lock := range locks {
		if lock.Path != d.ControlLocks[i].Path || !MatchPersistedDirectory(lock.Identity, d.ControlLocks[i].Identity, parentDev) {
			return l, orphanConflict("control_lock", lock.Path, fmt.Errorf("control changed while acquiring permanent locks"))
		}
	}
	if r.ExpectedPolicy != nil && *r.ExpectedPolicy != d.Policy {
		return l, ErrPolicyConflict
	}
	next := cloneOrphanLedger(l)
	delete(next.Consumers, req.ConsumerID)
	for _, path := range d.Files {
		delete(next.Files, path)
	}
	next.Generation++
	next.PolicyGeneration++
	next, err = refreshLedgerIdentities(next)
	if err != nil {
		return l, err
	}
	tx := transaction{Schema: transactionSchemaV5, Before: l, After: next, Files: []File{}, ConfigPaths: []string{filepath.Join(r.ControlRoot, "agent-notifications.json")}, OrphanRecovery: &d}
	if err := validateOrphanTransaction(tx); err != nil {
		return l, err
	}
	if err := validateOrphanLive(r.ControlRoot, tx); err != nil {
		return l, err
	}
	if err := writeTransaction(filepath.Join(r.ControlRoot, "transaction.json"), tx); err != nil {
		return l, err
	}
	if r.Fault != nil {
		if err := r.Fault("transaction"); err != nil {
			return l, err
		}
	}
	if err := recoverTransaction(ctx, r.ControlRoot, l, tx, r.Fault); err != nil {
		return l, err
	}
	return readLedger(r.ControlRoot)
}
