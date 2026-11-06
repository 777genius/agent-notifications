//go:build linux

// ABOUTME: Daemon server that maintains persistent D-Bus connection for click-to-focus notifications.
// ABOUTME: Listens on Unix socket for IPC requests and handles notification action callbacks.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/esiqveland/notify"
	"github.com/godbus/dbus/v5"
)

// Server is the notification daemon server
type Server struct {
	conn      *dbus.Conn
	notifier  notify.Notifier
	listener  net.Listener
	startTime time.Time

	// Focus context mapping: notification ID -> focus info
	focusCtx   map[uint32]FocusHints
	focusCtxMu sync.RWMutex

	// Idle timeout for auto-shutdown
	idleTimeout  time.Duration
	lastActivity time.Time
	activityMu   sync.Mutex

	// Shutdown handling
	done     chan struct{}
	wg       sync.WaitGroup
	shutdown bool
	mu       sync.Mutex
}

// ServerConfig contains server configuration options
type ServerConfig struct {
	IdleTimeout time.Duration // Auto-shutdown after this duration of inactivity (0 = disabled)
}

// DefaultServerConfig returns the default server configuration
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		IdleTimeout: 5 * time.Minute,
	}
}

// NewServer creates a new daemon server
func NewServer(cfg ServerConfig) (*Server, error) {
	// Connect to D-Bus session bus
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to D-Bus session bus: %w", err)
	}

	s := &Server{
		conn:         conn,
		startTime:    time.Now(),
		focusCtx:     make(map[uint32]FocusHints),
		idleTimeout:  cfg.IdleTimeout,
		lastActivity: time.Now(),
		done:         make(chan struct{}),
	}

	// Create notifier with action callback
	notifier, err := notify.New(conn,
		notify.WithOnAction(s.onActionInvoked),
		notify.WithOnClosed(s.onNotificationClosed),
	)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to create notifier: %w", err)
	}
	s.notifier = notifier

	return s, nil
}

// Run starts the daemon server
func (s *Server) Run() error {
	socketPath := GetSocketPath()

	// Remove existing socket
	_ = os.Remove(socketPath)

	// Create listener
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to create socket: %w", err)
	}
	s.listener = listener

	// Set socket permissions
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	// Write PID file
	pidPath := GetPidFilePath()
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0600); err != nil {
		log.Printf("[WARN] Failed to write PID file: %v", err)
	}

	log.Printf("[INFO] Daemon started, listening on %s", socketPath)

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start idle timeout checker if enabled
	if s.idleTimeout > 0 {
		s.wg.Add(1)
		go s.idleChecker()
	}

	// Strict actions use a raw D-Bus signal so their sender can be checked.
	// The legacy notifier callback does not expose signal sender identity.
	ready := make(chan struct{})
	s.wg.Add(1)
	go s.listenStrictActions(ready)
	<-ready

	// Accept connections
	s.wg.Add(1)
	go s.acceptLoop()

	// Wait for shutdown signal
	select {
	case sig := <-sigChan:
		log.Printf("[INFO] Received signal %v, shutting down", sig)
	case <-s.done:
		log.Printf("[INFO] Shutdown requested")
	}

	return s.Shutdown()
}

// acceptLoop accepts incoming connections
func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			shutdown := s.shutdown
			s.mu.Unlock()

			if shutdown {
				return
			}
			log.Printf("[ERROR] Accept error: %v", err)
			continue
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

// handleConnection handles a single client connection
func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	s.updateActivity()

	// Set read deadline
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		log.Printf("[ERROR] Failed to set read deadline: %v", err)
		return
	}

	// Read request
	decoder := json.NewDecoder(conn)
	var req Request
	if err := decoder.Decode(&req); err != nil {
		log.Printf("[ERROR] Failed to decode request: %v", err)
		s.sendError(conn, "invalid request")
		return
	}

	// Handle request
	var resp Response
	resp.Type = req.Type

	switch req.Type {
	case MessageTypeNotify, MessageTypeNotifyStrict:
		if req.Notify == nil || (req.Type == MessageTypeNotifyStrict && req.Notify.Strict == nil) ||
			(req.Type == MessageTypeNotify && req.Notify.Strict != nil) {
			s.sendError(conn, "missing or conflicting notify payload")
			return
		}
		notifyResp, err := s.handleNotification(req.Notify, req.Type == MessageTypeNotifyStrict)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Notify = notifyResp
		}

	case MessageTypePing:
		resp.Ping = &PingResponse{
			Version: ProtocolVersion,
			Uptime:  int64(time.Since(s.startTime).Seconds()),
		}

	case MessageTypeStop:
		log.Printf("[INFO] Stop command received")
		resp.Ping = &PingResponse{
			Version: ProtocolVersion,
			Uptime:  int64(time.Since(s.startTime).Seconds()),
		}
		// Signal shutdown after sending response
		defer func() {
			s.mu.Lock()
			if !s.shutdown {
				s.shutdown = true
				close(s.done)
			}
			s.mu.Unlock()
		}()

	default:
		s.sendError(conn, "unknown message type")
		return
	}

	// Send response
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(resp); err != nil {
		log.Printf("[ERROR] Failed to encode response: %v", err)
	}
}

