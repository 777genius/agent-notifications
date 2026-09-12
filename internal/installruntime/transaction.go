package installruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// ErrPolicyRecovery leaves pending installer work untouched for its owner.
var ErrPolicyRecovery = errors.New("pending installation transaction requires installer recovery")

// Identity includes existence: an empty file is not an absent file.
type Identity struct {
	Link   string
	Exists bool
	SHA256 string
	Mode   uint32
}
type File struct {
	// WindowsReplacementID binds the evacuated preimage used by Windows redo.
	WindowsReplacementID string
	Parents              []PathAnchor
	BeforeData           []byte
	Link                 string
	Path                 string
	Before               Identity
	Data                 []byte
	Mode                 uint32
	Remove               bool
	DataSHA256           string `json:",omitempty"`
	BeforeDataSHA256     string `json:",omitempty"`
}
type Consumer struct {
	RuntimeRoot  string
	Registration string
	Commands     []string
}
type Ledger struct {
	WriterFloor      int
	Enabled          bool
	Native           *NativeRecord
	Schema           int
	ID               string
	Owner            string
	RuntimeRoot      string
	Generation       uint64
	PolicyGeneration uint64
	Consumers        map[string]Consumer
	Files            map[string]Identity
	DecoderFloor     int
}
type transaction struct {
	ConfigPaths []string
	Native      *NativeChange
	Schema      int
	Before      Ledger
	After       Ledger
	Files       []File
}

// Request stages ordinary file bytes before Commit. Prepare runs under the
// component and config locks, and may only compute adapter-owned JSON changes.
type Request struct {
	// PolicyOnly requires an already-managed runtime and existing kernel locks.
	// It refuses recovery and asset/consumer mutations; setup cannot accidentally
	// promote native or rewrite hooks from an unrelated pending transaction.
	PolicyOnly bool
	// PolicyEnabled changes explicit intent; nil preserves it. Mutations require
	// an expected generation and share component/config locking and recovery.
	PolicyEnabled *bool
	// PolicyFields merges installer-owned route/rates and setupState ownership
	// members into the same
	// policy CAS transaction. Nil preserves all existing fields.
	PolicyFields map[string]json.RawMessage
	// ExpectedPolicy optionally fences the exact setup policy preimage, including
	// manual config edits that do not increment the installation generation.
	ExpectedPolicy *Identity
	// RefreshOnly updates an already registered runtime without adding a consumer.
	RefreshOnly bool
	// RollbackPending explicitly reverses a pending transaction using per-file CAS.
	RollbackPending bool
	Native          *NativeChange
	PurgeNative     bool
	// RetireNative removes only the drained predecessor, retaining the stable reader.
	// Requires ExpectedGeneration; may not be combined with native promotion/removal.
	RetireNative                                bool
	ControlRoot, Owner, RuntimeRoot, ConsumerID string
	Consumer                                    Consumer
	RemoveConsumer                              bool
	ExpectedGeneration                          *uint64
	Files                                       []File
	ConfigPaths                                 []string
	Prepare                                     func() ([]File, error)
	// Fault is a test seam; returning an error intentionally leaves recovery data.
	Fault func(string) error
}

func Fingerprint(path string) (Identity, error) {
	return safeFingerprint(path)
}

func identity(data []byte, mode uint32) Identity {
	sum := sha256.Sum256(data)
	return Identity{Exists: true, SHA256: hex.EncodeToString(sum[:]), Mode: identityMode(mode)}
}
func desired(f File) Identity {
	if f.Remove {
		return Identity{}
	}
	if f.Link != "" {
		return Identity{Exists: true, Link: f.Link}
	}
	return identity(f.Data, f.Mode)
}
func readLedger(root string) (Ledger, error) {
	var l Ledger
	data, err := readRegularFile(filepath.Join(root, "ownership.json"))
	if os.IsNotExist(err) {
		return Ledger{Schema: ledgerSchemaV1, Consumers: map[string]Consumer{}, Files: map[string]Identity{}}, nil
	}
	if err != nil {
		return l, err
	}
	err = json.Unmarshal(data, &l)
	if err == nil && ((l.Schema != ledgerSchemaV1 && l.Schema != ledgerSchemaV2) || l.ID == "" || l.Generation == 0 || l.Consumers == nil || l.Files == nil) {
		err = fmt.Errorf("invalid ownership ledger")
	}
	return l, err
}
func durable(path string, data []byte, mode os.FileMode) error {
	return safePublish(File{Path: path, Data: data, Mode: uint32(mode)}, false)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return durable(path, append(data, '\n'), 0600)
}

