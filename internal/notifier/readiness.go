package notifier

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// PermissionProcess extends only the known capability mode; unsupported
// implementations fail closed. Acquire must verify offline before any execution.
type PermissionProcess interface {
	ProbePermission(context.Context, string, string, string) ([]byte, error)
}

var _ notification.ReadinessPort = (*StructuredDelivery)(nil)

func (d *StructuredDelivery) permission(ctx context.Context, r notification.Request, lease NativeLease) string {
	if _, err := d.remaining(r); err != nil || ctx.Err() != nil {
		return "expired"
	}
	process, ok := d.Process.(PermissionProcess)
	if !ok {
		return "readiness_unavailable"
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "readiness_unavailable"
	}
	random[6] = (random[6] & 15) | 64
	random[8] = (random[8] & 63) | 128
	nonce := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	probe, cancel := context.WithTimeout(ctx, time.Second)
	data, err := process.ProbePermission(probe, lease.ExecutablePath(), r.CorrelationID, nonce)
	timedOut := probe.Err() != nil
	cancel()
	if _, e := d.remaining(r); e != nil || ctx.Err() != nil {
		return "expired"
	}
	p, decodeErr := nativeprotocol.DecodePermission(data, r.CorrelationID, nonce)
	if err != nil || timedOut || decodeErr != nil {
		return "readiness_unavailable"
	}
	switch p.Permission {
	case "allowed":
		return ""
	case "undetermined":
		return "activation_required"
	case "denied":
		return "permission_denied"
	default:
		return "readiness_unavailable"
	}
}
