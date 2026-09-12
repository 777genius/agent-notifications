package nativeprotocol

// SetupCapabilities is separate from notification actions and send versions.
// Callers must verify managed artifact identity and known base capabilities
// before probing with exactly --capabilities-json --setup. No launcher lives here.
type SetupCapabilities struct {
	SchemaVersion             int    `json:"schemaVersion"`
	PermissionRequestVersions []int  `json:"permissionRequestVersions"`
	Backend                   string `json:"backend"`
}

// DecodeSetupCapabilities fails closed unless the exact supported v1 contract
// is advertised. Permission outcomes use DecodePermission, never DecodeReceipt.
func DecodeSetupCapabilities(data []byte) (SetupCapabilities, error) {
	var c SetupCapabilities
	if decode(data, &c, "schemaVersion", "permissionRequestVersions", "backend") != nil ||
		c.SchemaVersion != 1 || c.Backend != "macos.usernotifications" ||
		len(c.PermissionRequestVersions) != 1 || c.PermissionRequestVersions[0] != 1 {
		return SetupCapabilities{}, ErrInvalidEnvelope
	}
	return c, nil
}
