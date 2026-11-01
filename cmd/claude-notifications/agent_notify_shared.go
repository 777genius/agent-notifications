package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	notifymcp "github.com/777genius/agent-notifications/internal/agentnotify/mcp"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/agentnotify/sharedrpc"
	"github.com/777genius/agent-notifications/internal/agentnotify/sharedstate"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

var errSharedUnavailable = errors.New("shared_service_unavailable")

func agentPortableBridge(ctx context.Context, data, name string, options notifyruntime.Options) int {
	discovery, err := portable.Discover(ctx, data, name)
	if err != nil {
		_, _ = os.Stderr.WriteString("portable-launch: portable_binding_invalid\n")
		return 2
	}
	boot := options.BootClock
	if boot == nil {
		boot = notifier.SystemBootClock{}
	}
	client := sharedrpc.Client{Dial: func(call context.Context) (net.Conn, error) {
		return connectSharedDaemon(call, data, name)
	}}
	return agentNotifyExecutePorts(ctx, "mcp-server", []string{"--integration", string(discovery.Binding.Integration)}, options,
		&agentMCPPorts{Backend: client, Status: client, Clock: notifyruntime.Clock{Boot: boot}})
}

func bindingState(data, name, sha string) (*sharedstate.Store, error) {
	key := strings.TrimSuffix(strings.TrimPrefix(name, "agent-notify-"), ".json")
	return sharedstate.Open(data, key, sha)
}

// Every retry here precedes RPC content. After Dial returns an authenticated
// connection, sharedrpc owns a single attempt and never resends the request.
func connectSharedDaemon(ctx context.Context, data, name string) (net.Conn, error) {
	discovery, err := portable.Discover(ctx, data, name)
	if err != nil {
		return nil, errSharedUnavailable
	}
	state, err := bindingState(data, name, discovery.SHA256)
	if err != nil {
		return nil, errSharedUnavailable
	}
	if conn, err := dialSharedRecord(ctx, state); err == nil {
		return conn, nil
	}
	release, err := state.LockStart(ctx)
	if err != nil {
		return nil, errSharedUnavailable
	}
	defer release()
	if conn, err := dialSharedRecord(ctx, state); err == nil {
		return conn, nil
	}
	if _, err := state.Read(); err != nil && !os.IsNotExist(err) {
		return nil, errSharedUnavailable
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var starting <-chan error
	attempts := 0
	for {
		if conn, err := dialSharedRecord(ctx, state); err == nil {
			return conn, nil
		}
		if starting != nil {
			select {
			case <-starting:
				starting = nil
				if attempts >= 2 {
					return nil, errSharedUnavailable
				}
			default:
			}
		}
		if starting == nil {
			// A draining predecessor may release the singleton after our first
			// probe. Re-elect before content, rather than wasting this whole call.
			probeCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			serverRelease, lockErr := state.LockServer(probeCtx)
			cancel()
			if lockErr == nil {
				serverRelease()
				lease, err := portable.Acquire(ctx, data, name)
				if err != nil {
					return nil, errSharedUnavailable
				}
				if lease.SHA256 != discovery.SHA256 {
					lease.Release()
					return nil, errSharedUnavailable
				}
				starting, err = startSharedDaemon(lease, name)
				lease.Release() // Child revalidates after Start, never under our lease.
				if err != nil {
					return nil, errSharedUnavailable
				}
				attempts++
			} else if !errors.Is(lockErr, context.DeadlineExceeded) {
				return nil, errSharedUnavailable
			}
		}
		select {
		case <-ctx.Done():
			return nil, errSharedUnavailable
		case <-ticker.C:
		}
	}
}

func dialSharedRecord(ctx context.Context, state *sharedstate.Store) (net.Conn, error) {
	record, err := state.Read()
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp4", record.Address)
	if err != nil {
		return nil, err
	}
	token, err := hex.DecodeString(record.Token)
	if err != nil {
		_ = conn.Close()
		return nil, errSharedUnavailable
	}
	secured, err := sharedrpc.AuthenticateClient(ctx, conn, state.Identity(), token)
	if err != nil {
		_ = conn.Close()
		return nil, errSharedUnavailable
	}
	return secured, nil
}

func startSharedDaemon(lease *portable.Lease, name string) (<-chan error, error) {
	// The service is not CommandContext-owned by any one chat. Null streams
	// prevent it from retaining that chat's stdio after the bridge exits.
	cmd := exec.Command(lease.Executable, "portable-shared-daemon", "--locator", name, "--runtime-sha256", lease.SHA256)
	cmd.Env = portableChildEnvironment(lease.Binding.DataRoot, os.Getenv("PLUGIN_ROOT"))
	cmd.Dir = lease.Binding.RuntimeRoot
	configureSharedDaemonProcess(cmd)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = null.Close() }()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done, nil
}

