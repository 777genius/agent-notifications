package installruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/strictjson"
)

// PolicySnapshot belongs to one request, never to a service lifetime. Fields
// contains the authoritative policy (including adapter-owned route settings),
// without a second on-disk configuration. Callers must treat it as immutable.
// Pass Installation unchanged to AcquireInstalledLease for readiness/handoff.
type PolicySnapshot struct {
	// Preimage identifies exactly the bytes parsed into Fields, not a later
	// fingerprint. Setup may pass it unchanged as Request.ExpectedPolicy.
	Preimage     Identity
	Installation InstalledSnapshot
	Policy       UserPolicy
	Fields       map[string]json.RawMessage
}

// RevocationSnapshot is only a CAS preimage for turning a consumer's channels
// off. It deliberately does not assert that native or owned assets are usable.
type RevocationSnapshot struct {
	Generation uint64
	Preimage   Identity
}

// ReadRevocationSnapshot keeps the same component/config lock order as policy
// reads while allowing a damaged delivery asset to be revoked. Commit performs
// the final generation, ownership and exact policy preimage checks again.
func ReadRevocationSnapshot(ctx context.Context, root string) (RevocationSnapshot, error) {
	var result RevocationSnapshot
	if root == "" {
		return result, fmt.Errorf("managed control root required")
	}
	if err := privateDirectory(root); err != nil {
		return result, err
	}
	release, err := LockExisting(ctx, filepath.Join(root, ".component-install.lock"))
	if err != nil {
		return result, err
	}
	defer release()
	configRelease, err := LockExisting(ctx, filepath.Join(root, "agent-notifications.json.lock"))
	if err != nil {
		return result, err
	}
	defer configRelease()
	l, err := readLedger(root)
	if err != nil {
		return result, err
	}
	if l.ID == "" {
		return result, fmt.Errorf("managed installation required")
	}
	if _, err := os.Lstat(filepath.Join(root, "transaction.json")); err == nil {
		return result, ErrPolicyRecovery
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if err := checkPolicyGeneration(root, l); err != nil {
		return result, err
	}
	_, _, result.Preimage, err = readPolicyForUpdate(root)
	if err != nil {
		return result, err
	}
	result.Generation = l.Generation
	return result, nil
}

// ReadPolicySnapshot reads policy and installation generation under component,
// then policy config locking. It never creates state or recovers transactions.
// Call once per request without a journal lock; release precedes journal work.
// Managed mutations use the same lock order. Manual edits have snapshot semantics.
func ReadPolicySnapshot(ctx context.Context, root string) (PolicySnapshot, error) {
	var result PolicySnapshot
	var err error
	if root == "" {
		root, err = ControlRoot()
		if err != nil {
			return result, err
		}
	}
	if err = privateDirectory(root); os.IsNotExist(err) {
		// Do not reopen an absent root without the locks: a concurrent setup
		// could otherwise splice a new installation into a missing-policy read.
		result.Policy = UserPolicy{SchemaVersion: 1}
		result.Fields = map[string]json.RawMessage{}
		return result, nil
	} else if err != nil {
		return result, err
	}
	release, err := LockExisting(ctx, filepath.Join(root, ".component-install.lock"))
	if err != nil {
		return result, err
	}
	defer release()
	configRelease, err := LockExisting(ctx, filepath.Join(root, "agent-notifications.json.lock"))
	if err != nil {
		return result, err
	}
	defer configRelease()
	result.Policy, result.Fields, result.Preimage, err = readPolicyForUpdate(root)
	if err != nil {
		return result, err
	}
	result.Installation, err = readInstalledSnapshot(root, &result.Policy)
	return result, err
}

// UserPolicy is durable user intent. Runtime eligibility and its generation
// fence remain in the ledger; routes belong only in this authoritative file.
type UserPolicy struct {
	SchemaVersion int  `json:"schemaVersion"`
	Enabled       bool `json:"enabled"`
}

// ReadUserPolicy never creates or migrates configuration. Missing means disabled.
// Unknown fields are retained by managed enable/disable for future route adapters.
func ReadUserPolicy(root string) (UserPolicy, error) {
	p, _, err := readUserPolicy(root)
	return p, err
}
func readUserPolicy(root string) (UserPolicy, map[string]json.RawMessage, error) {
	p := UserPolicy{SchemaVersion: 1}
	data, err := readControlDocument(filepath.Join(root, "agent-notifications.json"))
	if os.IsNotExist(err) {
		return p, map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return p, nil, err
	}
	return decodeUserPolicy(data)
}

func decodeUserPolicy(data []byte) (UserPolicy, map[string]json.RawMessage, error) {
	if strictjson.Validate(data, strictjson.Budget{Bytes: 64 * 1024, Depth: 16, Entries: 1024}) != nil {
		return UserPolicy{}, nil, fmt.Errorf("configuration_invalid: invalid explicit user policy JSON")
	}
	p := UserPolicy{SchemaVersion: 1}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil || json.Unmarshal(data, &p) != nil || p.SchemaVersion != 1 || (fields["schemaVersion"] == nil || string(fields["schemaVersion"]) == "null") || fields["enabled"] == nil || string(fields["enabled"]) == "null" {
		return UserPolicy{}, nil, fmt.Errorf("configuration_invalid: invalid explicit user policy")
	}
	return p, fields, nil
}
func policyFile(root string, enabled bool, fields map[string]json.RawMessage, before Identity) (File, error) {
	fields["schemaVersion"] = json.RawMessage("1")
	fields["enabled"], _ = json.Marshal(enabled)
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return File{}, err
	}
	data = append(data, '\n')
	if _, _, err := decodeUserPolicy(data); err != nil {
		return File{}, err
	}
	path := filepath.Join(root, "agent-notifications.json")
	return File{Path: path, Before: before, Data: data, Mode: 0600}, err
}

