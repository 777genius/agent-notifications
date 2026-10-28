//go:build linux || darwin

package main

import (
	"os"

	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// The subprocess executes the production composition, never a substitute
// backend. A fixed clock permits Linux composition; native construction panics.
// This fixture sends read-only MCP status requests exclusively. Keeping its
// dispatch separate lets the bounded managed image omit unrelated test bodies.
func init() {
	// Retain the real CLI entry and writer declaration used by managed-image
	// validation, rather than relying on unrelated test bodies to link them.
	if len(os.Args) > 1 && os.Args[1] == "internal-writer-protocol" {
		main()
		os.Exit(0)
	}
	if len(os.Args) > 1 && (os.Args[1] == "portable-launch" || os.Args[1] == "portable-primary") {
		os.Exit(agentPortableRun(os.Args[1], os.Args[2:], notifyruntime.Options{BootClock: portableTestClock{}, DeliveryFactory: func(notifier.ManagedInstallation, string, notifier.BootClock) notifyruntime.Delivery {
			panic("unexpected native composition")
		}}))
	}
}

type portableTestClock struct{}

func (portableTestClock) Now() (string, float64, error) { return "portable-test-boot", 100, nil }
