package installruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

// rawPolicyRequest has no caller path, assets, consent or lifecycle authority.
// Prepare remains the existing pure, empty-result full-ledger observation fence.
func rawPolicyRequest(r Request) bool {
	return r.PolicyOnly && r.RefreshOnly && r.Owner == "existing-installer" && r.ConsumerID == openCodeConsumer &&
		r.ExpectedGeneration != nil && r.ExpectedPolicy != nil && r.Prepare != nil &&
		r.PolicyEnabled == nil && len(r.PolicyFields) == 0 && len(r.Files) == 0 && len(r.ConfigPaths) == 0 &&
		r.Native == nil && !r.RemoveConsumer && !r.PurgeNative && !r.RetireNative && !r.RollbackPending &&
		!r.RecoverOnly && !r.RevokeOpenCode && !r.RevokeGemini && !r.RevokeCopilotVSCode && !r.RelocateVersionedCache &&
		r.Reservation == nil && !r.ClearReservation && reflect.DeepEqual(r.Consumer, Consumer{})
}

// Decode protected values losslessly: formatting and object order may change,
// but nested setup/route/origin values and large integers may not.
func validateRawPolicyDocument(before map[string]json.RawMessage, data []byte) error {
	_, after, err := decodeUserPolicy(data)
	if err != nil {
		return err
	}
	decode := func(fields map[string]json.RawMessage) (map[string]any, error) {
		b, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if err = d.Decode(&result); err != nil || !unambiguousPolicyValue(result) {
			return nil, fmt.Errorf("ambiguous raw policy document")
		}
		for key := range result {
			for _, known := range []string{"schemaVersion", "enabled", "notifications", "statuses", "debug", "route", "rates", "setupState"} {
				if strings.EqualFold(key, known) && key != known {
					return nil, fmt.Errorf("ambiguous raw policy field")
				}
			}
		}
		for _, key := range []string{"notifications", "statuses", "debug"} {
			delete(result, key)
		}
		return result, nil
	}
	old, err := decode(before)
	if err != nil {
		return err
	}
	next, err := decode(after)
	if err != nil || !reflect.DeepEqual(old, next) {
		return fmt.Errorf("raw policy changed protected fields")
	}
	return nil
}

func unambiguousPolicyValue(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		seen := map[string]bool{}
		for key, child := range v {
			folded := strings.ToLower(key)
			if seen[folded] || !unambiguousPolicyValue(child) {
				return false
			}
			seen[folded] = true
		}
	case []any:
		for _, child := range v {
			if !unambiguousPolicyValue(child) {
				return false
			}
		}
	}
	return true
}

func validRawPolicyRegistration(l Ledger, c Consumer) bool {
	if c.OpenCode == nil || !c.OpenCode.Valid() || !c.OpenCode.OriginBound ||
		!filepath.IsAbs(c.Registration) || filepath.Base(c.Registration) != "agent-notifications.js" || len(c.Commands) != 4 {
		return false
	}
	binary := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if !reflect.DeepEqual(c.Commands, []string{filepath.Join(c.RuntimeRoot, binary), "opencode-event", "--protocol", "1"}) {
		return false
	}
	bundle, ok := OwnedFile(l, c.Registration)
	executable, executableOK := OwnedFile(l, c.Commands[0])
	return ok && bundle.Exists && bundle.Link == "" && bundle.SHA256 == c.OpenCode.BundleSHA256 &&
		executableOK && executable.Exists && executable.Link == ""
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
