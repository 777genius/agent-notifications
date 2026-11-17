package sharedrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/mcp"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

type backendFunc func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt

func (f backendFunc) Notify(c context.Context, p agentnotify.Payload, o origin.Context, d notification.Deadline) agentnotify.Receipt {
	return f(c, p, o, d)
}

type statusFunc func(context.Context, origin.Context) (mcp.Status, error)

func (f statusFunc) Status(c context.Context, o origin.Context) (mcp.Status, error) { return f(c, o) }

func testOrigin(session string) origin.Context {
	return origin.Context{Provider: "codex", Namespace: "mcp", SessionID: session, AnonymousCaller: "shared_mcp", Provenance: origin.ClientMetadata, Locality: origin.LocalityUnknown, Interface: origin.InterfaceUnknown}
}

func testPayload() agentnotify.Payload {
	return agentnotify.Payload{Title: "Test", Body: "Private notification body", Category: "attention", Navigation: notification.Required}
}

func testDeadline() notification.Deadline {
	return notification.Deadline{BootID: "test-boot", NotAfter: 115}
}

func testOptions(b mcp.Backend) ServerOptions {
	return ServerOptions{Backend: b, Status: statusFunc(func(context.Context, origin.Context) (mcp.Status, error) {
		return mcp.Status{Enabled: true, Configuration: "configured", Capability: "available", Navigation: agentnotify.NavigationStatus{Capability: "unavailable", Precision: "none", Reason: "test"}}, nil
	}), Clock: agentnotify.ClockFunc(func() notification.Deadline { return notification.Deadline{BootID: "test-boot", NotAfter: 100} }), Identity: "codex:test-profile:sha256", Token: bytes.Repeat([]byte{37}, 32), AdapterKind: "codex"}
}

func startServer(t *testing.T, o ServerOptions) (Client, context.CancelFunc, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, o) }()
	t.Cleanup(func() { cancel(); _ = listener.Close() })
	c := Client{Dial: func(ctx context.Context) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", listener.Addr().String())
		if err != nil {
			return nil, err
		}
		secured, authErr := AuthenticateClient(ctx, conn, o.Identity, o.Token)
		if authErr != nil {
			_ = conn.Close()
			return nil, authErr
		}
		return secured, nil
	}}
	return c, cancel, done
}

func awaitServer(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("server did not join")
		return nil
	}
}

