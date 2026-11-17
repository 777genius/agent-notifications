package installruntime

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

func cloneOrphanLedger(l Ledger) Ledger {
	data, _ := json.Marshal(l)
	var copy Ledger
	_ = json.Unmarshal(data, &copy)
	return copy
}

func validateOrphanAssets(l Ledger, d orphanDecision) error {
	if l.DecoderFloor < 0 {
		return fmt.Errorf("invalid native decoder floor")
	}
	selected := map[string]bool{}
	for _, p := range d.Files {
		selected[p] = true
	}
	paths := make([]string, 0, len(l.Files))
	for p := range l.Files {
		if !selected[p] {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		got, err := Fingerprint(p)
		if err != nil {
			return orphanConflict("retained_file", p, err)
		}
		if got != l.Files[p] {
			return orphanConflict("retained_file", p, fmt.Errorf("managed fingerprint changed without transaction"))
		}
	}
	if l.Native == nil {
		if l.DecoderFloor != 0 {
			return fmt.Errorf("native decoder floor without record")
		}
		return nil
	}
	if l.DecoderFloor != l.Native.DecoderFloor {
		return fmt.Errorf("native decoder floor mismatch")
	}
	if err := validateOrphanPreviousNative(l.Native, d.ControlRoot); err != nil {
		return err
	}
	if err := validateRetainedNative(l.Native); err != nil {
		return orphanConflict("native", l.Native.Path, err)
	}
	if persistedDevRelaxed {
		if _, ok := nativeDeviceCorrespondence(l.Native); !ok {
			return orphanConflict("native", l.Native.Path, fmt.Errorf("retained native volume correspondence changed"))
		}
	}
	// Every present generation whose DirectoryID could be refreshed must first
	// prove its original bytes. Missing historical generations retain their IDs.
	for _, gen := range importNativeGenerations(*l.Native) {
		if gen.DecoderFloor < 0 || !cleanPhysicalPath(gen.Path) || filepath.Dir(gen.Path) != filepath.Join(d.ControlRoot, "native") {
			return fmt.Errorf("native generation escaped persistent owner")
		}
		if err := checkNativeDirectoryIDIfPresent(gen); err != nil {
			return orphanConflict("native", gen.Path, err)
		}
	}
	return nil
}

func validateOrphanPreviousNative(record *NativeRecord, root string) error {
	if record == nil || record.PreviousPath == "" && record.PreviousSHA256 == "" && record.PreviousDirectoryID == "" {
		return nil
	}
	// Import can omit an incomplete previous record or deduplicate it against
	// another generation, while identity refresh still visits its explicit path.
	// Qualify that original evidence independently before either operation.
	if !cleanPhysicalPath(record.PreviousPath) || filepath.Dir(record.PreviousPath) != filepath.Join(root, "native") {
		return orphanConflict("native", record.PreviousPath, fmt.Errorf("previous native generation escaped persistent owner"))
	}
	if !ownedTransactionBlobName(record.PreviousSHA256) || !validOrphanObjectID(record.PreviousDirectoryID) {
		return orphanConflict("native", record.PreviousPath, fmt.Errorf("incomplete previous native generation evidence"))
	}
	gen := NativeGeneration{Path: record.PreviousPath, SHA256: record.PreviousSHA256, DirectoryID: record.PreviousDirectoryID}
	if err := checkNativeDirectoryIDIfPresent(gen); err != nil {
		return orphanConflict("native", gen.Path, err)
	}
	return nil
}

func checkNativeDirectoryIDIfPresent(gen NativeGeneration) error {
	got, err := treeFingerprint(gen.Path)
	if err != nil {
		return err
	}
	if got == "" {
		return nil
	}
	if got != gen.SHA256 {
		return fmt.Errorf("retained native generation bytes changed")
	}
	return checkNativeDirectoryID(gen.Path, gen.DirectoryID)
}

func validateOrphanLive(root string, tx transaction) error {
	if err := validateOrphanTransaction(tx); err != nil {
		return err
	}
	d := *tx.OrphanRecovery
	if root != d.ControlRoot {
		return fmt.Errorf("orphan control root mismatch")
	}
	if err := orphanControl(root); err != nil {
		return err
	}
	current, err := readOrphanLedger(root)
	if err != nil {
		return err
	}
	if !ledgerMatchesJournal(current, tx.Before, nil) && !ledgerMatchesJournal(current, tx.After, nil) {
		return orphanConflict("installation_fence", root, fmt.Errorf("orphan transaction ledger snapshot mismatch"))
	}
	data, err := readRegularFile(filepath.Join(root, "policy-generation.json"))
	if err != nil {
		return err
	}
	var generation runtimePolicy
	if json.Unmarshal(data, &generation) != nil ||
		generation != (runtimePolicy{tx.Before.PolicyGeneration, tx.Before.Enabled}) &&
			(generation.Generation != tx.After.PolicyGeneration || generation.Enabled && generation.Enabled != tx.After.Enabled) &&
			(!tx.Rollback || generation != (runtimePolicy{d.SourcePolicyGeneration + 1, false})) {
		return orphanConflict("policy_generation", root, fmt.Errorf("policy generation does not belong to the pending decision"))
	}
	policy, _, preimage, err := readPolicyForUpdate(root)
	if err != nil {
		return err
	}
	if preimage != d.Policy || policy.Enabled != tx.After.Enabled {
		return ErrPolicyConflict
	}
	// Reuse the transaction-wide Darwin device bijection for every absence
	// chain together with the ordinary publication anchors.
	observation := tx
	observation.Files = append([]File(nil), tx.Files...)
	observation.Files = append(observation.Files, File{Path: filepath.Join(root, "agent-notifications.json"), Parents: d.ControlParents})
	absences := append([]orphanAbsence(nil), d.RuntimeAbsences...)
	if d.RegistrationAbsence != nil {
		absences = append(absences, *d.RegistrationAbsence)
	}
	for _, a := range absences {
		got, err := observeOrphanAbsence(a.Path)
		if err != nil {
			return orphanConflict("absence_changed", a.Path, err)
		}
		if got.Missing != a.Missing || len(got.Parents) != len(a.Parents) {
			return orphanConflict("absence_changed", a.Path, fmt.Errorf("first absent component changed"))
		}
		if err := checkPersistedAnchors(a.Parents, got.Parents); err != nil {
			return orphanConflict("absence_changed", a.Path, err)
		}
		observation.Files = append(observation.Files, File{Path: a.Path, Parents: a.Parents})
	}
	if err := preflightTransactionAnchors(observation); err != nil {
		return err
	}
	lockMapping := newDeviceRenumbering()
	if persistedDevRelaxed {
		for _, f := range observation.Files {
			fresh, err := pathAnchors(f.Path, false)
			if err != nil || !lockMapping.add(f.Parents, fresh) {
				return fmt.Errorf("orphan control/absence device mapping changed")
			}
		}
	}
	controlID, err := nativeDirectoryID(root)
	if err != nil {
		return err
	}
	parentDev := objectDevice(controlID)
	for _, lock := range d.ControlLocks {
		fresh, err := orphanPermanentLockIdentity(lock.Path)
		if err != nil || !MatchPersistedDirectory(lock.Identity, fresh, parentDev) {
			return orphanConflict("control_lock", lock.Path, fmt.Errorf("permanent lock inode changed"))
		}
		if persistedDevRelaxed && !lockMapping.add([]PathAnchor{lock}, []PathAnchor{{Path: lock.Path, Identity: fresh}}) {
			return orphanConflict("control_lock", lock.Path, fmt.Errorf("permanent lock device mapping changed"))
		}
	}
	// tx.Before can be a reverse decision without orphan metadata. Assets in
	// either image are identical once the selected absent paths are excluded.
	if err := validateOrphanAssets(tx.Before, d); err != nil {
		return err
	}
	return validateOrphanAssets(tx.After, d)
}

// Validate the exact reversible metadata delta without trusting the decision's
// file list, schema checksum, or the caller's replacement records.
func validateOrphanTransaction(tx transaction) error {
	if tx.Schema != transactionSchemaV5 || tx.OrphanRecovery == nil || tx.Native != nil || tx.OpenCodeInit != nil || tx.OpenCodePurge != nil || len(tx.Files) != 0 {
		return fmt.Errorf("schema5 requires only an orphan metadata decision")
	}
	d := *tx.OrphanRecovery
	if !cleanPhysicalPath(d.ControlRoot) || !reflect.DeepEqual(tx.ConfigPaths, []string{filepath.Join(d.ControlRoot, "agent-notifications.json")}) || d.Request.ExpectedGeneration > ^uint64(0)-2 || d.SourcePolicyGeneration == 0 || d.SourcePolicyGeneration > ^uint64(0)-2 {
		return fmt.Errorf("invalid orphan decision fence")
	}
	// Delta comparison can refresh identities on Darwin. Each ledger image
	// must prove its explicit previous generation before that normalization.
	for _, l := range []Ledger{tx.Before, tx.After} {
		if err := validateOrphanPreviousNative(l.Native, d.ControlRoot); err != nil {
			return err
		}
	}
	policyPath := filepath.Join(d.ControlRoot, "agent-notifications.json")
	if !validOrphanAbsence(orphanAbsence{Path: policyPath, Missing: policyPath, Parents: d.ControlParents}) || len(d.ControlLocks) != 2 {
		return fmt.Errorf("invalid orphan control anchors")
	}
	for i, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		if d.ControlLocks[i].Path != filepath.Join(d.ControlRoot, name) || !validOrphanObjectID(d.ControlLocks[i].Identity) {
			return fmt.Errorf("invalid orphan permanent lock identity")
		}
	}
	original := cloneOrphanLedger(tx.Before)
	if tx.Rollback {
		original = cloneOrphanLedger(tx.After)
		original.Generation, original.PolicyGeneration = d.Request.ExpectedGeneration, d.SourcePolicyGeneration
	}
	files, wrappers, err := orphanSelection(d.ControlRoot, original, d.Request)
	if err != nil {
		return err
	}
	if original.PolicyGeneration != d.SourcePolicyGeneration || !reflect.DeepEqual(files, d.Files) || len(files) != len(d.Identities) {
		return fmt.Errorf("invalid orphan ownership set")
	}
	for i, p := range files {
		if original.Files[p] != d.Identities[i] {
			return fmt.Errorf("orphan before identity changed")
		}
	}
	paths := orphanRuntimePaths(files, wrappers)
	if len(paths) != len(d.RuntimeAbsences) {
		return fmt.Errorf("invalid orphan absence set")
	}
	for i, path := range paths {
		if d.RuntimeAbsences[i].Path != path || !validOrphanAbsence(d.RuntimeAbsences[i]) {
			return fmt.Errorf("invalid runtime absence proof")
		}
	}
	if d.Request.Consumer.Registration == "" {
		if d.RegistrationAbsence != nil {
			return fmt.Errorf("unexpected registration proof")
		}
	} else if d.RegistrationAbsence == nil || d.RegistrationAbsence.Path != d.Request.Consumer.Registration || !validOrphanAbsence(*d.RegistrationAbsence) {
		return fmt.Errorf("invalid registration absence proof")
	}
	if d.Policy != (Identity{}) && (!meaningfulOrphanIdentity(d.Policy) || d.Policy.Link != "") {
		return fmt.Errorf("invalid policy preimage")
	}
	removed := cloneOrphanLedger(original)
	delete(removed.Consumers, d.Request.ConsumerID)
	for _, p := range files {
		delete(removed.Files, p)
	}
	removed.Generation++
	removed.PolicyGeneration++
	if !tx.Rollback {
		if !orphanLedgerDeltaEqual(removed, tx.After) {
			return fmt.Errorf("orphan decision exceeds permitted ledger delta")
		}
	} else {
		if !orphanLedgerDeltaEqual(original, tx.Before) && !orphanLedgerDeltaEqual(removed, tx.Before) {
			return fmt.Errorf("orphan rollback before-image mismatch")
		}
		original.Generation += 2
		original.PolicyGeneration += 2
		if !orphanLedgerDeltaEqual(original, tx.After) {
			return fmt.Errorf("orphan rollback exceeds permitted ledger delta")
		}
	}
	return nil
}

