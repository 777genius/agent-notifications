//go:build windows

package notifier

// TEMPORARY DIAGNOSTIC ONLY. Never merge into the final product candidate.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type testDesktopPhase string

const (
	testNativeStarted testDesktopPhase = "native_started"
	testFocusReady    testDesktopPhase = "focus_ready"
	testToastStarted  testDesktopPhase = "toast_started"
	testToastReturned testDesktopPhase = "toast_returned"
	testSoundStarted  testDesktopPhase = "sound_started"
	testSoundReturned testDesktopPhase = "sound_returned"
)

func traceTestDesktopPhase(ctx context.Context, phase testDesktopPhase) {
	defer func() { _ = recover() }()
	if ctx == nil {
		return
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return
	}
	// The actual Pi operation owns the unchanged 15-second deadline. Use its
	// monotonic context deadline here, without importing the Pi domain adapter.
	remaining := time.Until(deadline).Milliseconds()
	state := "active"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		state = "deadline_exceeded"
	} else if ctx.Err() != nil {
		state = "cancelled"
	}
	row := struct {
		Type        string           `json:"type"`
		Phase       testDesktopPhase `json:"phase"`
		Channel     string           `json:"channel"`
		ElapsedMS   int64            `json:"elapsed_ms"`
		RemainingMS int64            `json:"remaining_ms"`
		Context     string           `json:"context"`
	}{"TEST_pi_native_trace", phase, "desktop", max(0, 15000-remaining), max(0, remaining), state}
	_ = json.NewEncoder(os.Stderr).Encode(row)
}