// Fails if concurrent bridges share mutable origin/deadline state or serialize
// all requests behind a connection. The barrier requires 64 calls in flight.
func TestConcurrentCallerIsolationAndOriginalDeadline(t *testing.T) {
	const n = 64
	var entered atomic.Int32
	barrier := make(chan struct{})
	b := backendFunc(func(ctx context.Context, p agentnotify.Payload, o origin.Context, d notification.Deadline) agentnotify.Receipt {
		if entered.Add(1) == n {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-ctx.Done():
			return uncertain("canceled")
		}
		if !reflect.DeepEqual(d, testDeadline()) || p.Body != o.SessionID || o.Namespace != "mcp" || o.CallID != "call-"+o.SessionID || o.Provenance != origin.ClientMetadata {
			return rejected("context_changed")
		}
		return agentnotify.Receipt{Status: "submitted", TrackingID: o.SessionID}
	})
	c, cancel, done := startServer(t, testOptions(b))
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := fmt.Sprintf("session-%d", i)
			o := testOrigin(session)
			o.CallID = "call-" + session
			p := testPayload()
			p.Body = session
			r := c.Notify(ctx, p, o, testDeadline())
			if r.Status != "submitted" || r.TrackingID != session {
				t.Errorf("caller %s: %+v", session, r)
			}
		}()
	}
	wg.Wait()
	cancel()
	if err := awaitServer(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestProviderAndDeadlineConfinement(t *testing.T) {
	var calls atomic.Int32
	o := testOptions(backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		calls.Add(1)
		return agentnotify.Receipt{Status: "submitted"}
	}))
	c, cancel, done := startServer(t, o)
	for _, tc := range []struct {
		name string
		o    origin.Context
		d    notification.Deadline
	}{
		{"wrong_provider", origin.Context{Provider: "claude"}, testDeadline()},
		{"reboot", testOrigin("session"), notification.Deadline{BootID: "other-boot", NotAfter: 115}},
		{"extended", testOrigin("session"), notification.Deadline{BootID: "test-boot", NotAfter: 115.01}},
		{"expired", testOrigin("session"), notification.Deadline{BootID: "test-boot", NotAfter: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Notify(context.Background(), testPayload(), tc.o, tc.d)
			if r.Status != "rejected" || !r.RetrySafe {
				t.Fatalf("invalid call admitted: %+v", r)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("backend called %d times", calls.Load())
	}
	cancel()
	if err := awaitServer(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
}

// Fails if a fake daemon at a reused port sees any notification bytes before
// proving possession of the private token; the token itself must never travel.
func TestFakeServerReceivesNoPayloadOrToken(t *testing.T) {
	var attempts atomic.Int32
	captured := make(chan []byte, 1)
	secret := bytes.Repeat([]byte{133}, 32)
	c := Client{Dial: func(ctx context.Context) (net.Conn, error) {
		attempts.Add(1)
		client, fake := net.Pipe()
		go func() {
			defer func() { _ = fake.Close() }()
			hello := make([]byte, 4096)
			n, _ := fake.Read(hello)
			hello = hello[:n]
			_ = writeAll(fake, []byte("invalid TLS handshake"))
			more, _ := io.ReadAll(fake)
			captured <- append(hello, more...)
		}()
		secured, err := AuthenticateClient(ctx, client, "bound-runtime", secret)
		if err != nil {
			_ = client.Close()
			return nil, err
		}
		return secured, nil
	}}
	r := c.Notify(context.Background(), testPayload(), testOrigin("private-thread"), testDeadline())
	if r.Status != "rejected" || !r.RetrySafe || attempts.Load() != 1 {
		t.Fatalf("auth failure: %+v attempts=%d", r, attempts.Load())
	}
	select {
	case data := <-captured:
		if len(data) == 0 || bytes.Contains(data, secret) || bytes.Contains(data, []byte("Private")) || bytes.Contains(data, []byte("private-thread")) {
			t.Fatal("secret or RPC content reached fake server")
		}
	case <-time.After(time.Second):
		t.Fatal("fake server did not finish")
	}
}

func TestWrongAuthenticationNeverDispatches(t *testing.T) {
	var calls atomic.Int32
	o := testOptions(backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		calls.Add(1)
		return agentnotify.Receipt{Status: "submitted"}
	}))
	for _, wrongIdentity := range []bool{false, true} {
		t.Run(fmt.Sprint(wrongIdentity), func(t *testing.T) {
			client, server := net.Pipe()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() { _ = server.Close() }()
				serveConnection(context.Background(), server, o)
			}()
			identity, token := o.Identity, append([]byte(nil), o.Token...)
			if wrongIdentity {
				identity = "other-profile"
			} else {
				token[0] ^= 1
			}
			if _, err := AuthenticateClient(context.Background(), client, identity, token); err == nil || err.Error() != "shared_authentication_failed" {
				t.Fatalf("wrong auth result: %v", err)
			}
			_ = client.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("server authentication did not finish")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated backend call")
	}
}

// A lost reply after successful submission must not cause an automatic resend
// or tell the caller a retry is safe, regardless of transport error text.
func TestDroppedReplyIsUnknownAndNeverRetried(t *testing.T) {
	var dials, effects atomic.Int32
	c := Client{Dial: func(context.Context) (net.Conn, error) {
		dials.Add(1)
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			var req request
			if readFrame(server, &req) == nil {
				effects.Add(1)
			}
		}()
		return client, nil // authenticated Dial contract exercised separately
	}}
	r := c.Notify(context.Background(), testPayload(), testOrigin("session"), testDeadline())
	if r.Status != "unknown" || r.RetrySafe || dials.Load() != 1 || effects.Load() != 1 {
		t.Fatalf("lost reply: %+v dials=%d effects=%d", r, dials.Load(), effects.Load())
	}
}

func TestStrictFramesAndNoUntrustedControls(t *testing.T) {
	var calls atomic.Int32
	o := testOptions(backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		calls.Add(1)
		return agentnotify.Receipt{Status: "submitted"}
	}))
	base := `{"version":1,"method":"status","origin":{"Provider":"codex","Namespace":"mcp","SessionID":"s","Provenance":"client_metadata","Locality":"unknown","Interface":"unknown"}}`
	for _, body := range []string{
		strings.TrimSuffix(base, "}") + `,"path":"/private/profile"}`,
		strings.Replace(base, `"method":"status"`, `"method":"status","method":"notify"`, 1),
		strings.Replace(base, `"Provider":"codex"`, `"Provider":"codex","Target":"forbidden"`, 1),
		strings.Replace(base, `"SessionID":"s"`, `"SessionID":"\ud800"`, 1),
		`{"version":1,"method":"shutdown"}`,
	} {
		client, server := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { _ = server.Close() }()
			serveConnection(context.Background(), server, o)
		}()
		secured, err := AuthenticateClient(context.Background(), client, o.Identity, o.Token)
		if err != nil {
			t.Fatal(err)
		}
		client = secured
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(body)))
		_ = writeAll(client, append(size[:], body...))
		var r response
		err = readFrame(client, &r)
		if err == nil && r.Error != "invalid_request" {
			t.Errorf("invalid request accepted: %s %+v", body, r)
		}
		_ = client.Close()
		<-done
	}
	if calls.Load() != 0 {
		t.Fatal("untrusted control reached backend")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], maxFrame+1)
	if readFrame(bytes.NewReader(size[:]), new(request)) == nil {
		t.Fatal("oversize allocation accepted")
	}
}

