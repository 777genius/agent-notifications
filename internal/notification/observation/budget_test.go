package observation

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
)

type budgetClock struct {
	boot    string
	seconds float64
	err     error
}

func (c budgetClock) Now() (string, float64, error) {
	return c.boot, c.seconds, c.err
}

// A repeated Windows clock tick must retain its freshly admitted budget, even
// when adding four seconds crosses a float64 precision boundary. At long
// uptimes the subtraction can round upward by more than a nanosecond.
func TestRemainingAdmissionBudgetRepeatedTick(t *testing.T) {
	for _, sample := range []struct {
		name        string
		seconds     int64
		nanoseconds int64
	}{
		{"short uptime", 511, 100100},
		{"long uptime", 16777215, 100000},
	} {
		t.Run(sample.name, func(t *testing.T) {
			clock := budgetClock{boot: "same-boot", seconds: float64(sample.seconds) + float64(sample.nanoseconds)/1e9}
			_, deadline, cancel, err := Admission(context.Background(), clock)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			remaining, ok := Remaining(clock, deadline)
			if !ok || remaining != 4*time.Second {
				t.Fatalf("fresh admission on repeated tick: remaining=%v accepted=%v", remaining, ok)
			}
		})
	}
}

func TestRemainingRejectsInvalidDeadline(t *testing.T) {
	good := budgetClock{boot: "same-boot", seconds: 100}
	deadline := notification.Deadline{BootID: good.boot, NotAfter: 104}
	for _, test := range []struct {
		name     string
		clock    Clock
		deadline notification.Deadline
	}{
		{"nil clock", nil, deadline},
		{"clock error", budgetClock{boot: good.boot, seconds: 100, err: errors.New("unavailable")}, deadline},
		{"empty boot", budgetClock{seconds: 100}, deadline},
		{"changed boot", budgetClock{boot: "other-boot", seconds: 100}, deadline},
		{"negative clock", budgetClock{boot: good.boot, seconds: -1}, deadline},
		{"nan clock", budgetClock{boot: good.boot, seconds: math.NaN()}, deadline},
		{"infinite clock", budgetClock{boot: good.boot, seconds: math.Inf(1)}, deadline},
		{"expired", good, notification.Deadline{BootID: good.boot, NotAfter: 100}},
		{"past", good, notification.Deadline{BootID: good.boot, NotAfter: 99}},
		{"nan deadline", good, notification.Deadline{BootID: good.boot, NotAfter: math.NaN()}},
		{"infinite deadline", good, notification.Deadline{BootID: good.boot, NotAfter: math.Inf(1)}},
		{"negative infinite deadline", good, notification.Deadline{BootID: good.boot, NotAfter: math.Inf(-1)}},
		{"forged extra budget", good, notification.Deadline{BootID: good.boot, NotAfter: math.Nextafter(104, math.Inf(1))}},
		{"clock moved backward", budgetClock{boot: good.boot, seconds: 99.9999999}, deadline},
	} {
		t.Run(test.name, func(t *testing.T) {
			if remaining, ok := Remaining(test.clock, test.deadline); ok || remaining != 0 {
				t.Fatalf("invalid deadline accepted: remaining=%v accepted=%v", remaining, ok)
			}
		})
	}
}

func TestRemainingDecreasesWithElapsedTime(t *testing.T) {
	deadline := notification.Deadline{BootID: "same-boot", NotAfter: 104}
	for _, test := range []struct {
		now  float64
		want time.Duration
	}{
		{100, 4 * time.Second},
		{101.25, 2750 * time.Millisecond},
		{103.5, 500 * time.Millisecond},
	} {
		remaining, ok := Remaining(budgetClock{boot: deadline.BootID, seconds: test.now}, deadline)
		if !ok || remaining != test.want {
			t.Fatalf("now=%v remaining=%v accepted=%v want=%v", test.now, remaining, ok, test.want)
		}
	}
}
