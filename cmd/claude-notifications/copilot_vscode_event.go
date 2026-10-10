package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	source "github.com/777genius/agent-notifications/internal/copilotvscodesource"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/webhook"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

type localEventArgs struct{ Event, ControlRoot, Binding string }

func parseLocalEventArgs(argv []string) (a localEventArgs, ok bool) {
	if len(argv) != 6 {
		return a, false
	}
	seen := map[string]bool{}
	for i := 0; i < len(argv); i += 2 {
		key, value := argv[i], argv[i+1]
		if seen[key] || !localArgText(value) {
			return a, false
		}
		seen[key] = true
		switch key {
		case "--event":
			a.Event = value
		case "--control-root":
			a.ControlRoot = value
		case "--binding":
			a.Binding = value
		default:
			return a, false
		}
	}
	return a, a.Event == source.Stop && filepath.IsAbs(a.ControlRoot) && filepath.Clean(a.ControlRoot) == a.ControlRoot && len(a.ControlRoot) <= 4096 && len(a.Binding) <= 128 && !strings.HasPrefix(a.Binding, "--")
}
func localArgText(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type localPayloadResult struct {
	data []byte
	err  error
}

// The process owns stdin. Cancellation closes the pipe and joins its reader,
// including panic recovery. The short input timeout only restricts admission.
func readLocalPayload(parent context.Context, input io.ReadCloser) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	results := make(chan localPayloadResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if recover() != nil {
				results <- localPayloadResult{err: io.ErrUnexpectedEOF}
			}
		}()
		data, err := io.ReadAll(io.LimitReader(input, source.MaxPayloadBytes+1))
		results <- localPayloadResult{data: data, err: err}
	}()
	defer func() { _ = input.Close(); <-done }()
	select {
	case r := <-results:
		return r.data, r.err == nil && ctx.Err() == nil && len(r.data) > 0 && len(r.data) <= source.MaxPayloadBytes
	case <-ctx.Done():
		return nil, false
	}
}

// Installed composition preserves the original pre-stdin deadline and typed
// Stop facts. Unknown physical capability denies without public diagnostics.
// Public7f3 Local observer encodes {}: this transport alone emits {} plus LF,
// including invalid input/panic. A failed write returns 1 (never blocking 2),
// honestly indicating neutral output could not be delivered. SDK IO is private.
func runCopilotVSCodeEvent(argv []string, input io.ReadCloser, output io.Writer) (code int) {
	signal.Ignore(syscall.SIGPIPE)
	defer func() {
		_ = recover()
		_ = input.Close()
		if _, err := io.WriteString(output, "{}\n"); err != nil {
			code = 1
		}
	}()
	clock := notifier.SystemBootClock{}
	ctx, deadline, cancel, err := observation.Admission(context.Background(), clock)
	if err != nil {
		return 0
	}
	defer cancel()
	a, ok := parseLocalEventArgs(argv)
	if !ok {
		return 0
	}
	owned, ok := prepareLocalInput(input)
	if !ok {
		return 0
	}
	defer func() { _ = owned.Close() }()
	payload, ok := readLocalPayload(ctx, owned)
	if !ok {
		return 0
	}
	facts, err := source.Decode(ctx, a.Event, payload)
	if err != nil {
		return 0
	}
	b, err := portable.ReadLocalBinding(a.ControlRoot, a.Binding)
	if err != nil {
		return 0
	}
	temp, err := os.MkdirTemp("", "agent-notifications-local-snapshot-")
	if err != nil {
		return 0
	}
	defer func() { _ = os.RemoveAll(temp) }()
	temp, err = filepath.Abs(temp)
	if err != nil {
		return 0
	}
	root := filepath.Join(filepath.Dir(b.ControlRoot), "uap")
	cfg := uapinstaller.Config{StateRoot: filepath.Join(root, "state"), StateFile: filepath.Join(root, "state", "state-v2.json"),
		LockFile: filepath.Join(root, "state", "mutation.lock"), OperationsDir: filepath.Join(root, "state", "operations"),
		PluginDataBase: filepath.Join(root, "plugin-data"), ManagedRoot: filepath.Join(root, "managed"), TempRoot: temp}
	gate, _, effective, binding, err := copilotvscodeinstall.NewLocalGate(ctx, b, cfg)
	if err != nil {
		return 0
	}
	consumer := copilotvscodeevent.Consumer{Binding: binding, Gate: gate, Config: effective, Clock: clock,
		Cache:   &observation.RecentCache{Root: b.DataRoot, Clock: clock},
		Desktop: copilotvscodeinstall.NewDesktop(gate, binding, filepath.Join(b.ControlRoot, "copilot-vscode-native-spool"), nil),
		SendWebhook: copilotvscodeinstall.NewWebhookSender(gate, binding, func(ctx context.Context, cfg *config.Config, message webhook.SendContext) error {
			sender := webhook.NewWithContext(ctx, cfg)
			defer func() { _ = sender.Shutdown(50 * time.Millisecond) }()
			return sender.SendWithContext(message)
		}),
	}
	_ = consumer.Consume(ctx, facts, deadline)
	return 0
}
