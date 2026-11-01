// Package pievent owns Pi observation admission and effect policy.
package pievent

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
)

// TotalBudget is assigned once at admission. Cold Windows PowerShell startup,
// native toast submission, configured sound and the following webhook all share
// it; neither channel nor lifecycle shutdown may renew an admitted deadline.
const TotalBudget = 25 * time.Second

func validTime(boot string, now float64, err error) bool {
	return err == nil && boot != "" && now >= 0 && !math.IsNaN(now) && !math.IsInf(now, 0)
}
func Admission(parent context.Context, clock observation.Clock) (context.Context, notification.Deadline, context.CancelFunc, error) {
	if parent == nil || clock == nil {
		return nil, notification.Deadline{}, nil, errors.New("clock_unavailable")
	}
	boot, now, err := clock.Now()
	if !validTime(boot, now, err) {
		return nil, notification.Deadline{}, nil, errors.New("clock_unavailable")
	}
	d := notification.Deadline{BootID: boot, NotAfter: now + TotalBudget.Seconds()}
	ctx, cancel := watch(parent, clock, d, TotalBudget)
	return ctx, d, cancel, nil
}
func remaining(clock observation.Clock, d notification.Deadline) (time.Duration, bool) {
	if clock == nil {
		return 0, false
	}
	boot, now, err := clock.Now()
	delta := d.NotAfter - now
	if !validTime(boot, now, err) || boot != d.BootID || math.IsNaN(delta) || math.IsInf(delta, 0) || delta <= 0 || d.NotAfter > now+TotalBudget.Seconds() {
		return 0, false
	}
	return time.Duration(math.Min(delta, TotalBudget.Seconds()) * float64(time.Second)), true
}
func watch(parent context.Context, clock observation.Clock, d notification.Deadline, duration time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, duration)
	done := make(chan struct{})
	go func() {
		defer func() {
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
				if _, ok := remaining(clock, d); !ok {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}
