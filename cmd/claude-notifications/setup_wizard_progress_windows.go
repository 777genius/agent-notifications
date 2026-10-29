package main

import (
	"os"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func validateBootstrapProgressLeaf(stage string, _ *os.Root) error {
	// Existing handle-based checks require an owned private DACL, a regular
	// single-link file, and non-reparse ancestry. They never repair permissions.
	_, err := installruntime.ReadPrivateCacheDocument(stage, "progress.events", maxBootstrapIntent)
	return err
}
