//go:build linux

package notifier

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
)

func TestSystemBootClockMatchesJournalDefault(t *testing.T) {
	boot, seconds, err := SystemBootClock{}.Now()
	if err != nil || boot == "" || seconds < 0 {
		t.Fatal(boot, seconds, err)
	}
	sample := journal.DefaultClock().Sample()
	if !sample.Available || sample.Boot != boot {
		t.Fatalf("boot identity diverged journal=%+v boot=%s", sample, boot)
	}
}
