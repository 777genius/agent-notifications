package installruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/777genius/agent-notifications/internal/strictjson"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// ErrPolicyRecovery leaves pending installer work untouched for its owner.
var ErrPolicyRecovery = errors.New("pending installation transaction requires installer recovery")

// ErrPolicyConflict refuses a stale ExpectedPolicy under the commit locks,
// before publishing any transaction or product mutation.
var ErrPolicyConflict = errors.New("stale explicit policy bytes")

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
	PendingMutation  *PendingMutation `json:",omitempty"`
}
type transaction struct {
	ConfigPaths []string
	Native      *NativeChange
	Schema      int
	Before      Ledger
	After       Ledger
	Files       []File
	// Rollback marks a durable reverse decision. Retry must resume it instead
	// of reversing the reverse and republishing the interrupted upgrade.
	Rollback bool `json:",omitempty"`
}

// Request stages ordinary file bytes before Commit. Prepare runs under the
// component and config locks, and may only compute adapter-owned JSON changes.
type Request struct {
	// RevokeOpenCode permits only the exact desktop/webhook false policy patch
	// when delivery assets are damaged. It still requires a registered consumer,
	// generation and policy CAS; no asset, native or other policy mutation is allowed.
	RevokeOpenCode bool
	// RevokeGemini permits only Gemini's exact desktop/webhook false patch.
	// Damaged assets do not prevent revocation; ownership and CAS still apply.
	RevokeGemini bool
	// RevokeCopilotVSCode is the exact false-only portable Local specialization.
	// It skips delivery readiness, never written-file anchors, ownership or CAS.
	RevokeCopilotVSCode bool
	// RevokeCursor permits only the exact recorded portable Cursor false pair.
	RevokeCursor bool
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
	// RelocateVersionedCache explicitly moves only the existing Claude hooks
	// consumer between sibling, versioned Claude plugin cache directories.
	// Other consumers and their runtime roots are never moved implicitly.
	RelocateVersionedCache bool
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
	// RecoverOnly replays a pending journal and returns without refresh, install,
	// or consumer registration. It does not require owner/runtime/package.
	RecoverOnly bool
	// Reservation publishes or continues a kernel-owned pending mutation.
	Reservation *PendingMutation
	// ClearReservation removes a matching pending mutation. Floor/schema stay.
	ClearReservation bool
	// Fault is a test seam; returning an error intentionally leaves recovery data.
	Fault func(string) error
}

func Fingerprint(path string) (Identity, error) {
	return safeFingerprint(path)
}

