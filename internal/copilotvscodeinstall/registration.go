package copilotvscodeinstall

import (
	"reflect"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// There is deliberately no fixed Local consumer key. Native and later manual
// transport both use portable.Binding.Registration's canonical SHA256 identity.
func recorded(s installruntime.PolicySnapshot, b portable.Binding) bool {
	l := s.Installation.Ledger
	if (b.Integration != portable.CopilotVSCode && b.Integration != portable.Cursor) || s.Installation.Recovery || l.PendingMutation != nil ||
		l.ID == "" || l.ID != b.ComponentID || l.Owner != b.Owner || l.WriterFloor > installruntime.SupportedWriterFloor {
		return false
	}
	key, want, _, err := b.Registration()
	return err == nil && reflect.DeepEqual(l.Consumers[key], want)
}

func consumerBinding(s installruntime.PolicySnapshot, b portable.Binding) copilotvscodeevent.Binding {
	key, _, _, _ := b.Registration()
	product := config.AgentCopilotVSCode
	if b.Integration == portable.Cursor {
		product = config.AgentCursor
	}
	return copilotvscodeevent.Binding{InstallationID: b.InstallationID, BindingID: key,
		ProfileIdentity: b.ScopeID, Product: string(product), Generation: s.Installation.Ledger.Generation}
}