// handleNotification processes a notification request
func (s *Server) handleNotification(req *NotifyRequest, strict bool) (*NotifyResponse, error) {
	// Determine focus target
	focusTarget := req.FocusTarget
	if focusTarget == "" && !strict {
		focusTarget = GetTerminalName()
	}

	// Calculate timeout
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// Failure to validate an origin or the unique notification-server
	// owner leaves only a banner. Unauthenticated D-Bus ActionInvoked signals
	// must never activate a terminal, even with a known notification ID.
	strictAllowed := false
	strictOwner := ""
	if strict {
		op, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		strictAllowed = StrictTerminalAuthorized(op, req.Strict)
		if strictAllowed {
			strictOwner, strictAllowed = s.notificationServiceOwner(op)
		}
		cancel()
	}
	n := notify.Notification{
		AppName:       "claude-notifications",
		AppIcon:       req.AppIcon,
		Summary:       req.Title,
		Body:          req.Body,
		ExpireTimeout: timeout,
		Hints: map[string]dbus.Variant{
			"desktop-entry":  dbus.MakeVariant(GetNotificationDesktopEntryID(focusTarget)),
			"suppress-sound": dbus.MakeVariant(!strict || !req.StrictSound),
		},
	}

	if !strict || strictAllowed {
		n.Actions = []notify.Action{{Key: "default", Label: "Focus Terminal"}}
	}

	// Send notification
	id, err := s.notifier.SendNotification(n)
	if err != nil {
		return nil, fmt.Errorf("failed to send notification: %w", err)
	}

	// A strict callback owns only its verified snapshot. Legacy focus
	// paths retain their unchanged hints, without new agent authority.
	if !strict || strictAllowed {
		hints := FocusHints{
			TerminalName: focusTarget, FolderName: req.FocusFolder,
			ProjectPath: req.FocusProjectPath, IDEPID: req.FocusIDEPID,
			WindowID: req.FocusWindowID, WindowTitle: req.FocusWindowTitle,
			WezTermPaneID: req.FocusWezTermPaneID, WezTermSocket: req.FocusWezTermSocket,
			WarpFocusURL: req.FocusWarpURL, ZellijSession: req.FocusZellijSession,
			ZellijPaneID: req.FocusZellijPaneID, ZellijTabName: req.FocusZellijTabName,
			ZellijMode: req.FocusZellijMode,
		}
		if strict {
			captured := *req.Strict
			hints = FocusHints{Strict: &captured, StrictOwner: strictOwner}
		}
		s.focusCtxMu.Lock()
		s.focusCtx[id] = hints
		s.focusCtxMu.Unlock()
	}
	if strict {
		log.Printf("[INFO] Strict terminal notification sent: ID=%d, focus_enabled=%t", id, strictAllowed)
	} else {
		log.Printf("[INFO] Notification sent: ID=%d, focus_target=%s", id, focusTarget)
	}

	return &NotifyResponse{
		Success:        true,
		NotificationID: id,
		FocusEnabled:   strict && strictAllowed,
	}, nil
}

// onActionInvoked is called when a notification action is invoked
func (s *Server) onActionInvoked(sig *notify.ActionInvokedSignal) {
	log.Printf("[INFO] ActionInvoked: ID=%d, Action=%s", sig.ID, sig.ActionKey)

	if sig.ActionKey != "default" {
		return
	}

	// The notify library does not expose the D-Bus signal sender. Strict
	// callbacks are handled only by listenStrictActions after owner matching.
	s.focusCtxMu.RLock()
	hints, exists := s.focusCtx[sig.ID]
	s.focusCtxMu.RUnlock()
	if exists && hints.Strict != nil {
		return
	}

	// Consume strict callbacks atomically before any potentially slow OS
	// operation. Replayed clicks cannot trigger repeated window changes.
	s.focusCtxMu.RLock()
	hints, exists = s.focusCtx[sig.ID]
	s.focusCtxMu.RUnlock()
	if !exists {
		log.Printf("[WARN] No focus context for notification %d", sig.ID)
		return
	}
	// Attempt to focus
	log.Printf("[INFO] Attempting to focus: %s (folder: %s, window_id: %s, window_title: %q, wezterm_pane: %s, zellij: %s/%s mode=%s)",
		hints.TerminalName, hints.FolderName, hints.WindowID, hints.WindowTitle, hints.WezTermPaneID,
		hints.ZellijSession, hints.ZellijPaneID, hints.ZellijMode)
	if err := TryFocusWithHints(hints); err != nil {
		log.Printf("[ERROR] Focus failed: %v", err)
	} else {
		log.Printf("[INFO] Focus succeeded")
	}

	// Clean up focus context
	s.focusCtxMu.Lock()
	delete(s.focusCtx, sig.ID)
	s.focusCtxMu.Unlock()
}

