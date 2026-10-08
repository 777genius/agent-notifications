//go:build windows

package setup

import (
	"context"
	"encoding/json"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/windowscallback"
)

func stageWindowsRoute(ctx context.Context, o Options, r Request, fields map[string]json.RawMessage) (*installruntime.WindowsChange, error) {
	if o.Platform != "windows" || r.Route == nil || !r.Route.LocalRouting {
		return nil, nil
	}
	if r.Route.ApplicationPath != "" || r.Route.TeamID != "" {
		return nil, windowscallback.ErrUnavailable
	}
	if r.Route.Windows != (notification.WindowsBinding{}) {
		return nil, windowscallback.ErrUnavailable
	} // No user/model supplied binding.
	w, e := installruntime.StageWindowsGeneration(ctx, o.ControlRoot)
	if e != nil {
		return nil, e
	}
	route := *r.Route
	route.Windows = notification.WindowsBinding{SnapshotPath: w.Binding.SnapshotPath, SHA256: w.Binding.SHA256}
	fields["route"], e = json.Marshal(route)
	return w, e
}
func verifyWindowsRoute(ctx context.Context, b notification.WindowsBinding) error {
	return windowscallback.NewPort().CheckReadiness(ctx, windowscallback.Binding{SnapshotPath: b.SnapshotPath, SHA256: b.SHA256}, windowscallback.Deadline(ctx))
}
