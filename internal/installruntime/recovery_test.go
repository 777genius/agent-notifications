package installruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recoveryTransaction() transaction {
	return transaction{
		Schema: transactionSchemaV2,
		Before: Ledger{Schema: ledgerSchemaV2, ID: "runtime", Generation: 1, PolicyGeneration: 1, Consumers: map[string]Consumer{}, Files: map[string]Identity{}},
		After:  Ledger{Schema: ledgerSchemaV2, ID: "runtime", Generation: 2, PolicyGeneration: 2, Consumers: map[string]Consumer{}, Files: map[string]Identity{}},
	}
}

func TestLargeAssetTransactionStaysWithinReadCap(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "transaction.json")
	payload := bytes.Repeat([]byte("a"), 25<<20)
	tx := recoveryTransaction()
	tx.Files = []File{{Path: filepath.Join(t.TempDir(), "asset"), Data: payload, Mode: 0600}}
	if err := writeTransaction(marker, tx); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxManagedFile {
		t.Fatalf("journal %d exceeds read cap %d", info.Size(), maxManagedFile)
	}
	got, err := readTransactionFile(marker)
	if err != nil || len(got.Files) != 1 || !bytes.Equal(got.Files[0].Data, payload) {
		t.Fatalf("round-trip: %v %d", err, len(got.Files))
	}
}

func TestOversizedTransactionPayloadRejectedBeforeDurableWrite(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "transaction.json")
	payload := bytes.Repeat([]byte("b"), maxManagedFile+1)
	tx := recoveryTransaction()
	tx.Files = []File{{Path: filepath.Join(t.TempDir(), "asset"), Data: payload, Mode: 0600}}
	if err := writeTransaction(marker, tx); err == nil {
		t.Fatal("oversized payload persisted")
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("durable marker written for oversized payload")
	}
	if entries, err := os.ReadDir(transactionBlobDir(marker)); err == nil && len(entries) != 0 {
		t.Fatal("blob directory retained oversized payload")
	}
}

