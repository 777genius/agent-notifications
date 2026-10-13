//go:build linux

package linuxcallback

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// Red if a hung bus authentication outlives its callback/submit lifetime or a
// cancellation path leaves a detached waiter. The fixture is a fresh TEST socket.
func TestHungBusAuthenticationIsCanceledAndPeerCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TEST-bus")
	listener, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = listener.Close() }() // Best-effort private TEST fixture cleanup.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+path)
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := listener.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	conn, e := connect(ctx)
	if conn != nil || e == nil || time.Since(start) > time.Second {
		t.Fatal("unbounded authentication", conn, e, time.Since(start))
	}
	select {
	case peer := <-accepted:
		defer func() { _ = peer.Close() }() // Best-effort private TEST fixture cleanup.
		_ = peer.SetReadDeadline(time.Now().Add(time.Second))
		b := make([]byte, 4096)
		for {
			_, e = peer.Read(b)
			if e != nil {
				if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
					t.Fatal("canceled client transport remained open")
				}
				break
			}
		}
	case <-time.After(time.Second):
		t.Fatal("TEST socket not accepted")
	}
}
func TestNoImplicitOrNonUnixBusSelection(t *testing.T) {
	for _, address := range []string{"", "autolaunch:", "tcp:host=127.0.0.1,port=12345", "unix:path=/abs;unix:path=/other"} {
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
		c, e := connect(context.Background())
		if e == nil || c != nil {
			t.Fatal("unsupported bus selected", address)
		}
	}
}