// IdentityMode returns the permission bits retained by the host fingerprint.
// Windows has no Unix execute bit and normalizes its file modes accordingly.
func IdentityMode(mode uint32) uint32 { return identityMode(mode) }

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
	if strictjson.Validate(data, strictjson.Budget{Bytes: maxManagedFile, Depth: 32, Entries: 100000}) != nil {
		return l, fmt.Errorf("invalid ownership ledger JSON")
	}
	err = json.Unmarshal(data, &l)
	if err == nil && (!acceptedLedgerSchema(l.Schema) || l.ID == "" || l.Generation == 0 || l.Consumers == nil || l.Files == nil) {
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

func versionedClaudeCachePeers(oldRoot, newRoot string) bool {
	if oldRoot == newRoot || filepath.Dir(oldRoot) != filepath.Dir(newRoot) {
		return false
	}
	parent := filepath.Dir(oldRoot)
	if filepath.Base(parent) != "claude-notifications-go" ||
		filepath.Base(filepath.Dir(parent)) != "claude-notifications-go" ||
		filepath.Base(filepath.Dir(filepath.Dir(parent))) != "cache" {
		return false
	}
	for _, version := range []string{filepath.Base(oldRoot), filepath.Base(newRoot)} {
		parts := strings.Split(version, ".")
		if len(parts) != 3 {
			return false
		}
		for _, part := range parts {
			if len(part) == 0 || len(part) > 9 {
				return false
			}
			for _, digit := range part {
				if digit < '0' || digit > '9' {
					return false
				}
			}
		}
	}
	return true
}

func pathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// A portable binding keeps executing its recorded primary under the previous
// cache root until a separate binding migration changes that locator. Publish
// the same verified sender there in the relocation transaction, or refuse the
// move when a shared consumer cannot be identified precisely.
func retainedPortablePrimaryFiles(l Ledger, oldRoot, newRoot, movingID string, staged []File) ([]File, error) {
	byPath := make(map[string]File, len(staged))
	for _, file := range staged {
		byPath[file.Path] = file
	}
	retained := make(map[string]File)
	for id, consumer := range l.Consumers {
		if id == movingID || consumer.RuntimeRoot != oldRoot {
			continue
		}
		if !strings.HasPrefix(id, "portable:") || len(consumer.Commands) != 1 {
			return nil, fmt.Errorf("shared cache consumer requires explicit migration: %s", id)
		}
		command := consumer.Commands[0]
		if !pathWithinRoot(oldRoot, command) {
			return nil, fmt.Errorf("portable primary is outside the previous runtime")
		}
		relative, err := filepath.Rel(oldRoot, command)
		if err != nil || filepath.Dir(relative) != "bin" || !strings.HasPrefix(filepath.Base(relative), "claude-notifications-") {
			return nil, fmt.Errorf("portable primary is not a managed sender")
		}
		before, owned := l.Files[command]
		after, staged := byPath[filepath.Join(newRoot, relative)]
		// Windows executable files do not carry POSIX execute bits.
		executable := after.Mode&0111 != 0 || filepath.Ext(relative) == ".exe"
		if !owned || !before.Exists || before.Link != "" || !staged || after.Remove || after.Link != "" || len(after.Data) == 0 || !executable {
			return nil, fmt.Errorf("portable primary cannot be refreshed from the verified stage")
		}
		retained[command] = File{Path: command, Before: before, Data: after.Data, Mode: after.Mode}
	}
	paths := make([]string, 0, len(retained))
	for path := range retained {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]File, 0, len(paths))
	for _, path := range paths {
		files = append(files, retained[path])
	}
	return files, nil
}

