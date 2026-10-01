package geminievent

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
)

const TotalBudget = 4 * time.Second

// Clock is a same-boot continuous clock, including elapsed time during suspend.
type Clock interface {
	Now() (bootID string, seconds float64, err error)
}

func validTime(boot string, now float64, err error) bool {
	return err == nil && boot != "" && now >= 0 && !math.IsNaN(now) && !math.IsInf(now, 0)
}

// Admission captures the total budget before stdin/source/locks. Cancel must
// be called after the invocation. Continuous-clock expiry also cancels HTTP.
func Admission(parent context.Context, clock Clock) (context.Context, notification.Deadline, context.CancelFunc, error) {
	if clock == nil {
		return nil, notification.Deadline{}, nil, errors.New("clock_unavailable")
	}
	boot, now, err := clock.Now()
	if !validTime(boot, now, err) {
		return nil, notification.Deadline{}, nil, errors.New("clock_unavailable")
	}
	deadline := notification.Deadline{BootID: boot, NotAfter: now + TotalBudget.Seconds()}
	ctx, cancel := watchBudget(parent, clock, deadline, TotalBudget)
	return ctx, deadline, cancel, nil
}

func remaining(clock Clock, deadline notification.Deadline) (time.Duration, bool) {
	if clock == nil {
		return 0, false
	}
	boot, now, err := clock.Now()
	seconds := deadline.NotAfter - now
	if !validTime(boot, now, err) || boot != deadline.BootID || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > TotalBudget.Seconds() {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func watchBudget(parent context.Context, clock Clock, deadline notification.Deadline, duration time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, duration)
	done := make(chan struct{})
	go func() {
		defer func() {
			// A clock failure in this goroutine must cancel the effect, rather than
			// escaping the entrypoint's synchronous panic recovery and killing it.
			if recover() != nil {
				cancel()
			}
			close(done)
		}()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, ok := remaining(clock, deadline); !ok {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}
