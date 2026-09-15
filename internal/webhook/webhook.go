package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/google/uuid"
)

// Sender sends webhook notifications with professional patterns
type Sender struct {
	cfg            *config.Config
	client         *http.Client
	retry          *Retryer
	circuitBreaker *CircuitBreaker
	rateLimiter    *RateLimiter
	metrics        *Metrics
	formatters     map[string]Formatter

	// Graceful shutdown
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new professional webhook sender
func New(cfg *config.Config) *Sender {
	// Create base HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Parse retry config
	retryConfig := parseRetryConfig(cfg.Notifications.Webhook.Retry)
	retry := NewRetryer(retryConfig)

	// Parse circuit breaker config
	cbCfg := cfg.Notifications.Webhook.CircuitBreaker
	var circuitBreaker *CircuitBreaker
	if cbCfg.Enabled {
		timeout, _ := time.ParseDuration(cbCfg.Timeout)
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		circuitBreaker = NewCircuitBreaker(cbCfg.FailureThreshold, cbCfg.SuccessThreshold, timeout)
	}

	// Create rate limiter
	var rateLimiter *RateLimiter
	if cfg.Notifications.Webhook.RateLimit.Enabled {
		rateLimiter = NewRateLimiter(cfg.Notifications.Webhook.RateLimit.RequestsPerMinute)
	}

	// Create formatters
	formatters := map[string]Formatter{
		"slack":    &SlackFormatter{},
		"discord":  &DiscordFormatter{},
		"telegram": &TelegramFormatter{ChatID: cfg.Notifications.Webhook.ChatID},
		"lark":     &LarkFormatter{},
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())

	return &Sender{
		cfg:            cfg,
		client:         client,
		retry:          retry,
		circuitBreaker: circuitBreaker,
		rateLimiter:    rateLimiter,
		metrics:        NewMetrics(),
		formatters:     formatters,
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Send sends a webhook notification with full professional stack.
func (s *Sender) Send(status analyzer.Status, message, sessionID string) error {
	return s.SendWithContext(SendContext{
		Status:    status,
		Message:   message,
		SessionID: sessionID,
	})
}

// SendWithContext sends a webhook notification with optional runtime context.
func (s *Sender) SendWithContext(sendCtx SendContext) error {
	if !s.cfg.IsWebhookEnabled() {
		logging.Debug("Webhooks disabled, skipping")
		return nil
	}

	// Check rate limit (non-blocking check)
	if s.rateLimiter != nil && !s.rateLimiter.Allow() {
		s.metrics.RecordRateLimited()
		logging.Warn("Rate limit exceeded, dropping webhook")
		return ErrRateLimitExceeded
	}

	// Check circuit breaker
	if s.circuitBreaker != nil && s.circuitBreaker.GetState() == StateOpen {
		s.metrics.RecordCircuitOpen()
		logging.Warn("Circuit breaker is open, skipping webhook")
		return ErrCircuitOpen
	}

	// Generate request ID for tracing
	requestID := uuid.New().String()

	// Record metrics
	s.metrics.RecordRequest()
	start := time.Now()

	// Execute with retry and circuit breaker
	err := safeWebhookError(s.sendWithRetryAndCircuitBreaker(requestID, sendCtx))

	// Record result
	latency := time.Since(start)
	if err != nil {
		s.metrics.RecordFailure()
		logging.Error("[%s] Webhook failed after retries: %v (latency: %v)", requestID, err, latency)
	} else {
		s.metrics.RecordSuccess(sendCtx.Status, latency)
		logging.Info("[%s] Webhook sent successfully (latency: %v)", requestID, latency)
	}

	// Update circuit breaker state in metrics
	if s.circuitBreaker != nil {
		s.metrics.UpdateCircuitBreakerState(s.circuitBreaker.GetState())
	}

	return err
}

// sendWithRetryAndCircuitBreaker executes the webhook with retry and circuit breaker
func (s *Sender) sendWithRetryAndCircuitBreaker(requestID string, sendCtx SendContext) error {
	webhookCfg := s.cfg.Notifications.Webhook
	statusInfo, _ := s.cfg.GetStatusInfo(string(sendCtx.Status))
	runtimeCtx := newRuntimeContext(sendCtx, statusInfo)

	// Build payload
	payload, contentType, err := s.buildPayload(runtimeCtx)
	if err != nil {
		return fmt.Errorf("failed to build payload: %w", err)
	}

	headers := runtimeCtx.resolveHeaders(webhookCfg.Headers)

	// Validate URL
	if err := validateURL(webhookCfg.URL); err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}

	// Create request function for retry
	sendFn := func(ctx context.Context) error {
		return s.sendHTTPRequest(ctx, requestID, webhookCfg.URL, payload, contentType, headers)
	}

	// Execute with circuit breaker and retry
	var executeErr error
	if s.circuitBreaker != nil {
		// Wrap with circuit breaker
		executeErr = s.circuitBreaker.Execute(s.ctx, func() error {
			// Execute with retry
			return s.retry.Do(s.ctx, sendFn)
		})
	} else {
		// Just retry without circuit breaker
		executeErr = s.retry.Do(s.ctx, sendFn)
	}

	return executeErr
}

// buildPayload builds the webhook payload based on preset.
func (s *Sender) buildPayload(runtimeCtx *runtimeContext) ([]byte, string, error) {
	webhookCfg := s.cfg.Notifications.Webhook
	sendCtx := runtimeCtx.sendCtx
	statusInfo := runtimeCtx.statusInfo

	// Use formatter if available
	if formatter, ok := s.formatters[webhookCfg.Preset]; ok {
		payload, err := formatter.Format(sendCtx, statusInfo)
		if err != nil {
			return nil, "", err
		}
		payload, err = s.applyPayloadFields(payload, runtimeCtx, nil)
		if err != nil {
			return nil, "", err
		}
		data, err := json.Marshal(payload)
		return data, "application/json", err
	}

	// Fallback to custom format
	return s.buildCustomPayload(runtimeCtx, webhookCfg.Format)
}

// buildCustomPayload builds a custom webhook payload
func (s *Sender) buildCustomPayload(runtimeCtx *runtimeContext, format string) ([]byte, string, error) {
	sendCtx := runtimeCtx.sendCtx

	if format == "text" {
		text := fmt.Sprintf("[%s] %s", sendCtx.Status, sendCtx.Message)
		return []byte(text), "text/plain", nil
	}

	// JSON format
	//
	// schema_version, notification_type, and agent_source form the stable
	// notification-identity contract for machine consumers: agent_source says
	// which agent produced the event (claude/codex/...), notification_type is
	// an explicit alias for status kept separate so status can stay backward
	// compatible if its meaning ever narrows. See docs/webhooks/custom.md.
	payload := map[string]interface{}{
		"schema_version":    "1.0",
		"status":            string(sendCtx.Status),
		"notification_type": string(sendCtx.Status),
		"agent_source":      normalizeAgentSource(sendCtx.AgentSource),
		"message":           sendCtx.Message,
		"timestamp":         runtimeCtx.now.Format(time.RFC3339),
		"session_id":        sendCtx.SessionID,
		"source":            "claude-notifications",
		"title":             runtimeCtx.statusInfo.Title,
	}

	payloadWithFields, err := s.applyPayloadFields(payload, runtimeCtx, reservedCustomPayloadKeys)
	if err != nil {
		return nil, "", err
	}

	data, err := json.Marshal(payloadWithFields)
	return data, "application/json", err
}

// reservedCustomPayloadKeys are the generated notification-identity fields in
// the custom JSON payload. payloadFields must never be able to override them:
// external consumers rely on schema_version/status/notification_type/agent_source
// being exactly what the plugin computed, not arbitrary user config (which
// could even set the wrong type, e.g. agent_source as a number).
var reservedCustomPayloadKeys = map[string]bool{
	"schema_version":    true,
	"status":            true,
	"notification_type": true,
	"agent_source":      true,
}

// applyPayloadFields merges the webhook.payloadFields config into base.
// Keys listed in protectedKeys are kept at their generated value; any
// payloadFields entry attempting to override one is dropped with a warning
// instead of silently winning.
func (s *Sender) applyPayloadFields(base interface{}, runtimeCtx *runtimeContext, protectedKeys map[string]bool) (interface{}, error) {
	extraFields, err := runtimeCtx.resolvePayloadFields(s.cfg.Notifications.Webhook.PayloadFields)
	if err != nil {
		return nil, err
	}
	if len(extraFields) == 0 {
		return base, nil
	}

	baseMap, ok := base.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("payloadFields are only supported for JSON webhook payloads")
	}

	for key := range protectedKeys {
		if _, overridden := extraFields[key]; overridden {
			logging.Warn("Ignoring payloadFields override of reserved key %q", key)
			delete(extraFields, key)
		}
	}

	mergePayloadMaps(baseMap, extraFields)
	return baseMap, nil
}

// sendHTTPRequest sends the actual HTTP request
func (s *Sender) sendHTTPRequest(ctx context.Context, requestID, url string, payload []byte, contentType string, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "claude-notifications/1.0")
	req.Header.Set("X-Request-ID", requestID)

	// Set custom headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Send request
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body (limited to 1MB)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return NewHTTPError(resp, string(body))
	}

	return nil
}

