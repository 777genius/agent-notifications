# Webhook Monitoring & Metrics

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


Track webhook health, debug issues, and optimize performance.

## Table of Contents

- [Metrics Overview](#metrics-overview)
- [Debug Logging](#debug-logging)
- [Request Tracing](#request-tracing)
- [Monitoring Examples](#monitoring-examples)
- [Performance Tips](#performance-tips)

## Metrics Overview

The webhook system tracks comprehensive metrics for monitoring health and performance.

### Available Metrics

#### Counters

| Metric | Description |
|--------|-------------|
| `TotalRequests` | Total webhook requests attempted |
| `SuccessfulRequests` | Successfully delivered webhooks (HTTP 2xx) |
| `FailedRequests` | Failed webhook deliveries (after all retries) |
| `RetriedRequests` | Number of retry attempts made |
| `RateLimitedRequests` | Requests blocked by rate limiter |
| `CircuitOpenRequests` | Requests blocked by open circuit |

#### Per-Status Counters

```go
StatusCounts["task_complete"]        // Count of task_complete notifications
StatusCounts["question"]             // Count of question notifications
StatusCounts["plan_ready"]           // Count of plan_ready notifications
StatusCounts["review_complete"]      // Count of review_complete notifications
StatusCounts["session_limit_reached"] // Count of session_limit_reached notifications
```

#### Performance Metrics

| Metric | Description |
|--------|-------------|
| `AverageLatencyMs` | Average request latency in milliseconds |
| `CircuitBreakerState` | Current state: `"closed"`, `"open"`, or `"half-open"` |

### Accessing Metrics

Metrics are tracked internally and accessible programmatically:

```go
import "github.com/777genius/agent-notifications/internal/webhook"

sender := webhook.New(cfg)
stats := sender.GetMetrics()

// Print metrics
fmt.Printf("Total Requests: %d\n", stats.TotalRequests)
fmt.Printf("Successful: %d\n", stats.SuccessfulRequests)
fmt.Printf("Failed: %d\n", stats.FailedRequests)
fmt.Printf("Success Rate: %.1f%%\n", stats.SuccessRate())
fmt.Printf("Average Latency: %dms\n", stats.AverageLatencyMs)
fmt.Printf("Circuit State: %s\n", stats.CircuitBreakerState)

// Per-status breakdown
for status, count := range stats.StatusCounts {
    fmt.Printf("  %s: %d\n", status, count)
}
```

### Calculated Metrics

#### Success Rate

```go
successRate := float64(stats.SuccessfulRequests) / float64(stats.TotalRequests) * 100
```

**Healthy range:** 95-100%

#### Retry Rate

```go
retryRate := float64(stats.RetriedRequests) / float64(stats.TotalRequests) * 100
```

**Healthy range:** 0-10%

#### Rate Limit Hit Rate

```go
rateLimitRate := float64(stats.RateLimitedRequests) / float64(stats.TotalRequests) * 100
```

**Healthy range:** 0-5%

## Debug Logging

All webhook operations are logged to `notification-debug.log`.

### Log Format

```
[webhook] message
```

### Common Log Messages

#### Successful Delivery

```
[webhook] Sending webhook: status=task_complete session=abc-123 request_id=550e8400-e29b-41d4-a716-446655440000
[webhook] Webhook sent successfully: latency=125ms
```

#### Retry Attempts

```
[webhook] Webhook failed (attempt 1/3): status=503, error=service unavailable
[webhook] Retrying webhook in 1s...
[webhook] Webhook retry successful (attempt 2): latency=250ms
```

#### Circuit Breaker

```
[webhook] Circuit breaker opened after 5 consecutive failures
[webhook] Circuit breaker half-open, testing recovery
[webhook] Circuit breaker closed after 2 successful requests
```

#### Rate Limiting

```
[webhook] Rate limit exceeded: requests=11 limit=10/min
[webhook] Webhook blocked by rate limiter
```

#### Errors

```
[webhook] Webhook failed (final attempt): status=500, error=internal server error
[webhook] Webhook URL invalid: parse error
[webhook] Authentication failed: status=401
```

### Viewing Logs

#### Tail logs in real-time:

```bash
tail -f notification-debug.log | grep webhook
```

#### Filter by status:

```bash
grep "status=task_complete" notification-debug.log
```

#### Find errors:

```bash
grep -i "error\|failed" notification-debug.log | grep webhook
```

#### Count successes vs failures:

```bash
grep "webhook sent successfully" notification-debug.log | wc -l
grep "webhook failed (final attempt)" notification-debug.log | wc -l
```

### Verbose Logging

Enable verbose logging for debugging:

```bash
export CLAUDE_NOTIFICATIONS_DEBUG=1
bin/claude-notifications handle-hook Stop < test-data.json
```

This adds additional debug information:
- Request payloads
- Response headers
- Retry calculations
- Circuit breaker state changes

## Request Tracing

Every webhook request includes a unique request ID for distributed tracing.

### Request ID Format

UUIDs (v4) like: `550e8400-e29b-41d4-a716-446655440000`

### HTTP Header

Request ID is sent in HTTP header:

```
X-Request-ID: 550e8400-e29b-41d4-a716-446655440000
```

### Finding Request in Logs

```bash
# Find all logs for a specific request
grep "550e8400-e29b-41d4-a716-446655440000" notification-debug.log
```

Example output:
```
[webhook] Sending webhook: status=task_complete session=abc-123 request_id=550e8400-e29b-41d4-a716-446655440000
[webhook] Webhook failed (attempt 1/3): request_id=550e8400-e29b-41d4-a716-446655440000 status=503
[webhook] Retrying webhook: request_id=550e8400-e29b-41d4-a716-446655440000
[webhook] Webhook sent successfully: request_id=550e8400-e29b-41d4-a716-446655440000 latency=250ms
```

### Correlating with External Systems

If your webhook endpoint logs requests, use the `X-Request-ID` header to correlate:

**Agent Notifications log:**
```
[webhook] Sending webhook: request_id=550e8400-... session=abc-123
```

**Your webhook endpoint log:**
```
Received webhook: request_id=550e8400-... from=agent-notifications
```

## Monitoring Examples

### Example 1: Check Success Rate

```bash
#!/bin/bash

SUCCESS=$(grep -c "webhook sent successfully" notification-debug.log)
FAILED=$(grep -c "webhook failed (final attempt)" notification-debug.log)
TOTAL=$((SUCCESS + FAILED))

if [ $TOTAL -gt 0 ]; then
    SUCCESS_RATE=$(echo "scale=1; $SUCCESS * 100 / $TOTAL" | bc)
    echo "Success Rate: $SUCCESS_RATE% ($SUCCESS/$TOTAL)"

    if (( $(echo "$SUCCESS_RATE < 95" | bc -l) )); then
        echo "⚠️  WARNING: Success rate below 95%"
    fi
fi
```

### Example 2: Monitor Circuit Breaker

```bash
#!/bin/bash

CIRCUIT_STATE=$(grep "Circuit breaker" notification-debug.log | tail -1)

if echo "$CIRCUIT_STATE" | grep -q "opened"; then
    echo "⚠️  Circuit breaker is OPEN"
elif echo "$CIRCUIT_STATE" | grep -q "half-open"; then
    echo "⚠️  Circuit breaker is HALF-OPEN (testing recovery)"
else
    echo "✅ Circuit breaker is CLOSED (healthy)"
fi
```

### Example 3: Latency Tracking

```bash
#!/bin/bash

# Extract latency values from logs
LATENCIES=$(grep "webhook sent successfully" notification-debug.log | \
    grep -oP 'latency=\K[0-9]+' | \
    awk '{sum+=$1; count++} END {print sum/count}')

echo "Average Latency: ${LATENCIES}ms"
```

### Example 4: Rate Limit Monitoring

```bash
#!/bin/bash

RATE_LIMITED=$(grep -c "Rate limit exceeded" notification-debug.log)

if [ $RATE_LIMITED -gt 0 ]; then
    echo "⚠️  Rate limit hit $RATE_LIMITED times"
    echo "Consider increasing requestsPerMinute in config"
fi
```

## Performance Tips

### Optimize Latency

1. **Reduce retry attempts** - Fewer retries = faster failures
   ```json
   {"retry": {"maxAttempts": 2}}
   ```

2. **Lower initial backoff** - Faster retry attempts
   ```json
   {"retry": {"initialBackoff": "500ms"}}
   ```

3. **Use faster endpoints** - Minimize webhook processing time

4. **Enable rate limiting** - Prevent overwhelming slow endpoints
   ```json
   {"rateLimit": {"enabled": true, "requestsPerMinute": 60}}
   ```

### Reduce Failures

1. **Enable circuit breaker** - Stop sending to failing endpoints
   ```json
   {"circuitBreaker": {"enabled": true}}
   ```

2. **Increase failure threshold** - More lenient before opening circuit
   ```json
   {"circuitBreaker": {"failureThreshold": 10}}
   ```

3. **Use reliable endpoints** - Choose platforms with high uptime

4. **Monitor logs** - Identify recurring errors and fix root cause

### Handle High Throughput

1. **Increase rate limits** - Allow more requests per minute
   ```json
   {"rateLimit": {"requestsPerMinute": 120}}
   ```

2. **Reduce retry attempts** - Fail faster on errors
   ```json
   {"retry": {"maxAttempts": 2}}
   ```

3. **Lenient circuit breaker** - Higher thresholds before opening
   ```json
   {
     "circuitBreaker": {
       "failureThreshold": 10,
       "successThreshold": 5,
       "timeout": "10s"
     }
   }
   ```

## Alerting

### Alert Conditions

**Critical Alerts:**
- Success rate < 90%
- Circuit breaker open for > 5 minutes
- No successful requests in 10 minutes

**Warning Alerts:**
- Success rate < 95%
- Retry rate > 20%
- Rate limit hit > 10 times/hour
- Average latency > 5s

### Example Alert Script

```bash
#!/bin/bash

LOG_FILE="notification-debug.log"

# Check success rate
SUCCESS=$(grep -c "webhook sent successfully" "$LOG_FILE")
FAILED=$(grep -c "webhook failed (final attempt)" "$LOG_FILE")
TOTAL=$((SUCCESS + FAILED))

if [ $TOTAL -gt 0 ]; then
    SUCCESS_RATE=$(echo "scale=1; $SUCCESS * 100 / $TOTAL" | bc)

    if (( $(echo "$SUCCESS_RATE < 90" | bc -l) )); then
        echo "🚨 CRITICAL: Webhook success rate is $SUCCESS_RATE%"
        # Send alert (email, Slack, PagerDuty, etc.)
    elif (( $(echo "$SUCCESS_RATE < 95" | bc -l) )); then
        echo "⚠️  WARNING: Webhook success rate is $SUCCESS_RATE%"
    fi
fi

# Check circuit breaker state
if grep "Circuit breaker opened" "$LOG_FILE" | tail -1 | grep -q "$(date +%Y-%m-%d)"; then
    echo "🚨 CRITICAL: Circuit breaker is OPEN"
fi
```

## Graceful Shutdown

The webhook system ensures all in-flight requests complete before shutdown.

### Shutdown Timeout

Default: 5 seconds

```go
sender := webhook.New(cfg)

// ... use sender ...

// Shutdown with custom timeout
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := sender.Shutdown(ctx); err != nil {
    log.Printf("Shutdown error: %v", err)
}
```

### Shutdown Log Messages

```
[webhook] Shutdown initiated, waiting for in-flight requests...
[webhook] Shutdown complete: 2 requests completed
```

Or if timeout exceeded:
```
[webhook] Shutdown timeout exceeded: 1 requests incomplete
```

## Learn More

- [Configuration Reference](configuration.md) - Optimize settings
- [Troubleshooting Guide](troubleshooting.md) - Fix common issues
- [Slack Setup](slack.md)
- [Discord Setup](discord.md)
- [Telegram Setup](telegram.md)
- [Custom Webhooks](custom.md)

---

[← Back to Webhook Overview](README.md)
