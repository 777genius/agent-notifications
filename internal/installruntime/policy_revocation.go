package installruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/strictjson"
)

// A pure revocation needs no native availability. It still passes the writer
// floor, ownership, registered-runtime, generation, policy CAS and file checks.
func policyDisableOnly(r Request) bool {
	return r.RefreshOnly && r.PolicyEnabled != nil && !*r.PolicyEnabled && r.ExpectedGeneration != nil &&
		len(r.PolicyFields) == 0 && len(r.Files) == 0 && r.Prepare == nil && r.Native == nil &&
		!r.RemoveConsumer && !r.PurgeNative && !r.RetireNative && !r.RollbackPending &&
		r.Reservation == nil && !r.ClearReservation
}

func openCodeRevokeOnly(r Request) bool {
	if r.RevokeCursor || r.RevokeCopilotVSCode || r.RevokeGemini || !r.PolicyOnly || !r.RefreshOnly || r.Owner != "existing-installer" || r.ConsumerID != "opencode-notifications" ||
		r.ExpectedGeneration == nil || r.ExpectedPolicy == nil || r.PolicyEnabled != nil ||
		len(r.PolicyFields) != 1 || string(r.PolicyFields["route"]) != `{"openCodeNotifications":{"desktop":false,"webhook":false}}` ||
		len(r.Files) != 0 || len(r.ConfigPaths) != 0 || r.Prepare != nil || r.Native != nil ||
		r.RemoveConsumer || r.PurgeNative || r.RetireNative || r.RollbackPending || r.RecoverOnly ||
		r.Reservation != nil || r.ClearReservation || r.RelocateVersionedCache {
		return false
	}
	return true
}

func geminiRevokeOnly(r Request) bool {
	if r.RevokeCursor || r.RevokeCopilotVSCode || r.RevokeOpenCode || !r.PolicyOnly || !r.RefreshOnly || r.Owner != "existing-installer" || r.ConsumerID != "gemini-notifications" ||
		r.ExpectedGeneration == nil || r.ExpectedPolicy == nil || r.PolicyEnabled != nil ||
		len(r.PolicyFields) != 1 || string(r.PolicyFields["route"]) != `{"geminiNotifications":{"desktop":false,"webhook":false}}` ||
		len(r.Files) != 0 || len(r.ConfigPaths) != 0 || r.Prepare != nil || r.Native != nil ||
		r.RemoveConsumer || r.PurgeNative || r.RetireNative || r.RollbackPending || r.RecoverOnly ||
		r.Reservation != nil || r.ClearReservation || r.RelocateVersionedCache {
		return false
	}
	return true
}

// Cursor revocation admits only the paired false leaves and an unchanged binding.
func cursorRevokeOnly(r Request) bool {
	if r.RevokeCopilotVSCode || r.RevokeGemini || r.RevokeOpenCode || !r.PolicyOnly || !r.RefreshOnly ||
		r.Owner != "existing-installer" || r.ControlRoot == "" || r.ExpectedGeneration == nil || r.ExpectedPolicy == nil ||
		r.PolicyEnabled != nil || len(r.PolicyFields) != 1 || len(r.Files) != 0 || len(r.ConfigPaths) != 0 ||
		r.Prepare != nil || r.Native != nil || r.RemoveConsumer || r.PurgeNative || r.RetireNative ||
		r.RollbackPending || r.RecoverOnly || r.Reservation != nil || r.ClearReservation || r.RelocateVersionedCache ||
		r.RuntimeRoot != r.Consumer.RuntimeRoot || !cursorPortableConsumer(r.ConsumerID, r.Consumer) {
		return false
	}
	raw := r.PolicyFields["route"]
	if strictjson.Validate(raw, strictjson.Budget{Bytes: 1024, Depth: 4, Entries: 8}) != nil {
		return false
	}
	route, err := copilotObject(raw, false)
	if err != nil || len(route) != 1 {
		return false
	}
	leaves, err := copilotObject(route["cursorNotifications"], false)
	return err == nil && len(leaves) == 2 && bytes.Equal(bytes.TrimSpace(leaves["desktop"]), []byte("false")) &&
		bytes.Equal(bytes.TrimSpace(leaves["webhook"]), []byte("false"))
}

