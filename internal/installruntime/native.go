package installruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	ledgerSchemaV1      = 1
	ledgerSchemaV2      = 2
	ledgerSchemaV3      = 3
	transactionSchemaV1 = 1
	transactionSchemaV2 = 2
	transactionSchemaV3 = 3
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
	defer func() { _ = tree.Close() }()
	hash := sha256.New()
	err = walkNativeTree(tree, func(rel string, info os.FileInfo, f *os.File) error {
		size := info.Size()
		if info.IsDir() {
			size = 0
		}
		_, _ = fmt.Fprintf(hash, "%d:%s:%o:%d\n", len(rel), rel, info.Mode().Perm(), size)
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
	defer func() { _ = sourceRoot.Close() }()
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
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, path, args...)
	command.WaitDelay = 100 * time.Millisecond
	out := &limitedOutput{}
	command.Stdout = out
	command.Stderr = io.Discard
	err := command.Run()
	if commandCtx.Err() != nil {
		return out.data, commandCtx.Err()
	}
	return out.data, err
}
