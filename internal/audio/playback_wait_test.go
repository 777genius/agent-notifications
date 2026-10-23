package audio

import (
	"strings"
	"testing"
	"time"
)

func TestWaitForPlayback(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		if err := waitForPlayback(done, time.Hour); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("timeout is an error", func(t *testing.T) {
		err := waitForPlayback(make(chan struct{}), 0)
		if err == nil || !strings.Contains(err.Error(), "audio playback timed out") {
			t.Fatalf("expected playback timeout error, got %v", err)
		}
	})
}
