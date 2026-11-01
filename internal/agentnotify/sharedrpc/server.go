package sharedrpc

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/mcp"
	"github.com/777genius/agent-notifications/internal/notification"
)

type ServerOptions struct {
	Backend     mcp.Backend
	Status      mcp.StatusPort
	Clock       agentnotify.Clock
	Identity    string
	Token       []byte
	AdapterKind string
	// IdleTimeout defaults to ten minutes, and runs only without active calls.
	IdleTimeout time.Duration
	// MaxActive defaults to 256 and cannot exceed 256. Excess connections close.
	MaxActive int
	serverTLS *tls.Config
}

// Serve owns listener. It accepts only loopback TCP, authenticates before reading
// RPC data, and serves exactly one bounded request on each connection. On shutdown
// it closes admission, cancels active work, and joins every handler before return.
// Composition must close backend resources only after Serve returns. Ports must
// honor context cancellation; arbitrary Go functions cannot be forcibly stopped.
func Serve(ctx context.Context, listener net.Listener, o ServerOptions) error {
	if ctx == nil || listener == nil || !localTCP(listener.Addr()) || nilValue(o.Backend) || nilValue(o.Status) || nilValue(o.Clock) || !validCredentials(o.Identity, o.Token) || (o.AdapterKind != "codex" && o.AdapterKind != "claude") || o.IdleTimeout < 0 || o.MaxActive < 0 || o.MaxActive > 256 {
		return errors.New("invalid_shared_options")
	}
	if o.IdleTimeout == 0 {
		o.IdleTimeout = 10 * time.Minute
	}
	if o.MaxActive == 0 {
		o.MaxActive = 256
	}
	o.Token = append([]byte(nil), o.Token...)
	var err error
	o.serverTLS, err = authenticationConfig(o.Identity, o.Token, false)
	if err != nil {
		return errors.New("invalid_shared_options")
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer listener.Close()
	var mu sync.Mutex
	active := 0
	lastIdle := time.Now()
	stopping := false
	var handlers sync.WaitGroup
	changed := make(chan struct{}, 1)
	signal := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		timer := time.NewTimer(o.IdleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-serveCtx.Done():
				mu.Lock()
				stopping = true
				_ = listener.Close()
				mu.Unlock()
				return
			case <-changed:
			case <-timer.C:
			}
			mu.Lock()
			if active == 0 && time.Since(lastIdle) >= o.IdleTimeout {
				stopping = true
				_ = listener.Close()
				mu.Unlock()
				return
			}
			wait := o.IdleTimeout
			if active == 0 {
				wait -= time.Since(lastIdle)
			}
			mu.Unlock()
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(wait)
		}
	}()
	var result error
	for {
		conn, err := listener.Accept()
		if err != nil {
			mu.Lock()
			normal := stopping || ctx.Err() != nil
			mu.Unlock()
			if !normal {
				result = errors.New("shared_listener_closed")
			}
			break
		}
		mu.Lock()
		if stopping || active >= o.MaxActive || !localTCP(conn.RemoteAddr()) {
			mu.Unlock()
			_ = conn.Close()
			continue
		}
		active++
		handlers.Add(1)
		mu.Unlock()
		signal()
		go func() {
			defer handlers.Done()
			defer func() {
				_ = conn.Close()
				mu.Lock()
				active--
				if active == 0 {
					lastIdle = time.Now()
				}
				mu.Unlock()
				signal()
			}()
			serveConnection(serveCtx, conn, o)
		}()
	}
	cancel()
	<-watchDone
	handlers.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return result
}

func serveConnection(ctx context.Context, conn net.Conn, o ServerOptions) {
	config := o.serverTLS
	if config == nil {
		var err error
		config, err = authenticationConfig(o.Identity, o.Token, false)
		if err != nil {
			return
		}
	}
	secured, err := authenticateServer(ctx, conn, config)
	if err != nil {
		return
	}
	conn = secured
	cleanup, err := boundConnection(ctx, conn, 15*time.Second)
	if err != nil {
		return
	}
	defer cleanup()
	var req request
	if readFrame(conn, &req) != nil {
		return
	}
	// There is no second request on this connection. EOF (bridge cancellation)
	// or any extra input cancels the effect, including while the backend waits.
	callCtx, cancel := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		var extra [1]byte
		_, _ = conn.Read(extra[:])
		cancel()
	}()
	defer func() { cancel(); _ = conn.Close(); <-watchDone }()
	// Recover only at the effect boundary; a panic after dispatch cannot be
	// represented as a safe rejection, even if the backend already submitted.
	var reply response
	func() {
		defer func() {
			if recover() != nil {
				r := uncertain("internal_error")
				reply = response{Version: 1, Receipt: &r}
			}
		}()
		reply = dispatch(callCtx, req, o)
	}()
	frame, err := marshalFrame(reply)
	if err == nil {
		_ = writeAll(conn, frame)
	}
}

func dispatch(ctx context.Context, req request, o ServerOptions) response {
	refuse := func() response { return response{Version: 1, Error: "invalid_request"} }
	if req.Version != 1 || req.Origin == nil || req.Origin.Provider != o.AdapterKind {
		return refuse()
	}
	switch req.Method {
	case "notify":
		if req.Payload == nil || req.Deadline == nil {
			return refuse()
		}
		navigation := req.Payload.Navigation
		if navigation == "" {
			navigation = notification.Required
		}
		if req.Origin.Validate(navigation) != nil {
			return refuse()
		}
		seconds := remaining(o.Clock.Now(), *req.Deadline)
		if seconds == 0 || ctx.Err() != nil {
			r := rejected("deadline_expired")
			return response{Version: 1, Receipt: &r}
		}
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds*float64(time.Second)))
		defer cancel()
		r := o.Backend.Notify(callCtx, *req.Payload, *req.Origin, *req.Deadline)
		if !validReceipt(r) {
			r = uncertain("invalid_backend_result")
		}
		if r.Status == "unknown" {
			r.RetrySafe = false
		}
		return response{Version: 1, Receipt: &r}
	case "status":
		if req.Payload != nil || req.Deadline != nil || req.Origin.Validate(notification.None) != nil {
			return refuse()
		}
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		s, err := o.Status.Status(callCtx, *req.Origin)
		if err != nil || !validStatus(s) {
			return response{Version: 1, Error: "status_unavailable"}
		}
		return response{Version: 1, Status: &s}
	default:
		return refuse()
	}
}

func localTCP(addr net.Addr) bool {
	a, ok := addr.(*net.TCPAddr)
	return ok && a.IP.IsLoopback() && a.Port > 0
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
