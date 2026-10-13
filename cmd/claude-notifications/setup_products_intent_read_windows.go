package main

import (
	"os"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func readBootstrapIntentDocument(stage string, _ *os.Root, leaf string) ([]byte, error) {
	return installruntime.ReadPrivateCacheDocument(stage, leaf, maxBootstrapIntent)
}
