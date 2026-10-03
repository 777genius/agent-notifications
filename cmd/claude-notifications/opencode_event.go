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
	"github.com/777genius/agent-notifications/internal/opencodecodec"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
	"github.com/777genius/agent-notifications/internal/webhook"
)

func runOpenCodeEvent(args []string, input io.Reader, output io.Writer) int {
	started := time.Now()
	if len(args) == 2 && args[0] == "--protocol" && args[1] == "1" {
		if _, ok := input.(io.ReadCloser); !ok {
			_ = json.NewEncoder(output).Encode(opencodeevent.Receipt{Status: "rejected", Reason: "invalid_frame"})
			return 0
		}
	}
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithDeadline(signals, started.Add(20*time.Second))
	defer cancel()
	source := opencodeevent.NewSystemSnapshotPort()
	entry, err := opencodeevent.BeginEntry(ctx, started, source)
	result := opencodeevent.Receipt{Status: "rejected", Reason: "invalid_command"}
	var held *runtimeImageLease
	if entry != nil {
		defer func() {
			entry.Close() // join the original watcher before image release
			if held != nil {
				held.Close()
			}
		}()
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
				owned := ownedRuntimeDescriptor()
				var liveErr error
				held, liveErr = holdRuntimeLiveImage(entry.Context(), owned)
				if liveErr != nil || owned.ControlRoot != root || !validPrivateOrigin(owned.Origin) {
					_ = json.NewEncoder(output).Encode(opencodeevent.Receipt{Status: "suppressed", Reason: string(opencodeevent.TimeUnverified)})
					return 0
				}
				guard := &runtimeLifetimeGuard{lease: held, cancel: cancel}
				selected, selectionErr := opencodeevent.SelectTrustedImageClock(runtimeImageKey(held.image))
				candidate, known := opencodecodec.LookupCandidate(runtimeImageKey(held.image))
				_, readerBound := runtimeReaderTuple(held.image, candidate.Version)
				if selectionErr != nil || !known || !readerBound {
					_ = json.NewEncoder(output).Encode(opencodeevent.Receipt{Status: "suppressed", Reason: string(opencodeevent.TimeUnverified)})
					return 0
				}
				guardedSource := runtimeGuardedSnapshot{source: source, guard: guard, ctx: entry.Context()}
				consumer := opencodeevent.ComposedConsumer{ControlRoot: root, Executable: executable, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
					Source: guardedSource, Selection: selected, Assets: config.AssetContext{PluginRoot: filepath.Dir(executable)},
					Desktop: func(h *opencodeevent.Handoff) notification.DeliveryPort {
						return runtimeGuardedDelivery{port: newOpenCodeDesktopPort(h, root), guard: guard}
					},
					SendWebhook: func(ctx context.Context, cfg *config.Config, msg webhook.SendContext) error {
						if !guard.check(ctx) {
							return errRuntimePort
						}
						return webhook.NewWithContext(ctx, cfg).SendWithContext(msg)
					}}
				if closable, ok := input.(io.ReadCloser); ok {
					result = consumeRuntimeBoundFrame(consumer, entry, closable, owned.Origin)
				} else {
					result = opencodeevent.Receipt{Status: "rejected", Reason: "invalid_frame"}
				}
			}
		}
	}
	_ = json.NewEncoder(output).Encode(result)
	return 0
}
