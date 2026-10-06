package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/cursorevent"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/cursorsource"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

func parseCursorEventArgs(argv []string) (string, bool) {
	if len(argv) != 3 || argv[0] != "stop" || argv[1] != "--binding" {
		return "", false
	}
	selector := argv[2]
	return selector, localArgText(selector) && len(selector) <= 4096 && filepath.IsAbs(selector) && filepath.Clean(selector) == selector
}

// Only the transport emits the public observer's {} plus LF. Admission is
// captured before stdin, SDK decoding or installed-state observation; its exact
// same-boot deadline reaches Consume unchanged. No event field grants authority.
func runCursorEvent(argv []string, input io.ReadCloser, output io.Writer) (code int) {
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
	selector, ok := parseCursorEventArgs(argv)
	if !ok {
		return 0
	}
	owned, ok := prepareLocalInput(input)
	if !ok {
		return 0
	}
	defer func() { _ = owned.Close() }()
	// Cursor and Local both alias the pinned public SDK MaxPayloadBytes. Keep
	// Cursor's explicit check as well if the shared reader's limit ever changes.
	payload, ok := readLocalPayload(ctx, owned)
	if !ok || len(payload) > cursorsource.MaxPayloadBytes {
		return 0
	}
	facts, err := cursorsource.Decode(ctx, cursorsource.Selector, payload)
	if err != nil {
		return 0
	}
	b, err := portable.ReadCursorBinding(selector)
	if err != nil {
		return 0
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || b.CheckSnapshot(snapshot) != nil {
		return 0
	}
	// Snapshot scratch is disposable, private and explicitly supplied to the
	// public verifier. It is never a profile/source authority or retained asset.
	temp, err := os.MkdirTemp("", "agent-notifications-cursor-snapshot-")
	if err != nil {
		return 0
	}
	defer func() { _ = os.RemoveAll(temp) }()
	temp, err = filepath.Abs(temp)
	if err != nil {
		return 0
	}
	cfg, fixed, err := cursorInstalledInputs(ctx, b, snapshot, temp)
	if err != nil {
		return 0
	}
	gate, err := copilotvscodeinstall.NewCursorGate(b, cfg, fixed)
	if err != nil {
		return 0
	}
	binding, err := gate.ConsumerBinding(ctx)
	if err != nil {
		return 0
	}
	effective, err := gate.EffectiveConfig(ctx)
	if err != nil {
		return 0
	}
	consumer := cursorevent.Consumer{Binding: binding, Gate: gate, Config: effective, Clock: clock,
		Cache:   &observation.RecentCache{Root: b.DataRoot, Clock: clock},
		Desktop: copilotvscodeinstall.NewCursorDesktop(gate, binding, newCursorDesktopPort()),
		SendWebhook: copilotvscodeinstall.NewCursorWebhookSender(gate, binding, func(ctx context.Context, cfg *config.Config, message webhook.SendContext) error {
			sender := webhook.NewWithContext(ctx, cfg)
			defer func() { _ = sender.Shutdown(50 * time.Millisecond) }()
			return sender.SendWithContext(message)
		}),
	}
	_ = consumer.Consume(ctx, facts, deadline)
	return 0
}

// The locator selects only the current wizard layout. Store.Load validates
// persisted public linkage; these checks select the exact recorded Cursor facts
// for the fixed adapter. NewCursorGate owns repeated physical/receipt/projection
// proof, effective consent and retained effect leases, including drift refusal.
func cursorInstalledInputs(ctx context.Context, b portable.Binding, snapshot installruntime.InstalledSnapshot, temp string) (uapinstaller.Config, cursorinstall.Authority, error) {
	root := filepath.Join(filepath.Dir(b.ControlRoot), "uap")
	cfg := uapinstaller.Config{StateRoot: filepath.Join(root, "state"), StateFile: filepath.Join(root, "state", "state-v2.json"),
		LockFile: filepath.Join(root, "state", "mutation.lock"), OperationsDir: filepath.Join(root, "state", "operations"),
		PluginDataBase: filepath.Join(root, "plugin-data"), ManagedRoot: filepath.Join(root, "managed"), TempRoot: temp}
	deny := func() (uapinstaller.Config, cursorinstall.Authority, error) {
		return cfg, cursorinstall.Authority{}, portable.ErrInvalid
	}
	if ctx.Err() != nil || b.Integration != portable.Cursor || b.CheckSnapshot(snapshot) != nil {
		return deny()
	}
	state, err := (statev2.Store{Path: cfg.StateFile}).Load()
	if err != nil {
		return cfg, cursorinstall.Authority{}, err
	}
	var selected *domain.ClientBinding
	for _, installation := range state.Installations {
		if installation.InstallationID != b.InstallationID {
			continue
		}
		c, ok := installation.Clients[b.BindingID]
		if selected != nil || !ok || installation.NeedsRebind || c.ClientBindingID != b.BindingID ||
			c.ClientID != string(domain.ClientCursor) || c.Scope != b.ScopeID || c.Scope != string(domain.ScopeUser) ||
			c.ClientBindingID != domain.ComputeClientBindingID(b.InstallationID, c.ClientID, c.Scope, c.TargetLocator) ||
			c.NativeProfileRoot != b.ScopeRoot || c.ProfileNamespace != cfg.StateRoot || c.ProfileAuthority == nil ||
			c.ProfileAuthority.IsZero() || c.ProfileAuthority.Facts().CanonicalRoot != b.ScopeRoot ||
			c.SelectedDelivery.Validate() != nil || c.SelectedDelivery.ValidateClient(domain.ClientCursor) != nil ||
			c.SelectedDelivery.ValidateCursorObjects(c.NativeObjects) != nil {
			return deny()
		}
		data, ok := installation.DataReceipts[c.DataReceiptID]
		if !ok || data.Locator != b.DataRoot {
			return deny()
		}
		selected = &c
	}
	if selected == nil {
		return deny()
	}
	facts, ok := selected.SelectedDelivery.CursorFacts()
	executable, err := portable.ResolvePrimaryExecutable(snapshot.Ledger, b.Primary)
	name, nameErr := b.Filename()
	if !ok || err != nil || nameErr != nil || facts.ProfileRoot != b.ScopeRoot ||
		facts.Selector != filepath.Join(b.DataRoot, name) || facts.Executable != executable || ctx.Err() != nil {
		return deny()
	}
	return cfg, cursorinstall.Authority{ProfileRoot: facts.ProfileRoot, CursorVersion: facts.CursorVersion,
		QualificationID: facts.QualificationID, Executable: executable,
		ExecutableDigest: "sha256:" + snapshot.Ledger.Files[executable].SHA256, Selector: facts.Selector, ObjectID: facts.ObjectID}, nil
}