func TestLifecycleIdleDoesNotStopActiveCallAndShutdownJoins(t *testing.T) {
	entered := make(chan struct{})
	joined := make(chan struct{})
	o := testOptions(backendFunc(func(ctx context.Context, _ agentnotify.Payload, _ origin.Context, _ notification.Deadline) agentnotify.Receipt {
		close(entered)
		<-ctx.Done()
		close(joined)
		return uncertain("canceled")
	}))
	o.IdleTimeout = 30 * time.Millisecond
	c, cancel, done := startServer(t, o)
	callDone := make(chan agentnotify.Receipt, 1)
	go func() {
		callDone <- c.Notify(context.Background(), testPayload(), testOrigin("session"), testDeadline())
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not enter")
	}
	select {
	case err := <-done:
		t.Fatalf("idle shutdown with active call: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if err := awaitServer(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("Serve returned before backend joined")
	}
	select {
	case r := <-callDone:
		if r.Status != "unknown" || r.RetrySafe {
			t.Fatalf("canceled active call: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not unblock")
	}
}

func TestIdleExitAndStatusOrigin(t *testing.T) {
	o := testOptions(backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		t.Error("status sent a notification")
		return uncertain("unexpected")
	}))
	o.IdleTimeout = 40 * time.Millisecond
	o.Status = statusFunc(func(_ context.Context, got origin.Context) (mcp.Status, error) {
		if !reflect.DeepEqual(got, testOrigin("status-session")) {
			t.Error("status origin changed")
		}
		return mcp.Status{Configuration: "configured", Capability: "available", Navigation: agentnotify.NavigationStatus{Reason: "test"}}, nil
	})
	c, _, done := startServer(t, o)
	if _, err := c.Status(context.Background(), testOrigin("status-session")); err != nil {
		t.Fatal(err)
	}
	if err := awaitServer(t, done); err != nil {
		t.Fatalf("idle exit: %v", err)
	}
}

