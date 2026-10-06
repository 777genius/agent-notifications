//go:build linux

package linuxcallback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/godbus/dbus/v5"
)

const (
	frontend      = "org.freedesktop.portal.Desktop"
	backend       = "org.freedesktop.impl.portal.desktop.gtk"
	notifications = "org.freedesktop.Notifications"
	portalPath    = dbus.ObjectPath("/org/freedesktop/portal/desktop")
)

type Owners struct{ BusGUID, GTK, Frontend, Notifications string }
type Record struct {
	Version                                       int
	Key, InstallationID, SnapshotSHA256, ThreadID string
	Owners                                        Owners
}

func randomKey() (string, error) {
	var b [32]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func owners(ctx context.Context, c *dbus.Conn) (Owners, error) {
	var o Owners
	if e := c.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetId", 0).Store(&o.BusGUID); e != nil {
		return o, e
	}
	for name, target := range map[string]*string{frontend: &o.Frontend, backend: &o.GTK, notifications: &o.Notifications} {
		if e := c.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, name).Store(target); e != nil {
			return o, e
		}
		if !strings.HasPrefix(*target, ":") {
			return o, ErrUnavailable
		}
	}
	if e := qualifiedStack(ctx, c, o); e != nil {
		return o, e
	}
	return o, nil
}
func writeRecord(s Snapshot, r Record) error {
	if e := os.Mkdir(s.Records, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	if _, e := privateInfo(s.Records, true); e != nil {
		return e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(s.Records, r.Key+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	d, e := os.Open(s.Records)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

// Portal is opened only for an explicitly selected navigation target. Ordinary
// no-navigation delivery retains its existing Freedesktop channel.
type Portal struct {
	conn     *dbus.Conn
	snapshot Snapshot
	binding  notification.LinuxBinding
}

func Open(ctx context.Context, binding notification.LinuxBinding) (*Portal, error) {
	s, e := Load(binding)
	if e != nil {
		return nil, e
	}
	if e = CheckInstallation(binding, s); e != nil {
		return nil, e
	}
	if e = CheckSelectedContext(ctx, s); e != nil {
		return nil, e
	}
	c, e := connect(ctx)
	if e != nil {
		return nil, e
	}
	if ctx.Err() != nil {
		c.Close()
		return nil, ctx.Err()
	}
	return &Portal{c, s, binding}, nil
}
func (p *Portal) Close() error { return p.conn.Close() }
func (p *Portal) Ready(ctx context.Context) error {
	_, e := owners(ctx, p.conn)
	if e != nil {
		return e
	}
	var xml string
	if e = p.conn.Object(frontend, portalPath).CallWithContext(ctx, "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); e != nil {
		return e
	}
	if !strings.Contains(xml, "org.freedesktop.host.portal.Registry") {
		return ErrUnavailable
	}
	return nil
}
func (p *Portal) Submit(ctx context.Context, r notification.Request) (uint32, error) {
	if r.Target.Provider != "codex" || r.Target.Linux != p.binding {
		return 0, ErrUnavailable
	}
	if _, e := ThreadURI(r.Target.ThreadID); e != nil {
		return 0, e
	}
	original, e := owners(ctx, p.conn)
	if e != nil {
		return 0, e
	}
	if e = p.conn.Object(original.Frontend, portalPath).CallWithContext(ctx, "org.freedesktop.host.portal.Registry.Register", 0, p.snapshot.ApplicationID, map[string]dbus.Variant{}).Err; e != nil {
		return 0, e
	}
	key, e := randomKey()
	if e != nil {
		return 0, e
	}
	record := Record{1, key, p.snapshot.InstallationID, p.binding.SHA256, r.Target.ThreadID, original}
	if e = writeRecord(p.snapshot, record); e != nil {
		return 0, e
	}
	current, e := owners(ctx, p.conn)
	if e != nil || current != original {
		return 0, ErrUnavailable
	}
	content := map[string]dbus.Variant{"title": dbus.MakeVariant(r.Content.Title), "body": dbus.MakeVariant(r.Content.Body), "default-action": dbus.MakeVariant("app.open"), "default-action-target": dbus.MakeVariant(key)}
	if r.Silent {
		content["priority"] = dbus.MakeVariant("low")
	}
	// The opaque portal ID is our key, not an inferred frontend/server Notify ID.
	e = p.conn.Object(original.Frontend, portalPath).CallWithContext(ctx, "org.freedesktop.portal.Notification.AddNotification", 0, key, content).Err
	if e != nil {
		return 0, e
	}
	current, e = owners(ctx, p.conn)
	if e != nil || current != original {
		return 0, ErrUnavailable
	}
	return 0, nil
}

type Clock interface {
	Now() (string, float64, error)
}
type continuousClock struct{}

func (continuousClock) Now() (string, float64, error) {
	b, s, n, ok := journal.LinuxBootSample(journal.TrustedBootIDPath)
	if !ok {
		return "", 0, ErrUnavailable
	}
	return b, float64(s) + float64(n)/1e9, nil
}

// Handler's injected effects are narrow callback contracts. A single method
// invocation creates one fresh click budget and invokes the opener at most once.
type Handler struct {
	Snapshot   Snapshot
	Binding    notification.LinuxBinding
	Clock      Clock
	ReadOwners func(context.Context) (Owners, error)
	Verify     func(context.Context, Snapshot) error
	Launch     func(context.Context, Snapshot, string, string) error
}

func (h *Handler) Handle(ctx context.Context, sender, key, token string) error {
	if !validKey(key) || token == "" || len(token) > 4096 || strings.ContainsAny(token, "\x00\r\n") || h.Clock == nil || h.ReadOwners == nil || h.Verify == nil || h.Launch == nil {
		return ErrUnavailable
	}
	boot, start, e := h.Clock.Now()
	if e != nil || boot == "" || start < 0 || math.IsNaN(start) || math.IsInf(start, 0) {
		return ErrUnavailable
	}
	operation, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	live := func() bool {
		b, n, e := h.Clock.Now()
		return e == nil && b == boot && n >= start && n-start < 3 && operation.Err() == nil
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-operation.Done():
				return
			case <-ticker.C:
				if !live() {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-stopped }()
	b, e := ReadOwned(filepath.Join(h.Snapshot.Records, key+".json"), 16384)
	if e != nil {
		return ErrUnavailable
	}
	var r Record
	if decode(b, &r) != nil || r.Version != 1 || r.Key != key || r.InstallationID != h.Snapshot.InstallationID || r.SnapshotSHA256 != h.Binding.SHA256 || sender != r.Owners.GTK || !strings.HasPrefix(sender, ":") {
		return ErrUnavailable
	}
	current, e := h.ReadOwners(operation)
	if e != nil || current != r.Owners || !live() {
		return ErrUnavailable
	}
	uri, e := ThreadURI(r.ThreadID)
	if e != nil {
		return e
	}
	if e = h.Verify(operation, h.Snapshot); e != nil || !live() {
		return ErrUnavailable
	}
	current, e = h.ReadOwners(operation)
	if e != nil || current != r.Owners || !live() {
		return ErrUnavailable
	}
	// No close/remove, no retry and no alternate opener after a possible effect.
	return h.Launch(operation, h.Snapshot, uri, token)
}
func launch(ctx context.Context, s Snapshot, uri, token string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	cmd := exec.Command(s.Launcher, uri)
	allowed := map[string]bool{"DISPLAY": true, "WAYLAND_DISPLAY": true, "XDG_RUNTIME_DIR": true, "DBUS_SESSION_BUS_ADDRESS": true, "HOME": true, "USER": true, "LOGNAME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XAUTHORITY": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true}
	for _, v := range os.Environ() {
		name, _, ok := strings.Cut(v, "=")
		if ok && allowed[name] && !strings.ContainsAny(v, "\x00\r\n") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "PATH=/usr/bin:/bin")
	cmd.Dir = filepath.Dir(s.Executable)
	cmd.Env = append(cmd.Env, "XDG_ACTIVATION_TOKEN="+token, "DESKTOP_STARTUP_ID="+token)
	if e := cmd.Start(); e != nil {
		return e
	}
	// Reap without killing the selected client when the callback budget finishes.
	go func() { _ = cmd.Wait() }()
	return nil
}

type application struct {
	handler  *Handler
	lifetime context.Context
	mu       sync.Mutex
	closed   bool
}

// dbus.Sender is injected from HeaderFieldSender by godbus and is absent from
// the wire signature. Platform data and action arguments cannot forge it.
func (a *application) ActivateAction(sender dbus.Sender, name string, parameters []dbus.Variant, data map[string]dbus.Variant) *dbus.Error {
	if a.lifetime == nil || a.lifetime.Err() != nil || name != "open" || len(parameters) != 1 || len(data) > 3 {
		return dbus.MakeFailedError(ErrUnavailable)
	}
	for k, v := range data {
		if k != "activation-token" && k != "desktop-startup-id" {
			return dbus.MakeFailedError(ErrUnavailable)
		}
		s, ok := v.Value().(string)
		if !ok || len(s) > 4096 || strings.ContainsAny(s, "\x00\r\n") {
			return dbus.MakeFailedError(ErrUnavailable)
		}
	}
	key, ok := parameters[0].Value().(string)
	if !ok {
		return dbus.MakeFailedError(ErrUnavailable)
	}
	token, ok := data["activation-token"].Value().(string)
	if !ok {
		return dbus.MakeFailedError(ErrUnavailable)
	}
	// No queued callback can extend its budget while another attempt is active.
	if !a.mu.TryLock() {
		return dbus.MakeFailedError(errors.New("callback_busy"))
	}
	defer a.mu.Unlock()
	if a.closed || a.lifetime.Err() != nil {
		return dbus.MakeFailedError(ErrUnavailable)
	}
	if e := a.handler.Handle(a.lifetime, string(sender), key, token); e != nil {
		return dbus.MakeFailedError(e)
	}
	return nil
}
func Serve(ctx context.Context, binding notification.LinuxBinding) error {
	s, e := Load(binding)
	if e != nil {
		return e
	}
	c, e := connect(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	a := &application{lifetime: ctx, handler: &Handler{Snapshot: s, Binding: binding, Clock: continuousClock{}, ReadOwners: func(ctx context.Context) (Owners, error) { return owners(ctx, c) }, Verify: func(ctx context.Context, s Snapshot) error {
		if e := CheckInstallation(binding, s); e != nil {
			return e
		}
		return CheckSelectedContext(ctx, s)
	}, Launch: launch}}
	path := dbus.ObjectPath("/" + strings.ReplaceAll(s.ApplicationID, ".", "/"))
	if e = c.Export(a, path, "org.freedesktop.Application"); e != nil {
		return e
	}
	reply, e := c.RequestName(s.ApplicationID, dbus.NameFlagDoNotQueue)
	if e != nil {
		return e
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return ErrUnavailable
	}
	// Bounded cold service, independent of sender lifetime; future activations
	// are handled by the transaction-installed D-Bus service reader again.
	<-ctx.Done()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return nil
}
