package installruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

const (
	ledgerSchemaV1      = 1
	ledgerSchemaV2      = 2
	transactionSchemaV1 = 1
	transactionSchemaV2 = 2
)

// NativeGeneration is one published callback identity. Paths and inodes stay
// durable after later promotions; the ledger only selects the active artifact.
type NativeGeneration struct {
	DirectoryID, Path, SHA256 string
	DecoderFloor              int
	Attestation               []byte
	InstalledTreeSHA256       string
}

// NativeRecord is separate from ordinary runtime files: final consumer removal
// retains the callback reader and its previously published artifacts by default.
type NativeRecord struct {
	DirectoryID, PreviousDirectoryID string
	// Attestation stores the external post-signing package evidence durably,
	// outside the signed bundle. InstalledTreeSHA256 binds it to bundle bytes.
	Attestation                                []byte
	InstalledTreeSHA256                        string
	Path, SHA256, PreviousPath, PreviousSHA256 string
	DecoderFloor                               int
	Published                                  []NativeGeneration
}
type NativeChange struct {
	Parents       []PathAnchor
	PurgeTrees    []PurgeTree
	Staged        string
	StagedID      string
	Before, After NativeRecord
	Purge         bool
	Retire        bool
}
type nativeManifest struct {
	SchemaVersion, ProtocolVersion, DecoderFloor int
	ExecutableSHA256                             string
}

