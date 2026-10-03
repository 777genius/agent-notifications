package observation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/trace"
	"syscall"
	"time"
)

// ClaimFailure preserves bounded diagnostics without retaining paths, markers,
// document bytes or raw operating-system errors. Its public message stays fixed.
// Operation failure and deadline state are independent: both may occur together.
type ClaimFailure struct {
	Phase            string
	Class            string
	OSCode           uint32
	TotalElapsed     time.Duration
	StageElapsed     time.Duration
	BudgetElapsed    time.Duration
	BudgetState      string
	MayHavePublished bool
}

func (*ClaimFailure) Error() string { return "cache_unavailable" }

type claimDiagnostics struct {
	ctx           context.Context
	started       time.Time
	stageStarted  time.Time
	budgetStarted time.Time
	budget        context.Context
	phase         string
	region        *trace.Region
	publication   bool
}

func startClaimDiagnostics(ctx context.Context) *claimDiagnostics {
	d := &claimDiagnostics{ctx: ctx, started: time.Now()}
	d.enter("validate")
	return d
}

func (d *claimDiagnostics) enter(phase string) {
	d.end()
	d.phase, d.stageStarted = phase, time.Now()
	// Callers supply fixed labels only; never include request data in a trace.
	d.region = trace.StartRegion(d.ctx, "observation.claim/"+phase)
	if phase == "publish" {
		// An error cannot prove that publication did not happen. In particular,
		// close and deadline failures can follow a committed attempted bit.
		d.publication = true
	}
}

func (d *claimDiagnostics) end() {
	if d.region != nil {
		d.region.End()
		d.region = nil
	}
}

func (d *claimDiagnostics) fail(err error, fallback string) error {
	now := time.Now()
	f := &ClaimFailure{Phase: d.phase, Class: fallback, TotalElapsed: now.Sub(d.started),
		StageElapsed: now.Sub(d.stageStarted), BudgetState: "not_started", MayHavePublished: d.publication}
	defer func() {
		tracing := trace.IsEnabled()
		diagnostics := os.Getenv("AGENT_NOTIFICATIONS_OBSERVATION_DIAGNOSTICS") == "1"
		if tracing || diagnostics {
			detail := fmt.Sprintf(
				"phase=%s class=%s code=%d elapsed_ns=%d stage_ns=%d budget_ns=%d budget=%s publication_possible=%t",
				f.Phase, f.Class, f.OSCode, f.TotalElapsed, f.StageElapsed, f.BudgetElapsed, f.BudgetState, f.MayHavePublished)
			if tracing {
				trace.Log(d.ctx, "observation.claim.failure", detail)
			}
			if diagnostics {
				// Explicit operator diagnostics stay on stderr; hook stdout and
				// public receipts remain unchanged. No raw error is formatted.
				_, _ = fmt.Fprintln(os.Stderr, "observation.claim.failure", detail)
			}
		}
	}()
	if d.budget != nil {
		f.BudgetElapsed = now.Sub(d.budgetStarted)
		f.BudgetState = "active"
		if errors.Is(d.budget.Err(), context.DeadlineExceeded) {
			f.BudgetState = "deadline"
		} else if errors.Is(d.budget.Err(), context.Canceled) {
			f.BudgetState = "canceled"
		}
	}
	if err == nil {
		return f
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		f.Class = "deadline"
	case errors.Is(err, context.Canceled):
		f.Class = "canceled"
	default:
		var errno syscall.Errno
		if errors.As(err, &errno) {
			f.OSCode = uint32(errno)
			f.Class = "os_error"
			if runtime.GOOS == "windows" && errno == 32 {
				f.Class = "sharing_violation"
			} else if runtime.GOOS == "windows" && errno == 33 {
				f.Class = "lock_violation"
			} else if os.IsPermission(err) {
				f.Class = "permission"
			} else if os.IsNotExist(err) {
				f.Class = "not_found"
			}
		}
	}
	return f
}
