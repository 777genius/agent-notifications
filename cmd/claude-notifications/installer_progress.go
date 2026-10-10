package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/installprogress"
	"github.com/charmbracelet/x/term"
)

func installerProgressMain(args []string, stdout io.Writer, stderr *os.File) int {
	if len(args) == 1 && args[0] == "--capabilities" {
		fmt.Fprintln(stdout, installprogress.Capability)
		return 0
	}
	flags := flag.NewFlagSet("installer-progress", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("events", "", "private append-only event file")
	mode := flags.String("ui", "auto", "auto, plain or rich")
	ownerPID := flags.Int("owner-pid", 0, "installer process to observe")
	transient := flags.Bool("transient", false, "clear rich board on exit")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *path == "" || *ownerPID < 0 || (*mode != "auto" && *mode != "plain" && *mode != "rich") {
		fmt.Fprintln(stderr, "installer-progress: invalid arguments")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	// An abandoned producer cannot leave an observer running indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	terminalWidth := func() int { width, _, _ := term.GetSize(stderr.Fd()); return width }
	noColor := os.Getenv("NO_COLOR") != ""
	rich := *mode != "plain" && !noColor && os.Getenv("TERM") != "dumb" && term.IsTerminal(stderr.Fd())
	if err := installprogress.Run(ctx, *path, installprogress.Options{Output: stderr, Rich: rich, Width: terminalWidth(), TerminalWidth: terminalWidth, OwnerPID: *ownerPID, Transient: *transient}); err != nil {
		fmt.Fprintln(stderr, "installer-progress: observer unavailable")
		return 1
	}
	return 0
}
