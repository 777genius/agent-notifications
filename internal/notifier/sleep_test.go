package notifier

import (
	"testing"
	"time"
)

// TestIsDisplayAsleep_IsBounded runs the real detector for the current
// platform. The value depends on the machine (a developer with the display
// actually asleep will get true), so only the contract that matters on the
// hook path is asserted: the probe returns, and it returns fast.
func TestIsDisplayAsleep_IsBounded(t *testing.T) {
	const budget = 5 * time.Second

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = IsDisplayAsleep()
	}()

	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("IsDisplayAsleep() did not return within %v", budget)
	}
}
