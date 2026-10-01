package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/geminievent"
	"github.com/777genius/agent-notifications/internal/geminiinstall"
	"github.com/777genius/agent-notifications/internal/geminisource"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/webhook"
)

type geminiGate struct {
	args       geminiEventArgs
	executable string
	expected   installruntime.PolicySnapshot
}

func (g geminiGate) Channels(ctx context.Context, binding geminievent.Binding) geminievent.Channels {
	l := g.expected.Installation.Ledger
	if ctx.Err() != nil || binding.InstallationID != l.ID || binding.Generation != l.Generation || !geminiinstall.SnapshotCurrent(g.args.ControlRoot, g.expected) {
		return geminievent.Channels{}
	}
	d, w := geminiinstall.ChannelsFromSnapshot(g.expected, g.args.ControlRoot, g.executable, g.args.Binding, runtime.GOOS, runtime.GOARCH)
	return geminievent.Channels{Desktop: d, Webhook: w}
}
func (g geminiGate) Recheck(ctx context.Context, binding geminievent.Binding, channel geminievent.Channel) bool {
	c := g.Channels(ctx, binding)
	return (channel == geminievent.DesktopChannel && c.Desktop) || (channel == geminievent.WebhookChannel && c.Webhook)
}
func (g geminiGate) binding() geminievent.Binding {
	l := g.expected.Installation.Ledger
	return geminievent.Binding{InstallationID: l.ID, Generation: l.Generation}
}
func (g geminiGate) acquire(ctx context.Context, channel geminievent.Channel) (func(), error) {
	_, release, err := installruntime.AcquirePolicyLease(ctx, g.args.ControlRoot, g.expected)
	if err != nil {
		return nil, err
	}
	if !g.Recheck(ctx, g.binding(), channel) {
		release()
		return nil, errors.New("Gemini channel revoked")
	}
	return release, nil
}

type geminiLeasedDesktop struct {
	gate geminiGate
	port notification.DeliveryPort
}

func (p geminiLeasedDesktop) Deliver(ctx context.Context, req notification.Request) notification.Receipt {
	release, err := p.gate.acquire(ctx, geminievent.DesktopChannel)
	if err != nil {
		return notification.Receipt{CorrelationID: req.CorrelationID, Status: "suppressed", Reason: "revoked"}
	}
	defer release()
	return p.port.Deliver(ctx, req)
}

func runGeminiEvent(args []string, input io.ReadCloser, output io.Writer) (code int) {
	// Admission precedes stdin. Even boot-clock failures and admission panics have
	// the exact neutral response, without global error logging initialization.
	admitted := false
	defer func() {
		if recover() != nil || !admitted {
			_, _ = io.WriteString(output, "{}\n")
		}
	}()
	clock := notifier.SystemBootClock{}
	ctx, deadline, cancel, err := geminievent.Admission(context.Background(), clock)
	if err != nil {
		return 0
	}
	defer cancel()
	admitted = true
	return runGeminiEventWith(ctx, args, input, output, func(ctx context.Context, a geminiEventArgs, payload []byte) {
		facts, err := geminisource.Decode(ctx, a.Event, payload)
		if err != nil {
			return
		}
		executable, err := os.Executable()
		if err != nil {
			return
		}
		snapshot, err := installruntime.ReadPolicySnapshot(ctx, a.ControlRoot)
		if err != nil {
			return
		}
		gate := geminiGate{args: a, executable: executable, expected: snapshot}
		channels := gate.Channels(ctx, gate.binding())
		if !channels.Desktop && !channels.Webhook {
			return
		}
		cfg, err := config.LoadForAgentQuiet(filepath.Dir(executable), config.AgentGemini)
		if err != nil {
			return
		}
		consumer := geminievent.Consumer{Gate: gate, Binding: gate.binding(), Config: cfg, Clock: clock,
			Cache: &geminievent.RecentCache{Root: filepath.Join(a.ControlRoot, "gemini-observations"), Clock: clock}, Desktop: newGeminiDesktopPort(gate)}
		consumer.SendWebhook = func(ctx context.Context, cfg *config.Config, message webhook.SendContext) error {
			release, err := gate.acquire(ctx, geminievent.WebhookChannel)
			if err != nil {
				return err
			}
			defer release()
			sender := webhook.NewWithContext(ctx, cfg)
			defer func() { _ = sender.Shutdown(50 * time.Millisecond) }()
			return sender.SendWithContext(message)
		}
		_ = consumer.Consume(ctx, facts, deadline)
	})
}
