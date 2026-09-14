package main

import (
	"fmt"
	"os"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Package authentication is a prerequisite supplied by the installation route;
// this offline check only prevents delegation to a historical mutable writer.
func requireCompatibleUpdateScript(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !installruntime.WriterCompatible(data) {
		return fmt.Errorf("installer is below managed writer protocol floor; obtain a current authenticated package before updating")
	}
	return nil
}

// Pass paths as positional arguments, never interpolate them into shell source.
// Recheck on every delayed retry in case the package manager rolled back since
// scheduling. The immediate check also prevents launching the delegate at all
// for an already incompatible script.
const lazyUpdateShellCommand = `LC_ALL=C grep -aqF 'agent-notifications-managed-writer-protocol-v1' "$1" && INSTALL_TARGET_DIR="$2" exec "$1" --force`
