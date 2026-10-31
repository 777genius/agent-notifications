//go:build linux

// ABOUTME: Client library for communicating with the notification daemon.
// ABOUTME: Provides functions to check daemon status, start on-demand, and send notifications.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Client communicates with the daemon via Unix socket
type Client struct {
	socketPath string
}

// NewClient creates a new daemon client
func NewClient() (*Client, error) {
	socketPath := GetSocketPath()

	// Check if socket exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("daemon not running: socket does not exist")
	}

	return &Client{socketPath: socketPath}, nil
}

// SendNotification sends a notification request to the daemon.
// hints identify the session to focus when the notification is clicked; every
// field is optional and an empty one simply narrows the daemon's search less.
func (c *Client) SendNotification(
	title,
	body,
	appIcon string,
	hints FocusHints,
	timeout int,
) (*NotifyResponse, error) {
	return c.SendNotificationContext(context.Background(), title, body, appIcon, hints, timeout)
}

// SendNotificationContext uses the same daemon protocol and retains the caller's
// deadline through dial, write and read. It is the existing focus-on protocol.
func (c *Client) SendNotificationContext(ctx context.Context, title, body, appIcon string, hints FocusHints, timeout int) (*NotifyResponse, error) {
	req := Request{
		Type:    MessageTypeNotify,
		Version: ProtocolVersion,
		Notify: &NotifyRequest{
			Title:              title,
			Body:               body,
			AppIcon:            appIcon,
			FocusTarget:        hints.TerminalName,
			FocusFolder:        hints.FolderName,
			FocusProjectPath:   hints.ProjectPath,
			FocusIDEPID:        hints.IDEPID,
			FocusWindowID:      hints.WindowID,
			FocusWindowTitle:   hints.WindowTitle,
			FocusWezTermPaneID: hints.WezTermPaneID,
			FocusWezTermSocket: hints.WezTermSocket,
			FocusWarpURL:       hints.WarpFocusURL,
			FocusZellijSession: hints.ZellijSession,
			FocusZellijPaneID:  hints.ZellijPaneID,
			FocusZellijTabName: hints.ZellijTabName,
			FocusZellijMode:    hints.ZellijMode,
			Timeout:            timeout,
		},
	}

	resp, err := c.sendContext(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}

	return resp.Notify, nil
}

// Ping checks if the daemon is responding and returns status info
func (c *Client) Ping() (*PingResponse, error) {
	return c.pingContext(context.Background())
}
func (c *Client) pingContext(ctx context.Context) (*PingResponse, error) {
	req := Request{
		Type:    MessageTypePing,
		Version: ProtocolVersion,
	}

	resp, err := c.sendContext(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}

	return resp.Ping, nil
}

// Stop requests the daemon to shut down
func (c *Client) Stop() error {
	req := Request{
		Type:    MessageTypeStop,
		Version: ProtocolVersion,
	}

	_, err := c.send(req)
	return err
}

// send sends a request to the daemon and returns the response
func (c *Client) send(req Request) (*Response, error) {
	return c.sendContext(context.Background(), req)
}
func (c *Client) sendContext(parent context.Context, req Request) (*Response, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	// Set deadlines
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("failed to set deadline: %w", err)
	}

	// Send request
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	decoder := json.NewDecoder(io.LimitReader(conn, 64<<10))
	var resp Response
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return &resp, nil
}

// IsDaemonRunning checks if the daemon is running and responsive
func IsDaemonRunning() bool {
	return isDaemonRunningContext(context.Background())
}
func isDaemonRunningContext(ctx context.Context) bool {
	client, err := NewClient()
	if err != nil {
		return false
	}

	_, err = client.pingContext(ctx)
	return err == nil
}

// StartDaemonOnDemand starts the daemon if it's not already running.
// Returns true if daemon is running (either started now or was already running).
func StartDaemonOnDemand() bool {
	return startDaemonOnDemand(context.Background(), "")
}

// StartDaemonOnDemandContext owns startup readiness, not the persistent daemon.
// A daemon is deliberately shared and survives a successful readiness probe.
func StartDaemonOnDemandContext(ctx context.Context) bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	return startDaemonOnDemand(ctx, executable)
}
func startDaemonOnDemand(ctx context.Context, executable string) bool {
	// Check if already running
	if isDaemonRunningContext(ctx) {
		return true
	}
	if ctx.Err() != nil {
		return false
	}

	// Find the daemon binary
	daemonPath := executable
	if daemonPath == "" {
		var err error
		daemonPath, err = findDaemonBinary()
		if err != nil {
			return false
		}
	}

	// Start daemon in background
	cmd := exec.Command(daemonPath, "--daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session (detach from terminal)
	}

	// Redirect stdout/stderr to /dev/null
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		cmd.Stdout = devNull
		cmd.Stderr = devNull
		defer func() { _ = devNull.Close() }()
	}

	if err := cmd.Start(); err != nil {
		return false
	}
	// Retain ownership until readiness succeeds. Failed startup must not leave
	// this detached process or its descendants behind; an already-running daemon
	// was handled above and never enters this ownership scope.
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	ready := false
	defer func() {
		if !ready {
			// Setsid gives this child its own process group. Do not use a socket
			// PID or a broad daemon search: only this startup instance is ours.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}()

	// Wait for daemon to be ready (up to 5 seconds)
	startupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < 50; i++ {
		select {
		case <-startupCtx.Done():
			return false
		case <-done:
			return false
		case <-ticker.C:
		}
		if isDaemonRunningContext(startupCtx) {
			ready = true // The shared daemon and its eventual reap now outlive us.
			return true
		}
	}

	return false
}

// StopDaemon stops the running daemon
func StopDaemon() error {
	client, err := NewClient()
	if err != nil {
		return err
	}
	return client.Stop()
}

// findDaemonBinary locates the daemon binary
func findDaemonBinary() (string, error) {
	// Check CLAUDE_PLUGIN_ROOT
	if pluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT"); pluginRoot != "" {
		// Try different binary names
		for _, name := range []string{"claude-notifications", "focus-daemon"} {
			path := filepath.Join(pluginRoot, "bin", name)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}

	// Check if current executable supports daemon mode
	if exe, err := os.Executable(); err == nil {
		return exe, nil
	}

	// Try to find in PATH
	if path, err := exec.LookPath("claude-notifications"); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("daemon binary not found")
}

// GetDaemonPID returns the PID of the running daemon, or 0 if not running
func GetDaemonPID() int {
	pidPath := GetPidFilePath()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}

	// Check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return 0
	}

	// On Unix, FindProcess always succeeds; check if process exists with signal 0
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return 0
	}

	return pid
}
