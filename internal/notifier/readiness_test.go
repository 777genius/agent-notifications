package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

func permissionReply(c, n, status string) []byte {
	b, _ := json.Marshal(nativeprotocol.Permission{SchemaVersion: 1, CorrelationID: c, Nonce: n, Backend: "macos.usernotifications", Permission: status})
	return b
}

// Existing delivery fixtures now explicitly model authorized permission.
func (p pr3Process) ProbePermission(ctx context.Context, e, c, n string) ([]byte, error) {
	return permissionReply(c, n, "allowed"), nil
}

type readinessProcess struct {
	pr3Process
	permission func(context.Context, string, string, string) ([]byte, error)
}

func (p readinessProcess) ProbePermission(ctx context.Context, e, c, n string) ([]byte, error) {
	return p.permission(ctx, e, c, n)
}

func TestReadinessPermissionAndRelease(t *testing.T) {
	for _, tt := range []struct{ permission, status, reason string }{{"allowed", "ready", "permission_authorized"}, {"denied", "rejected", "permission_denied"}, {"undetermined", "rejected", "activation_required"}, {"unavailable", "rejected", "readiness_unavailable"}} {
		t.Run(tt.permission, func(t *testing.T) {
			h := newPR3Harness(t)
			count := 0
			h.delivery.Process = readinessProcess{h.delivery.Process.(pr3Process), func(ctx context.Context, e, c, n string) ([]byte, error) {
				count++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded probe")
				}
				return permissionReply(c, n, tt.permission), nil
			}}
			h.delivery.Spool = nil
			for i := 0; i < 3; i++ {
				h.released = false
				out := h.delivery.CheckReadiness(context.Background(), pr3Request())
				if out.Status != tt.status || out.Reason != tt.reason || !h.released || out.Navigation.Capability != "available" || out.Backend != "macos_native" {
					t.Fatalf("%+v released=%v", out, h.released)
				}
			}
			if count != 3 || h.prepares != 0 || h.launches != 0 || h.spool.expired != 0 {
				t.Fatal("readiness effects")
			}
		})
	}
}
func TestReadinessRevocationAndBudget(t *testing.T) {
	h := newPR3Harness(t)
	permission := "allowed"
	calls := 0
	h.delivery.Process = readinessProcess{h.delivery.Process.(pr3Process), func(ctx context.Context, e, c, n string) ([]byte, error) {
		calls++
		return permissionReply(c, n, permission), nil
	}}
	r := pr3Request()
	if h.delivery.CheckReadiness(context.Background(), r).Status != "ready" {
		t.Fatal("not ready")
	}
	permission = "denied"
	if out := h.delivery.Deliver(context.Background(), r); out.Reason != "permission_denied" || h.prepares != 0 || h.launches != 0 {
		t.Fatalf("%+v", out)
	}
	h.clock.set(111)
	if out := h.delivery.CheckReadiness(context.Background(), r); out.Reason != "expired" || calls != 2 {
		t.Fatalf("%+v calls=%d", out, calls)
	}
	if out := h.delivery.Deliver(context.Background(), r); out.Reason != "expired" || calls != 2 {
		t.Fatal(out)
	}
}
func TestReadinessZeroProbeAndSuspend(t *testing.T) {
	for _, kind := range []string{"invalid", "disabled", "unknown", "suspend"} {
		t.Run(kind, func(t *testing.T) {
			h := newPR3Harness(t)
			r := pr3Request()
			switch kind {
			case "invalid":
				r.Content.Title = "\x00"
			case "disabled":
				r.Policy.ExplicitEnabled = false
			case "unknown":
				h.delivery.Installation = pr3Install{func(context.Context) (NativeLease, error) { return nil, errors.New("unverified") }}
			case "suspend":
				h.delivery.Installation = pr3Install{func(context.Context) (NativeLease, error) { h.clock.set(111); return pr3Lease{&h.released}, nil }}
			}
			out := h.delivery.CheckReadiness(context.Background(), r)
			if out.Status == "ready" || h.probes != 0 || h.prepares != 0 || h.launches != 0 {
				t.Fatalf("%+v", out)
			}
		})
	}
}
func TestReadinessPermissionCommand(t *testing.T) {
	cmd := nativePermissionCommand(context.Background(), "/verified/helper", pr3Correlation, pr3Nonce)
	want := []string{"/verified/helper", "--capabilities-json", "--permission-status", "--correlation-id", pr3Correlation, "--nonce", pr3Nonce}
	if !reflect.DeepEqual(cmd.Args, want) || cmd.Dir != "/" || cmd.Cancel == nil {
		t.Fatal(cmd.Args)
	}
}

func TestReadinessSuspendDuringPermission(t *testing.T) {
	h := newPR3Harness(t)
	h.delivery.Process = readinessProcess{h.delivery.Process.(pr3Process), func(ctx context.Context, e, c, n string) ([]byte, error) {
		h.clock.set(111)
		return permissionReply(c, n, "allowed"), nil
	}}
	if out := h.delivery.Deliver(context.Background(), pr3Request()); out.Reason != "expired" || h.prepares != 0 || h.launches != 0 || !h.released {
		t.Fatal(out)
	}
}
func TestReadinessThenNativeRejectOrUnknown(t *testing.T) {
	for _, status := range []string{"rejected", "unknown"} {
		t.Run(status, func(t *testing.T) {
			h := newPR3Harness(t)
			if h.delivery.CheckReadiness(context.Background(), pr3Request()).Status != "ready" {
				t.Fatal("not ready")
			}
			reason := "permission_denied"
			if status == "unknown" {
				reason = "timeout"
			}
			h.reply = pr3Receipt(status, reason)
			if out := h.delivery.Deliver(context.Background(), pr3Request()); out.Status != status || out.Reason != reason {
				t.Fatal(out)
			}
		})
	}
}

func TestReadinessPermissionTimeout(t *testing.T) {
	h := newPR3Harness(t)
	h.delivery.Process = readinessProcess{h.delivery.Process.(pr3Process), func(ctx context.Context, e, c, n string) ([]byte, error) {
		<-ctx.Done()
		return permissionReply(c, n, "allowed"), nil
	}}
	if out := h.delivery.CheckReadiness(context.Background(), pr3Request()); out.Reason != "readiness_unavailable" || h.prepares != 0 || h.launches != 0 || !h.released {
		t.Fatal(out)
	}
}
func TestReadinessReacquiresGeneration(t *testing.T) {
	h := newPR3Harness(t)
	if h.delivery.CheckReadiness(context.Background(), pr3Request()).Status != "ready" {
		t.Fatal("not ready")
	}
	h.delivery.Installation = pr3Install{func(context.Context) (NativeLease, error) { return nil, errors.New("generation replaced") }}
	if out := h.delivery.Deliver(context.Background(), pr3Request()); out.Reason != "unsupported_notifier" || h.prepares != 0 || h.launches != 0 || h.probes != 1 {
		t.Fatal(out)
	}
}