// Bind parsed fields to exactly the bytes used for CAS, including a manual
// edit that arrives between the fingerprint read and the policy read.
func readPolicyForUpdate(root string) (UserPolicy, map[string]json.RawMessage, Identity, error) {
	path := filepath.Join(root, "agent-notifications.json")
	before, err := Fingerprint(path)
	if err != nil {
		return UserPolicy{}, nil, before, err
	}
	data, err := readControlDocument(path)
	if os.IsNotExist(err) && !before.Exists {
		return UserPolicy{SchemaVersion: 1}, map[string]json.RawMessage{}, before, nil
	}
	if err != nil {
		return UserPolicy{}, nil, before, err
	}
	if identity(data, before.Mode) != before {
		return UserPolicy{}, nil, before, fmt.Errorf("policy changed while reading CAS preimage")
	}
	policy, fields, err := decodeUserPolicy(data)
	return policy, fields, before, err
}

// mergePolicyFields retains foreign top-level and nested policy members. The
// setup adapter validates domain semantics; the kernel bounds the writable seam.
func mergePolicyFields(fields, changes map[string]json.RawMessage) error {
	for key, raw := range changes {
		if key != "route" && key != "rates" && key != "setupState" {
			return fmt.Errorf("unsupported policy mutation: %s", key)
		}
		if strictjson.Validate(raw, strictjson.Budget{Bytes: 64 * 1024, Depth: 16, Entries: 1024}) != nil {
			return fmt.Errorf("invalid policy JSON: %s", key)
		}
		var patch map[string]json.RawMessage
		if json.Unmarshal(raw, &patch) != nil || patch == nil {
			return fmt.Errorf("invalid policy object: %s", key)
		}
		current := map[string]json.RawMessage{}
		if old, exists := fields[key]; exists {
			if json.Unmarshal(old, &current) != nil || current == nil {
				return fmt.Errorf("invalid existing policy object: %s", key)
			}
		}
		for member, value := range patch {
			if key == "route" && member == "copilotVSCodeNotifications" {
				merged, err := mergeCopilotPolicy(current[member], value)
				if err != nil {
					return err
				}
				current[member] = merged
			} else {
				current[member] = value
			}
		}
		merged, err := json.Marshal(current)
		if err != nil {
			return err
		}
		fields[key] = merged
	}
	return nil
}

