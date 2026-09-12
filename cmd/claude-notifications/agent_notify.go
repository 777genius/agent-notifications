package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	notifycli "github.com/777genius/agent-notifications/internal/agentnotify/cli"
	notifymcp "github.com/777genius/agent-notifications/internal/agentnotify/mcp"
	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
)

const agentNotifyHelp = "Usage: notify [--context-file PATH] [--help]\nRead one literal notification JSON document from stdin through EOF.\nUnknown outcomes must never be retried automatically.\n"
const agentMCPHelp = "Usage: mcp-server --integration codex|claude [--help]\nServe explicit notifications over newline-delimited JSON-RPC stdio.\nIntegration is operator configuration, not notification content.\n"

// Validate before opening stdio, resolving runtime paths, or consulting configuration.
// Keep notify's public grammar identical to the adapter's bounded grammar.
func agentNotifyFlags(command string, args []string) (integration string, help, valid bool) {
	if len(args) > 3 {
		return "", false, false
	}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 4096 || seen[a] {
			return "", false, false
		}
		seen[a] = true
		switch a {
		case "--help":
			help = true
		case "--context-file", "--integration":
			if (command == "notify") != (a == "--context-file") {
				return "", false, false
			}
			i++
			if i == len(args) || args[i] == "" || len(args[i]) > 4096 {
				return "", false, false
			}
			if a == "--integration" {
				integration = args[i]
				if integration != "codex" && integration != "claude" {
					return "", false, false
				}
			}
		default:
			return "", false, false
		}
	}
	return integration, help, command == "notify" || (command == "mcp-server" && (help || integration != ""))
}

type agentNotifyStatus struct{ backend *notifyruntime.Backend }

func (s agentNotifyStatus) Status(ctx context.Context) (notifymcp.Status, error) {
	return agentNotifyStatusValue(s.backend.Status(ctx)), nil
}
func agentNotifyStatusValue(s notifyruntime.Status) notifymcp.Status {
	return notifymcp.Status{Enabled: s.ExplicitIntent && s.DesktopEnabled, Configuration: s.Configuration, Capability: s.OfflineCapability}
}

// Only main supplies production defaults. Tests inject native effects into the
// real runtime constructor; there is no environment-selected alternate backend.
func agentNotifyMain(command string, args []string, options notifyruntime.Options) (code int) {
	// Explicit commands never fall into the legacy global panic logger. A
	// composition panic is uncertain, so use the non-retryable exit category.
	defer func() {
		if recover() != nil {
			code = notifycli.ExitUnknown
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return agentNotifyExecute(ctx, command, args, options)
}
func agentNotifyExecute(ctx context.Context, command string, args []string, options notifyruntime.Options) int {
	integration, help, valid := agentNotifyFlags(command, args)
	// Release flag leases only after every adapter, watcher and stream is closed.
	// Reverse setup order also handles stdout/stderr sharing an open description.
	var streams []*agentNotifyStream
	defer func() {
		for _, s := range streams {
			_ = s.Close()
		}
		for i := len(streams) - 1; i >= 0; i-- {
			streams[i].release()
		}
	}()
	open := func(f *os.File) (*agentNotifyStream, error) {
		s, err := agentNotifyFile(f)
		if err == nil {
			streams = append(streams, s)
		}
		return s, err
	}
	// Even help and diagnostics use bounded OS output; neither touches input.
	diagnostic, err := open(os.Stderr)
	if err != nil {
		return notifycli.ExitInvalid
	}
	defer diagnostic.Close()
	fail := func(reason string) int { _, _ = diagnostic.Write([]byte(reason + "\n")); return notifycli.ExitInvalid }
	if !valid {
		return fail("invalid_flags")
	}
	output, err := open(os.Stdout)
	if err != nil {
		return fail("stdio_unavailable")
	}
	defer output.Close()
	if help {
		text := agentNotifyHelp
		if command == "mcp-server" {
			text = agentMCPHelp
		}
		if _, err = io.WriteString(output, text); err != nil {
			return fail("output_failed")
		}
		return 0
	}
	backend, err := notifyruntime.New(options)
	if err != nil {
		return fail("runtime_unavailable")
	}
	// Close only after adapter work and its input/output workers have joined.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = backend.Close(closeCtx)
	}()
	if backend.Clock().Now().BootID == "" {
		return fail("unsupported_platform")
	}
	input, err := open(os.Stdin)
	if err != nil {
		return fail("stdio_unavailable")
	}
	owned := &agentNotifyIO{input: input, output: output}
	defer owned.Close()
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			_ = owned.Close()
			_ = diagnostic.Close()
		case <-done:
		}
	}()
	defer func() { close(done); <-joined }()
	if command == "notify" {
		return notifycli.Run(ctx, args, input, output, diagnostic, notifycli.Options{Backend: backend, Clock: backend.Clock()})
	}
	err = notifymcp.Run(ctx, owned, notifymcp.Options{Backend: backend, Clock: backend.Clock(), Status: agentNotifyStatus{backend}, AdapterKind: integration})
	if err != nil {
		return fail("connection_closed")
	}
	return 0
}

type agentNotifyIO struct {
	input, output *agentNotifyStream
	once          sync.Once
}

func (s *agentNotifyIO) Read(p []byte) (int, error)  { return s.input.Read(p) }
func (s *agentNotifyIO) Write(p []byte) (int, error) { return s.output.Write(p) }
func (s *agentNotifyIO) Close() error {
	s.once.Do(func() { _ = s.input.Close(); _ = s.output.Close() })
	return nil
}

// Pipes retain runtime-poller cancellation and write deadlines. Local regular
// files and /dev/null use synchronous OS I/O: a hung filesystem syscall cannot
// be given a hard shutdown bound. No arbitrary nonpollable devices are accepted.
type agentNotifyStream struct {
	file     *os.File
	pollable bool
	release  func()
	mu       sync.Mutex
	closed   bool
	work     sync.WaitGroup
	once     sync.Once
	closeErr error
}

func (s *agentNotifyStream) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.work.Add(1)
	return true
}

func (s *agentNotifyStream) Read(p []byte) (int, error) {
	if !s.begin() {
		return 0, os.ErrClosed
	}
	defer s.work.Done()
	return s.file.Read(p)
}
func (s *agentNotifyStream) Write(p []byte) (int, error) {
	if !s.begin() {
		return 0, os.ErrClosed
	}
	defer s.work.Done()
	if s.pollable {
		if err := s.file.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			return 0, err
		}
	}
	return s.file.Write(p)
}
func (s *agentNotifyStream) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.closeErr = s.file.Close()
		// Nonpollable File.Close need not join an outstanding OS syscall.
		// Never return or restore shared flags while owned I/O is still active.
		s.work.Wait()
	})
	return s.closeErr
}