func orphanLedgerDeltaEqual(a, b Ledger) bool {
	// Existing conservative normalization proves every present inode and keeps
	// missing generations' recorded IDs. Never erase IDs to compare deltas.
	return ledgerMatchesJournal(a, b, nil)
}

func validOrphanAbsence(a orphanAbsence) bool {
	if !cleanPhysicalPath(a.Path) || !cleanPhysicalPath(a.Missing) || a.Missing != a.Path && !pathWithinRoot(a.Missing, a.Path) || len(a.Parents) == 0 || len(a.Parents) > 256 {
		return false
	}
	parent := filepath.VolumeName(a.Path) + string(filepath.Separator)
	for _, anchor := range a.Parents {
		// Windows includes the volume-root anchor; Unix starts at its first child.
		if anchor.Path != parent && filepath.Dir(anchor.Path) != parent {
			return false
		}
		if !validOrphanObjectID(anchor.Identity) {
			return false
		}
		parent = anchor.Path
	}
	return filepath.Dir(a.Missing) == parent
}

func validOrphanObjectID(id string) bool {
	if runtime.GOOS != "windows" {
		_, ino, ok := splitObjectID(id)
		return ok && ino != "0"
	}
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[1] == "0" && parts[2] == "0" {
		return false
	}
	for _, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return false
		}
	}
	return true
}

func reverseOrphanTransaction(current Ledger, tx transaction) (transaction, error) {
	if err := validateOrphanLive(tx.OrphanRecovery.ControlRoot, tx); err != nil {
		return transaction{}, err
	}
	after := cloneOrphanLedger(tx.Before)
	after.Generation, after.PolicyGeneration = tx.After.Generation+1, tx.After.PolicyGeneration+1
	var err error
	after, err = refreshLedgerIdentities(after)
	if err != nil {
		return transaction{}, err
	}
	reverse := transaction{Schema: transactionSchemaV5, Before: current, After: after, ConfigPaths: append([]string(nil), tx.ConfigPaths...), Files: []File{}, Rollback: true, OrphanRecovery: tx.OrphanRecovery}
	if err := validateOrphanTransaction(reverse); err != nil {
		return transaction{}, err
	}
	return reverse, nil
}