func TestDiscardTransactionBlobsRefusesDirectorySymlink(t *testing.T) {
	foreign := t.TempDir()
	sentinel := filepath.Join(foreign, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	control := t.TempDir()
	marker := filepath.Join(control, "transaction.json")
	if err := os.WriteFile(marker, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, transactionBlobDir(marker)); err != nil {
		t.Fatal(err)
	}
	if err := discardTransactionBlobs(marker); err == nil {
		t.Fatal("followed blob directory symlink")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("deleted foreign sentinel: %s %v", data, err)
	}
}

func TestReversePublishedMutatedNativeRefused(t *testing.T) {
	skipUnsupportedNative(t)
	before := nativeBundle(t)
	hashA, err := treeFingerprint(before)
	if err != nil || hashA == "" {
		t.Fatal(err)
	}
	after := nativeBundle(t)
	hashB, err := treeFingerprint(after)
	if err != nil || hashB == "" {
		t.Fatal(err)
	}
	tx := recoveryTransaction()
	tx.Native = &NativeChange{
		Before: NativeRecord{Path: before, SHA256: hashA},
		After:  NativeRecord{Path: after, SHA256: hashB},
	}
	tx.Before.Native = &NativeRecord{Path: before, SHA256: hashA}
	tx.After.Native = &NativeRecord{Path: after, SHA256: hashB}
	if err := os.WriteFile(filepath.Join(after, "Contents", "MacOS", "terminal-notifier-modern"), []byte("mutated"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := reverseTransaction(tx.After, tx); err == nil {
		t.Fatal("mutated published native rolled back")
	}
}

func TestReverseUnpublishedNativeKeepsPredecessor(t *testing.T) {
	skipUnsupportedNative(t)
	before := nativeBundle(t)
	hashA, err := treeFingerprint(before)
	if err != nil || hashA == "" {
		t.Fatal(err)
	}
	afterPath := filepath.Join(t.TempDir(), "ClaudeNotifier.app")
	hashB := strings.Repeat("a", 64)
	tx := recoveryTransaction()
	tx.Native = &NativeChange{
		Before: NativeRecord{Path: before, SHA256: hashA},
		After:  NativeRecord{Path: afterPath, SHA256: hashB},
	}
	tx.Before.Native = &NativeRecord{Path: before, SHA256: hashA}
	tx.After.Native = &NativeRecord{Path: afterPath, SHA256: hashB}
	reverse, err := reverseTransaction(tx.After, tx)
	if err != nil {
		t.Fatal(err)
	}
	if reverse.After.Native == nil || reverse.After.Native.Path != before || reverse.After.Native.SHA256 != hashA {
		t.Fatalf("predecessor lost: %+v", reverse.After.Native)
	}
	if reverse.Native == nil || reverse.Native.After.Path != before || reverse.Native.After.SHA256 != hashA {
		t.Fatalf("predecessor NativeChange not persisted: %+v", reverse.Native)
	}
}

func TestReversePublishedNativePersistsChange(t *testing.T) {
	skipUnsupportedNative(t)
	before := nativeBundle(t)
	hashA, err := treeFingerprint(before)
	if err != nil || hashA == "" {
		t.Fatal(err)
	}
	idA, err := nativeDirectoryID(before)
	if err != nil || idA == "" {
		t.Fatal(err)
	}
	after := nativeBundle(t)
	hashB, err := treeFingerprint(after)
	if err != nil || hashB == "" {
		t.Fatal(err)
	}
	idB, err := nativeDirectoryID(after)
	if err != nil || idB == "" {
		t.Fatal(err)
	}
	tx := recoveryTransaction()
	tx.Native = &NativeChange{
		Before: NativeRecord{Path: before, SHA256: hashA, DirectoryID: idA},
		After:  NativeRecord{Path: after, SHA256: hashB, DirectoryID: idB},
	}
	tx.Before.Native = &NativeRecord{Path: before, SHA256: hashA, DirectoryID: idA}
	tx.After.Native = &NativeRecord{Path: after, SHA256: hashB, DirectoryID: idB}
	reverse, err := reverseTransaction(tx.After, tx)
	if err != nil {
		t.Fatal(err)
	}
	if reverse.After.Native == nil || reverse.After.Native.Path != after || reverse.After.Native.SHA256 != hashB {
		t.Fatalf("published native lost: %+v", reverse.After.Native)
	}
	if reverse.Native == nil || reverse.Native.After.Path != after || reverse.Native.After.DirectoryID != idB || reverse.Native.Staged != after {
		t.Fatalf("published NativeChange not persisted: %+v", reverse.Native)
	}
}

func TestDiscardTransactionBlobsPreservesUnrecognizedFiles(t *testing.T) {
	control := t.TempDir()
	marker := filepath.Join(control, "transaction.json")
	dir := transactionBlobDir(marker)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("a"), maxInlineTransactionBytes+1)
	sum := sha256.Sum256(payload)
	name := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(dir, name), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := discardTransactionBlobs(marker); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(notes); err != nil || string(data) != "keep" {
		t.Fatalf("deleted unowned notes: %s %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatal("owned blob retained")
	}
}

func TestDiscardTransactionBlobsRefusesChangedBlob(t *testing.T) {
	control := t.TempDir()
	marker := filepath.Join(control, "transaction.json")
	dir := transactionBlobDir(marker)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("a", 64)
	changed := filepath.Join(dir, name)
	if err := os.WriteFile(changed, []byte("not-the-digest"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := discardTransactionBlobs(marker); err == nil {
		t.Fatal("changed blob deleted")
	}
	if data, err := os.ReadFile(changed); err != nil || string(data) != "not-the-digest" {
		t.Fatalf("changed blob mutated: %s %v", data, err)
	}
}

func TestReversePublishedReplacedNativeIdentityRefused(t *testing.T) {
	skipUnsupportedNative(t)
	before := nativeBundle(t)
	hashA, err := treeFingerprint(before)
	if err != nil || hashA == "" {
		t.Fatal(err)
	}
	idA, err := nativeDirectoryID(before)
	if err != nil || idA == "" {
		t.Fatal(err)
	}
	after := nativeBundle(t)
	hashB, err := treeFingerprint(after)
	if err != nil || hashB == "" {
		t.Fatal(err)
	}
	idB, err := nativeDirectoryID(after)
	if err != nil || idB == "" {
		t.Fatal(err)
	}
	replaceNativeTree(t, after)
	tx := recoveryTransaction()
	tx.Native = &NativeChange{
		Before: NativeRecord{Path: before, SHA256: hashA, DirectoryID: idA},
		After:  NativeRecord{Path: after, SHA256: hashB, DirectoryID: idB},
	}
	tx.Before.Native = &NativeRecord{Path: before, SHA256: hashA, DirectoryID: idA}
	tx.After.Native = &NativeRecord{Path: after, SHA256: hashB, DirectoryID: idB}
	if _, err := reverseTransaction(tx.After, tx); err == nil {
		t.Fatal("replaced published native identity rolled back")
	}
}

func TestReverseUnpublishedReplacedPredecessorRefused(t *testing.T) {
	skipUnsupportedNative(t)
	before := nativeBundle(t)
	hashA, err := treeFingerprint(before)
	if err != nil || hashA == "" {
		t.Fatal(err)
	}
	idA, err := nativeDirectoryID(before)
	if err != nil || idA == "" {
		t.Fatal(err)
	}
	replaceNativeTree(t, before)
	afterPath := filepath.Join(t.TempDir(), "ClaudeNotifier.app")
	tx := recoveryTransaction()
	tx.Native = &NativeChange{
		Before: NativeRecord{Path: before, SHA256: hashA, DirectoryID: idA},
		After:  NativeRecord{Path: afterPath, SHA256: strings.Repeat("a", 64)},
	}
	tx.Before.Native = &NativeRecord{Path: before, SHA256: hashA, DirectoryID: idA}
	tx.After.Native = &NativeRecord{Path: afterPath, SHA256: strings.Repeat("a", 64)}
	if _, err := reverseTransaction(tx.After, tx); err == nil {
		t.Fatal("replaced predecessor identity rolled back")
	}
}

func replaceNativeTree(t *testing.T, path string) {
	t.Helper()
	replica := filepath.Join(t.TempDir(), filepath.Base(path))
	if err := copyTree(path, replica); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replica, path); err != nil {
		t.Fatal(err)
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
