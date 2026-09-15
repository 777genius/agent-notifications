//go:build linux

package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- GetSocketPath tests ---

func TestGetSocketPath_WithXDGRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	result := GetSocketPath()
	expected := filepath.Join("/run/user/1000", "claude-notifications.sock")
	if result != expected {
		t.Errorf("GetSocketPath() = %q, want %q", result, expected)
	}
}

func TestGetSocketPath_WithoutXDGRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if err := os.Unsetenv("XDG_RUNTIME_DIR"); err != nil {
		t.Fatal(err)
	}

	result := GetSocketPath()
	expected := fmt.Sprintf("/tmp/claude-notifications-%d.sock", os.Getuid())
	if result != expected {
		t.Errorf("GetSocketPath() = %q, want %q", result, expected)
	}
}

func TestGetSocketPath_ContainsUID(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if err := os.Unsetenv("XDG_RUNTIME_DIR"); err != nil {
		t.Fatal(err)
	}

	result := GetSocketPath()
	uid := fmt.Sprintf("%d", os.Getuid())
	if !strings.Contains(result, uid) {
		t.Errorf("GetSocketPath() = %q, should contain UID %s", result, uid)
	}
}

func TestGetSocketPath_TrailingSlashXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000/")

	result := GetSocketPath()
	// filepath.Join normalizes trailing slashes
	if strings.Contains(result, "//") {
		t.Errorf("GetSocketPath() = %q, should not contain double slashes", result)
	}
	if !strings.HasSuffix(result, ".sock") {
		t.Errorf("GetSocketPath() = %q, should end with .sock", result)
	}
}

// --- GetPidFilePath tests ---

func TestGetPidFilePath_WithXDGRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	result := GetPidFilePath()
	expected := filepath.Join("/run/user/1000", "claude-notifications.pid")
	if result != expected {
		t.Errorf("GetPidFilePath() = %q, want %q", result, expected)
	}
}

func TestGetPidFilePath_WithoutXDGRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if err := os.Unsetenv("XDG_RUNTIME_DIR"); err != nil {
		t.Fatal(err)
	}

	result := GetPidFilePath()
	expected := fmt.Sprintf("/tmp/claude-notifications-%d.pid", os.Getuid())
	if result != expected {
		t.Errorf("GetPidFilePath() = %q, want %q", result, expected)
	}
}

func TestGetSocketAndPidPath_Consistency(t *testing.T) {
	// Socket and PID file should be in the same directory
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	socketDir := filepath.Dir(GetSocketPath())
	pidDir := filepath.Dir(GetPidFilePath())
	if socketDir != pidDir {
		t.Errorf("Socket dir %q != PID dir %q", socketDir, pidDir)
	}
}

func TestGetSocketAndPidPath_ConsistencyNoXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if err := os.Unsetenv("XDG_RUNTIME_DIR"); err != nil {
		t.Fatal(err)
	}

	socketDir := filepath.Dir(GetSocketPath())
	pidDir := filepath.Dir(GetPidFilePath())
	if socketDir != pidDir {
		t.Errorf("Socket dir %q != PID dir %q (no XDG)", socketDir, pidDir)
	}
}

// --- Protocol types JSON serialization tests ---

