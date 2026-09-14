package installruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
			current[member] = value
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
		!r.RemoveConsumer && !r.PurgeNative && !r.RetireNative && !r.RollbackPending
}