// treeFingerprint rejects links/special nodes rather than hashing a different
// file from the one the OS will execute. Names, modes and bytes are all bound.
func treeFingerprint(root string) (string, error) {
	if info, err := os.Lstat(root); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", fmt.Errorf("native root must be a non-link directory")
	}
	tree, err := openNativeRoot(root)
	if err != nil {
		return "", err
	}
	defer tree.Close()
	hash := sha256.New()
	err = walkNativeTree(tree, func(rel string, info os.FileInfo, f *os.File) error {
		size := info.Size()
		if info.IsDir() {
			size = 0
		}
		fmt.Fprintf(hash, "%d:%s:%o:%d\n", len(rel), rel, info.Mode().Perm(), size)
		if f == nil {
			return nil
		}
		_, err := io.Copy(hash, f)
		return err
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// StageNative accepts executable-code trust from the adapter's explicitly
// selected installation source (the same package authority as the Go sender).
// A self-signed manifest does not establish that authority. Shell uses its fixed
// upstream HTTPS release source; setup uses the user-selected plugin package.
// Missing manifests are legacy and never probed. Existing bin fallbacks must use
// StageRetainedNative instead: their presence does not authorize execution.
func StageNative(ctx context.Context, control, source string) (*NativeChange, error) {
	return stageNative(ctx, control, source, true)
}

// StageRetainedNative preserves existing unknown callbacks without probing them.
// Only a previously recorded exact managed fingerprint can retain a known floor.
func StageRetainedNative(ctx context.Context, control, source string) (*NativeChange, error) {
	return stageNative(ctx, control, source, false)
}

func stageNative(ctx context.Context, control, source string, sourceTrusted bool) (*NativeChange, error) {
	var resolveErr error
	source, resolveErr = filepath.EvalSymlinks(source)
	if resolveErr != nil {
		return nil, resolveErr
	}
	if control == "" {
		var err error
		control, err = ControlRoot()
		if err != nil {
			return nil, err
		}
	}
	control, err := CanonicalPath(control)
	if err != nil {
		return nil, err
	}
	sourceRoot, err := openNativeRoot(source)
	if err != nil {
		return nil, err
	}
	defer sourceRoot.Close()
	parent := filepath.Join(control, "native")
	initialAnchors, err := pathAnchors(filepath.Join(parent, ".identity"), true)
	if err != nil {
		return nil, err
	}
	stage, stagedID, err := allocateNativeStage(ctx, parent, initialAnchors)
	if err != nil {
		return nil, err
	}
	// The caller may discard this only before a durable transaction references it.
	failed := true
	defer func() {
		if failed {
			_ = removeFailedNativeStage(stage, initialAnchors, stagedID)
		}
	}()
	err = copyOpenedNativeTree(sourceRoot, stage, stagedID)
	if err != nil {
		return nil, err
	}

	if err := syncNativeTree(stage); err != nil {
		return nil, err
	}
	if err := syncDir(parent); err != nil {
		return nil, err
	}

	digest, err := treeFingerprint(stage)
	if err != nil {
		return nil, err
	}
	ledger, err := readLedger(control)
	if err != nil {
		return nil, err
	}
	floor := 0
	var attestation []byte
	if ledger.Native != nil && ledger.Native.SHA256 == digest {
		floor = ledger.Native.DecoderFloor
		attestation = append([]byte(nil), ledger.Native.Attestation...)
	} else if sourceTrusted {
		attestation, err = readRegularFile(source + ".managed-runtime.json")
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		floor, err = verifyNativeEvidence(ctx, stage, attestation)
		if err != nil {
			return nil, err
		}
	}
	// Fresh installs publish a unique generation. Existing managed callbacks
	// keep every already-published path; the ledger only selects the active one.
	live := filepath.Join(parent, "ClaudeNotifier.app")
	if ledger.Native != nil {
		if filepath.Dir(ledger.Native.Path) != parent {
			return nil, fmt.Errorf("native owner outside persistent directory")
		}
		live = ledger.Native.Path
	}
	before, err := treeFingerprint(live)
	if err != nil {
		return nil, err
	}
	anchors, err := pathAnchors(live, false)
	if err != nil {
		return nil, err
	}
	if err = checkAnchors(initialAnchors, anchors); err != nil {
		return nil, err
	}
	directoryID, err := nativeDirectoryID(stage)
	if err != nil {
		return nil, err
	}
	if directoryID != stagedID {
		return nil, fmt.Errorf("native candidate inode changed during staging")
	}
	published := nativeGenerationPath(parent, directoryID)
	after := NativeRecord{DirectoryID: directoryID, Path: published, SHA256: digest, DecoderFloor: floor, Attestation: attestation, InstalledTreeSHA256: installedNativeHash(digest, attestation)}
	if ledger.Native != nil {
		after = publishNativeRecord(*ledger.Native, after)
	} else {
		after.Published = []NativeGeneration{nativeGenerationOf(after)}
	}
	failed = false
	return &NativeChange{Parents: anchors, Staged: stage, StagedID: stagedID, Before: NativeRecord{Path: live, SHA256: before}, After: after}, nil
}

// limitedOutput terminates a noisy helper instead of allocating unbounded data.
type limitedOutput struct {
	data     []byte
	overflow bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > 16384 {
		b.overflow = true
		return 0, fmt.Errorf("native output exceeds limit")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func boundedCommand(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.WaitDelay = 100 * time.Millisecond
	out := &limitedOutput{}
	command.Stdout = out
	command.Stderr = io.Discard
	err := command.Run()
	return out.data, err
}

// Native OS verification is replaced only by inert protocol fixtures in tests.
var nativePlatformCheck = verifyNativePlatform

func verifyNative(ctx context.Context, bundle, attestationPath string) (int, error) {
	evidence, err := readRegularFile(attestationPath)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	return verifyNativeEvidence(ctx, bundle, evidence)
}

func verifyNativeEvidence(ctx context.Context, bundle string, attestation []byte) (int, error) {
	manifestPath := filepath.Join(bundle, "Contents", "Resources", "managed-runtime.json")
	data, err := readRegularFile(manifestPath)
	if os.IsNotExist(err) {
		return 0, nil
	} // Never execute a legacy/unknown notifier.
	if err != nil {
		return 0, err
	}
	var manifest nativeManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.SchemaVersion != 1 || manifest.ProtocolVersion != 1 || manifest.DecoderFloor != 1 || manifest.ExecutableSHA256 != "" {
		return 0, fmt.Errorf("unsupported native offline manifest")
	}
	// The sealed marker states the protocol floor. The adjacent package
	// attestation is emitted AFTER signing: embedding a final executable hash
	// in its own signed resource seal would introduce a hash/signature cycle.
	// Neither file grants publisher trust; the adapter supplies source authority.
	if len(attestation) == 0 {
		return 0, nil
	} // Unknown fingerprint: never probe.
	var expected nativeManifest
	if json.Unmarshal(attestation, &expected) != nil || expected.SchemaVersion != manifest.SchemaVersion || expected.ProtocolVersion != manifest.ProtocolVersion || expected.DecoderFloor != manifest.DecoderFloor {
		return 0, fmt.Errorf("native package attestation disagrees with sealed floor")
	}
	executable, err := readRegularFile(filepath.Join(bundle, "Contents", "MacOS", "terminal-notifier-modern"))
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(executable)
	if expected.ExecutableSHA256 != hex.EncodeToString(digest[:]) {
		return 0, fmt.Errorf("native offline executable fingerprint mismatch")
	}
	if err := nativePlatformCheck(ctx, bundle); err != nil {
		return 0, err
	}
	// Signature verifies bundle integrity, not publisher trust. Bind staged bytes
	// around the probe; additional supported actions do not enable those actions.
	before, err := treeFingerprint(bundle)
	if err != nil {
		return 0, err
	}
	output, err := boundedCommand(ctx, filepath.Join(bundle, "Contents", "MacOS", "terminal-notifier-modern"), "--capabilities-json")
	if err != nil {
		return 0, err
	}
	if err := validateNativeCapabilities(output); err != nil {
		return 0, err
	}
	after, err := treeFingerprint(bundle)
	if err != nil {
		return 0, err
	}
	if before != after {
		return 0, fmt.Errorf("native changed during probe")
	}
	return manifest.DecoderFloor, nil
}

func validateNativeCapabilities(output []byte) error {
	caps, err := nativeprotocol.DecodeCapabilities(output)
	if err != nil || !caps.Supports(1, "none") {
		return fmt.Errorf("native capabilities disagree with bounded PR1 floor")
	}
	return nil
}

func validateNative(change *NativeChange) error {
	if change == nil {
		return nil
	}
	if err := checkNativeParents(change); err != nil {
		return err
	}
	current, err := treeFingerprint(change.After.Path)
	if err != nil {
		return err
	}
	if change.Purge {
		if current != "" && current != change.Before.SHA256 {
			return fmt.Errorf("native purge fingerprint conflict")
		}
		if current != "" {
			return checkNativeDirectoryID(change.Before.Path, change.Before.DirectoryID)
		}
		return nil
	}
	if current == change.After.SHA256 {
		return checkNativeDirectoryID(change.After.Path, change.After.DirectoryID)
	}
	if change.Before.SHA256 != "" && change.Before.Path != change.After.Path {
		before, err := treeFingerprint(change.Before.Path)
		if err != nil {
			return err
		}
		if before != change.Before.SHA256 {
			return fmt.Errorf("native live fingerprint conflict")
		}
		if err := checkNativeDirectoryID(change.Before.Path, change.Before.DirectoryID); err != nil {
			return err
		}
	} else if current != "" && current != change.Before.SHA256 {
		return fmt.Errorf("native live fingerprint conflict")
	} else if current != "" {
		if err := checkNativeDirectoryID(change.Before.Path, change.Before.DirectoryID); err != nil {
			return err
		}
	}
	staged, err := treeFingerprint(change.Staged)
	if err != nil {
		return err
	}
	if staged != change.After.SHA256 {
		return fmt.Errorf("native staged fingerprint conflict")
	}
	id := change.StagedID
	if id == "" {
		id = change.After.DirectoryID
	}
	return checkNativeDirectoryID(change.Staged, id)
}
func promoteNative(change *NativeChange) error {
	if change == nil {
		return nil
	}
	if err := validateNative(change); err != nil {
		return err
	}
	current, err := treeFingerprint(change.After.Path)
	if err != nil {
		return err
	}
	if change.Purge {
		if current == "" {
			return nil
		}
		// Remove only the explicitly purged, fingerprint-checked owned callback.
		if err := renameNative(change.Before.Path, change.Staged, change.Parents, change.Before.DirectoryID); err != nil {
			return err
		}
		return syncDir(filepath.Dir(change.Before.Path))
	}
	if current == change.After.SHA256 {
		return nil
	}
	if current == "" {
		err = renameNative(change.Staged, change.After.Path, change.Parents, change.After.DirectoryID)
	} else if change.Before.Path != "" && change.Before.Path != change.After.Path {
		// Publish the new generation beside the previous one. Never exchange
		// the already-sent callback identity.
		err = renameNative(change.Staged, change.After.Path, change.Parents, change.After.DirectoryID)
	} else {
		return fmt.Errorf("new native bytes reused live callback identity")
	}
	if err != nil {
		return err
	}
	return syncDir(filepath.Dir(change.After.Path))
}

// NativeAlias retains every pre-existing concrete bundle path. Hook discovery
// uses a stable ClaudeNotifier.app name that is retargeted at the active
// published generation. Retargeting that alias does not swap the queued
// callback inode.
func NativeAlias(change *NativeChange, bin string) ([]File, error) {
	if change == nil {
		return nil, nil
	}
	path := filepath.Join(bin, "ClaudeNotifier.app")
	info, err := os.Lstat(path)
	if err == nil && info.IsDir() {
		return nil, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	before, err := Fingerprint(path)
	if err != nil {
		return nil, err
	}
	if before.Exists && before.Link != change.After.Path && !ownedNativeAlias(before.Link, change) {
		return nil, fmt.Errorf("foreign native alias cannot be replaced")
	}
	return []File{{Path: path, Before: before, Link: change.After.Path}}, nil
}

// DiscardNative removes only this invocation's unused, unchanged candidate.
// The component lock and recovery references prevent deleting either an
// interrupted transaction's input or the previous callback retained by a swap.
// This is best-effort staging cleanup, never the transaction's rollback path.
func DiscardNative(ctx context.Context, control string, change *NativeChange) error {
	if change == nil {
		return nil
	}
	if control == "" {
		var err error
		control, err = ControlRoot()
		if err != nil {
			return err
		}
	}
	unlock, err := Lock(ctx, filepath.Join(control, ".component-install.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	parent, err := filepath.EvalSymlinks(filepath.Join(control, "native"))
	if err != nil {
		return err
	}
	if filepath.Dir(change.Staged) != parent || !strings.HasPrefix(filepath.Base(change.Staged), ".candidate-") {
		return fmt.Errorf("candidate cleanup path is not owned staging")
	}
	ledger, err := readLedger(control)
	if err != nil {
		return err
	}
	if ledger.Native != nil && nativeRecordContainsPath(*ledger.Native, change.Staged) {
		return nil
	}
	data, err := readRegularFile(filepath.Join(control, "transaction.json"))
	if err == nil {
		tx, err := decodeTransactionBlobs(data, transactionBlobDir(filepath.Join(control, "transaction.json")))
		if err != nil {
			return err
		}
		if tx.Native != nil && tx.Native.Staged == change.Staged {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	digest, err := treeFingerprint(change.Staged)
	if err != nil || digest == "" {
		return err
	}
	if digest != change.After.SHA256 {
		return fmt.Errorf("candidate changed; preserving for inspection")
	}
	if err := discardNativeCandidate(change); err != nil {
		return err
	}
	return syncDir(parent)
}

// cleanupPurgedNative is replayable while the transaction marker still exists.
// Explicit purge authorizes removal of only these exact recorded identities;
// unknown files and the journal namespace never participate.
func cleanupPurgedNative(change *NativeChange, fault func(string) error) error {
	if change == nil || (!change.Purge && !change.Retire) {
		return nil
	}
	if len(change.PurgeTrees) == 0 {
		return fmt.Errorf("legacy purge marker lacks per-entry ownership; preserve for inspection")
	}
	for _, tree := range change.PurgeTrees {
		if filepath.Dir(tree.Path) != filepath.Dir(change.Before.Path) || !nativeRecordContainsPath(change.Before, tree.Path) && tree.Path != change.Staged {
			return fmt.Errorf("invalid purge tree ownership")
		}
		if err := cleanupPurgeTree(tree, fault); err != nil {
			return err
		}
	}
	return syncDir(filepath.Dir(change.Before.Path))
}

func allocateNativeStage(ctx context.Context, parent string, expected []PathAnchor) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	root, err := openNativeRoot(parent)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	if err = checkOpenedNativeRoot(root, expected); err != nil {
		return "", "", err
	}
	release, err := lockNativeStage(ctx, root)
	if err != nil {
		return "", "", err
	}
	defer release()
	directory, err := root.Open(".")
	if err != nil {
		return "", "", err
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return "", "", err
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".candidate-") {
			count++
		}
	}
	if count >= 16 {
		return "", "", fmt.Errorf("native staging capacity reached; inspect orphaned candidates")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", "", err
	}
	// The exchanged predecessor keeps this name; retain an app bundle suffix.
	name := ".candidate-" + hex.EncodeToString(random[:]) + ".app"
	if err = root.Mkdir(name, 0700); err != nil {
		return "", "", err
	}
	child, err := root.Open(name)
	if err != nil {
		return "", "", err
	}
	id, err := openedDirectoryIdentity(child)
	child.Close()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(parent, name), id, nil
}

func installedNativeHash(bundleHash string, evidence []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "managed-native-v1:%s:%d:", bundleHash, len(evidence))
	h.Write(evidence)
	return hex.EncodeToString(h.Sum(nil))
}

func validateNativeRecord(record *NativeRecord) error {
	if record == nil {
		return nil
	}
	if record.InstalledTreeSHA256 != installedNativeHash(record.SHA256, record.Attestation) {
		return fmt.Errorf("installed native attestation binding mismatch")
	}
	for _, gen := range record.Published {
		if gen.Path == "" || gen.SHA256 == "" || gen.InstalledTreeSHA256 != installedNativeHash(gen.SHA256, gen.Attestation) {
			return fmt.Errorf("published native generation binding mismatch")
		}
		if filepath.Dir(gen.Path) != filepath.Dir(record.Path) {
			return fmt.Errorf("published native generation escaped owner")
		}
	}
	return nil
}

func nativeGenerationPath(parent, directoryID string) string {
	return filepath.Join(parent, "generation-"+directoryID+".app")
}

func nativeGenerationOf(record NativeRecord) NativeGeneration {
	return NativeGeneration{
		DirectoryID: record.DirectoryID, Path: record.Path, SHA256: record.SHA256,
		DecoderFloor: record.DecoderFloor, Attestation: append([]byte(nil), record.Attestation...),
		InstalledTreeSHA256: record.InstalledTreeSHA256,
	}
}

func importNativeGenerations(record NativeRecord) []NativeGeneration {
	seen := map[string]bool{}
	var out []NativeGeneration
	add := func(gen NativeGeneration) {
		if gen.Path == "" || gen.SHA256 == "" || seen[gen.Path] {
			return
		}
		seen[gen.Path] = true
		out = append(out, gen)
	}
	// The live record is authoritative for the active generation, including
	// qualification evidence patched after the original Published snapshot.
	add(nativeGenerationOf(record))
	for _, gen := range record.Published {
		add(gen)
	}
	if record.PreviousPath != "" && record.PreviousSHA256 != "" {
		add(NativeGeneration{
			DirectoryID: record.PreviousDirectoryID, Path: record.PreviousPath, SHA256: record.PreviousSHA256,
			DecoderFloor: record.DecoderFloor, InstalledTreeSHA256: installedNativeHash(record.PreviousSHA256, nil),
		})
	}
	return out
}

func nativeGenerationByDigest(record NativeRecord, digest string) *NativeGeneration {
	for _, gen := range importNativeGenerations(record) {
		if gen.SHA256 == digest {
			g := gen
			return &g
		}
	}
	return nil
}

func nativeRecordContainsPath(record NativeRecord, path string) bool {
	if record.Path == path || record.PreviousPath == path {
		return true
	}
	for _, gen := range record.Published {
		if gen.Path == path {
			return true
		}
	}
	return false
}

func ownedNativeAlias(link string, change *NativeChange) bool {
	if change == nil || link == "" {
		return false
	}
	parent := filepath.Dir(change.After.Path)
	if filepath.Dir(link) != parent {
		return false
	}
	return nativeRecordContainsPath(change.Before, link) || nativeRecordContainsPath(change.After, link)
}

func publishNativeRecord(before, after NativeRecord) NativeRecord {
	gens := importNativeGenerations(before)
	if match := nativeGenerationByDigest(NativeRecord{Published: gens}, after.SHA256); match != nil {
		after.Path = match.Path
		after.DirectoryID = match.DirectoryID
		after.Attestation = append([]byte(nil), match.Attestation...)
		after.InstalledTreeSHA256 = match.InstalledTreeSHA256
		if after.DecoderFloor < match.DecoderFloor {
			after.DecoderFloor = match.DecoderFloor
		}
		after.Published = gens
	} else {
		after.Published = append(gens, nativeGenerationOf(after))
	}
	if before.Path != "" && before.Path != after.Path {
		after.PreviousPath = before.Path
		after.PreviousSHA256 = before.SHA256
		after.PreviousDirectoryID = before.DirectoryID
	}
	return after
}
