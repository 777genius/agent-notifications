package installruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// A checksum detects valid-JSON corruption of the recovery record. It is not
// authentication against the user who owns the private control directory.
type transactionEnvelope struct {
	SHA256      string
	Transaction json.RawMessage
}

func writeTransaction(path string, tx transaction) error {
	files := make([]File, len(tx.Files))
	copy(files, tx.Files)
	tx.Files = files
	blobDir := transactionBlobDir(path)
	for i, file := range tx.Files {
		data, ref, err := persistTransactionBytes(blobDir, file.Data)
		if err != nil {
			return err
		}
		tx.Files[i].Data = data
		tx.Files[i].DataSHA256 = ref
		before, beforeRef, err := persistTransactionBytes(blobDir, file.BeforeData)
		if err != nil {
			return err
		}
		tx.Files[i].BeforeData = before
		tx.Files[i].BeforeDataSHA256 = beforeRef
	}
	data, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	if len(data) > maxManagedFile {
		return fmt.Errorf("transaction journal exceeds size limit")
	}
	digest := sha256.Sum256(data)
	envelope, err := json.MarshalIndent(transactionEnvelope{hex.EncodeToString(digest[:]), data}, "", "  ")
	if err != nil {
		return err
	}
	envelope = append(envelope, '\n')
	if len(envelope) > maxManagedFile {
		return fmt.Errorf("transaction journal exceeds size limit")
	}
	return durable(path, envelope, 0600)
}

const maxInlineTransactionBytes = 1 << 20

func transactionBlobDir(marker string) string {
	return filepath.Join(filepath.Dir(marker), "transaction.blobs")
}

func persistTransactionBytes(dir string, data []byte) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", nil
	}
	if len(data) > maxManagedFile {
		return nil, "", fmt.Errorf("managed input exceeds size limit")
	}
	if len(data) <= maxInlineTransactionBytes {
		return data, "", nil
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])
	if err := durable(filepath.Join(dir, name), data, 0600); err != nil {
		return nil, "", err
	}
	return nil, name, nil
}

func attachTransactionBlobs(tx *transaction, dir string) error {
	for i, file := range tx.Files {
		data, err := readTransactionBlob(dir, file.DataSHA256)
		if err != nil {
			return err
		}
		if data != nil {
			tx.Files[i].Data = data
		}
		before, err := readTransactionBlob(dir, file.BeforeDataSHA256)
		if err != nil {
			return err
		}
		if before != nil {
			tx.Files[i].BeforeData = before
		}
	}
	return nil
}

func readTransactionBlob(dir, name string) ([]byte, error) {
	if name == "" {
		return nil, nil
	}
	if len(name) != 64 {
		return nil, fmt.Errorf("invalid transaction blob name")
	}
	for _, c := range name {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, fmt.Errorf("invalid transaction blob name")
		}
	}
	data, err := readRegularFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != name {
		return nil, fmt.Errorf("transaction blob checksum mismatch")
	}
	return data, nil
}

func discardTransactionBlobs(marker string) error {
	return removePhysicalDirectory(transactionBlobDir(marker))
}

func readTransactionFile(path string) (transaction, error) {
	data, err := readRegularFile(path)
	if err != nil {
		return transaction{}, err
	}
	return decodeTransactionBlobs(data, transactionBlobDir(path))
}

func decodeTransaction(data []byte) (transaction, error) {
	return decodeTransactionBlobs(data, "")
}

func decodeTransactionBlobs(data []byte, blobDir string) (transaction, error) {
	var tx transaction
	var envelope transactionEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return tx, err
	}
	// MarshalIndent reformats embedded RawMessage; checksum canonical compact JSON.
	var compact bytes.Buffer
	if err := json.Compact(&compact, envelope.Transaction); err != nil {
		return tx, err
	}
	digest := sha256.Sum256(compact.Bytes())
	if envelope.SHA256 != hex.EncodeToString(digest[:]) {
		return tx, fmt.Errorf("transaction checksum mismatch")
	}
	if err := json.Unmarshal(envelope.Transaction, &tx); err != nil {
		return tx, err
	}
	if blobDir != "" {
		if err := attachTransactionBlobs(&tx, blobDir); err != nil {
			return tx, err
		}
	} else {
		for _, f := range tx.Files {
			if f.DataSHA256 != "" || f.BeforeDataSHA256 != "" {
				return tx, fmt.Errorf("transaction payload stored out of band")
			}
		}
	}
	if (tx.Schema != transactionSchemaV1 && tx.Schema != transactionSchemaV2) || tx.After.Generation <= tx.Before.Generation || tx.After.PolicyGeneration <= tx.Before.PolicyGeneration || tx.After.ID == "" || tx.After.Consumers == nil || tx.After.Files == nil || tx.Before.Consumers == nil || tx.Before.Files == nil {
		return tx, fmt.Errorf("invalid transaction schema")
	}
	seen := map[string]bool{}
	for _, f := range tx.Files {
		if !filepath.IsAbs(f.Path) || seen[f.Path] {
			return tx, fmt.Errorf("invalid transaction path")
		}
		seen[f.Path] = true
		if f.Before.Exists && f.Before.Link == "" && identity(f.BeforeData, f.Before.Mode) != f.Before {
			return tx, fmt.Errorf("invalid per-file recovery identity")
		}
	}
	return tx, nil
}

