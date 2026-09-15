package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

func newTestConfig(url string) *config.Config {
	return &config.Config{
		Notifications: config.NotificationsConfig{
			Webhook: config.WebhookConfig{
				Enabled: true,
				URL:     url,
				Format:  "json",
				Preset:  "",
				Retry: config.RetryConfig{
					Enabled:        true,
					MaxAttempts:    3,
					InitialBackoff: "10ms",
					MaxBackoff:     "100ms",
				},
				CircuitBreaker: config.CircuitBreakerConfig{
					Enabled:          true,
					FailureThreshold: 3,
					SuccessThreshold: 2,
					Timeout:          "100ms",
				},
				RateLimit: config.RateLimitConfig{
					Enabled:           false,
					RequestsPerMinute: 60,
				},
			},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
			"question":      {Title: "Question"},
		},
	}
}

func createGitRepoForWebhookTests(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	if err := runGitCommand(tmpDir, "init"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if err := runGitCommand(tmpDir, "config", "user.email", "webhook@test.com"); err != nil {
		t.Fatalf("git config user.email failed: %v", err)
	}
	if err := runGitCommand(tmpDir, "config", "user.name", "Webhook Tester"); err != nil {
		t.Fatalf("git config user.name failed: %v", err)
	}

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if err := runGitCommand(tmpDir, "add", "."); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if err := runGitCommand(tmpDir, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
	if err := runGitCommand(tmpDir, "branch", "-M", "webhook-tests"); err != nil {
		t.Fatalf("git branch rename failed: %v", err)
	}

	return tmpDir
}

func runGitCommand(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run()
}

func TestSenderSendSuccess(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	err := sender.Send(analyzer.StatusTaskComplete, "Test message", "session-123")
	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}

	// Check metrics
	stats := sender.GetMetrics()
	if stats.SuccessfulRequests != 1 {
		t.Errorf("Expected 1 successful request, got %d", stats.SuccessfulRequests)
	}
	if stats.FailedRequests != 0 {
		t.Errorf("Expected 0 failed requests, got %d", stats.FailedRequests)
	}
}

func TestSenderSendWithRetry(t *testing.T) {
	attempts := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		if count < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	err := sender.Send(analyzer.StatusTaskComplete, "Test message", "session-123")
	if err != nil {
		t.Errorf("Expected success after retry, got error: %v", err)
	}

	if attempts.Load() != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts.Load())
	}

	stats := sender.GetMetrics()
	if stats.SuccessfulRequests != 1 {
		t.Errorf("Expected 1 successful request, got %d", stats.SuccessfulRequests)
	}
}

func TestSenderSendMaxRetriesExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	err := sender.Send(analyzer.StatusTaskComplete, "Test message", "session-123")
	if err == nil {
		t.Error("Expected error after max retries, got nil")
	}

	stats := sender.GetMetrics()
	if stats.FailedRequests != 1 {
		t.Errorf("Expected 1 failed request, got %d", stats.FailedRequests)
	}
}

func TestSenderSendCircuitBreaker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	// Trigger circuit breaker by failing threshold times
	for i := 0; i < 3; i++ {
		_ = sender.Send(analyzer.StatusTaskComplete, "Test", "session-123")
	}

	// Next request should fail with circuit open
	err := sender.Send(analyzer.StatusTaskComplete, "Test", "session-123")
	if err != ErrCircuitOpen {
		t.Errorf("Expected ErrCircuitOpen, got: %v", err)
	}

	stats := sender.GetMetrics()
	if stats.CircuitOpenRequests == 0 {
		t.Error("Expected circuit open requests to be recorded")
	}
}

func TestSenderSendRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.RateLimit.Enabled = true
	cfg.Notifications.Webhook.RateLimit.RequestsPerMinute = 60 // 1 per second, capacity 60
	sender := New(cfg)

	// Exhaust the rate limiter bucket (starts with 60 tokens)
	for i := 0; i < 70; i++ {
		_ = sender.Send(analyzer.StatusTaskComplete, "Test", "session-123")
	}

	// Next request should be rate limited
	err := sender.Send(analyzer.StatusTaskComplete, "Test", "session-123")
	if err != ErrRateLimitExceeded {
		t.Errorf("Expected ErrRateLimitExceeded, got: %v", err)
	}

	stats := sender.GetMetrics()
	if stats.RateLimitedRequests == 0 {
		t.Error("Expected rate limited requests to be recorded")
	}
}

func TestSenderSendSlackFormat(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.Preset = "slack"
	sender := New(cfg)

	err := sender.Send(analyzer.StatusTaskComplete, "Test message", "session-123")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify Slack format
	attachments, ok := receivedPayload["attachments"].([]interface{})
	if !ok || len(attachments) == 0 {
		t.Fatal("Expected Slack attachments")
	}

	attachment := attachments[0].(map[string]interface{})
	if attachment["color"] != "#28a745" {
		t.Errorf("Expected green color, got %v", attachment["color"])
	}
}