func TestBridgeCancellationCancelsBackendWithoutStoppingService(t *testing.T) {
	entered := make(chan struct{})
	joined := make(chan struct{})
	o := testOptions(backendFunc(func(ctx context.Context, _ agentnotify.Payload, _ origin.Context, _ notification.Deadline) agentnotify.Receipt {
		close(entered)
		<-ctx.Done()
		close(joined)
		return uncertain("canceled")
	}))
	c, cancelServer, done := startServer(t, o)
	ctx, cancelCall := context.WithCancel(context.Background())
	callDone := make(chan agentnotify.Receipt, 1)
	go func() { callDone <- c.Notify(ctx, testPayload(), testOrigin("s"), testDeadline()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("backend did not enter")
	}
	cancelCall()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("bridge disconnect did not cancel backend")
	}
	select {
	case r := <-callDone:
		if r.Status != "unknown" || r.RetrySafe {
			t.Fatalf("canceled result: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge did not return")
	}
	if _, err := c.Status(context.Background(), testOrigin("still-running")); err != nil {
		t.Fatalf("bridge disconnect stopped service: %v", err)
	}
	cancelServer()
	if err := awaitServer(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
}

// Possession of the server role key must not authorize a client certificate.
func TestServerCertificateCannotReflectAsClient(t *testing.T) {
	o := testOptions(nil)
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	serverConfig, err := authenticationConfig(o.Identity, o.Token, false)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig, err := authenticationConfig(o.Identity, o.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig.Certificates = serverConfig.Certificates
	done := make(chan error, 1)
	go func() {
		defer func() { _ = server.Close() }()
		_, err := authenticateServer(context.Background(), server, serverConfig)
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tlsClient := tls.Client(client, clientConfig)
	_ = tlsClient.HandshakeContext(ctx)
	_ = client.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("server certificate accepted as client")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reflection handshake did not finish")
	}
}

func TestActiveConnectionBoundIncludesAuthenticatedIdleClients(t *testing.T) {
	o := testOptions(backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		t.Error("unexpected backend effect")
		return uncertain("unexpected")
	}))
	o.MaxActive = 1
	c, cancel, done := startServer(t, o)
	// Completing auth guarantees this first connection occupies a server slot.
	first, err := c.Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	r := c.Notify(context.Background(), testPayload(), testOrigin("overflow"), testDeadline())
	if r.Status != "rejected" || !r.RetrySafe {
		t.Fatalf("capacity refusal must precede RPC write: %+v", r)
	}
	cancel()
	if err := awaitServer(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
}

type capturedStream struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (c *capturedStream) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.Write(p)
}

func (c *capturedStream) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.b.Bytes()...)
}

// A malicious process can transparently relay a genuine handshake. It must
// still see only encrypted records, never the body, thread identity or token.
func TestActiveTCPRelayCannotReadNotification(t *testing.T) {
	var effects atomic.Int32
	o := testOptions(backendFunc(func(_ context.Context, p agentnotify.Payload, _ origin.Context, _ notification.Deadline) agentnotify.Receipt {
		effects.Add(1)
		if p.Body != "PRIVATE-RELAY-NOTIFICATION-body-0123456789" {
			return rejected("body_changed")
		}
		return agentnotify.Receipt{Status: "submitted"}
	}))
	daemon, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = daemon.Close() }()
	proxy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- Serve(ctx, daemon, o) }()
	var capture capturedStream
	proxyDone := make(chan struct{})
	go func() {
		defer close(proxyDone)
		front, err := proxy.Accept()
		if err != nil {
			return
		}
		defer func() { _ = front.Close() }()
		back, err := net.Dial("tcp4", daemon.Addr().String())
		if err != nil {
			return
		}
		defer func() { _ = back.Close() }()
		copied := make(chan struct{}, 1)
		go func() { _, _ = io.Copy(back, io.TeeReader(front, &capture)); _ = back.Close(); copied <- struct{}{} }()
		_, _ = io.Copy(front, io.TeeReader(back, &capture))
		_ = front.Close()
		<-copied
	}()
	c := Client{Dial: func(ctx context.Context) (net.Conn, error) {
		raw, err := (&net.Dialer{}).DialContext(ctx, "tcp4", proxy.Addr().String())
		if err != nil {
			return nil, err
		}
		secured, err := AuthenticateClient(ctx, raw, o.Identity, o.Token)
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
		return secured, nil
	}}
	p := testPayload()
	p.Body = "PRIVATE-RELAY-NOTIFICATION-body-0123456789"
	r := c.Notify(context.Background(), p, testOrigin("PRIVATE-RELAY-THREAD-0123456789"), testDeadline())
	if r.Status != "submitted" || effects.Load() != 1 {
		t.Fatalf("relayed effect: %+v count=%d", r, effects.Load())
	}
	select {
	case <-proxyDone:
	case <-time.After(time.Second):
		t.Fatal("relay did not finish")
	}
	data := capture.Bytes()
	if len(data) < 100 || bytes.Contains(data, []byte(p.Body)) || bytes.Contains(data, []byte("PRIVATE-RELAY-THREAD-0123456789")) || bytes.Contains(data, o.Token) || bytes.Contains(data, []byte(o.Identity)) {
		t.Fatal("active relay observed private plaintext")
	}
	cancel()
	if err := awaitServer(t, serverDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
}
