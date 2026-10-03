// Package copilotvscodeinstall owns binding-scoped Local consent and effect
// authority. This checkpoint supplies no installer, receipt producer or CLI.
package copilotvscodeinstall

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

var ErrDenied = errors.New("copilot_vscode_authority_unavailable")

// Choices changes exactly the three owned leaves. Nil preserves only consent
// from the same recorded binding; a fresh registration starts with all false.
type Choices struct{ Desktop, Webhook, Manual *bool }

// Consent is an immutable observation of explicit user intent, not an effect
// grant. Only ReadConsent can retain affirmative omitted choices.
type Consent struct {
	binding                  portable.Binding
	desktop, webhook, manual bool
	recorded                 bool
}

func policyObject(raw json.RawMessage, absent bool) (map[string]json.RawMessage, error) {
	if raw == nil && absent {
		return map[string]json.RawMessage{}, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, ErrDenied
	}
	return fields, nil
}

func explicitTrue(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("true")) }

// ReadConsent refuses malformed containers and any mismatched registration.
// Missing/null/nonboolean leaves deny independently. Shared enabled is neither
// borrowed for native channels nor changed by a Local selection.
func ReadConsent(s installruntime.PolicySnapshot, b portable.Binding) (Consent, error) {
	var out Consent
	if !recorded(s, b) {
		return out, ErrDenied
	}
	route, err := policyObject(s.Fields["route"], true)
	if err != nil {
		return out, err
	}
	local, err := policyObject(route["copilotVSCodeNotifications"], true)
	if err != nil {
		return out, err
	}
	manual, err := policyObject(local["manual"], true)
	if err != nil {
		return out, err
	}
	return Consent{binding: b, recorded: true, desktop: explicitTrue(local["desktop"]), webhook: explicitTrue(local["webhook"]), manual: explicitTrue(manual["enabled"])}, nil
}

// PolicyPatch computes desired intent only. It cannot produce physical proof or
// authorize a backend. The caller must still use generation AND policy-byte CAS.
// Kernel leaf merging preserves unknown nested and sibling members.
func PolicyPatch(b portable.Binding, choices Choices, previous *Consent) (map[string]json.RawMessage, error) {
	if b.Integration != portable.CopilotVSCode {
		return nil, ErrDenied
	}
	if _, _, _, err := b.Registration(); err != nil {
		return nil, err
	}
	saved := Consent{}
	if previous != nil {
		if !previous.recorded || previous.binding != b {
			return nil, ErrDenied
		}
		saved = *previous
	}
	pick := func(choice *bool, old bool) bool {
		if choice != nil {
			return *choice
		}
		return old
	}
	value := struct {
		Desktop bool `json:"desktop"`
		Webhook bool `json:"webhook"`
		Manual  struct {
			Enabled bool `json:"enabled"`
		} `json:"manual"`
	}{Desktop: pick(choices.Desktop, saved.desktop), Webhook: pick(choices.Webhook, saved.webhook)}
	value.Manual.Enabled = pick(choices.Manual, saved.manual)
	raw, err := json.Marshal(map[string]any{"copilotVSCodeNotifications": value})
	return map[string]json.RawMessage{"route": raw}, err
}