func TestSenderSendDiscordFormat(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.Preset = "discord"
	sender := New(cfg)

	err := sender.Send(analyzer.StatusQuestion, "What should we do?", "session-456")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify Discord format
	embeds, ok := receivedPayload["embeds"].([]interface{})
	if !ok || len(embeds) == 0 {
		t.Fatal("Expected Discord embeds")
	}

	embed := embeds[0].(map[string]interface{})
	// Discord color is a number in JSON
	if embed["color"] == nil {
		t.Error("Expected color field")
	}
}

func TestSenderSendTelegramFormat(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.Preset = "telegram"
	cfg.Notifications.Webhook.ChatID = "123456789"
	sender := New(cfg)

	err := sender.Send(analyzer.StatusTaskComplete, "Done!", "session-789")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify Telegram format
	if receivedPayload["chat_id"] != "123456789" {
		t.Errorf("Expected chat_id 123456789, got %v", receivedPayload["chat_id"])
	}
	if receivedPayload["parse_mode"] != "HTML" {
		t.Errorf("Expected HTML parse mode, got %v", receivedPayload["parse_mode"])
	}

	text, ok := receivedPayload["text"].(string)
	if !ok || !strings.Contains(text, "<b>") {
		t.Error("Expected HTML formatted text")
	}
}

func TestSenderSendCustomHeaders(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.Headers = map[string]string{
		"Authorization": "Bearer secret-token",
		"X-Custom":      "CustomValue",
	}
	sender := New(cfg)

	err := sender.Send(analyzer.StatusTaskComplete, "Test", "session-123")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Check custom headers
	if receivedHeaders.Get("Authorization") != "Bearer secret-token" {
		t.Error("Authorization header not set correctly")
	}
	if receivedHeaders.Get("X-Custom") != "CustomValue" {
		t.Error("X-Custom header not set correctly")
	}

	// Check default headers
	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Error("Content-Type not set")
	}
	if receivedHeaders.Get("User-Agent") != "claude-notifications/1.0" {
		t.Error("User-Agent not set")
	}
	if receivedHeaders.Get("X-Request-ID") == "" {
		t.Error("X-Request-ID not set")
	}
}

