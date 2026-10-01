package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
	"github.com/777genius/agent-notifications/internal/webhook"
)

func runOpenCodeEvent(args []string, input io.Reader, output io.Writer) int {
	started := time.Now()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	source := opencodeevent.NewSystemSnapshotPort()
	entry, err := opencodeevent.BeginEntry(ctx, started, source)
	result := opencodeevent.Receipt{Status: "rejected", Reason: "invalid_command"}
	if entry != nil {
		defer entry.Close()
	}
	if len(args) == 2 && args[0] == "--protocol" && args[1] == "1" {
		if err != nil {
			result = opencodeevent.Receipt{Status: "suppressed", Reason: string(opencodeevent.TimeUnverified)}
		} else {
			root := os.Getenv("AGENT_NOTIFICATIONS_CONTROL_ROOT")
			executable, execErr := os.Executable()
			if execErr != nil || !canonicalPrivatePath(root) || !canonicalPrivatePath(executable) {
				result = opencodeevent.Receipt{Status: "suppressed", Reason: string(opencodeevent.NotRegistered)}
			} else {
				selected, _ := opencodeevent.SelectTrustedClock(runtime.GOOS, runtime.GOARCH)
				consumer := opencodeevent.ComposedConsumer{ControlRoot: root, Executable: executable, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
					Source: source, Selection: selected, Assets: config.AssetContext{PluginRoot: filepath.Dir(executable)},
					Desktop: func(h *opencodeevent.Handoff) notification.DeliveryPort { return newOpenCodeDesktopPort(h, root) },
					SendWebhook: func(ctx context.Context, cfg *config.Config, msg webhook.SendContext) error {
						return webhook.NewWithContext(ctx, cfg).SendWithContext(msg)
					}}
				if closable, ok := input.(io.ReadCloser); ok {
					result = consumer.ConsumeEntry(entry, closable)
				} else {
					result = opencodeevent.Receipt{Status: "rejected", Reason: "invalid_frame"}
				}
			}
		}
	}
	_ = json.NewEncoder(output).Encode(result)
	return 0
}
