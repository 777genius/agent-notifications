package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/installprogress"
	"github.com/charmbracelet/x/term"
)

func installerProgressMain(args []string, stdout io.Writer, stderr *os.File) int {
	// Bootstrap must negotiate worker isolation before backgrounding product
	// setup. Older released helpers retain the synchronous owner protocol.
	if len(args) == 1 && args[0] == "--parallel-capabilities" {
		// Shell-owned process groups are qualified on POSIX. Native Windows
		// descendant containment must be qualified before advertising fan-out;
		// its existing synchronous bootstrap remains available.
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return 1
		}
		if n, err := fmt.Fprintln(stdout, "installer-parallel-v1"); err != nil || n != len("installer-parallel-v1\n") {
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "--result-capabilities" {
		if _, err := fmt.Fprintln(stdout, installerResultCapability); err != nil {
			return 1
		}
		return 0
	}
	if (len(args) == 2 || len(args) == 3 && args[2] == "--recovery") && args[0] == "--result" {
		write := writeInstallerResult
		if len(args) == 3 {
			write = writeInstallerRecovery
		}
		if err := write(args[1], stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, "installer-progress: result unavailable")
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "--promotion" {
		if err := installprogress.Promotion(stdout); err != nil {
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "--session-capabilities" {
		if n, err := fmt.Fprintln(stdout, installprogress.SessionCapability); err != nil || n != len(installprogress.SessionCapability)+1 {
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "--capabilities" {
		if n, err := fmt.Fprintln(stdout, installprogress.Capability); err != nil || n != len(installprogress.Capability)+1 {
			return 1
		}
		return 0
	}
	flags := flag.NewFlagSet("installer-progress", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	session := flags.Bool("session", false, "session-v1 events")
	stopFile := flags.String("stop-file", "", "private observer stop control")
	activity := flags.String("activity", "compact", "compact, expanded or off")
	limit := flags.Int("activity-limit", 0, "visible checkpoints per client")
	path := flags.String("events", "", "private append-only event file")
	mode := flags.String("ui", "auto", "auto, plain or rich")
	ownerPID := flags.Int("owner-pid", 0, "installer process to observe")
	transient := flags.Bool("transient", false, "clear rich board on exit")
	if flags.Parse(args) != nil || (*activity != "compact" && *activity != "expanded" && *activity != "off") || *limit < 0 || *limit > 16 || flags.NArg() != 0 || *path == "" || *ownerPID < 0 || (*mode != "auto" && *mode != "plain" && *mode != "rich") {
		_, _ = fmt.Fprintln(stderr, "installer-progress: invalid arguments")
		return 2
	}
	invalidLimit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "activity-limit" && *limit == 0 {
			invalidLimit = true
		}
	})
	if invalidLimit {
		_, _ = fmt.Fprintln(stderr, "installer-progress: invalid arguments")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	// An abandoned producer cannot leave an observer running indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	terminalSize := func() (int, int) { w, h, _ := term.GetSize(stderr.Fd()); return w, h }
	terminalWidth := func() int { width, _, _ := term.GetSize(stderr.Fd()); return width }
	noColor := os.Getenv("NO_COLOR") != ""
	rich := os.Getenv("BOOTSTRAP_VERBOSE") != "1" && *mode != "plain" && !noColor && os.Getenv("TERM") != "dumb" && term.IsTerminal(stderr.Fd())
	run := installprogress.Run
	if *session {
		run = installprogress.RunSession
	}
	width, height := terminalSize()
	if *session && (width <= 0 || height <= 0) {
		rich = false
	}
	if err := run(ctx, *path, installprogress.Options{Output: stderr, Rich: rich, Width: width, Height: height, TerminalWidth: terminalWidth, TerminalSize: terminalSize, OwnerPID: *ownerPID, Transient: *transient, Activity: *activity, ActivityLimit: *limit, StopFile: *stopFile}); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "installer-progress: observer unavailable")
		return 1
	}

	return 0
}
