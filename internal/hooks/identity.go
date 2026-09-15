package hooks

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// eventKeys are the state/dedup identities used by the delivery pipeline.
//
// Claude keeps the original byte-for-byte session id for both keys so every
// pre-upgrade state filename and cooldown survives. Codex identities are
// hashed: raw Codex ids never become filename fragments, and the lock key is
// turn-scoped so parallel turns cannot collapse into one dedup entry.
type eventKeys struct {
	// stateKey scopes session state (cooldowns, last-notification, content locks).
	stateKey string
	// lockKey scopes the per-event dedup lock.
	lockKey string
}

func claudeKeys(sessionID string) eventKeys {
	return eventKeys{stateKey: sessionID, lockKey: sessionID}
}

func codexKeys(ev Event) eventKeys {
	keys := eventKeys{
		stateKey: hashedIdentity("codex", ev.Session.SessionID),
	}
	lockFields := []string{"codex", ev.Session.SessionID, ev.Session.TurnID}
	switch p := ev.Payload.(type) {
	case PermissionRequestPayload:
		lockFields = append(lockFields, "tool", p.ToolName)
		if p.Agent != nil {
			lockFields = append(lockFields, "agent", p.Agent.ID)
		}
	case PreToolUsePayload:
		lockFields = append(lockFields, "tool", p.ToolName, "call", p.ToolUseID)
	case SubagentStopPayload:
		if p.Agent != nil {
			lockFields = append(lockFields, "agent", p.Agent.ID)
		}
	}
	keys.lockKey = hashedIdentity(lockFields...)
	return keys
}

// hashedIdentity builds a filename-safe SHA-256 identity over length-prefixed
// fields. Length prefixes make the encoding injective: no separator collision
// is possible no matter what the raw ids contain.
func hashedIdentity(fields ...string) string {
	h := sha256.New()
	var lenBuf [8]byte
	for _, f := range fields {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(f)))
		h.Write(lenBuf[:])
		h.Write([]byte(f))
	}
	return "codex-" + hex.EncodeToString(h.Sum(nil))[:32]
}
