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
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// This is a private TEST bus, never the user's actual desktop session. Red if
// godbus stops injecting HeaderFieldSender or action data can forge authority.
func TestActivateActionAuthenticatesActualWireSender(t *testing.T) {
	if _, e := exec.LookPath("dbus-daemon"); e != nil {
		t.Skip("private test bus unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
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
	defer server.Close()
	trusted, e := dbus.ConnectSessionBus()
	if e != nil {
		t.Fatal(e)
	}
	defer trusted.Close()
	forged, e := dbus.ConnectSessionBus()
	if e != nil {
		t.Fatal(e)
	}
	defer forged.Close()
	h, key, _, effects := callbackFixture(t)
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
	a := &application{handler: h, lifetime: ctx}
	path := dbus.ObjectPath("/org/agentnotifications/TEST")
	if e = server.Export(a, path, "org.freedesktop.Application"); e != nil {
		t.Fatal(e)
	}
	call := func(c *dbus.Conn, parameters ...any) *dbus.Call {
		return c.Object(server.Names()[0], path).CallWithContext(ctx, "org.freedesktop.Application.ActivateAction", 0, parameters...)
	}
	data := map[string]dbus.Variant{"activation-token": dbus.MakeVariant("native-token")}
	if c := call(forged, "open", []dbus.Variant{dbus.MakeVariant(key)}, data); c.Err == nil || *effects != 0 {
		t.Fatal("forged wire sender launched", c.Err, *effects)
	}
	// Adding a caller-controlled Sender argument changes the wire signature,
	// rather than supplying godbus's actual injected Sender parameter.
	if c := call(forged, trusted.Names()[0], "open", []dbus.Variant{dbus.MakeVariant(key)}, data); c.Err == nil || *effects != 0 {
		t.Fatal("wire supplied sender accepted", c.Err, *effects)
	}
	if c := call(trusted, "open", []dbus.Variant{dbus.MakeVariant(key)}, data); c.Err != nil || *effects != 1 {
		t.Fatal("trusted actual sender rejected", c.Err, *effects)
	}
	cancel()
	if e := a.ActivateAction(dbus.Sender(trusted.Names()[0]), "open", []dbus.Variant{dbus.MakeVariant(key)}, data); e == nil || *effects != 1 {
		t.Fatal("shutdown admitted a late callback", e, *effects)
	}
}
