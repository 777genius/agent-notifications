package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/desktoptemplate"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/pievent"
	"github.com/777genius/agent-notifications/internal/pisource"
	"github.com/777genius/agent-notifications/internal/platform"
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
				notifier.WithHookPresentation(notifier.HookPresentation{SessionName: "Pi", Folder: filepath.Base(request.CWD), Body: request.Body}),
				notifier.WithTemplateData(piTemplateData(ctx, cfg, request)))
		},
		SendWebhook: func(ctx context.Context, cfg *config.Config, send webhook.SendContext) error {
			sender := webhook.NewWithContext(ctx, cfg)
			err := sender.SendWithContext(send)
			_ = sender.Shutdown(50 * time.Millisecond)
			return err
		},
	}, nil
}

// piTemplateData collects desktop template values for a Pi notification. Each
// git fact costs a bounded subprocess, so it is looked up only when a template
// for the notified status uses it.
func piTemplateData(ctx context.Context, cfg *config.Config, request pievent.DesktopRequest) notifier.TemplateData {
	data := notifier.TemplateData{SessionName: "Pi", SessionID: request.SessionID, Folder: filepath.Base(request.CWD), Body: request.Body, Agent: "pi"}
	templates := cfg.DesktopTemplatesFor(string(request.Status))
	if !templates.References(desktoptemplate.Branch) && !templates.References(desktoptemplate.Repo) {
		return data
	}
	if ctx == nil || ctx.Err() != nil {
		return data
	}
	// Desktop is admitted before webhook under one 25s Pi deadline. Optional
	// metadata must not exhaust the webhook's existing 10s HTTP allowance.
	const gitBudget = 200 * time.Millisecond
	// Each Git helper can need another 200ms to drain an inherited pipe
	// after cancellation. Reserve both cleanup windows before webhook time.
	const gitCleanupReserve = 2 * 200 * time.Millisecond
	if cfg.IsStatusWebhookEnabled(string(request.Status)) {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= 10*time.Second+gitBudget+gitCleanupReserve {
			return data
		}
	}
	gitCtx, cancel := context.WithTimeout(ctx, gitBudget)
	defer cancel()
	if templates.References(desktoptemplate.Branch) {
		data.Branch = platform.GetGitBranchContext(gitCtx, request.CWD)
	}
	if templates.References(desktoptemplate.Repo) {
		data.Repo = platform.GetGitRepoNameContext(gitCtx, request.CWD)
	}
	return data
}
