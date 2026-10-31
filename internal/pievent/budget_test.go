package pievent

import (
	"context"
	"testing"
	"time"
)

type budgetClock float64

func (c budgetClock) Now() (string, float64, error) { return "TEST-boot", float64(c), nil }

func TestAdmissionAssignsOneDeadlineForColdSequentialDelivery(t *testing.T) {
	ctx, deadline, cancel, err := Admission(context.Background(), budgetClock(100))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if deadline.BootID != "TEST-boot" || deadline.NotAfter != 125 {
		t.Fatalf("admission deadline: %+v", deadline)
	}
	if end, ok := ctx.Deadline(); !ok || time.Until(end) <= 24*time.Second || time.Until(end) > 25*time.Second {
		t.Fatal("admission did not bound its context to 25s")
	}
	// Completing the desktop late must leave only the original remainder for
	// the webhook, rather than grant a fresh full budget to the next channel.
	if left, ok := remaining(budgetClock(124), deadline); !ok || left != time.Second {
		t.Fatalf("original deadline renewed: remaining=%v valid=%t", left, ok)
	}
	if _, ok := remaining(budgetClock(125), deadline); ok {
		t.Fatal("expired admitted event revived")
	}
	deadline.NotAfter = 125.001
	if _, ok := remaining(budgetClock(100), deadline); ok {
		t.Fatal("deadline beyond the admission maximum accepted")
	}
}

func TestAdmissionPreservesEarlierParentCancellation(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	ctx, _, cancel, err := Admission(parent, budgetClock(100))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("longer admission budget deferred parent cancellation")
	}
}
