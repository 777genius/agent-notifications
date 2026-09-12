package runtime

import (
	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// ValidateSetupPolicy uses the runtime's exact partial rate defaults and strict
// canonical global reader. Inputs belong to trusted installer composition. It
// never initializes state, creates configuration or invokes native code.
func ValidateSetupPolicy(s installruntime.PolicySnapshot, globalPath string) (agentnotify.Policy, error) {
	b := &Backend{opts: Options{GlobalConfig: globalPath, ReadGlobal: readGlobal}}
	return b.policy(s)
}

// ValidatePreparedSetupPolicy validates a strict in-memory preparation candidate
// with the same runtime parser; this never supplies production runtime defaults.
func ValidatePreparedSetupPolicy(s installruntime.PolicySnapshot, raw []byte) (agentnotify.Policy, error) {
	b := &Backend{opts: Options{ReadGlobal: func(string) ([]byte, error) { return raw, nil }}}
	return b.policy(s)
}
