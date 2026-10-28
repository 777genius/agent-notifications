package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/pievent"
	"github.com/777genius/agent-notifications/internal/pisource"
	"github.com/777genius/agent-notifications/internal/webhook"
)

func resolveInstalledPiConsumer(ctx context.Context, selector string, facts pisource.Facts) (pievent.Consumer, error) {
	gate, binding, cfg, assets, err := piauthority.Open(ctx, selector, facts.Revision)
	if err != nil {
		return pievent.Consumer{}, err
	}
	clock := notifier.SystemBootClock{}
	return pievent.Consumer{Binding: binding, Gate: gate, Config: cfg,
		LocalTerminal: notifier.HasLocalTerminalContext(ctx), Clock: clock,
		Cache: &observation.RecentCache{Root: gate.Binding.DataRoot, Clock: clock},
		Desktop: func(ctx context.Context, request pievent.DesktopRequest) error {
			return notifier.New(cfg).SendDesktopContext(ctx, request.Status, request.Body, request.SessionID, request.CWD,
				notifier.WithInstalledRuntime(assets.RuntimeRoot, assets.NativeHelper),
				notifier.WithHookPresentation(notifier.HookPresentation{SessionName: "Pi", Folder: filepath.Base(request.CWD), Body: request.Body}))
		},
		SendWebhook: func(ctx context.Context, cfg *config.Config, send webhook.SendContext) error {
			sender := webhook.NewWithContext(ctx, cfg)
			err := sender.SendWithContext(send)
			_ = sender.Shutdown(50 * time.Millisecond)
			return err
		},
	}, nil
}
