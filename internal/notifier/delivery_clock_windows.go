//go:build windows

package notifier

import (
	"errors"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
)

// SystemBootClock uses the same kernel boot-environment GUID and interrupt
// time as the production journal clock, with 100ns precision for deadlines.
type SystemBootClock struct{}

func (SystemBootClock) Now() (string, float64, error) {
	boot, sec, nsec, ok := journal.WindowsBootSample()
	if !ok {
		return "", 0, errors.New("qualified continuous clock unavailable")
	}
	return boot, float64(sec) + float64(nsec)/1e9, nil
}