// Structural identity only: damaged executable bytes never authorize delivery.
func cursorPortableConsumer(key string, c Consumer) bool {
	if len(c.Commands) != 1 || !filepath.IsAbs(c.RuntimeRoot) || filepath.Clean(c.RuntimeRoot) != c.RuntimeRoot ||
		!filepath.IsAbs(c.Commands[0]) || filepath.Clean(c.Commands[0]) != c.Commands[0] || !pathWithinRoot(c.RuntimeRoot, c.Commands[0]) {
		return false
	}
	sum := sha256.Sum256([]byte(c.Registration))
	if key != "portable:"+hex.EncodeToString(sum[:]) || strictjson.Validate([]byte(c.Registration), strictjson.Budget{Bytes: 16384, Depth: 4, Entries: 32}) != nil {
		return false
	}
	var b struct {
		Version                                  int
		Integration, Owner, RuntimeRoot, Primary string
	}
	if json.Unmarshal([]byte(c.Registration), &b) != nil || b.Version != 1 || b.Integration != "cursor" || b.Owner != "existing-installer" || b.RuntimeRoot != c.RuntimeRoot ||
		b.Primary == "" || strings.ContainsAny(b.Primary, `\:`) || !filepath.IsLocal(filepath.FromSlash(b.Primary)) || filepath.ToSlash(filepath.Clean(filepath.FromSlash(b.Primary))) != b.Primary {
		return false
	}
	return c.Commands[0] == filepath.Join(c.RuntimeRoot, filepath.FromSlash(b.Primary))
}

// RevokeCopilotVSCode permits precisely native, manual, or all false leaves.
// Consumer bytes must be supplied from the single recorded portable binding.
func copilotRevokeOnly(r Request) bool {
	if r.RevokeCursor || r.RevokeGemini || r.RevokeOpenCode || !r.PolicyOnly || !r.RefreshOnly ||
		r.Owner != "existing-installer" || r.ExpectedGeneration == nil || r.ExpectedPolicy == nil ||
		r.PolicyEnabled != nil || len(r.PolicyFields) != 1 || len(r.Files) != 0 ||
		len(r.ConfigPaths) != 0 || r.Prepare != nil || r.Native != nil || r.RemoveConsumer ||
		r.PurgeNative || r.RetireNative || r.RollbackPending || r.RecoverOnly ||
		r.Reservation != nil || r.ClearReservation || r.RelocateVersionedCache {
		return false
	}
	if !filepath.IsAbs(r.RuntimeRoot) || filepath.Clean(r.RuntimeRoot) != r.RuntimeRoot ||
		r.Consumer.RuntimeRoot != r.RuntimeRoot || !localPortableConsumer(r.ConsumerID, r.Consumer) {
		return false
	}
	raw := r.PolicyFields["route"]
	if strictjson.Validate(raw, strictjson.Budget{Bytes: 1024, Depth: 4, Entries: 8}) != nil {
		return false
	}
	route, err := copilotObject(raw, false)
	if err != nil || len(route) != 1 {
		return false
	}
	leaves, err := copilotObject(route["copilotVSCodeNotifications"], false)
	if err != nil {
		return false
	}
	native, manual := false, false
	for key, raw := range leaves {
		switch key {
		case "desktop", "webhook":
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
				return false
			}
			native = true
		case "manual":
			m, err := copilotObject(raw, false)
			if err != nil || len(m) != 1 || !bytes.Equal(bytes.TrimSpace(m["enabled"]), []byte("false")) {
				return false
			}
			manual = true
		default:
			return false
		}
	}
	if native && (leaves["desktop"] == nil || leaves["webhook"] == nil) {
		return false
	}
	return native || manual
}

// No per-file consumer ownership is inferred. This only recognizes the recorded
// registration's identity; portable owns the complete binding validation.
func localPortableConsumer(key string, c Consumer) bool {
	if !strings.HasPrefix(key, "portable:") || len(c.Commands) != 1 ||
		!filepath.IsAbs(c.RuntimeRoot) || filepath.Clean(c.RuntimeRoot) != c.RuntimeRoot ||
		!pathWithinRoot(c.RuntimeRoot, c.Commands[0]) || filepath.Clean(c.Commands[0]) != c.Commands[0] {
		return false
	}
	sum := sha256.Sum256([]byte(c.Registration))
	if key != "portable:"+hex.EncodeToString(sum[:]) {
		return false
	}
	if strictjson.Validate([]byte(c.Registration), strictjson.Budget{Bytes: 16384, Depth: 4, Entries: 32}) != nil {
		return false
	}
	var b struct{ Integration, Owner, RuntimeRoot string }
	return json.Unmarshal([]byte(c.Registration), &b) == nil && b.Integration == "copilot-vscode" &&
		b.Owner == "existing-installer" && b.RuntimeRoot == c.RuntimeRoot
}

func localPolicyMutation(r Request) bool {
	if r.RevokeCursor || cursorPortableConsumer(r.ConsumerID, r.Consumer) || r.RevokeCopilotVSCode || localPortableConsumer(r.ConsumerID, r.Consumer) {
		return true
	}
	var route map[string]json.RawMessage
	return json.Unmarshal(r.PolicyFields["route"], &route) == nil && (route["copilotVSCodeNotifications"] != nil || route["cursorNotifications"] != nil)
}
