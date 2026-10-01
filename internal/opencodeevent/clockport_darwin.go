//go:build darwin

package opencodeevent

import (
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
)

type systemCounter struct{}

func (systemCounter) SampleCounter() (CounterSample, bool) {
	boot, sec, nsec, ok := journal.DarwinBootSample()
	// Pinned x/sys exposes no Darwin ClockGetres. Keep the existing integer
	// clock binding; the local budget still requires native qualification.
	return CounterSample{strings.ToLower(boot), "darwin-kernel", "darwin-monotonic-raw", sec, nsec}, ok
}