func agentPortableSharedDaemon(ctx context.Context, binding portable.Binding, name, sha string, options notifyruntime.Options) int {
	state, err := bindingState(binding.DataRoot, name, sha)
	if err != nil {
		return 2
	}
	lockCtx, cancelLock := context.WithTimeout(ctx, 100*time.Millisecond)
	release, err := state.LockServer(lockCtx)
	cancelLock()
	if err != nil {
		return 2
	}
	defer release()
	// Take the singleton before expensive validation. Even if the elected
	// starter dies, other bridges cannot create a second validating backend.
	lease, err := portable.Acquire(ctx, binding.DataRoot, name)
	if err != nil {
		return 2
	}
	current, executableErr := os.Executable()
	valid := executableErr == nil && portable.SameInstalledFile(current, lease.Executable) && lease.SHA256 == sha
	lease.Release()
	if !valid {
		return 2
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	options.ControlRoot, options.GlobalConfig = binding.ControlRoot, binding.GlobalConfig
	options.JournalRoot, options.SpoolRoot = "", ""
	read := options.ReadSnapshot
	if read == nil {
		read = installruntime.ReadPolicySnapshot
	}
	reads := sharedPolicyReads{lifetime: serveCtx, read: read}
	options.ReadSnapshot = func(c context.Context, root string) (installruntime.PolicySnapshot, error) {
		snapshot, err := reads.get(c, root)
		if err == nil {
			err = binding.CheckRuntimeSnapshot(snapshot.Installation, sha)
		}
		return snapshot, err
	}
	backend, err := notifyruntime.New(options)
	if err != nil {
		return 2
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = backend.Close(c)
	}()
	guard := sharedBoundBackend{backend: backend, data: binding.DataRoot, name: name, sha: sha, submits: make(chan struct{}, 2)}
	if !guard.valid(ctx) {
		return 2
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 2
	}
	defer func() { _ = listener.Close() }()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return 2
	}
	record := sharedstate.Record{Version: 1, Address: listener.Addr().String(), Token: hex.EncodeToString(token), PID: os.Getpid()}
	if err := state.Publish(record); err != nil {
		return 2
	}
	// Serve drains all handlers before conditional removal; retain the singleton
	// lock through that removal so this daemon cannot erase a successor's record.
	defer func() { _ = state.Remove(record) }()
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-serveCtx.Done():
				return
			case <-ticker.C:
				if !guard.valid(serveCtx) {
					cancel()
					return
				}
			}
		}
	}()
	err = sharedrpc.Serve(serveCtx, listener, sharedrpc.ServerOptions{
		Backend: guard, Status: guard, Clock: backend.Clock(), Identity: state.Identity(), Token: token, AdapterKind: string(binding.Integration),
	})
	cancel()
	<-monitorDone
	if err != nil && !errors.Is(err, context.Canceled) {
		return 2
	}
	return 0
}

type sharedBoundBackend struct {
	backend         *notifyruntime.Backend
	data, name, sha string
	submits         chan struct{}
}

func (b sharedBoundBackend) valid(ctx context.Context) bool {
	discovery, err := portable.Discover(ctx, b.data, b.name)
	return err == nil && discovery.SHA256 == b.sha
}

func (b sharedBoundBackend) Notify(ctx context.Context, payload agentnotify.Payload, caller origin.Context, deadline notification.Deadline) agentnotify.Receipt {
	// Queue only in the shared adapter, bounded by transport admission and the
	// original call deadline. Preserve the service's two concurrent submissions
	// without rejecting unrelated chats merely because two peers are active.
	if err := acquireSharedSubmit(ctx, b.submits); err != nil {
		return agentnotify.Receipt{Status: "rejected", Reason: "canceled", RetrySafe: true}
	}
	defer func() { <-b.submits }()
	// Guard before replay too: replay lookup intentionally skips policy loading.
	if !b.valid(ctx) {
		return agentnotify.Receipt{Status: "rejected", Reason: "portable_binding_invalid", RetrySafe: true}
	}
	return b.backend.Notify(ctx, payload, caller, deadline)
}

func (b sharedBoundBackend) Status(ctx context.Context, caller origin.Context) (notifymcp.Status, error) {
	if !b.valid(ctx) {
		return notifymcp.Status{}, errSharedUnavailable
	}
	return agentNotifyStatus{b.backend}.Status(ctx, caller)
}

func acquireSharedSubmit(ctx context.Context, slots chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
