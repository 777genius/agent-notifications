package installruntime

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/777genius/agent-notifications/internal/windowscallback"
)

// Windows transaction schema6 retains the protected Changes carrier used by
// schema4 so former readers reject its type before attaching blobs. It is a
// separate decoder from orphan schema5, which remains absence-metadata-only.
type windowsTransactionWire struct {
	*transaction
	Files struct{ Changes []File }
}

func marshalWindowsTransaction(tx transaction) ([]byte, error) {
	if err := validateWindowsTransaction(tx); err != nil {
		return nil, err
	}
	wire := windowsTransactionWire{transaction: &tx}
	wire.Files.Changes = tx.Files
	return json.Marshal(wire)
}

func unmarshalWindowsTransaction(data []byte, tx *transaction) error {
	if err := validateVersionedFiles(data, "schema6"); err != nil {
		return err
	}
	wire := windowsTransactionWire{transaction: tx}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	tx.Files = wire.Files.Changes
	return validateWindowsTransaction(*tx)
}

func validateWindowsTransaction(tx transaction) error {
	if tx.Schema != WindowsGenerationTransactionSchema || tx.Windows == nil || tx.OrphanRecovery != nil {
		return fmt.Errorf("schema6 requires a Windows participant")
	}
	readerFloor, declared := windowsLedgerReaderFloor(tx.After.Schema)
	if !declared || readerFloor != WindowsGenerationLedgerReaderFloor ||
		tx.After.WriterFloor < WindowsGenerationWriterFloor || tx.Before.DecoderFloor > tx.After.DecoderFloor {
		return fmt.Errorf("windows participant requires ledger/floor6")
	}
	if tx.Before.WriterFloor > tx.After.WriterFloor {
		return fmt.Errorf("windows participant lowers writer floor")
	}
	if validateWindowsRetained(tx.Before) != nil || validateWindowsRetained(tx.After) != nil {
		return fmt.Errorf("invalid Windows retained journal")
	}
	if err := validateWindowsPayload(tx.Windows); err != nil {
		return err
	}
	want := append(append([]windowscallback.Binding(nil), tx.Before.WindowsRetained...), tx.Windows.Binding)
	rollbackRetained := tx.Rollback && reflect.DeepEqual(tx.After.WindowsRetained, tx.Before.WindowsRetained) &&
		containsWindowsBinding(tx.Before.WindowsRetained, tx.Windows.Binding)
	if !reflect.DeepEqual(tx.After.WindowsRetained, want) && !rollbackRetained {
		return fmt.Errorf("windows participant binding is not the bounded owner append")
	}
	return nil
}

func containsWindowsBinding(values []windowscallback.Binding, want windowscallback.Binding) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