// Commit serializes all component decisions, then config locks in canonical
// order. The durable redo record precedes every live mutation. Recovery checks
// every identity before changing anything and refuses ambiguous foreign edits.
func Commit(ctx context.Context, r Request) (Ledger, error) {
	if r.RevokeCursor && !cursorRevokeOnly(r) {
		return Ledger{}, fmt.Errorf("invalid Cursor channel revocation")
	}
	if r.RevokeCopilotVSCode && !copilotRevokeOnly(r) {
		return Ledger{}, fmt.Errorf("invalid Copilot VS Code channel revocation")
	}
	if r.RevokeOpenCode && !openCodeRevokeOnly(r) {
		return Ledger{}, fmt.Errorf("invalid OpenCode channel revocation")
	}
	if r.RevokeGemini && !geminiRevokeOnly(r) {
		return Ledger{}, fmt.Errorf("invalid Gemini channel revocation")
	}
	if err := reservationRequestInvalid(r); err != nil {
		return Ledger{}, err
	}
	if r.RelocateVersionedCache && (r.RefreshOnly || r.RemoveConsumer || r.RetireNative || r.RecoverOnly || r.RollbackPending || r.ConsumerID != "claude-hooks" || r.Owner != "existing-installer") {
		return Ledger{}, fmt.Errorf("versioned cache relocation requires a Claude hooks install")
	}
	if r.PolicyOnly && (!r.RefreshOnly || r.ExpectedGeneration == nil || len(r.Files) != 0 || r.Native != nil || r.RemoveConsumer || r.PurgeNative || r.RetireNative || r.RollbackPending || r.Reservation != nil || r.ClearReservation) {
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
	// Only already-validated bounded channel revocations may use a recorded
	// runtime name without resolving damaged assets. Compare it under both locks.
	if filepath.IsAbs(r.RuntimeRoot) && !r.RevokeGemini && !r.RevokeCopilotVSCode && !r.RevokeCursor {
		r.RuntimeRoot, err = CanonicalPath(r.RuntimeRoot)
		if err != nil {
			return Ledger{}, err
		}
	}
	if r.RecoverOnly {
		if _, journalErr := os.Lstat(filepath.Join(root, "transaction.json")); os.IsNotExist(journalErr) {
			return recoverOnlyNoJournal(root)
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
	if l.WriterFloor > SupportedWriterFloor {
		return l, fmt.Errorf("installed writer floor requires a newer compatible kernel")
	}
	floor := l.WriterFloor
	if localPolicyMutation(r) && floor < LocalPolicyWriterFloor {
		floor = LocalPolicyWriterFloor
	}
	if err := validateWriterFilesAtFloor(r.Files, floor); err != nil {
		return l, err
	}
	if readErr == nil {
		if err := preflightTransactionAnchors(pending); err != nil {
			return l, err
		}
		if r.RollbackPending {
			if !ledgerMatchesJournal(l, pending.Before, pending.Native) && !ledgerMatchesJournal(l, pending.After, pending.Native) {
				return l, fmt.Errorf("rollback ledger mismatch")
			}
			if pending.Rollback {
				if e := recoverTransaction(ctx, root, l, pending, r.Fault); e != nil {
					return l, e
				}
				return readLedger(root)
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
			return readLedger(root)
		}
		if err := recoverTransaction(ctx, root, l, pending, nil); err != nil {
			return l, err
		}
		l, err = readLedger(root)
		if err != nil {
			return l, err
		}
		if l.WriterFloor > SupportedWriterFloor {
			return l, fmt.Errorf("recovered writer floor requires a newer compatible kernel")

		}
		if r.RecoverOnly {
			return l, nil
		}
	} else {
		if err := checkPolicyGeneration(root, l); err != nil {
			return l, err
		}
		if r.RollbackPending || r.RecoverOnly {
			return l, nil
		}
	}

	// Recovery can promote the floor: never continue with pre-recovery capability.
	if l.WriterFloor > SupportedWriterFloor {
		return l, fmt.Errorf("recovered writer floor requires a newer compatible kernel")
	}
	floor = l.WriterFloor
	if localPolicyMutation(r) && floor < LocalPolicyWriterFloor {
		floor = LocalPolicyWriterFloor
	}
	if err := validateWriterFilesAtFloor(r.Files, floor); err != nil {
		return l, err
	}
	if err := reservationAllows(r, l); err != nil {
		return l, err
	}

	if l.Native != nil && !policyDisableOnly(r) && !r.RevokeOpenCode && !r.RevokeGemini && !r.RevokeCopilotVSCode && !r.RevokeCursor {
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
	previous, registered := l.Consumers[r.ConsumerID]
	if r.RevokeCopilotVSCode || r.RevokeCursor {
		// Structural ledger validity is required even when its delivery tree is damaged.
		if err := validateNativeRecord(l.Native); err != nil {
			return l, err
		}
		var binding struct{ ComponentID, ControlRoot string }
		if json.Unmarshal([]byte(previous.Registration), &binding) != nil || binding.ComponentID != l.ID || binding.ControlRoot != r.ControlRoot {
			return l, fmt.Errorf("portable revocation binding/owner mismatch")
		}
	}
	if r.RevokeCursor && (!registered || !reflect.DeepEqual(previous, r.Consumer) || !cursorPortableConsumer(r.ConsumerID, previous)) {
		return l, fmt.Errorf("Cursor revocation requires its exact recorded portable consumer")
	}
	if r.RevokeCopilotVSCode && (!registered || !reflect.DeepEqual(previous, r.Consumer) || !localPortableConsumer(r.ConsumerID, previous)) {
		return l, fmt.Errorf("local revocation requires its exact recorded portable consumer")
	}
	if r.RevokeOpenCode && (!registered || previous.RuntimeRoot != r.RuntimeRoot || previous.Registration == "") {
		return l, fmt.Errorf("OpenCode revocation requires its registered runtime")
	}
	if r.RevokeGemini && (!registered || !filepath.IsAbs(previous.RuntimeRoot) ||
		filepath.Clean(previous.RuntimeRoot) != previous.RuntimeRoot || filepath.Clean(r.RuntimeRoot) != r.RuntimeRoot ||
		previous.RuntimeRoot != r.RuntimeRoot || previous.Registration == "") {
		return l, fmt.Errorf("gemini revocation requires its registered runtime")
	}
	relocating := !r.RefreshOnly && registered && previous.RuntimeRoot != "" && previous.RuntimeRoot != r.RuntimeRoot
	if relocating && (!r.RelocateVersionedCache || r.RemoveConsumer || r.ConsumerID != "claude-hooks" || r.Owner != "existing-installer" ||
		!versionedClaudeCachePeers(previous.RuntimeRoot, r.RuntimeRoot)) {
		return l, fmt.Errorf("consumer runtime relocation requires explicit takeover")
	}
	retireOldCache := relocating
	if retireOldCache {
		for id, consumer := range l.Consumers {
			if id != r.ConsumerID && consumer.RuntimeRoot == previous.RuntimeRoot {
				retireOldCache = false
				break
			}
		}
	}
	var retainedPrimaries []File
	if relocating && !retireOldCache {
		retainedPrimaries, err = retainedPortablePrimaryFiles(l, previous.RuntimeRoot, r.RuntimeRoot, r.ConsumerID, r.Files)
		if err != nil {
			return l, err
		}
	}
	if r.RefreshOnly {
		registered := false
		for _, consumer := range l.Consumers {
			if consumer.RuntimeRoot == r.RuntimeRoot {
				registered = true
			}
		}
		// The last consumer may be removed while a confirmed wizard intent is
		// still pending. Permit only its exact cleanup transaction afterward;
		// an ordinary refresh must still have a live consumer.
		finalIntentCleanup := r.ClearReservation && len(l.Consumers) == 0 && l.PendingMutation != nil &&
			r.Reservation != nil && *r.Reservation == *l.PendingMutation && r.Native == nil &&
			r.Prepare == nil && !r.PurgeNative && !r.RetireNative && !r.RollbackPending &&
			r.PolicyEnabled == nil && len(r.PolicyFields) == 0 && r.ExpectedGeneration != nil && len(r.Files) <= 1 &&
			(len(r.Files) == 0 || (r.Files[0].Remove && r.Files[0].Path == l.PendingMutation.IntentRef))
			// Retained-only reinstall has no live consumer to refresh. Permit only the
			// owner's exact private intent publication/patch, never runtime files,
			// policy changes or an unreserved refresh. The new consumer is registered
			// by the subsequent guarded install transaction.
		emptyIntentReservation := len(l.Consumers) == 0 && l.ID != "" && l.RuntimeRoot == r.RuntimeRoot &&
			r.ConsumerID == "reservation-publisher" && r.Reservation != nil && r.Reservation.Owner == r.Owner &&
			!r.ClearReservation && !r.PolicyOnly && r.ExpectedGeneration != nil && len(r.Files) == 1 &&
			!r.Files[0].Remove && r.Files[0].Link == "" && r.Files[0].Mode == 0600 && len(r.Files[0].Data) > 0 &&
			r.Files[0].Path == r.Reservation.IntentRef && r.Files[0].Path == filepath.Join(r.ControlRoot, "portable-handoff.json") &&
			r.Native == nil && r.Prepare == nil &&
			!r.PurgeNative && !r.RetireNative && !r.RollbackPending && r.PolicyEnabled == nil && len(r.PolicyFields) == 0
		if (!registered && !finalIntentCleanup && !emptyIntentReservation) || r.RemoveConsumer {
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
		if r.RevokeOpenCode || r.RevokeGemini || r.RevokeCopilotVSCode || r.RevokeCursor {
			break
		}
		// Claude owns the previous versioned cache: it may prune files or
		// restore the marketplace checkout, whose launcher link and skill mode
		// differ from what the installer wrote. Nothing here writes to that
		// root and every entry under it is de-owned below, so drift there is
		// not a foreign edit and its files are not read.
		if retireOldCache && pathWithinRoot(previous.RuntimeRoot, path) {
			continue
		}
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
	next.WriterFloor = l.WriterFloor
	next.Schema = l.Schema
	applyReservationProtocol(&next, r, l)
	if err := applyReservationState(&next, r, l); err != nil {
		return l, err
	}
	next.Owner = r.Owner
	if next.RuntimeRoot == "" {
		next.RuntimeRoot = r.RuntimeRoot
	}
	policy, policyFields, policyBefore, err := readPolicyForUpdate(root)
	if err != nil {
		return l, err
	}
	if r.ExpectedPolicy != nil && *r.ExpectedPolicy != policyBefore {
		return l, ErrPolicyConflict
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
	if retireOldCache {
		if next.RuntimeRoot == previous.RuntimeRoot {
			next.RuntimeRoot = r.RuntimeRoot
		}
		// Leave Claude's old cache bytes in place for in-flight hooks. They
		// are no longer component-owned after the last consumer leaves.
		for path := range next.Files {
			if pathWithinRoot(previous.RuntimeRoot, path) {
				delete(next.Files, path)
			}
		}
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
	if relocating {
		for _, file := range files {
			if pathWithinRoot(previous.RuntimeRoot, file.Path) {
				return l, fmt.Errorf("cache relocation cannot mutate the previous runtime")
			}
		}
	}
	files = append(files, retainedPrimaries...)
	// Last-consumer cleanup uses the identities decided under this same lock.
	// The control/state namespace is never part of this list. Callback bundles
	// have their own retained record and are intentionally not ordinary Files.
	if r.RemoveConsumer && len(next.Consumers) == 0 {
		for path, before := range l.Files {
			if next.PendingMutation != nil && path == next.PendingMutation.IntentRef {
				continue
			}
			files = append(files, File{Path: path, Before: before, Remove: true})
		}
	}
	if err := validateWriterFilesAtFloor(files, next.WriterFloor); err != nil {
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
	tx := transaction{Schema: transactionSchemaFor(next, r), Before: l, After: next, Files: files, Native: native, ConfigPaths: r.ConfigPaths}
	// Global disable is also reconstructible, but an explicit Cursor request
	// must preserve the ledger's global intent even when its leaves were already false.
	if r.RevokeCursor && (tx.Before.Enabled != tx.After.Enabled || !boundedPolicyRevocation(root, tx)) {
		return l, fmt.Errorf("Cursor revocation journal cannot reconstruct exact false-only operation")
	}
	if !boundedPolicyRevocation(root, tx) {
		tx.After, err = refreshLedgerIdentities(next)
		if err != nil {
			return l, err
		}
	}
	// Refuse a retained-native conflict before persisting a decision that its
	// own replay cannot complete. Request flags alone do not prove revocation.
	if tx.Native == nil && tx.After.Native != nil && !boundedPolicyRevocation(root, tx) {
		if err := validateRetainedNative(tx.After.Native); err != nil {
			return l, err
		}
	}
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
	return readLedger(root)
}

func validateRetainedNative(native *NativeRecord) error {
	if native == nil {
		return nil
	}
	if err := validateNativeRecord(native); err != nil {
		return err
	}
	if err := checkNativeDirectoryID(native.Path, native.DirectoryID); err != nil {
		return err
	}
	digest, err := treeFingerprint(native.Path)
	if err != nil {
		return err
	}
	if digest == "" || digest != native.SHA256 {
		return fmt.Errorf("native live fingerprint conflict")
	}
	return nil
}

func recoverTransaction(ctx context.Context, root string, current Ledger, tx transaction, fault func(string) error) error {
	if current.WriterFloor > SupportedWriterFloor || tx.Before.WriterFloor > SupportedWriterFloor || tx.After.WriterFloor > SupportedWriterFloor {
		return fmt.Errorf("installed writer floor requires a newer compatible kernel")
	}
	if !ledgerMatchesJournal(current, tx.Before, tx.Native) && !ledgerMatchesJournal(current, tx.After, tx.Native) {
		return fmt.Errorf("transaction ledger snapshot mismatch")
	}
	if current.Generation != tx.Before.Generation && current.Generation != tx.After.Generation {
		return fmt.Errorf("transaction generation mismatch")
	}
	if current.ID != "" && current.ID != tx.After.ID {
		return fmt.Errorf("transaction owner mismatch")
	}
	if err := preflightTransactionAnchors(tx); err != nil {
		return err
	}
	tx.Files = append([]File(nil), tx.Files...)
	for i, f := range tx.Files {
		anchors, e := pathAnchors(f.Path, false)
		if e != nil {
			return e
		}
		if e = checkPersistedAnchors(f.Parents, anchors); e != nil {
			return e
		}
		tx.Files[i].Parents = anchors
		got, err := replacementFingerprint(f)
		if err != nil {
			return err
		}
		if got != f.Before && got != desired(f) {
			return fmt.Errorf("recovery conflict; preserving foreign edit: %s", f.Path)
		}
	}
	// Without a native promotion, replay must still prove the active callback
	// before publishing policy. Identity refresh permits missing paths and does
	// not check bytes. Only a proven bounded revocation may bypass this.
	preserveNative := boundedPolicyRevocation(root, tx)
	if tx.Native == nil && !preserveNative {
		if err := validateRetainedNative(tx.After.Native); err != nil {
			return err
		}
		if _, err := refreshLedgerIdentities(tx.After); err != nil {
			return err
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
	after := tx.After
	if !preserveNative {
		var err error
		after, err = refreshLedgerIdentities(after)
		if err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(root, "ownership.json"), after); err != nil {
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

// No request flags survive in the journal. Prove revocation from the complete
// ledger delta and the single policy file's CAS-bound before/after bytes instead.
// Unverified native ownership is retained verbatim; this grants no asset use.
func boundedPolicyRevocation(root string, tx transaction) bool {
	before, after := tx.Before, tx.After
	if tx.Rollback || tx.Native != nil || len(tx.Files) != 1 || before.ID == "" || before.Owner == "" ||
		before.Generation == 0 || after.Generation <= before.Generation || after.PolicyGeneration <= before.PolicyGeneration ||
		after.Generation != before.Generation+1 || after.PolicyGeneration != before.PolicyGeneration+1 ||
		before.WriterFloor > SupportedWriterFloor || len(before.Consumers) == 0 {
		return false
	}
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	file := tx.Files[0]
	if file.Path != filepath.Join(physicalRoot, "agent-notifications.json") || file.Remove || file.Link != "" || file.Mode != 0600 ||
		file.Before.Link != "" || (file.Before.Exists && identity(file.BeforeData, file.Before.Mode) != file.Before) {
		return false
	}
	oldPolicy := UserPolicy{SchemaVersion: 1}
	oldFields := map[string]json.RawMessage{}
	if file.Before.Exists {
		oldPolicy, oldFields, err = decodeUserPolicy(file.BeforeData)
		if err != nil {
			return false
		}
	} else if file.Before != (Identity{}) || len(file.BeforeData) != 0 {
		return false
	}
	newPolicy, _, err := decodeUserPolicy(file.Data)
	if err != nil || after.Enabled != newPolicy.Enabled {
		return false
	}
	// Global disable keeps every other policy member unchanged. Channel
	// revocations keep global intent and require that consumer's registration.
	patches := []string{"", "geminiNotifications", "openCodeNotifications", "copilot-native", "copilot-manual", "copilot-all", "cursorNotifications"}
	for _, channel := range patches {
		if channel == "" && newPolicy.Enabled || channel != "" && (before.Enabled != oldPolicy.Enabled || newPolicy.Enabled != oldPolicy.Enabled || before.PendingMutation != nil) {
			continue
		}
		local := channel == "copilot-native" || channel == "copilot-manual" || channel == "copilot-all"
		expected := before
		// Preserve the exact monotonic migration used by the admitted request.
		applyReservationProtocol(&expected, Request{RevokeCopilotVSCode: local, RevokeCursor: channel == "cursorNotifications"}, before)
		expected.Generation, expected.PolicyGeneration, expected.Enabled = after.Generation, after.PolicyGeneration, after.Enabled
		if !reflect.DeepEqual(expected, after) {
			continue
		}
		registered := false
		for id, consumer := range before.Consumers {
			if !filepath.IsAbs(consumer.RuntimeRoot) || filepath.Clean(consumer.RuntimeRoot) != consumer.RuntimeRoot {
				continue
			}
			if before.Owner == "existing-installer" && (local && localPortableConsumer(id, consumer) || channel == "cursorNotifications" && cursorPortableConsumer(id, consumer)) {
				var binding struct{ ComponentID, ControlRoot string }
				if json.Unmarshal([]byte(consumer.Registration), &binding) == nil && binding.ComponentID == before.ID {
					// Admission requires the exact recorded spelling. A durable
					// journal may be recovered through a fixed OS alias or a clean
					// spelling, without accepting arbitrary directory symlinks.
					bindingRoot, bindingErr := PhysicalPath(binding.ControlRoot)
					recoveryRoot, recoveryErr := PhysicalPath(filepath.Clean(root))
					if bindingErr == nil && recoveryErr == nil && bindingRoot == recoveryRoot {
						registered = true
					}
				}
			}
			if channel == "" || before.Owner == "existing-installer" && consumer.Registration != "" &&
				(channel == "geminiNotifications" && id == "gemini-notifications" || channel == "openCodeNotifications" && id == "opencode-notifications") {
				registered = true
			}
		}
		if !registered {
			continue
		}
		fields := make(map[string]json.RawMessage, len(oldFields))
		for key, value := range oldFields {
			fields[key] = value
		}
		if channel != "" {
			patch := json.RawMessage(fmt.Sprintf(`{"%s":{"desktop":false,"webhook":false}}`, channel))
			switch channel {
			case "copilot-native":
				patch = json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":false,"webhook":false}}`)
			case "copilot-manual":
				patch = json.RawMessage(`{"copilotVSCodeNotifications":{"manual":{"enabled":false}}}`)
			case "copilot-all":
				patch = json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":false,"webhook":false,"manual":{"enabled":false}}}`)
			}
			if mergePolicyFields(fields, map[string]json.RawMessage{"route": patch}) != nil {
				continue
			}
		}
		want, err := policyFile(physicalRoot, newPolicy.Enabled, fields, file.Before)
		if err != nil {
			continue
		}
		// Object order and indentation are not policy changes. Strict bounded
		// decoding above has already rejected duplicate/invalid JSON members.
		var wanted, actual any
		wantDecoder, actualDecoder := json.NewDecoder(bytes.NewReader(want.Data)), json.NewDecoder(bytes.NewReader(file.Data))
		wantDecoder.UseNumber()
		actualDecoder.UseNumber()
		if wantDecoder.Decode(&wanted) == nil && actualDecoder.Decode(&actual) == nil && reflect.DeepEqual(wanted, actual) {
			return true
		}
	}
	return false
}