func TestRequest_JSONRoundtrip_Notify(t *testing.T) {
	req := Request{
		Type:    MessageTypeNotify,
		Version: ProtocolVersion,
		Notify: &NotifyRequest{
			Title:            "Test Title",
			Body:             "Test Body",
			FocusTarget:      "code",
			FocusWindowID:    "12345",
			FocusWindowTitle: "project - Terminator",
			FocusWarpURL:     "warp://session/6b7be92641ae8ced80188a4d87e4b200",
			Timeout:          30,
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Type != MessageTypeNotify {
		t.Errorf("Type = %q, want %q", decoded.Type, MessageTypeNotify)
	}
	if decoded.Version != ProtocolVersion {
		t.Errorf("Version = %q, want %q", decoded.Version, ProtocolVersion)
	}
	if decoded.Notify == nil {
		t.Fatal("Notify is nil after roundtrip")
	}
	if decoded.Notify.Title != "Test Title" {
		t.Errorf("Title = %q, want %q", decoded.Notify.Title, "Test Title")
	}
	if decoded.Notify.Body != "Test Body" {
		t.Errorf("Body = %q, want %q", decoded.Notify.Body, "Test Body")
	}
	if decoded.Notify.FocusTarget != "code" {
		t.Errorf("FocusTarget = %q, want %q", decoded.Notify.FocusTarget, "code")
	}
	if decoded.Notify.FocusWindowID != "12345" {
		t.Errorf("FocusWindowID = %q, want %q", decoded.Notify.FocusWindowID, "12345")
	}
	if decoded.Notify.FocusWindowTitle != "project - Terminator" {
		t.Errorf("FocusWindowTitle = %q, want %q", decoded.Notify.FocusWindowTitle, "project - Terminator")
	}
	if decoded.Notify.FocusWarpURL != "warp://session/6b7be92641ae8ced80188a4d87e4b200" {
		t.Errorf("FocusWarpURL = %q, want session URL", decoded.Notify.FocusWarpURL)
	}
	if decoded.Notify.Timeout != 30 {
		t.Errorf("Timeout = %d, want %d", decoded.Notify.Timeout, 30)
	}
}

func TestRequest_JSONRoundtrip_Ping(t *testing.T) {
	req := Request{
		Type:    MessageTypePing,
		Version: ProtocolVersion,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Type != MessageTypePing {
		t.Errorf("Type = %q, want %q", decoded.Type, MessageTypePing)
	}
	if decoded.Notify != nil {
		t.Error("Notify should be nil for ping request")
	}
}

func TestRequest_JSONRoundtrip_Stop(t *testing.T) {
	req := Request{
		Type:    MessageTypeStop,
		Version: ProtocolVersion,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Type != MessageTypeStop {
		t.Errorf("Type = %q, want %q", decoded.Type, MessageTypeStop)
	}
	if decoded.Notify != nil {
		t.Error("Notify should be nil for stop request")
	}
}

func TestResponse_JSONRoundtrip_WithError(t *testing.T) {
	resp := Response{
		Type:  MessageTypeNotify,
		Error: "something went wrong",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Error != "something went wrong" {
		t.Errorf("Error = %q, want %q", decoded.Error, "something went wrong")
	}
}

func TestResponse_JSONRoundtrip_NotifyResponse(t *testing.T) {
	resp := Response{
		Type: MessageTypeNotify,
		Notify: &NotifyResponse{
			Success:        true,
			NotificationID: 42,
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Notify == nil {
		t.Fatal("Notify is nil after roundtrip")
	}
	if !decoded.Notify.Success {
		t.Error("Notify.Success should be true")
	}
	if decoded.Notify.NotificationID != 42 {
		t.Errorf("NotificationID = %d, want 42", decoded.Notify.NotificationID)
	}
}

func TestResponse_JSONRoundtrip_NotifyResponseWithError(t *testing.T) {
	resp := Response{
		Type: MessageTypeNotify,
		Notify: &NotifyResponse{
			Success: false,
			Error:   "notification failed",
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Notify == nil {
		t.Fatal("Notify is nil after roundtrip")
	}
	if decoded.Notify.Success {
		t.Error("Notify.Success should be false")
	}
	if decoded.Notify.Error != "notification failed" {
		t.Errorf("Notify.Error = %q, want %q", decoded.Notify.Error, "notification failed")
	}
}

func TestResponse_JSONOmitEmpty(t *testing.T) {
	// Empty optional fields should not appear in JSON
	resp := Response{
		Type: MessageTypePing,
		Ping: &PingResponse{
			Version: "1.0",
			Uptime:  300,
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, "notify") {
		t.Errorf("JSON should not contain 'notify' when nil: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"error"`) {
		t.Errorf("JSON should not contain 'error' when empty: %s", jsonStr)
	}
}

// --- Constants tests ---

func TestProtocolVersion(t *testing.T) {
	if ProtocolVersion == "" {
		t.Error("ProtocolVersion should not be empty")
	}
}

func TestMessageTypes(t *testing.T) {
	// Ensure message types are distinct
	types := []MessageType{MessageTypeNotify, MessageTypePing, MessageTypeStop}
	seen := make(map[MessageType]bool)

	for _, mt := range types {
		if mt == "" {
			t.Error("MessageType should not be empty")
		}
		if seen[mt] {
			t.Errorf("Duplicate MessageType: %q", mt)
		}
		seen[mt] = true
	}
}

func TestMessageTypes_Values(t *testing.T) {
	if MessageTypeNotify != "notify" {
		t.Errorf("MessageTypeNotify = %q, want %q", MessageTypeNotify, "notify")
	}
	if MessageTypePing != "ping" {
		t.Errorf("MessageTypePing = %q, want %q", MessageTypePing, "ping")
	}
	if MessageTypeStop != "stop" {
		t.Errorf("MessageTypeStop = %q, want %q", MessageTypeStop, "stop")
	}
}

// --- Error types tests ---

func TestErrDaemonNotAvailable(t *testing.T) {
	if ErrDaemonNotAvailable == nil {
		t.Fatal("ErrDaemonNotAvailable should not be nil")
	}
	if ErrDaemonNotAvailable.Error() == "" {
		t.Error("ErrDaemonNotAvailable.Error() should not be empty")
	}
}

func TestErrDaemonNotRunning(t *testing.T) {
	if ErrDaemonNotRunning == nil {
		t.Fatal("ErrDaemonNotRunning should not be nil")
	}
	if ErrDaemonNotRunning.Error() == "" {
		t.Error("ErrDaemonNotRunning.Error() should not be empty")
	}
}

func TestErrorTypes_Distinct(t *testing.T) {
	if ErrDaemonNotAvailable.Error() == ErrDaemonNotRunning.Error() {
		t.Error("ErrDaemonNotAvailable and ErrDaemonNotRunning should have distinct messages")
	}
}
