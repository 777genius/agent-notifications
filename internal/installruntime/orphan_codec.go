package installruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Schema5 has no publication capabilities. Keep v4's protected Changes carrier
// so even older decoders which unmarshal before gating cannot read it as []File.
// No legacy transaction or durable ledger encoding is changed.
type orphanTransactionWire struct {
	Schema         int
	Before         Ledger
	After          Ledger
	Files          struct{ Changes []File }
	ConfigPaths    []string
	OrphanRecovery *orphanDecision
	Rollback       bool `json:",omitempty"`
}

func marshalOrphanTransaction(tx transaction) ([]byte, error) {
	if err := validateOrphanTransaction(tx); err != nil {
		return nil, err
	}
	w := orphanTransactionWire{Schema: tx.Schema, Before: tx.Before, After: tx.After, ConfigPaths: tx.ConfigPaths, OrphanRecovery: tx.OrphanRecovery, Rollback: tx.Rollback}
	w.Files.Changes = []File{}
	return json.Marshal(w)
}

func unmarshalOrphanTransaction(data []byte, tx *transaction) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields["OrphanRecovery"] == nil || bytes.Equal(bytes.TrimSpace(fields["OrphanRecovery"]), []byte("null")) {
		return fmt.Errorf("unsupported transaction schema: schema5 requires an orphan metadata decision")
	}
	if err := exactOrphanJSON(data, reflect.TypeOf(orphanTransactionWire{})); err != nil {
		return err
	}
	var w orphanTransactionWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	if w.Files.Changes == nil || len(w.Files.Changes) != 0 {
		return fmt.Errorf("schema5 has no file publications")
	}
	*tx = transaction{Schema: w.Schema, Before: w.Before, After: w.After, Files: w.Files.Changes, ConfigPaths: w.ConfigPaths, OrphanRecovery: w.OrphanRecovery, Rollback: w.Rollback}
	return validateOrphanTransaction(*tx)
}

func validateOrphanEnvelope(data []byte) error {
	return exactOrphanJSON(data, reflect.TypeOf(transactionEnvelope{}))
}

// The legacy typed decoder accepts unknown keys and case aliases. Schema5 is
// deliberately exact at every struct boundary (including nested ownership).
// Dynamic map keys remain data, validated by the namespace/delta predicate.
// strictjson.Validate has already bounded depth/size and rejected duplicates.
func exactOrphanJSON(data []byte, typ reflect.Type) error {
	return validateOrphanJSON(data, typ, true)
}

// Existing ledgers may omit fields introduced by earlier compatible kernels.
// Unknown ownership fields still cannot be discarded by a metadata cleanup.
func validateOrphanJSON(data []byte, typ reflect.Type, requireFields bool) error {
	data = bytes.TrimSpace(data)
	if typ.Kind() == reflect.Pointer {
		if bytes.Equal(data, []byte("null")) {
			return nil
		}
		return validateOrphanJSON(data, typ.Elem(), requireFields)
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			return fmt.Errorf("invalid schema5 object")
		}
		known := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			if name == "-" {
				continue
			}
			known[name] = f.Type
			optional := false
			for _, option := range tag[1:] {
				if option == "omitempty" {
					optional = true
				}
			}
			if fields[name] == nil && !optional && requireFields {
				return fmt.Errorf("missing schema5 field %s", name)
			}
		}
		for name, value := range fields {
			field, ok := known[name]
			if !ok {
				return fmt.Errorf("unknown schema5 field %s", name)
			}
			if err := validateOrphanJSON(value, field, requireFields); err != nil {
				return err
			}
		}
	case reflect.Map:
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			return fmt.Errorf("invalid schema5 map")
		}
		for _, value := range fields {
			if err := validateOrphanJSON(value, typ.Elem(), requireFields); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			break
		} // base64 bytes, checked by typed unmarshal
		var values []json.RawMessage
		if json.Unmarshal(data, &values) != nil {
			return fmt.Errorf("invalid schema5 array")
		}
		for _, value := range values {
			if err := validateOrphanJSON(value, typ.Elem(), requireFields); err != nil {
				return err
			}
		}
	default:
		if bytes.Equal(data, []byte("null")) {
			return fmt.Errorf("null schema5 scalar")
		}
	}
	return nil
}
