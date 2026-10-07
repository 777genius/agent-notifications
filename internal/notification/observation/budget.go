// Package observation bounds notification work with a same-boot continuous clock.
package observation

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
	ctx, cancel := WatchBudget(parent, clock, deadline, TotalBudget)
	return ctx, deadline, cancel, nil
}

// Remaining rejects expired or invalid deadlines and clock/boot changes.
// It never grants more than the fixed admission budget.
func Remaining(clock Clock, deadline notification.Deadline) (time.Duration, bool) {
	if clock == nil {
		return 0, false
	}
	boot, now, err := clock.Now()
	seconds := deadline.NotAfter - now
	// Use the same absolute bound as Admission: subtracting across a float64
	// precision boundary can round a freshly admitted four seconds upward.
	if !validTime(boot, now, err) || boot != deadline.BootID || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || deadline.NotAfter > now+TotalBudget.Seconds() {
		return 0, false
	}
	return time.Duration(math.Min(seconds, TotalBudget.Seconds()) * float64(time.Second)), true
}

// WatchBudget cancels on parent timeout or continuous-clock expiry/failure.
// Duration is the caller's remaining timeout, not a configurable admission
// budget; deadline stays unchanged. Cancel joins the watcher before returning.
func WatchBudget(parent context.Context, clock Clock, deadline notification.Deadline, duration time.Duration) (context.Context, context.CancelFunc) {
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
				if _, ok := Remaining(clock, deadline); !ok {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}
