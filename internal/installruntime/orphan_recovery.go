package installruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/777genius/agent-notifications/internal/codexcommand"
)

const transactionSchemaV5 = 5
const maxOrphanPaths = 4096

// OrphanRecoveryRequest fences a single observed consumer, never a guessed
// namespace. Registration and Commands must be the complete recorded values.
type OrphanRecoveryRequest struct {
	InstallationID     string
	ExpectedGeneration uint64
	ConsumerID         string
	RuntimeRoot        string
	Consumer           Consumer
}

// OrphanRecoveryPreview is an observation, not a lease. On conflict the bounded
// result and error both describe the failed admission; commit repeats admission.
type OrphanRecoveryPreview struct {
	InstallationID        string
	Generation            uint64
	ConsumerID            string
	RuntimeRoot           string
	SelectedPaths         []string
	SelectedCount         int
	Admissible            bool
	ConflictCode          string
	ConflictPath          string
	NativeValidated       bool
	NativeIdentityRefresh bool
	ExpectedPolicy        Identity
}

// OrphanRecoveryConflict permits an adapter to expose bounded categories.
type OrphanRecoveryConflict struct {
	Code, Path string
	Err        error
}

func (e *OrphanRecoveryConflict) Error() string {
	return fmt.Sprintf("orphan recovery %s at %q: %v", e.Code, e.Path, e.Err)
}
func (e *OrphanRecoveryConflict) Unwrap() error { return e.Err }
func orphanConflict(code, path string, err error) error {
	return &OrphanRecoveryConflict{Code: code, Path: path, Err: err}
}

type orphanAbsence struct {
	Path    string
	Missing string
	Parents []PathAnchor
}

// The full before-image and identities are in transaction.Before. Registration
// is a separate observation: it never expands Files or the publication set.
type orphanDecision struct {
	ControlRoot            string
	ControlParents         []PathAnchor
	ControlLocks           []PathAnchor
	Request                OrphanRecoveryRequest
	Files                  []string
	Identities             []Identity
	SourcePolicyGeneration uint64
	RuntimeAbsences        []orphanAbsence
	RegistrationAbsence    *orphanAbsence
	Policy                 Identity
}

func cleanPhysicalPath(path string) bool {
	if path == "" || len(path) > 4096 || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	physical, err := PhysicalPath(path)
	return err == nil && physical == path
}

