package geminievent

import (
	"context"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
)

const TotalBudget = observation.TotalBudget

// Clock is a same-boot continuous clock, including elapsed time during suspend.
type Clock = observation.Clock

// Admission captures the total budget before stdin/source/locks. Cancel must
// be called after the invocation. Continuous-clock expiry also cancels HTTP.
func Admission(parent context.Context, clock Clock) (context.Context, notification.Deadline, context.CancelFunc, error) {
	return observation.Admission(parent, clock)
}

func remaining(clock Clock, deadline notification.Deadline) (time.Duration, bool) {
	return observation.Remaining(clock, deadline)
}

func watchBudget(parent context.Context, clock Clock, deadline notification.Deadline, duration time.Duration) (context.Context, context.CancelFunc) {
	return observation.WatchBudget(parent, clock, deadline, duration)
}