// A pure revocation needs no native availability. It still passes the writer
// floor, ownership, registered-runtime, generation, policy CAS and file checks.
func policyDisableOnly(r Request) bool {
	return r.RefreshOnly && r.PolicyEnabled != nil && !*r.PolicyEnabled && r.ExpectedGeneration != nil &&
		len(r.PolicyFields) == 0 && len(r.Files) == 0 && r.Prepare == nil && r.Native == nil &&
		!r.RemoveConsumer && !r.PurgeNative && !r.RetireNative && !r.RollbackPending &&
		r.Reservation == nil && !r.ClearReservation
}

func openCodeRevokeOnly(r Request) bool {
	if r.RevokeCopilotVSCode || r.RevokeGemini || !r.PolicyOnly || !r.RefreshOnly || r.Owner != "existing-installer" || r.ConsumerID != "opencode-notifications" ||
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
	if r.RevokeCopilotVSCode || r.RevokeOpenCode || !r.PolicyOnly || !r.RefreshOnly || r.Owner != "existing-installer" || r.ConsumerID != "gemini-notifications" ||
		r.ExpectedGeneration == nil || r.ExpectedPolicy == nil || r.PolicyEnabled != nil ||
		len(r.PolicyFields) != 1 || string(r.PolicyFields["route"]) != `{"geminiNotifications":{"desktop":false,"webhook":false}}` ||
		len(r.Files) != 0 || len(r.ConfigPaths) != 0 || r.Prepare != nil || r.Native != nil ||
		r.RemoveConsumer || r.PurgeNative || r.RetireNative || r.RollbackPending || r.RecoverOnly ||
		r.Reservation != nil || r.ClearReservation || r.RelocateVersionedCache {
		return false
	}
	return true
}

// Only the fixed Local object and its manual object merge at leaf granularity.
// Unknown leaves survive; malformed existing containers are never reconstructed.
func mergeCopilotPolicy(old, patch json.RawMessage) (json.RawMessage, error) {
	current, err := copilotObject(old, true)
	if err != nil {
		return nil, err
	}
	// A malformed existing manual container also refuses a native-only patch.
	if _, err := copilotObject(current["manual"], true); err != nil {
		return nil, err
	}
	changes, err := copilotObject(patch, false)
	if err != nil {
		return nil, err
	}
	for key, value := range changes {
		if key == "manual" {
			manual, err := copilotObject(current[key], true)
			if err != nil {
				return nil, err
			}
			leaves, err := copilotObject(value, false)
			if err != nil {
				return nil, err
			}
			for leaf, raw := range leaves {
				manual[leaf] = raw
			}
			value, err = json.Marshal(manual)
			if err != nil {
				return nil, err
			}
		}
		current[key] = value
	}
	return json.Marshal(current)
}

func copilotObject(raw json.RawMessage, absent bool) (map[string]json.RawMessage, error) {
	if raw == nil && absent {
		return map[string]json.RawMessage{}, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, fmt.Errorf("invalid Local policy container")
	}
	return fields, nil
}

// RevokeCopilotVSCode permits precisely native, manual, or all false leaves.
// Consumer bytes must be supplied from the single recorded portable binding.
func copilotRevokeOnly(r Request) bool {
	if r.RevokeGemini || r.RevokeOpenCode || !r.PolicyOnly || !r.RefreshOnly ||
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
	if r.RevokeCopilotVSCode || localPortableConsumer(r.ConsumerID, r.Consumer) {
		return true
	}
	var route map[string]json.RawMessage
	return json.Unmarshal(r.PolicyFields["route"], &route) == nil && route["copilotVSCodeNotifications"] != nil
}
