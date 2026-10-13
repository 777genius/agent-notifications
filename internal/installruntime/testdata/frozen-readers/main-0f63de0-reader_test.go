package installruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type frozenEnvelope struct {
	SHA256      string
	Transaction json.RawMessage
}

func frozenEnvelopeBytes(t *testing.T, body []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(body)
	data, err := json.Marshal(frozenEnvelope{SHA256: hex.EncodeToString(sum[:]), Transaction: body})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func frozenDecodeFixture(t *testing.T) ([]byte, json.RawMessage) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "windows6-missing-blobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope frozenEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	return data, envelope.Transaction
}

func frozenMutateBody(t *testing.T, body json.RawMessage, mutate func(map[string]any)) []byte {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	mutate(fields)
	next, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return frozenEnvelopeBytes(t, next)
}

func frozenDirectoryImage(t *testing.T, root string) map[string]string {
	t.Helper()
	image := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		image[relative] = info.Mode().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return image
}

// This overlay runs only against the exact 0f63de0 archive. Its production
// recovery.go, orphan_codec.go, reservation.go and writer.go bytes are checked
// by the current-tree provenance gate before this subprocess is launched.
func TestActualMain0F63RejectsWindows6BeforeBlobs(t *testing.T) {
	if SupportedWriterFloor != 4 {
		t.Fatalf("actual 0f63de0 writer ceiling changed: %d", SupportedWriterFloor)
	}
	if !acceptedTransactionSchema(transactionSchemaV5) || acceptedLedgerSchema(transactionSchemaV5) {
		t.Fatal("schema5 is not precisely an orphan transaction decoder")
	}
	if acceptedLedgerSchema(4) != true || acceptedTransactionSchema(6) {
		t.Fatal("actual 0f63de0 schema gate changed")
	}

	valid, body := frozenDecodeFixture(t)
	blobDir := filepath.Join(t.TempDir(), "transaction.blobs")
	if err := os.Mkdir(blobDir, 0700); err != nil {
		t.Fatal(err)
	}
	before := frozenDirectoryImage(t, filepath.Dir(blobDir))

	duplicate := bytesReplace(valid, []byte(`"Schema":6`), []byte(`"Schema":6,"Schema":6`), 1)
	if _, err := decodeTransactionBlobs(duplicate, blobDir); err == nil || !strings.Contains(err.Error(), "invalid transaction journal JSON") {
		t.Fatalf("strict JSON duplicate reached later gates: %v", err)
	}

	var corrupt frozenEnvelope
	if err := json.Unmarshal(valid, &corrupt); err != nil {
		t.Fatal(err)
	}
	corrupt.SHA256 = strings.Repeat("0", 64)
	corruptBytes, err := json.Marshal(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeTransactionBlobs(corruptBytes, blobDir); err == nil || !strings.Contains(err.Error(), "transaction checksum mismatch") {
		t.Fatalf("checksum corruption reached later gates: %v", err)
	}

	if _, err := decodeTransactionBlobs(valid, blobDir); err == nil || !strings.Contains(err.Error(), "unsupported transaction schema") {
		t.Fatalf("actual main reached schema6 unmarshal/blob attachment: %v", err)
	}

	writerFloor6 := frozenMutateBody(t, body, func(fields map[string]any) {
		fields["Schema"] = float64(transactionSchemaV4)
		for _, name := range []string{"Before", "After"} {
			image := fields[name].(map[string]any)
			image["Schema"] = float64(transactionSchemaV4)
			image["WriterFloor"] = float64(6)
		}
	})
	if _, err := decodeTransactionBlobs(writerFloor6, blobDir); err == nil || !strings.Contains(err.Error(), "unsupported transaction writer floor") {
		t.Fatalf("actual main attached blobs before refusing ordinary schema4 writer floor6: %v", err)
	}

	after := frozenDirectoryImage(t, filepath.Dir(blobDir))
	if len(before) != len(after) {
		t.Fatal("legacy decode changed the bounded filesystem image")
	}
	for name, mode := range before {
		if after[name] != mode {
			t.Fatalf("legacy decode changed %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(blobDir, strings.Repeat("a", 64))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy decode opened or created a missing blob: %v", err)
	}
}

func bytesReplace(data, old, replacement []byte, count int) []byte {
	return []byte(strings.Replace(string(data), string(old), string(replacement), count))
}
