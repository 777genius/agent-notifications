//go:build windows

package windowscallback

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Observation struct {
	State, Permission string
	EffectEntered     bool
}

func Deadline(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	remaining := 30 * time.Second
	if end, ok := ctx.Deadline(); ok && time.Until(end) < remaining {
		remaining = time.Until(end)
	}
	if remaining <= 0 {
		return 0
	}
	return BootMilliseconds() + uint64(remaining/time.Millisecond)
}

// Operator executes exclusively the physically guarded retained helper. The
// fixed command vocabulary and stdin envelope contain no paths/URI/commands.
func (g *Custody) Operator(ctx context.Context, mode string, fields []string, end uint64) (Observation, error) {
	switch mode {
	case "observe", "apply-clsid", "apply-aumid", "apply-shortcut", "restore-clsid", "restore-aumid", "restore-shortcut", "readback", "ready", "show", "verify-clsid", "verify-aumid", "verify-shortcut":
	default:
		return Observation{}, ErrUnavailable
	}
	if budget(ctx, end) != nil {
		return Observation{}, ErrUnavailable
	}
	if e := g.admitOperator(); e != nil {
		return Observation{}, e
	}
	var fence *operatorFence
	retained := false
	defer func() {
		if !retained {
			_ = fence.close()
			g.collectOperator()
		}
	}()
	if e := g.operatorObligation(); e != nil {
		return Observation{}, e
	}
	input := []byte{}
	if mode == "show" {
		if len(fields) != 4 {
			return Observation{}, ErrUnavailable
		}
		var e error
		input, e = encode(3, fields)
		if e != nil {
			return Observation{}, ErrUnavailable
		}
	}
	nonce, e := NewID()
	if e != nil {
		return Observation{}, ErrUnavailable
	}
	mutating := mode == "show" || strings.HasPrefix(mode, "apply-") || strings.HasPrefix(mode, "restore-")
	if mutating {
		fence, e = g.fenceOperator(nonce, mode, end)
		if e != nil {
			return Observation{}, e
		}
	}
	child := exec.Command(filepath.Join(g.Root, "helper.exe"), "--operator", mode, strconv.FormatUint(end, 10), nonce)
	// Caller expiry classifies the receipt; it does not kill an entered SDK
	// operation at 30s. Collect this owned helper under its original 65s lease,
	// then bound inherited-pipe drain. Unknown never grants replay/fallback.
	child.WaitDelay = 5 * time.Second
	child.Env = []string{}
	child.Stdin = bytes.NewReader(input)
	var output, stderr cappedOutput
	child.Stdout = &output
	child.Stderr = &stderr
	if budget(ctx, end) != nil {
		if fence != nil {
			_ = fence.clear(0, -1)
		}
		return Observation{}, ErrUnavailable
	}
	err := child.Start()
	if err != nil {
		if fence != nil {
			_ = fence.clear(0, -1)
		}
		return Observation{}, ErrUnavailable
	} // no process/API entry
	finished := make(chan error, 1)
	go func() { finished <- child.Wait() }()
	collection := time.NewTimer(70 * time.Second)
	defer collection.Stop()
	select {
	case err = <-finished:
	case <-collection.C:
		// Own child only; missing native lease/collection stays uncertain. This
		// last-resort kill never asserts broker/SDK/global quiescence.
		_ = child.Process.Kill()
		select {
		case err = <-finished:
		case <-time.After(5 * time.Second):
			// No actual collection: retain physical custody for this exact child.
			// Close refuses to release its handles until Wait really completes.
			retained = true
			go func() {
				<-finished
				if child.ProcessState == nil {
					// Wait returned without an observed process state. Keep
					// active physical custody/ticket for this Go incarnation.
					return
				}
				if fence != nil {
					_ = fence.collected(child.Process.Pid, child.ProcessState.ExitCode())
					_ = fence.close()
				}
				g.collectOperator()
			}()
			if mode == "show" {
				return Observation{State: "unknown"}, ErrUnknown
			}
			return Observation{}, ErrUnknown
		}
		if child.ProcessState == nil {
			retained = true
			return Observation{State: "unknown"}, ErrUnknown
		}
		if fence != nil {
			_ = fence.collected(child.Process.Pid, child.ProcessState.ExitCode())
		}
		return Observation{State: "unknown"}, ErrUnknown
	}
	if child.ProcessState == nil {
		// ExitCode's -1 on nil is not collection evidence. Do not publish a
		// collected fact, release custody, or retry process/SDK operations.
		retained = true
		return Observation{State: "unknown"}, ErrUnknown
	}
	if fence != nil {
		defer func() {
			if fence.held != 0 {
				if fence.collected(child.Process.Pid, child.ProcessState.ExitCode()) == nil {
					if fields := collectedOperatorDiagnostic(err, child.Process.Pid, child.ProcessState.ExitCode(), mutating, &output, &stderr); fields != nil {
						_ = fence.failure(child.Process.Pid, child.ProcessState.ExitCode(), fields)
					}
				}
			}
		}()
	}
	if output.overflow || budget(ctx, end) != nil {
		if mode == "show" {
			return Observation{State: "unknown"}, ErrUnknown
		}
		if mutating {
			return Observation{}, ErrUnknown
		}
		return Observation{}, ErrUnavailable
	}
	fieldsOut := strings.Fields(output.String())
	if len(fieldsOut) != 4 || fieldsOut[0] != "WCB1" || (fieldsOut[3] != "0" && fieldsOut[3] != "1") {
		if mode == "show" {
			return Observation{State: "unknown"}, ErrUnknown
		}
		if mutating {
			return Observation{}, ErrUnknown
		}
		return Observation{}, ErrUnavailable
	}
	o := Observation{fieldsOut[1], fieldsOut[2], fieldsOut[3] == "1"}
	if o.State == "declined" && o.Permission == "disabled" && !o.EffectEntered && (mode == "ready" || mode == "show") {
		// The immutable helper explicitly proved no Show, and this owned
		// process has actually returned/been Waited under the original end.
		// Clear only this known terminal, never by expiry or PID absence.
		if child.ProcessState.ExitCode() != 2 {
			return o, ErrUnknown
		}
		if fence != nil {
			if e = fence.clear(child.Process.Pid, child.ProcessState.ExitCode()); e != nil {
				return o, ErrUnknown
			}
		}
		return o, ErrDisabled
	}
	if mode == "show" && (o.EffectEntered || o.State == "unknown") && (err != nil || o.State != "submitted") {
		return o, ErrUnknown
	}
	if err != nil || (o.State != "ready" && o.State != "submitted") {
		if mutating {
			return o, ErrUnknown
		}
		return o, ErrUnavailable
	}
	if o.Permission != "not_checked" && o.Permission != "enabled" && o.Permission != "permission_unknown" {
		if mutating {
			return o, ErrUnknown
		}
		return o, ErrUnavailable
	}
	if fence != nil {
		if e = fence.clear(child.Process.Pid, child.ProcessState.ExitCode()); e != nil {
			return Observation{State: "unknown", EffectEntered: o.EffectEntered}, ErrUnknown
		}
	}
	return o, nil
}
func (p Port) CheckReadiness(ctx context.Context, b Binding, end uint64) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	g, e := Open(ctx, b, end)
	if e != nil {
		return e
	}
	defer g.Close()
	return g.Ready(ctx, end)
}
func (g *Custody) ReadyObservation(ctx context.Context, end uint64) (Observation, error) {
	if _, e := os.Lstat(filepath.Join(filepath.Dir(filepath.Dir(g.Root)), "transaction.json")); !os.IsNotExist(e) {
		return Observation{}, ErrUnavailable
	}
	return g.Operator(ctx, "ready", nil, end)
}
func (g *Custody) Ready(ctx context.Context, end uint64) error {
	_, e := g.ReadyObservation(ctx, end)
	return e
}
