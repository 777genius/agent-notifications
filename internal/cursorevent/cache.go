package cursorevent

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"

	source "github.com/777genius/agent-notifications/internal/cursorsource"
)

// marker is a private length-framed binding/native identity hash. Channel bits
// share this marker and the existing cache window; native IDs are never stored.
func marker(b Binding, f source.Facts) string {
	h := sha256.New()
	for _, s := range []string{b.InstallationID, b.BindingID, b.ProfileIdentity, strconv.FormatUint(b.Generation, 10), b.Product, f.ConversationID, f.GenerationID, f.Event, f.Status, strconv.FormatBool(f.LoopCountKnown), strconv.FormatInt(f.LoopCount, 10)} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}
