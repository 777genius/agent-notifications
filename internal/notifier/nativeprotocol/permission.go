package nativeprotocol

type Permission struct {
	SchemaVersion int    `json:"schemaVersion"`
	CorrelationID string `json:"correlationID"`
	Nonce         string `json:"nonce"`
	Backend       string `json:"backend"`
	Permission    string `json:"permission"`
}

func DecodePermission(data []byte, correlation, nonce string) (Permission, error) {
	var p Permission
	if decode(data, &p, "schemaVersion", "correlationID", "nonce", "backend", "permission") != nil || p.SchemaVersion != 1 || !validUUID(correlation) || !validUUID(nonce) || p.CorrelationID != correlation || p.Nonce != nonce || p.Backend != "macos.usernotifications" {
		return Permission{}, ErrInvalidEnvelope
	}
	switch p.Permission {
	case "allowed", "undetermined", "denied", "unavailable":
		return p, nil
	}
	return Permission{}, ErrInvalidEnvelope
}
