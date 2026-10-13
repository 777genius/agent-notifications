package installruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// ObserverChannelPreimage binds only one product's stored decision. Earlier
// sibling phases may update the shared policy without replacing this leaf.
func ObserverChannelPreimage(route []byte, key string) (string, error) {
	var fields map[string]json.RawMessage
	if len(route) > 0 && (json.Unmarshal(route, &fields) != nil || fields == nil) {
		return "", errors.New("invalid observer channel policy")
	}
	value, present := fields[key]
	if !present {
		return "absent", nil
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func CheckObserverChannelPreimage(route []byte, key, expected string) error {
	if expected == "" {
		return nil
	} // Direct advanced setup retains its explicit contract.
	actual, err := ObserverChannelPreimage(route, key)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("concurrent_change: confirmed observer channels changed")
	}
	return nil
}
