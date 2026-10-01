package main

import (
	"github.com/777genius/agent-notifications/internal/opencodeevent"
	"io"
)

func runOpenCodeClock(args []string, output io.Writer) int {
	return opencodeevent.WriteClockSnapshot(args, output, opencodeevent.NewSystemSnapshotPort())
}
