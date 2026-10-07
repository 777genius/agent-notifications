//go:build linux

package linuxcallback

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// This is a private TEST bus, never the user's actual desktop session. Red if
// godbus stops injecting HeaderFieldSender or action data can forge authority.
func privateCallerFixture(t *testing.T, configure func(*Handler), startup ...<-chan struct{}) (context.Context, *dbus.Conn, *dbus.Conn, *application, string, dbus.ObjectPath) {
	t.Helper()
	if _, e := exec.LookPath("dbus-daemon"); e != nil {
		t.Skip("private test bus unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	bus := exec.CommandContext(ctx, "dbus-daemon", "--session", "--nofork", "--print-address=1")
	out, e := bus.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	bus.Stderr = io.Discard
	if e = bus.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); _ = bus.Wait() })
	address, e := bufio.NewReader(out).ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address[:len(address)-1])
	server, e := dbus.ConnectSessionBus()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = server.Close() }) // Best-effort private TEST fixture cleanup.
	trusted, e := dbus.ConnectSessionBus()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = trusted.Close() }) // Best-effort private TEST fixture cleanup.
	h, key, _, _ := callbackFixture(t)
	var r Record
	b, e := ReadOwned(filepath.Join(h.Snapshot.Records, key+".json"), 16384)
	if e != nil || decode(b, &r) != nil {
		t.Fatal(e)
	}
	r.Owners.GTK = trusted.Names()[0]
	file := filepath.Join(h.Snapshot.Records, key+".json")
	if e = os.Chmod(file, 0600); e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(r)
	if e = os.WriteFile(file, b, 0400); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(file, 0400); e != nil {
		t.Fatal(e)
	}
	h.ReadOwners = func(context.Context) (Owners, error) { return r.Owners, nil }
	configure(h)
	service, stop := context.WithCancel(ctx)
	a := &application{handler: h, lifetime: service, cancel: stop, ready: true}
	if len(startup) > 0 {
		a.startup = startup[0]
		a.ready = false
	}
	t.Cleanup(a.stop)
	path := dbus.ObjectPath("/org/agentnotifications/TEST")
	if e = server.Export(a, path, "org.freedesktop.Application"); e != nil {
		t.Fatal(e)
	}
	return ctx, server, trusted, a, key, path
}

func TestActivateActionAuthenticatesActualWireSender(t *testing.T) {
	var effects atomic.Int64
	ctx, server, trusted, a, key, path := privateCallerFixture(t, func(h *Handler) {
		h.Launch = func(context.Context, Snapshot, string, string) error { effects.Add(1); return nil }
	})
	forged, e := dbus.ConnectSessionBus()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = forged.Close() })
	call := func(c *dbus.Conn, parameters ...any) *dbus.Call {
		return c.Object(server.Names()[0], path).CallWithContext(ctx, "org.freedesktop.Application.ActivateAction", 0, parameters...)
	}
	data := map[string]dbus.Variant{"activation-token": dbus.MakeVariant("native-token")}
	if c := call(forged, "open", []dbus.Variant{dbus.MakeVariant(key)}, data); c.Err == nil || effects.Load() != 0 {
		t.Fatal("forged wire sender launched", c.Err, effects.Load())
	}
	// Adding a caller-controlled Sender argument changes the wire signature,
	// rather than supplying godbus's actual injected Sender parameter.
	if c := call(forged, trusted.Names()[0], "open", []dbus.Variant{dbus.MakeVariant(key)}, data); c.Err == nil || effects.Load() != 0 {
		t.Fatal("wire supplied sender accepted", c.Err, effects.Load())
	}
	if c := call(trusted, "open", []dbus.Variant{dbus.MakeVariant(key)}, data); c.Err != nil || effects.Load() != 1 {
		t.Fatal("trusted actual sender rejected", c.Err, effects.Load())
	}
	a.cancel()
	if e := a.ActivateAction(dbus.Sender(trusted.Names()[0]), "open", []dbus.Variant{dbus.MakeVariant(key)}, data); e == nil || effects.Load() != 1 {
		t.Fatal("shutdown admitted a late callback", e, effects.Load())
	}
}
