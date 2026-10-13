package copilotvscodeevent

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	source "github.com/777genius/agent-notifications/internal/copilotvscodesource"
	"strconv"
)

// marker is private, length-framed and binding/profile scoped. Only its hash
// reaches the shared installed cache; absent session never calls this function.
func marker(b Binding, f source.Facts) string {
	h := sha256.New()
	for _, s := range []string{b.InstallationID, b.BindingID, b.ProfileIdentity, strconv.FormatUint(b.Generation, 10), b.Product, "copilot-vscode", f.SessionID, f.Event, "false", f.Timestamp} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}
