package notifier

// TEMPORARY NATIVE DIAGNOSTIC ONLY. Exclude from the final product candidate.
import (
	"context"
	"encoding/json"
	"os"
	"time"
)

var testNativeDiagnosticStarted = time.Now()

func traceTestNative(ctx context.Context, phase, code string) {
	defer func() { _ = recover() }()
	if os.Getenv("TEST_PI_NATIVE_DIAGNOSTIC") != "1" || ctx == nil {
		return
	}
	end, ok := ctx.Deadline()
	if !ok {
		return
	}
	_ = json.NewEncoder(os.Stderr).Encode(struct {
		Type        string `json:"type"`
		Phase       string `json:"phase"`
		Code        string `json:"code"`
		ElapsedMS   int64  `json:"elapsed_ms"`
		RemainingMS int64  `json:"remaining_ms"`
	}{"TEST_pi_native_delivery", phase, code, min(60000, max(0, time.Since(testNativeDiagnosticStarted).Milliseconds())),
		min(20000, max(0, time.Until(end).Milliseconds()))})
}
