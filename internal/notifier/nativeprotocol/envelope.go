// Package nativeprotocol validates the versioned native helper replies.
// It has no process, permission, notification, or legacy presentation effects.
package nativeprotocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxEnvelopeBytes = 16 * 1024

// ErrInvalidEnvelope deliberately contains no caller text or native output.
var ErrInvalidEnvelope = errors.New("invalid native protocol envelope")

type Capabilities struct {
	SchemaVersion                   int      `json:"schemaVersion"`
	ProtocolVersions                []int    `json:"protocolVersions"`
	ActionKinds                     []string `json:"actionKinds"`
	ReceiptSupport                  bool     `json:"receiptSupport"`
	Backend                         string   `json:"backend"`
	ExplicitFeatureEnabledByDefault bool     `json:"explicitFeatureEnabledByDefault"`
}

func (c Capabilities) Supports(version int, action string) bool {
	if !c.ReceiptSupport {
		return false
	}
	for _, v := range c.ProtocolVersions {
		if v != version {
			continue
		}
		for _, a := range c.ActionKinds {
			if a == action {
				return true
			}
		}
	}
	return false
}

// DecodeCapabilities is only a codec. A trusted managed artifact fingerprint
// must be verified before a caller launches a capabilities probe.
func DecodeCapabilities(data []byte) (Capabilities, error) {
	var c Capabilities
	if decode(data, &c, "schemaVersion", "protocolVersions", "actionKinds", "receiptSupport", "backend", "explicitFeatureEnabledByDefault") != nil ||
		c.SchemaVersion != 1 || c.Backend != "macos.usernotifications" ||
		c.ExplicitFeatureEnabledByDefault || len(c.ProtocolVersions) == 0 || len(c.ActionKinds) == 0 {
		return Capabilities{}, ErrInvalidEnvelope
	}
	versions := make(map[int]bool)
	for _, v := range c.ProtocolVersions {
		if v < 1 || versions[v] {
			return Capabilities{}, ErrInvalidEnvelope
		}
		versions[v] = true
	}
	actions := make(map[string]bool)
	for _, action := range c.ActionKinds {
		if action == "" || len(action) > 64 || actions[action] {
			return Capabilities{}, ErrInvalidEnvelope
		}
		for _, ch := range action {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' {
				return Capabilities{}, ErrInvalidEnvelope
			}
		}
		actions[action] = true
	}
	return c, nil
}

type Receipt struct {
	SchemaVersion  int    `json:"schemaVersion"`
	CorrelationID  string `json:"correlationID"`
	Nonce          string `json:"nonce"`
	NotificationID string `json:"notificationID"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	RetrySafe      bool   `json:"retrySafe"`
}

// DecodeReceipt requires the exact pending attempt's identity. Neither a
// process exit code nor an uncorrelated receipt proves OS acceptance.
func DecodeReceipt(data []byte, correlationID, nonce string) (Receipt, error) {
	var r Receipt
	if decode(data, &r, "schemaVersion", "correlationID", "nonce", "notificationID", "status", "reason", "retrySafe") != nil ||
		r.SchemaVersion != 1 || !validUUID(correlationID) || !validUUID(nonce) || r.CorrelationID != correlationID ||
		r.Nonce != nonce || r.NotificationID != correlationID || r.RetrySafe {
		return Receipt{}, ErrInvalidEnvelope
	}
	switch r.Status {
	case "submitted":
		if r.Reason != "os_accepted" {
			return Receipt{}, ErrInvalidEnvelope
		}
	case "unknown":
		if r.Reason != "timeout" {
			return Receipt{}, ErrInvalidEnvelope
		}
	case "rejected":
		switch r.Reason {
		case "malformed_request", "unsupported_version", "unsupported_action", "invalid_file", "expired", "activation_required", "permission_denied", "unsupported_notifier", "os_rejected":
		default:
			return Receipt{}, ErrInvalidEnvelope
		}
	default:
		return Receipt{}, ErrInvalidEnvelope
	}
	return r, nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil && len(decoded) == 16
}

// Native v1 replies are flat objects whose values are scalar or scalar arrays.
// Check duplicate/missing/null fields before Go's permissive struct decoder.
func decode(data []byte, dst any, fields ...string) error {
	if len(data) == 0 || len(data) > MaxEnvelopeBytes || !utf8.Valid(data) {
		return ErrInvalidEnvelope
	}
	d := json.NewDecoder(bytes.NewReader(data))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return ErrInvalidEnvelope
	}
	remaining := make(map[string]bool, len(fields))
	for _, f := range fields {
		remaining[f] = true
	}
	for d.More() {
		t, err = d.Token()
		key, ok := t.(string)
		if err != nil || !ok || !remaining[key] {
			return ErrInvalidEnvelope
		}
		delete(remaining, key)
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return ErrInvalidEnvelope
		}
	}
	if t, err = d.Token(); err != nil || t != json.Delim('}') || len(remaining) != 0 {
		return ErrInvalidEnvelope
	}
	if _, err = d.Token(); err != io.EOF {
		return ErrInvalidEnvelope
	}
	if json.Unmarshal(data, dst) != nil {
		return ErrInvalidEnvelope
	}
	return nil
}
