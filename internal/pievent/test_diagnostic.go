package pievent

// TEMPORARY DIAGNOSTIC ONLY. Never merge this instrumentation into the final
// product candidate; its observations cannot qualify release delivery.
import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
)

type TestPhase string

const (
	TestAdmitted        TestPhase = "admitted"
	TestResolveStarted  TestPhase = "resolve_started"
	TestResolveReady    TestPhase = "resolve_ready"
	TestResolveRefused  TestPhase = "resolve_refused"
	TestConsumeStarted  TestPhase = "consume_started"
	TestChannelsReady   TestPhase = "channels_ready"
	TestClaimStarted    TestPhase = "claim_started"
	TestLeaseStarted    TestPhase = "lease_started"
	TestLeaseReady      TestPhase = "lease_ready"
	TestInvokeStarted   TestPhase = "invoke_started"
	TestInvokeReturned  TestPhase = "invoke_returned"
	TestConsumeReturned TestPhase = "consume_returned"
)

func TraceTestPhase(ctx context.Context, clock observation.Clock, deadline notification.Deadline, phase TestPhase, channel Channel, receipt *Receipt) {
	defer func() { _ = recover() }()
	if ctx == nil || clock == nil {
		return
	}
	_, now, err := clock.Now()
	if err != nil || math.IsNaN(now) || math.IsInf(now, 0) {
		return
	}
	state := "active"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		state = "deadline_exceeded"
	} else if ctx.Err() != nil {
		state = "cancelled"
	}
	row := struct {
		Type        string    `json:"type"`
		Phase       TestPhase `json:"phase"`
		Channel     Channel   `json:"channel"`
		ElapsedMS   int64     `json:"elapsed_ms"`
		RemainingMS int64     `json:"remaining_ms"`
		Context     string    `json:"context"`
		Receipt     *Receipt  `json:"receipt,omitempty"`
	}{"TEST_pi_native_trace", phase, channel, int64(math.Max(0, now-deadline.NotAfter+TotalBudget.Seconds()) * 1000),
		int64(math.Max(0, deadline.NotAfter-now) * 1000), state, receipt}
	_ = json.NewEncoder(os.Stderr).Encode(row)
}