// Commit serializes all component decisions, then config locks in canonical
// order. The durable redo record precedes every live mutation. Recovery checks
// every identity before changing anything and refuses ambiguous foreign edits.
func Commit(ctx context.Context, r Request) (Ledger, error) {
	if r.PolicyOnly && (!r.RefreshOnly || r.ExpectedGeneration == nil || len(r.Files) != 0 || r.Native != nil || r.RemoveConsumer || r.PurgeNative || r.RetireNative || r.RollbackPending) {
		return Ledger{}, fmt.Errorf("policy-only transaction requires existing generation and no asset or consumer mutation")
	}
	root := r.ControlRoot
	var err error
	if root == "" {
		root, err = ControlRoot()
		if err != nil {
			return Ledger{}, err
		}
	}
	if filepath.IsAbs(r.RuntimeRoot) {
		r.RuntimeRoot, err = CanonicalPath(r.RuntimeRoot)
		if err != nil {
			return Ledger{}, err
		}
	}
	lockComponent := Lock
	if r.PolicyOnly {
		lockComponent = LockExisting
	}
	unlock, err := lockComponent(ctx, filepath.Join(root, ".component-install.lock"))
	if err != nil {
		return Ledger{}, err
	}
	defer unlock()
	if err := privateDirectory(root); err != nil {
		return Ledger{}, err
	}
	// Match policy lock identity in the same physical spelling as ConfigPaths.
	// Supported Darwin /var aliases must not turn LockExisting into Lock.
	policyRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Ledger{}, err
	}
	policyPath := filepath.Join(policyRoot, "agent-notifications.json")
	r.ConfigPaths = append(append([]string(nil), r.ConfigPaths...), policyPath)
	paths := append([]string(nil), r.ConfigPaths...)
	// Recovery may include configuration from a different adapter invocation.
	var pending transaction
	marker := filepath.Join(root, "transaction.json")
	var readErr error
	if _, err := os.Lstat(marker); os.IsNotExist(err) {
		readErr = err
	} else if err != nil {
		return Ledger{}, fmt.Errorf("corrupt installation transaction: %w", err)
	} else {
		pending, readErr = readTransactionFile(marker)
		if readErr != nil {
			return Ledger{}, fmt.Errorf("corrupt installation transaction: %w", readErr)
		}
	}
	if readErr == nil {
		if r.PolicyOnly {
			return Ledger{}, ErrPolicyRecovery
		}
		paths = append(paths, pending.ConfigPaths...)
	}

	for i, p := range paths {
		p, err = filepath.Abs(p)
		if err != nil {
			return Ledger{}, err
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(p))
		if e == nil {
			p = filepath.Join(parent, filepath.Base(p))
		}
		paths[i] = p
	}
	sort.Strings(paths)
	for i, p := range paths {
		if i > 0 && paths[i-1] == p {
			continue
		}
		lockConfig := Lock
		if r.PolicyOnly && p == policyPath {
			lockConfig = LockExisting
		}
		release, e := lockConfig(ctx, p+".lock")
		if e != nil {
			return Ledger{}, e
		}
		defer release()
	}
	l, err := readLedger(root)
	if err != nil {
		return l, err
	}
	if l.WriterFloor > WriterFloor {
		return l, fmt.Errorf("installed writer floor requires a newer compatible kernel")
	}
	if err := validateWriterFiles(r.Files); err != nil {
		return l, err
	}
	if readErr == nil {
		if r.RollbackPending {
			if !reflect.DeepEqual(l, pending.Before) && !reflect.DeepEqual(l, pending.After) {
				return l, fmt.Errorf("rollback ledger mismatch")
			}
			reverse, e := reverseTransaction(l, pending)
			if e != nil {
				return l, e
			}
			if e = writeTransaction(marker, reverse); e != nil {
				return l, e
			}
			if e = recoverTransaction(ctx, root, l, reverse, r.Fault); e != nil {
				return l, e
			}
			return reverse.After, nil
		}
		if err := recoverTransaction(ctx, root, l, pending, nil); err != nil {
			return l, err
		}
		l = pending.After
	} else {
		if err := checkPolicyGeneration(root, l); err != nil {
			return l, err
		}
		if r.RollbackPending {
			return l, nil
		}
	}

	if l.Native != nil && !policyDisableOnly(r) {
		if err := validateNativeRecord(l.Native); err != nil {
			return l, err
		}
		if err := checkNativeDirectoryID(l.Native.Path, l.Native.DirectoryID); err != nil {
			return l, err
		}
		got, e := treeFingerprint(l.Native.Path)
		if e != nil {
			return l, e
		}
		if got != l.Native.SHA256 {
			return l, fmt.Errorf("managed native changed without transaction")
		}
	}
	if r.Owner == "" || r.ConsumerID == "" || !filepath.IsAbs(r.RuntimeRoot) {
		return l, fmt.Errorf("owner, consumer and absolute runtime root required")
	}
	if l.ID != "" && l.Owner != r.Owner {
		return l, fmt.Errorf("component owned by %s at %s; explicit takeover required", l.Owner, l.RuntimeRoot)
	}
	if previous, ok := l.Consumers[r.ConsumerID]; !r.RefreshOnly && ok && previous.RuntimeRoot != "" && previous.RuntimeRoot != r.RuntimeRoot {
		return l, fmt.Errorf("consumer runtime relocation requires explicit takeover")
	}
	if r.RefreshOnly {
		registered := false
		for _, consumer := range l.Consumers {
			if consumer.RuntimeRoot == r.RuntimeRoot {
				registered = true
			}
		}
		if !registered || r.RemoveConsumer {
			return l, fmt.Errorf("runtime refresh requires an existing consumer at this path")
		}
	}
	if r.RemoveConsumer {
		if _, exists := l.Consumers[r.ConsumerID]; !exists && !r.PurgeNative {
			return l, nil
		}
		if len(r.Files) != 0 {
			return l, fmt.Errorf("consumer removal cannot install files")
		}
	}
	if r.ExpectedGeneration != nil && *r.ExpectedGeneration != l.Generation {
		return l, fmt.Errorf("stale installation generation")
	}
	// Existing managed identities must match, even when no transaction remains.
	// A deleted launcher/asset in this request may be republished; an existing
	// file whose fingerprint changed is a foreign edit and must refuse.
	replacing := map[string]bool{}
	for _, file := range r.Files {
		replacing[file.Path] = true
		if canonical, e := CanonicalPath(file.Path); e == nil {
			replacing[canonical] = true
		}
	}
	for path, want := range l.Files {
		got, e := Fingerprint(path)
		if e != nil {
			return l, e
		}
		if got == want {
			continue
		}
		if replacing[path] && !got.Exists && want.Exists {
			continue
		}
		return l, fmt.Errorf("managed fingerprint changed without transaction: %s", path)
	}
	nextData, _ := json.Marshal(l)
	var next Ledger
	_ = json.Unmarshal(nextData, &next)
	if next.ID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return l, err
		}
		next.ID = hex.EncodeToString(id[:])
	}
	next.WriterFloor = WriterFloor
	next.Schema = ledgerSchemaV2
	next.Owner = r.Owner
	if next.RuntimeRoot == "" {
		next.RuntimeRoot = r.RuntimeRoot
	}
	policy, policyFields, policyBefore, err := readPolicyForUpdate(root)
	if err != nil {
		return l, err
	}
	if r.ExpectedPolicy != nil && *r.ExpectedPolicy != policyBefore {
		return l, fmt.Errorf("stale explicit policy bytes")
	}
	next.Enabled = policy.Enabled
	if r.PolicyEnabled != nil || len(r.PolicyFields) != 0 {
		if r.RemoveConsumer || len(next.Consumers) == 0 || r.ExpectedGeneration == nil {
			return l, fmt.Errorf("policy mutation requires a registered consumer and expected generation")
		}
		if r.PolicyEnabled != nil {
			next.Enabled = *r.PolicyEnabled
		}
		if err := mergePolicyFields(policyFields, r.PolicyFields); err != nil {
			return l, err
		}
	}
	next.Generation++
	next.PolicyGeneration++
	if r.RemoveConsumer {
		delete(next.Consumers, r.ConsumerID)
	} else if !r.RefreshOnly && !r.RetireNative {
		r.Consumer.RuntimeRoot = r.RuntimeRoot
		next.Consumers[r.ConsumerID] = r.Consumer
	}
	if len(next.Consumers) == 0 {
		next.Enabled = false
	}
	native := r.Native
	if native != nil {
		canonicalRoot, e := CanonicalPath(root)
		if e != nil {
			return l, e
		}
		if filepath.Dir(native.After.Path) != filepath.Join(canonicalRoot, "native") {
			return l, fmt.Errorf("native live identity must remain inside the persistent native owner")
		}
	}
	if r.PurgeNative {
		if !r.RemoveConsumer || len(next.Consumers) != 0 {
			return l, fmt.Errorf("native purge requires final consumer removal")
		}
		if l.Native != nil {
			native = &NativeChange{Before: *l.Native, After: NativeRecord{Path: l.Native.Path}, Purge: true, Staged: l.Native.Path + fmt.Sprintf(".purged-%d", next.Generation)}
			next.Native = nil
			next.DecoderFloor = 0
		}
	} else if native != nil {
		if err := checkNativeParents(native); err != nil {
			return l, err
		}
		if native.After.DecoderFloor < l.DecoderFloor {
			return l, fmt.Errorf("native decoder downgrade refused")
		}
		if l.Native == nil && native.Before.SHA256 != "" {
			return l, fmt.Errorf("unmanaged callback path cannot be adopted")
		}
		if l.Native != nil {
			if native.Before.Path != l.Native.Path || native.Before.SHA256 != l.Native.SHA256 {
				return l, fmt.Errorf("stale native snapshot")
			}
			native.Before = *l.Native
			if native.After.SHA256 == l.Native.SHA256 {
				if native.Staged != l.Native.Path && !nativeRecordContainsPath(*l.Native, native.Staged) {
					if err := discardNativeCandidate(native); err != nil {
						return l, err
					}
				}
				native = nil
			} else {
				if native.After.Path == l.Native.Path {
					return l, fmt.Errorf("new native bytes reused live callback identity")
				}
				native.After = publishNativeRecord(*l.Native, native.After)
			}
		}
		if native != nil {
			next.Native = &native.After
			next.DecoderFloor = native.After.DecoderFloor
		}
	}
	if r.RetireNative {
		next.Enabled = l.Enabled
		if r.ExpectedGeneration == nil || r.Native != nil || r.RemoveConsumer || r.PurgeNative || r.PolicyEnabled != nil || len(r.PolicyFields) != 0 || len(r.Files) != 0 || r.Prepare != nil {
			return l, fmt.Errorf("retirement requires expected generation and no other mutation")
		}
		if l.Native == nil || l.Native.PreviousPath == "" {
			return l, nil
		}
		if nativeRecordContainsPath(*l.Native, l.Native.PreviousPath) && !strings.HasPrefix(filepath.Base(l.Native.PreviousPath), ".candidate-") {
			return l, nil
		}
		before := *l.Native
		after := before
		after.PreviousPath, after.PreviousSHA256, after.PreviousDirectoryID = "", "", ""
		native = &NativeChange{Before: before, After: after, Retire: true, Staged: before.PreviousPath}
		if err := qualifyRetirement(ctx, native); err != nil {
			return l, err
		}
		next.Native = &native.After
	}
	if native != nil && (native.Purge || native.Retire) {
		native.Parents, err = pathAnchors(native.After.Path, false)
		if err != nil {
			return l, err
		}
	}
	if err := preparePurge(native); err != nil {
		return l, err
	}
	if err := validateNative(native); err != nil {
		return l, err
	}
	files := append([]File(nil), r.Files...)
	if r.PolicyEnabled != nil || len(r.PolicyFields) != 0 || (r.RemoveConsumer && len(next.Consumers) == 0) {
		f, e := policyFile(policyRoot, next.Enabled, policyFields, policyBefore)
		if e != nil {
			return l, e
		}
		files = append(files, f)
	}
	if r.Prepare != nil {
		extra, e := r.Prepare()
		if e != nil {
			return l, e
		}
		if r.PolicyOnly && len(extra) != 0 {
			return l, fmt.Errorf("policy-only preparation cannot mutate assets")
		}
		files = append(files, extra...)
	}
	// Last-consumer cleanup uses the identities decided under this same lock.
	// The control/state namespace is never part of this list. Callback bundles
	// have their own retained record and are intentionally not ordinary Files.
	if r.RemoveConsumer && len(next.Consumers) == 0 {
		for path, before := range l.Files {
			files = append(files, File{Path: path, Before: before, Remove: true})
		}
	}
	if err := validateWriterFiles(files); err != nil {
		return l, err
	}
	seen := map[string]bool{}
	for i := range files {
		f := files[i]
		if !filepath.IsAbs(f.Path) || seen[f.Path] {
			return l, fmt.Errorf("invalid or duplicate mutation path")
		}
		seen[f.Path] = true
		anchors, e := pathAnchors(f.Path, true)
		if e != nil {
			return l, e
		}
		if e = checkAnchors(f.Parents, anchors); e != nil {
			return l, e
		}
		files[i].Parents = anchors
		got, e := Fingerprint(f.Path)
		if e != nil {
			return l, e
		}
		if got != f.Before {
			return l, fmt.Errorf("staged fingerprint changed: %s", f.Path)
		}
		if f.Before.Exists && f.Before.Link == "" {
			files[i].WindowsReplacementID, e = windowsReplacementIdentity(f.Path)
			if e != nil {
				return l, e
			}
			files[i].BeforeData, e = readRegularFile(f.Path)
			if e != nil {
				return l, e
			}
			if identity(files[i].BeforeData, f.Before.Mode) != f.Before {
				return l, fmt.Errorf("file changed while capturing recovery identity")
			}
		}
		// Config files are adapter-owned, so later unrelated JSON edits are allowed.
		config := false
		for _, p := range r.ConfigPaths {
			if p == f.Path {
				config = true
			}
		}
		if !config {
			if f.Remove {
				delete(next.Files, f.Path)
			} else {
				next.Files[f.Path] = desired(f)
			}
		}
	}
	tx := transaction{Schema: transactionSchemaV2, Before: l, After: next, Files: files, Native: native, ConfigPaths: r.ConfigPaths}
	if err := writeTransaction(marker, tx); err != nil {
		return l, err
	}
	if r.Fault != nil {
		if err := r.Fault("transaction"); err != nil {
			return l, err
		}
	}
	if err := recoverTransaction(ctx, root, l, tx, r.Fault); err != nil {
		return l, err
	}
	return next, nil
}
func recoverTransaction(ctx context.Context, root string, current Ledger, tx transaction, fault func(string) error) error {
	if !reflect.DeepEqual(current, tx.Before) && !reflect.DeepEqual(current, tx.After) {
		return fmt.Errorf("transaction ledger snapshot mismatch")
	}
	if current.Generation != tx.Before.Generation && current.Generation != tx.After.Generation {
		return fmt.Errorf("transaction generation mismatch")
	}
	if current.ID != "" && current.ID != tx.After.ID {
		return fmt.Errorf("transaction owner mismatch")
	}
	for _, f := range tx.Files {
		anchors, e := pathAnchors(f.Path, false)
		if e != nil {
			return e
		}
		if e = checkAnchors(f.Parents, anchors); e != nil {
			return e
		}
		got, err := replacementFingerprint(f)
		if err != nil {
			return err
		}
		if got != f.Before && got != desired(f) {
			return fmt.Errorf("recovery conflict; preserving foreign edit: %s", f.Path)
		}
	}
	if err := validateNative(tx.Native); err != nil {
		return err
	}
	if err := qualifyRetirement(ctx, tx.Native); err != nil {
		return err
	}
	// Policy revocation becomes visible before file removals/new admissions.
	if err := writeJSON(filepath.Join(root, "policy-generation.json"), struct {
		Generation uint64
		Enabled    bool
	}{tx.After.PolicyGeneration, false}); err != nil {
		return err
	}
	if err := promoteNative(tx.Native); err != nil {
		return err
	}
	if tx.Native != nil && fault != nil {
		if err := fault("native"); err != nil {
			return err
		}
	}
	for _, f := range tx.Files {
		got, err := replacementFingerprint(f)
		if err != nil {
			return err
		}
		if got == desired(f) {
			if err := safePublish(f, true); err != nil {
				return err
			}
			continue
		}
		if got != f.Before {
			return fmt.Errorf("concurrent edit: %s", f.Path)
		}
		err = safePublish(f, true)
		if err != nil {
			return err
		}
		if fault != nil {
			if err := fault("promotion:" + f.Path); err != nil {
				return err
			}
		}
	}
	if err := writeJSON(filepath.Join(root, "ownership.json"), tx.After); err != nil {
		return err
	}
	if fault != nil {
		if err := fault("ledger"); err != nil {
			return err
		}
	}
	if err := cleanupPurgedNative(tx.Native, fault); err != nil {
		return err
	}
	// Publish the final policy only after all assets and the ledger are durable.
	if err := writeJSON(filepath.Join(root, "policy-generation.json"), runtimePolicy{tx.After.PolicyGeneration, tx.After.Enabled}); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(root, "transaction.json")); err != nil {
		return err
	}
	if err := discardTransactionBlobs(filepath.Join(root, "transaction.json")); err != nil {
		return err
	}
	return syncDir(root)
}