type runtimePolicy struct {
	Generation uint64
	Enabled    bool
}

func checkPolicyGeneration(root string, l Ledger) error {
	data, err := readRegularFile(filepath.Join(root, "policy-generation.json"))
	if os.IsNotExist(err) {
		if l.Generation != 0 {
			return fmt.Errorf("missing policy generation")
		}
		return nil
	}
	if err != nil {
		return err
	}
	var policy runtimePolicy
	if json.Unmarshal(data, &policy) != nil || policy.Enabled != l.Enabled || policy.Generation != l.PolicyGeneration {
		return fmt.Errorf("policy/ledger mismatch without transaction; refusing ambiguous repair")
	}
	return nil
}

// reverseTransaction builds a new durable decision from checked individual
// snapshots. It never restores an entire directory and never downgrades an
// already promoted callback reader. A foreign edit makes rollback refuse.
func reverseTransaction(current Ledger, tx transaction) (transaction, error) {
	if tx.Native != nil && tx.Native.Retire {
		return transaction{}, fmt.Errorf("retirement deletion must finish forward recovery before rollback")
	}
	after := tx.Before
	after.Enabled = false
	after.WriterFloor = WriterFloor
	after.Generation = tx.After.Generation + 1
	after.PolicyGeneration = tx.After.PolicyGeneration + 1
	if after.ID == "" {
		after.ID = tx.After.ID
		after.Owner = tx.After.Owner
		after.RuntimeRoot = tx.After.RuntimeRoot
	}
	if tx.Native != nil {
		digest, err := treeFingerprint(tx.Native.After.Path)
		if err != nil {
			return transaction{}, err
		}
		if digest == tx.Native.After.SHA256 && tx.Native.After.SHA256 != "" && !tx.Native.Purge {
			after.Native = tx.After.Native
			after.DecoderFloor = tx.After.DecoderFloor
		} else if digest != "" {
			return transaction{}, fmt.Errorf("cannot rollback an ambiguous or purged native callback")
		} else {
			beforeDigest := ""
			if tx.Native.Before.Path != "" {
				beforeDigest, err = treeFingerprint(tx.Native.Before.Path)
				if err != nil {
					return transaction{}, err
				}
			}
			if beforeDigest != tx.Native.Before.SHA256 {
				return transaction{}, fmt.Errorf("cannot rollback an ambiguous or purged native callback")
			}
		}
	}
	reverse := transaction{Schema: transactionSchemaV2, Before: current, After: after, ConfigPaths: tx.ConfigPaths}
	for _, f := range tx.Files {
		if err := checkReplacementRollback(f); err != nil {
			return transaction{}, err
		}
		actual, err := Fingerprint(f.Path)
		if err != nil {
			return transaction{}, err
		}
		if actual != f.Before && actual != desired(f) {
			return transaction{}, fmt.Errorf("rollback conflict; preserving foreign edit: %s", f.Path)
		}
		undo := File{Parents: f.Parents, Path: f.Path, Before: actual, Data: f.BeforeData, Mode: f.Before.Mode, Link: f.Before.Link, Remove: !f.Before.Exists}
		if actual.Exists && actual.Link == "" {
			undo.WindowsReplacementID, err = windowsReplacementIdentity(f.Path)
			if err != nil {
				return transaction{}, err
			}
			undo.BeforeData, err = readRegularFile(f.Path)
			if err != nil {
				return transaction{}, err
			}
		}
		// A rollback to a pre-managed installation retains the compatible kernel
		// needed to maintain the callback/policy protocol. It is not an implicit reset.
		if managedWriter(f.Path) && undo.Remove && actual.Exists {
			after.Files[f.Path] = actual
			continue
		}
		reverse.Files = append(reverse.Files, undo)
	}
	if err := validateWriterFiles(reverse.Files); err != nil {
		return transaction{}, err
	}
	return reverse, nil
}