// notificationServiceOwner captures the D-Bus unique sender of the live
// notification service. A well-known name or a notification ID is not proof.
func (s *Server) notificationServiceOwner(ctx context.Context) (string, bool) {
	var owner string
	err := s.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, "org.freedesktop.Notifications").Store(&owner)
	return owner, err == nil && len(owner) > 1 && owner[0] == ':'
}

// listenStrictActions receives the raw signal (including Sender) alongside
// the legacy library. It never acts on a guessed sender or a reused ID.
func (s *Server) listenStrictActions(ready chan<- struct{}) {
	defer s.wg.Done()
	signals := make(chan *dbus.Signal, 32)
	s.conn.Signal(signals)
	defer s.conn.RemoveSignal(signals)
	close(ready)
	for {
		select {
		case <-s.done:
			return
		case sig, open := <-signals:
			if !open {
				return
			}
			if sig == nil || sig.Name != "org.freedesktop.Notifications.ActionInvoked" ||
				sig.Path != dbus.ObjectPath("/org/freedesktop/Notifications") ||
				len(sig.Body) != 2 {
				continue
			}
			id, okID := sig.Body[0].(uint32)
			action, okAction := sig.Body[1].(string)
			if !okID || !okAction || action != "default" {
				continue
			}
			s.focusCtxMu.Lock()
			hints, exists := s.focusCtx[id]
			if !exists || hints.Strict == nil || hints.StrictOwner == "" || hints.StrictOwner != sig.Sender {
				s.focusCtxMu.Unlock()
				continue
			}
			delete(s.focusCtx, id)
			s.focusCtxMu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			if err := ActivateStrictTerminal(ctx, hints.Strict); err != nil {
				log.Printf("[WARN] Strict terminal focus refused")
			} else {
				log.Printf("[INFO] Strict terminal focus succeeded")
			}
			cancel()
		}
	}
}

// onNotificationClosed is called when a notification is closed
func (s *Server) onNotificationClosed(sig *notify.NotificationClosedSignal) {
	s.focusCtxMu.Lock()
	hints, exists := s.focusCtx[sig.ID]
	if !exists || hints.Strict == nil {
		delete(s.focusCtx, sig.ID)
		s.focusCtxMu.Unlock()
		return
	}
	s.focusCtxMu.Unlock()
	// Some servers emit NotificationClosed before ActionInvoked. Give the
	// authenticated raw callback a brief window, then discard the target.
	time.AfterFunc(2*time.Second, func() {
		s.focusCtxMu.Lock()
		delete(s.focusCtx, sig.ID)
		s.focusCtxMu.Unlock()
	})
}

// updateActivity updates the last activity timestamp
func (s *Server) updateActivity() {
	s.activityMu.Lock()
	s.lastActivity = time.Now()
	s.activityMu.Unlock()
}

// idleChecker monitors for idle timeout
func (s *Server) idleChecker() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.activityMu.Lock()
			idle := time.Since(s.lastActivity)
			s.activityMu.Unlock()

			if idle >= s.idleTimeout {
				log.Printf("[INFO] Idle timeout reached (%v), shutting down", s.idleTimeout)
				s.mu.Lock()
				if !s.shutdown {
					s.shutdown = true
					close(s.done)
				}
				s.mu.Unlock()
				return
			}

		case <-s.done:
			return
		}
	}
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return nil
	}
	s.shutdown = true
	close(s.done)
	s.mu.Unlock()

	// Close listener
	if s.listener != nil {
		_ = s.listener.Close()
	}

	// Wait for goroutines with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("[WARN] Shutdown timeout, forcing exit")
	}

	// Close notifier
	if s.notifier != nil {
		_ = s.notifier.Close()
	}

	// Close D-Bus connection
	if s.conn != nil {
		_ = s.conn.Close()
	}

	// Clean up socket and PID files
	_ = os.Remove(GetSocketPath())
	_ = os.Remove(GetPidFilePath())

	log.Printf("[INFO] Daemon stopped")
	return nil
}

// sendError sends an error response
func (s *Server) sendError(conn net.Conn, msg string) {
	resp := Response{Error: msg}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(resp); err != nil {
		log.Printf("[ERROR] Failed to send error response: %v", err)
	}
}
