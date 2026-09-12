package installruntime

import (
	"bytes"
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
}
