//go:build linux

package notifier

import (
	"errors"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
)

// SystemBootClock uses the same trusted procfs boot identity and CLOCK_BOOTTIME
// epoch as the production journal clock, with nanosecond precision for deadlines.
type SystemBootClock struct{}

func (SystemBootClock) Now() (string, float64, error) {
	boot, sec, nsec, ok := journal.LinuxBootSample(journal.TrustedBootIDPath)
	if !ok {
		return "", 0, errors.New("qualified continuous clock unavailable")
	}
	return boot, float64(sec) + float64(nsec)/1e9, nil
}
