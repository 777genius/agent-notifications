package notifier

import (
	"testing"
	"time"
)

// TestIsDoNotDisturb_IsBounded runs the real detector for the current platform.
// The value depends on the machine (a developer running with DND on will get
// true), so only the contract that matters on the hook path is asserted: the
// probe returns, and it returns fast.
func TestIsDoNotDisturb_IsBounded(t *testing.T) {
	const budget = 5 * time.Second

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = IsDoNotDisturb()
	}()

	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("IsDoNotDisturb() did not return within %v", budget)
	}
}
