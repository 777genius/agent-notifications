package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/hooks"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/qoderinstall"
	"github.com/777genius/agent-notifications/internal/qodersource"
)

// Qoder notification hooks are observation-only: every failure stays silent
// and returns success. The source bounds stdin before SDK dispatch.
func handleQoderHook(event string) {
	defer errorhandler.HandlePanic()
	root := getPluginRootForProduct("qoder")
	if _, err := logging.InitLogger(root); err != nil {
		return
	}
	defer func() { _ = logging.Close() }()
	if !hooks.IsQoderHookEvent(event) {
		logging.Error("qoder: unsupported event")
		return
	}
	handler, err := hooks.NewHandlerWithSource(root, hooks.ProductQoder, hooks.QoderSource{})
	if err != nil {
		logging.Error("qoder: handler initialization failed")
		return
	}
	if err := handler.HandleHook(event, os.Stdin); err != nil {
		logging.Error("qoder: observation failed")
	}
}

// A separate entrypoint keeps standalone native bundles compatible while managed
// installs require their exact receipt and current channel consent.
func runManagedQoderHook(args []string) {
	defer errorhandler.HandlePanic()
	if len(args) == 0 || !hooks.IsQoderHookEvent(args[0]) {
		return
	}
	f := flag.NewFlagSet("qoder-event", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var control, binding string
	f.StringVar(&control, "control-root", "", "")
	f.StringVar(&binding, "binding", "", "")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 || !filepath.IsAbs(control) || binding == "" {
		return
	}
	// Host input can remain open indefinitely. Buffer it before taking the
	// installer lease so a stalled producer cannot prevent setup or removal.
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, qodersource.MaxPayloadBytes+1))
	if err != nil || len(raw) > qodersource.MaxPayloadBytes {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	snapshot, err := installruntime.ReadPolicySnapshot(ctx, control)
	if err != nil {
		return
	}
	current, release, err := installruntime.AcquirePolicyLease(ctx, control, snapshot)
	if err != nil {
		return
	}
	defer release()
	executable, err := os.Executable()
	if err != nil {
		return
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return
	}
	desktop, webhook := qoderinstall.ChannelsFromSnapshot(current, control, executable, binding)
	if !desktop && !webhook {
		return
	}
	root := current.Installation.Ledger.Consumers[qoderinstall.ConsumerID].RuntimeRoot
	if _, err = logging.InitLogger(control); err != nil {
		return
	}
	defer func() { _ = logging.Close() }()
	// The legacy desktop adapter discovers only the verified transaction-owned
	// helper aliases in the shared managed runtime. This process handles one event.
	if desktop {
		_ = os.Setenv("CLAUDE_PLUGIN_ROOT", root)
	}
	handler, err := hooks.NewHandlerWithSource(root, hooks.ProductQoder, hooks.QoderSource{})
	if err != nil {
		return
	}
	handler.RestrictChannels(desktop, webhook)
	_ = handler.HandleHook(args[0], bytes.NewReader(raw))
}