func TestSenderSendTemplatedHeaders(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repoDir := createGitRepoForWebhookTests(t)
	t.Setenv("WEBHOOK_TOKEN", "secret-token")

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.Headers = map[string]string{
		"Authorization": "Bearer ${{env.WEBHOOK_TOKEN}}",
		"X-Git-Email":   "${{git.user.email}}",
		"X-Branch":      "${{git.branch}}",
	}
	sender := New(cfg)

	err := sender.SendWithContext(SendContext{
		Status:    analyzer.StatusTaskComplete,
		Message:   "Test",
		SessionID: "session-123",
		CWD:       repoDir,
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if receivedHeaders.Get("Authorization") != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", receivedHeaders.Get("Authorization"), "Bearer secret-token")
	}
	if receivedHeaders.Get("X-Git-Email") != "webhook@test.com" {
		t.Errorf("X-Git-Email = %q, want %q", receivedHeaders.Get("X-Git-Email"), "webhook@test.com")
	}
	if receivedHeaders.Get("X-Branch") != "webhook-tests" {
		t.Errorf("X-Branch = %q, want %q", receivedHeaders.Get("X-Branch"), "webhook-tests")
	}
}

func TestSenderSendLegacyTemplateSyntaxIsLiteral(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repoDir := createGitRepoForWebhookTests(t)

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.Headers = map[string]string{
		"X-Legacy": "{{git.branch}}",
	}
	sender := New(cfg)

	err := sender.SendWithContext(SendContext{
		Status:    analyzer.StatusTaskComplete,
		Message:   "Test",
		SessionID: "session-123",
		CWD:       repoDir,
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if receivedHeaders.Get("X-Legacy") != "{{git.branch}}" {
		t.Errorf("X-Legacy = %q, want literal %q", receivedHeaders.Get("X-Legacy"), "{{git.branch}}")
	}
}

func TestSenderSendPayloadFields(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repoDir := createGitRepoForWebhookTests(t)
	t.Setenv("DEPLOY_ENV", "staging")

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.PayloadFields = map[string]interface{}{
		"context": map[string]interface{}{
			"git": map[string]interface{}{
				"userEmail": "${{git.user.email}}",
				"branch":    "${{git.branch}}",
			},
			"env": "${{env.DEPLOY_ENV}}",
		},
		"sent_at_unix": "${{time.unix}}",
		"session_name": "${{session_name}}",
	}
	sender := New(cfg)

	err := sender.SendWithContext(SendContext{
		Status:    analyzer.StatusTaskComplete,
		Message:   "Payload test",
		SessionID: "session-123",
		CWD:       repoDir,
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	contextMap, ok := receivedPayload["context"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected context payload field, got %T", receivedPayload["context"])
	}

	gitMap, ok := contextMap["git"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected context.git map, got %T", contextMap["git"])
	}
	if gitMap["userEmail"] != "webhook@test.com" {
		t.Errorf("context.git.userEmail = %v, want %q", gitMap["userEmail"], "webhook@test.com")
	}
	if gitMap["branch"] != "webhook-tests" {
		t.Errorf("context.git.branch = %v, want %q", gitMap["branch"], "webhook-tests")
	}
	if contextMap["env"] != "staging" {
		t.Errorf("context.env = %v, want %q", contextMap["env"], "staging")
	}

	if _, ok := receivedPayload["sent_at_unix"].(float64); !ok {
		t.Errorf("sent_at_unix should be numeric, got %T", receivedPayload["sent_at_unix"])
	}
	if receivedPayload["session_name"] == "" {
		t.Error("session_name should not be empty")
	}
}

func TestSenderSendPayloadFieldsSkipUnavailableValues(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.PayloadFields = map[string]interface{}{
		"missing_env": "${{env.DOES_NOT_EXIST}}",
		"context": map[string]interface{}{
			"still_here": "yes",
		},
	}
	sender := New(cfg)

	err := sender.SendWithContext(SendContext{
		Status:    analyzer.StatusTaskComplete,
		Message:   "Payload test",
		SessionID: "session-123",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if _, exists := receivedPayload["missing_env"]; exists {
		t.Error("missing_env should be omitted when template value is unavailable")
	}
	if receivedPayload["context"] == nil {
		t.Error("context should still be present")
	}
}

func TestSenderSendPayloadFieldsCannotOverrideIdentityFields(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.PayloadFields = map[string]interface{}{
		"schema_version":    "9.9",
		"status":            "spoofed",
		"notification_type": "spoofed",
		"agent_source":      123,
		"untouched":         "kept",
	}
	sender := New(cfg)

	err := sender.SendWithContext(SendContext{
		Status:      analyzer.StatusTaskComplete,
		Message:     "Identity test",
		SessionID:   "session-123",
		AgentSource: "codex",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if got := receivedPayload["schema_version"]; got != "1.0" {
		t.Errorf("schema_version = %v, want %q (payloadFields must not override it)", got, "1.0")
	}
	if got := receivedPayload["status"]; got != "task_complete" {
		t.Errorf("status = %v, want %q (payloadFields must not override it)", got, "task_complete")
	}
	if got := receivedPayload["notification_type"]; got != "task_complete" {
		t.Errorf("notification_type = %v, want %q (payloadFields must not override it)", got, "task_complete")
	}
	if got := receivedPayload["agent_source"]; got != "codex" {
		t.Errorf("agent_source = %v, want %q (payloadFields must not override it)", got, "codex")
	}
	if got := receivedPayload["untouched"]; got != "kept" {
		t.Errorf("untouched = %v, want %q (non-reserved payloadFields must still apply)", got, "kept")
	}
}

func TestSenderSendDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Server should not be called when webhooks disabled")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Notifications.Webhook.Enabled = false
	sender := New(cfg)

	err := sender.Send(analyzer.StatusTaskComplete, "Test", "session-123")
	if err != nil {
		t.Errorf("Send should succeed (skipped), got error: %v", err)
	}
}

func TestSenderSendAsync(t *testing.T) {
	completed := make(chan bool)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // Simulate slow response
		w.WriteHeader(http.StatusOK)
		completed <- true
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	// Send async - should not block
	start := time.Now()
	sender.SendAsync(analyzer.StatusTaskComplete, "Test", "session-123")
	elapsed := time.Since(start)

	// Should return well before the slow request completes, even under race-enabled CI.
	if elapsed > 50*time.Millisecond {
		t.Errorf("SendAsync blocked for %v", elapsed)
	}

	// Wait for completion
	select {
	case <-completed:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("Async send did not complete")
	}
}

func TestSenderShutdown(t *testing.T) {
	slowResponse := make(chan bool)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-slowResponse // Block until signaled
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	// Start async send
	sender.SendAsync(analyzer.StatusTaskComplete, "Test", "session-123")

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown with timeout
	shutdownDone := make(chan error)
	go func() {
		shutdownDone <- sender.Shutdown(2 * time.Second)
	}()

	// Release the request
	close(slowResponse)

	// Should complete gracefully
	err := <-shutdownDone
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}

func TestSenderShutdownCancelsRequests(t *testing.T) {
	requestCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		// Small delay to simulate processing
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	// Start multiple async sends
	for i := 0; i < 5; i++ {
		sender.SendAsync(analyzer.StatusTaskComplete, "Test", "session-123")
	}

	// Give requests time to start
	time.Sleep(100 * time.Millisecond)

	// Shutdown with generous timeout
	start := time.Now()
	err := sender.Shutdown(5 * time.Second)
	elapsed := time.Since(start)

	// Should complete reasonably quickly
	if elapsed > 2*time.Second {
		t.Errorf("Shutdown took too long: %v", elapsed)
	}

	// Should succeed (no timeout)
	if err != nil {
		t.Errorf("Shutdown should succeed, got: %v", err)
	}

	// At least some requests should have been processed
	if requestCount.Load() == 0 {
		t.Error("No requests were processed")
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"Valid HTTPS", "https://example.com/webhook", false},
		{"Valid HTTP", "http://example.com/webhook", false},
		{"Empty URL", "", true},
		{"Invalid scheme", "ftp://example.com", true},
		{"No host", "https://", true},
		{"Relative URL", "/webhook", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSenderMetricsTracking(t *testing.T) {
	successCount := atomic.Int32{}
	failCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := successCount.Add(1)
		if count%2 == 0 {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			failCount.Add(1)
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	// Send multiple requests
	for i := 0; i < 10; i++ {
		_ = sender.Send(analyzer.StatusTaskComplete, "Test", "session-123")
	}

	stats := sender.GetMetrics()

	// Should have tracked all requests
	if stats.TotalRequests != 10 {
		t.Errorf("Expected 10 total requests, got %d", stats.TotalRequests)
	}

	// Should have latency recorded
	if stats.AverageLatencyMs == 0 {
		t.Error("Expected non-zero average latency")
	}
}

func TestSenderContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // Long delay
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	// Cancel context immediately
	sender.cancel()

	// Send should fail with context canceled
	err := sender.Send(analyzer.StatusTaskComplete, "Test", "session-123")
	if err == nil {
		t.Error("Expected error with canceled context, got nil")
	}
}

func TestHTTPError(t *testing.T) {
	resp := &http.Response{
		StatusCode: 404,
		Status:     "404 Not Found",
	}

	err := NewHTTPError(resp, "Page not found")

	if err.StatusCode != 404 {
		t.Errorf("Expected status code 404, got %d", err.StatusCode)
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "404") {
		t.Error("Error message should contain status code")
	}
	if !strings.Contains(errMsg, "Page not found") {
		t.Error("Error message should contain response body")
	}
}

// TestSenderSendAsyncWithShutdown verifies that SendAsync + Shutdown work together
// ensuring all async requests complete before shutdown finishes
func TestSenderSendAsyncWithShutdown(t *testing.T) {
	receivedRequests := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Small delay to simulate real network
		time.Sleep(20 * time.Millisecond)
		receivedRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	// Send multiple async requests
	numRequests := 3
	for i := 0; i < numRequests; i++ {
		sender.SendAsync(analyzer.StatusTaskComplete, "Test message", "session-123")
	}

	// Immediately call shutdown - it should wait for all requests
	err := sender.Shutdown(5 * time.Second)
	if err != nil {
		t.Errorf("Shutdown should succeed, got: %v", err)
	}

	// After shutdown, all requests should have been processed
	received := receivedRequests.Load()
	if received != int32(numRequests) {
		t.Errorf("Expected %d requests to be received, got %d", numRequests, received)
	}
}

// TestWebhookShutdownWaitsForRequests verifies that Shutdown actually waits
// for in-flight requests to complete, not just returns immediately
func TestWebhookShutdownWaitsForRequests(t *testing.T) {
	requestCompleted := atomic.Bool{}
	requestDelay := 300 * time.Millisecond

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(requestDelay)
		requestCompleted.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	sender := New(cfg)

	// Start async send
	sender.SendAsync(analyzer.StatusTaskComplete, "Test", "session-123")

	// Give request time to start
	time.Sleep(50 * time.Millisecond)

	// Measure how long Shutdown takes
	start := time.Now()
	err := sender.Shutdown(2 * time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}

	// Shutdown should have waited for the request (at least ~250ms)
	// Using 200ms as threshold to account for timing variations
	if elapsed < 200*time.Millisecond {
		t.Errorf("Shutdown returned too quickly (%v), expected to wait for request (~%v)", elapsed, requestDelay)
	}

	// Request should have completed
	if !requestCompleted.Load() {
		t.Error("Request should have completed before Shutdown returned")
	}
}
