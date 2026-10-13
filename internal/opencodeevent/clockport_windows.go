//go:build windows

package opencodeevent

import "github.com/777genius/agent-notifications/internal/agentnotify/journal"

type systemCounter struct{}

func (systemCounter) SampleCounter() (CounterSample, bool) {
	boot, sec, nsec, ok := journal.WindowsPreciseBootSample()
	return CounterSample{boot, "windows-kernel", "windows-interrupt-precise", sec, nsec}, ok
}