func orphanOverlap(a, b string) bool {
	if runtime.GOOS == "windows" {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return a == b || pathWithinRoot(a, b) || pathWithinRoot(b, a)
}

func validateOrphanRequest(req OrphanRecoveryRequest) error {
	if req.InstallationID == "" || len(req.InstallationID) > 256 || req.ExpectedGeneration == 0 || req.ConsumerID == "" || len(req.ConsumerID) > 4096 || !cleanPhysicalPath(req.RuntimeRoot) || req.Consumer.RuntimeRoot != req.RuntimeRoot {
		return orphanConflict("selection", req.RuntimeRoot, fmt.Errorf("exact installation, generation, consumer and physical runtime required"))
	}
	return nil
}

// orphanSelection is pure: replay derives the permitted delta independently of
// any checksummed path list. Unknown private reference codecs fail closed.
func orphanSelection(root string, l Ledger, req OrphanRecoveryRequest) ([]string, []string, error) {
	if err := validateOrphanRequest(req); err != nil {
		return nil, nil, err
	}
	if l.ID != req.InstallationID || l.Generation != req.ExpectedGeneration || l.Owner != "existing-installer" || !acceptedLedgerSchema(l.Schema) || l.WriterFloor < 0 || l.WriterFloor > SupportedWriterFloor || l.Generation == ^uint64(0) || l.PolicyGeneration == 0 || l.PolicyGeneration == ^uint64(0) || l.PendingMutation != nil {
		return nil, nil, orphanConflict("installation_fence", root, fmt.Errorf("owner, generation, protocol or reservation conflict"))
	}
	c, ok := l.Consumers[req.ConsumerID]
	if !ok || !reflect.DeepEqual(c, req.Consumer) || c.OpenCode != nil || len(l.Consumers) < 2 || !cleanPhysicalPath(l.RuntimeRoot) || orphanOverlap(l.RuntimeRoot, req.RuntimeRoot) || orphanOverlap(root, req.RuntimeRoot) {
		return nil, nil, orphanConflict("consumer_fence", req.RuntimeRoot, fmt.Errorf("observed consumer differs, is primary/final, private, or overlaps control"))
	}
	wrappers, err := orphanCommandPaths(req.ConsumerID, c)
	if err != nil {
		return nil, nil, err
	}
	if c.Registration != "" {
		if !cleanPhysicalPath(c.Registration) || orphanOverlap(root, c.Registration) || orphanOverlap(l.RuntimeRoot, c.Registration) {
			return nil, nil, orphanConflict("registration", c.Registration, fmt.Errorf("unsupported registration namespace"))
		}
		if _, tracked := l.Files[c.Registration]; tracked && !pathWithinRoot(req.RuntimeRoot, c.Registration) {
			return nil, nil, orphanConflict("registration", c.Registration, fmt.Errorf("external registration is a managed file"))
		}
	}
	for id, retained := range l.Consumers {
		if id == req.ConsumerID {
			continue
		}
		if !cleanPhysicalPath(retained.RuntimeRoot) || orphanOverlap(req.RuntimeRoot, retained.RuntimeRoot) || c.Registration != "" && orphanOverlap(c.Registration, retained.RuntimeRoot) || retained.OpenCode != nil {
			return nil, nil, orphanConflict("shared_ownership", retained.RuntimeRoot, fmt.Errorf("overlapping or private retained consumer"))
		}
		if retained.Registration != "" {
			if !cleanPhysicalPath(retained.Registration) || orphanOverlap(req.RuntimeRoot, retained.Registration) || c.Registration != "" && orphanOverlap(c.Registration, retained.Registration) {
				return nil, nil, orphanConflict("shared_reference", retained.Registration, fmt.Errorf("retained registration cannot be excluded"))
			}
		}
		paths, err := orphanCommandPaths(id, retained)
		if err != nil {
			return nil, nil, orphanConflict("shared_reference", retained.RuntimeRoot, err)
		}
		for _, path := range paths {
			if orphanOverlap(req.RuntimeRoot, path) || c.Registration != "" && orphanOverlap(c.Registration, path) {
				return nil, nil, orphanConflict("shared_reference", path, fmt.Errorf("retained command references selected namespace"))
			}
		}
	}
	var selected []string
	for path, id := range l.Files {
		if !cleanPhysicalPath(path) || !meaningfulOrphanIdentity(id) {
			return nil, nil, orphanConflict("file_identity", path, fmt.Errorf("invalid managed identity"))
		}
		if path == req.RuntimeRoot {
			return nil, nil, orphanConflict("file_namespace", path, fmt.Errorf("runtime is a tracked file"))
		}
		if pathWithinRoot(req.RuntimeRoot, path) {
			selected = append(selected, path)
		}
	}
	if len(selected) == 0 || len(selected) > maxOrphanPaths {
		return nil, nil, orphanConflict("file_set", req.RuntimeRoot, fmt.Errorf("empty or oversized orphan file set"))
	}
	sort.Strings(selected)
	if runtime.GOOS == "windows" {
		seen := map[string]bool{}
		for _, path := range selected {
			key := strings.ToLower(path)
			if seen[key] {
				return nil, nil, orphanConflict("file_set", path, fmt.Errorf("case-alias orphan file paths"))
			}
			seen[key] = true
		}
	}
	return selected, wrappers, nil
}

func meaningfulOrphanIdentity(id Identity) bool {
	if !id.Exists {
		return false
	}
	if id.Link != "" {
		return id.SHA256 == "" && id.Mode == 0 && len(id.Link) <= 4096 && !strings.ContainsRune(id.Link, 0)
	}
	return ownedTransactionBlobName(id.SHA256) && id.Mode & ^uint32(0777) == 0
}

func orphanCommandPaths(id string, c Consumer) ([]string, error) {
	if strings.HasPrefix(id, "codex:") {
		if c.Registration != filepath.Join(filepath.Dir(c.RuntimeRoot), "hooks.json") || id != "codex:"+c.Registration || filepath.Base(c.RuntimeRoot) != codexcommand.InstallDirName || !reflect.DeepEqual(c.Commands, codexcommand.Commands(c.RuntimeRoot)) {
			return nil, orphanConflict("command_codec", c.RuntimeRoot, fmt.Errorf("codex registration must match the exact generated command set"))
		}
		return []string{filepath.Join(c.RuntimeRoot, "bin", "codex-hook-wrapper.cmd"), filepath.Join(c.RuntimeRoot, "bin", "codex-hook-wrapper.sh")}, nil
	}
	paths := make([]string, 0, len(c.Commands))
	if len(c.Commands) > 64 {
		return nil, fmt.Errorf("too many command references")
	}
	for _, command := range c.Commands {
		if !cleanPhysicalPath(command) || !pathWithinRoot(c.RuntimeRoot, command) {
			return nil, orphanConflict("command_codec", c.RuntimeRoot, fmt.Errorf("unsupported opaque command reference"))
		}
		paths = append(paths, command)
	}
	return paths, nil
}

// Use the existing no-follow descriptor-relative walker and fingerprint. Its
// partial anchor chain identifies the first missing component, not just a leaf
// below it. A new directory at that component invalidates the decision too.
func observeOrphanAbsence(path string) (orphanAbsence, error) {
	a := orphanAbsence{Path: path}
	if !cleanPhysicalPath(path) {
		return a, fmt.Errorf("invalid absence path")
	}
	parents, err := pathAnchors(path, false)
	if err != nil {
		return a, err
	}
	base := filepath.VolumeName(path) + string(filepath.Separator)
	if len(parents) > 0 {
		base = parents[len(parents)-1].Path
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return a, fmt.Errorf("invalid absence chain")
	}
	a.Missing = filepath.Join(base, strings.Split(rel, string(filepath.Separator))[0])
	a.Parents = parents
	got, err := Fingerprint(a.Missing)
	if err != nil {
		return a, err
	}
	if got.Exists {
		return a, fmt.Errorf("owned path or first absent component is present")
	}
	again, err := pathAnchors(path, false)
	if err != nil {
		return a, err
	}
	if !reflect.DeepEqual(parents, again) {
		return a, fmt.Errorf("absence parents changed while observing")
	}
	return a, nil
}

func orphanRuntimePaths(files, wrappers []string) []string {
	set := map[string]bool{}
	for _, paths := range [][]string{files, wrappers} {
		for _, p := range paths {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

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
		id, err := regularObjectID(path)
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
		fresh, err := regularObjectID(lock.Path)
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