// SendAsync sends a webhook asynchronously with graceful shutdown support.
func (s *Sender) SendAsync(status analyzer.Status, message, sessionID string) {
	s.SendAsyncWithContext(SendContext{
		Status:    status,
		Message:   message,
		SessionID: sessionID,
	})
}

// SendAsyncWithContext sends a webhook asynchronously with optional runtime context.
func (s *Sender) SendAsyncWithContext(sendCtx SendContext) {
	s.wg.Add(1)
	// Use SafeGo to protect against panics in async webhook sending
	errorhandler.SafeGo(func() {
		defer s.wg.Done()

		if err := s.SendWithContext(sendCtx); err != nil {
			errorhandler.HandleError(err, "Async webhook send failed")
		}
	})
}

// Shutdown gracefully shuts down the webhook sender
// Waits for in-flight requests to complete (with timeout)
// Only cancels context if timeout is reached
func (s *Sender) Shutdown(timeout time.Duration) error {
	logging.Info("Shutting down webhook sender...")

	// Wait for in-flight requests with timeout
	// Do NOT cancel context immediately - let requests complete gracefully
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All requests completed successfully
		s.cancel() // Clean up context after successful completion
		logging.Info("All webhook requests completed")
		return nil
	case <-time.After(timeout):
		// Timeout reached - force cancel remaining requests
		s.cancel()
		logging.Warn("Webhook shutdown timeout, some requests may be incomplete")
		return fmt.Errorf("shutdown timeout after %v", timeout)
	}
}

// GetMetrics returns current metrics
func (s *Sender) GetMetrics() Stats {
	return s.metrics.GetStats()
}

// Helper functions

// parseRetryConfig converts config.RetryConfig to webhook.RetryConfig
func parseRetryConfig(cfg config.RetryConfig) RetryConfig {
	initialBackoff, _ := time.ParseDuration(cfg.InitialBackoff)
	if initialBackoff == 0 {
		initialBackoff = 1 * time.Second
	}

	maxBackoff, _ := time.ParseDuration(cfg.MaxBackoff)
	if maxBackoff == 0 {
		maxBackoff = 10 * time.Second
	}

	return RetryConfig{
		Enabled:        cfg.Enabled,
		MaxAttempts:    cfg.MaxAttempts,
		InitialBackoff: initialBackoff,
		MaxBackoff:     maxBackoff,
		Multiplier:     2.0,
	}
}

// validateURL validates the webhook URL
func validateURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL is empty")
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme")
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL must have a host")
	}

	return nil
}
