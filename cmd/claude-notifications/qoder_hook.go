package main

import (
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/hooks"
	"github.com/777genius/agent-notifications/internal/logging"
	"os"
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
