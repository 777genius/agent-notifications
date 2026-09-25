//go:build linux

// ABOUTME: Client library for communicating with the notification daemon.
// ABOUTME: Provides functions to check daemon status, start on-demand, and send notifications.
package daemon

import (
	"encoding/json"
	"fmt"
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
	req := Request{
		Type:    MessageTypeNotify,
		Version: ProtocolVersion,
		Notify: &NotifyRequest{
			Title:              title,
			Body:               body,
			AppIcon:            appIcon,
			FocusTarget:        hints.TerminalName,
			FocusFolder:        hints.FolderName,
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

	resp, err := c.send(req)
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
	req := Request{
		Type:    MessageTypePing,
		Version: ProtocolVersion,
	}

	resp, err := c.send(req)
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
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Set deadlines
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("failed to set deadline: %w", err)
	}

	// Send request
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	decoder := json.NewDecoder(conn)
	var resp Response
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return &resp, nil
}

// IsDaemonRunning checks if the daemon is running and responsive
func IsDaemonRunning() bool {
	client, err := NewClient()
	if err != nil {
		return false
	}

	_, err = client.Ping()
	return err == nil
}

// StartDaemonOnDemand starts the daemon if it's not already running.
// Returns true if daemon is running (either started now or was already running).
func StartDaemonOnDemand() bool {
	// Check if already running
	if IsDaemonRunning() {
		return true
	}

	// Find the daemon binary
	daemonPath, err := findDaemonBinary()
	if err != nil {
		return false
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

	// Wait for daemon to be ready (up to 5 seconds)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if IsDaemonRunning() {
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
