# Webhook Configuration Reference

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


Complete configuration options for webhook reliability features.

## Table of Contents

- [Basic Configuration](#basic-configuration)
- [Retry Configuration](#retry-configuration)
- [Circuit Breaker](#circuit-breaker)
- [Rate Limiting](#rate-limiting)
- [Complete Examples](#complete-examples)

## Basic Configuration

### Required Fields

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "slack|discord|telegram|",
      "url": "https://your-webhook-url"
    }
  }
}
```

**Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | boolean | Yes | Enable/disable webhook notifications |
| `preset` | string | Yes | Platform preset: `"slack"`, `"discord"`, `"telegram"`, `"lark"`, or `""` (custom) |
| `url` | string | Yes | Webhook endpoint URL |

### Optional Fields

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://...",
      "chat_id": "",
      "format": "json",
      "headers": {},
      "payloadFields": {}
    }
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `chat_id` | string | For Telegram | Telegram chat/group ID |
| `format` | string | No | Payload format (default: `"json"`) |
| `headers` | object | No | Custom HTTP headers. Values can include templates like `${{env.API_TOKEN}}` |
| `payloadFields` | object | No | Extra JSON fields merged into the generated payload after template resolution |

### Template Variables

Header values and `payloadFields` string values can use templates:

- `${{status}}`, `${{title}}`, `${{message}}`
- `${{session_id}}`, `${{session_name}}`
- `${{cwd}}`, `${{folder}}`
- `${{time.rfc3339}}`, `${{time.unix}}`, `${{time.unix_ms}}`
- `${{env.MY_VAR}}`
- `${{git.branch}}`
- `${{git.user.name}}`, `${{git.user.email}}`
- `${{git.commit.hash}}`, `${{git.commit.short_hash}}`
- `${{git.commit.author.name}}`, `${{git.commit.author.email}}`

Notes:
- `payloadFields` works with JSON payloads only
- Missing template values are skipped instead of failing the webhook request

## Retry Configuration

Automatic retry with exponential backoff for transient failures.

### Configuration

```json
{
  "notifications": {
    "webhook": {
      "retry": {
        "enabled": true,
        "maxAttempts": 3,
        "initialBackoff": "1s",
        "maxBackoff": "10s"
      }
    }
  }
}
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `enabled` | boolean | `true` | Enable retry mechanism |
| `maxAttempts` | integer | `3` | Maximum retry attempts (1-10) |
| `initialBackoff` | duration | `"1s"` | Initial backoff delay |
| `maxBackoff` | duration | `"10s"` | Maximum backoff delay |

### Duration Format

Durations use Go duration syntax:
- `"1s"` - 1 second
- `"500ms"` - 500 milliseconds
- `"2m"` - 2 minutes
- `"30s"` - 30 seconds

### Backoff Algorithm

**Exponential backoff with jitter:**

```
Attempt 1: initialBackoff + random(0, initialBackoff * 0.25)
Attempt 2: 2 * initialBackoff + jitter
Attempt 3: 4 * initialBackoff + jitter (capped at maxBackoff)
```

**Example (initialBackoff=1s, maxBackoff=10s):**
- Attempt 1: ~1s (0.75s - 1.25s)
- Attempt 2: ~2s (1.5s - 2.5s)
- Attempt 3: ~4s (3s - 5s)

### Retryable Errors

Retry is triggered for:
- **5xx server errors** (500, 502, 503, 504)
- **429 Too Many Requests**
- **Network errors** (connection timeout, DNS failure)

### Non-Retryable Errors

No retry for:
- **4xx client errors** (except 429)
  - 400 Bad Request
  - 401 Unauthorized
  - 403 Forbidden
  - 404 Not Found
- **Context cancellation**
- **Invalid URL/configuration**

### Recommendations

| Scenario | maxAttempts | initialBackoff | maxBackoff |
|----------|-------------|----------------|------------|
| **Low latency** | 2 | 500ms | 2s |
| **Balanced** | 3 | 1s | 10s |
| **Reliable** | 5 | 2s | 30s |
| **High-throughput** | 2 | 500ms | 2s |

## Circuit Breaker

Automatic failure detection and recovery to prevent cascading failures.

### Configuration

```json
{
  "notifications": {
    "webhook": {
      "circuitBreaker": {
        "enabled": true,
        "failureThreshold": 5,
        "successThreshold": 2,
        "timeout": "30s"
      }
    }
  }
}
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `enabled` | boolean | `true` | Enable circuit breaker |
| `failureThreshold` | integer | `5` | Consecutive failures to open circuit |
| `successThreshold` | integer | `2` | Consecutive successes to close circuit |
| `timeout` | duration | `"30s"` | Time in open state before half-open |

### States

The circuit breaker has three states:

#### 1. Closed (Normal Operation)

- All requests pass through
- Failures are counted
- After `failureThreshold` failures → **Open**

#### 2. Open (Failing)

- All requests immediately fail with `ErrCircuitOpen`
- No requests sent to webhook
- After `timeout` duration → **Half-Open**

#### 3. Half-Open (Testing Recovery)

- Limited requests allowed through to test recovery
- After `successThreshold` successes → **Closed**
- After 1 failure → **Open**

### State Diagram

```
       5 failures
Closed ─────────────→ Open
   ↑                    │
   │                    │ 30s timeout
   │                    ↓
   │                Half-Open
   │                    │
   │    2 successes     │ 1 failure
   └────────────────────┴───────────→ Open
```

### Failure Criteria

What counts as a failure:
- HTTP status 5xx
- HTTP status 429 (after retry exhaustion)
- Network errors (timeout, connection refused)
- Context cancellation

What does NOT count as a failure:
- HTTP 2xx (success)
- HTTP 4xx (except 429) - these are client errors, not server failures

### Recommendations

| Scenario | failureThreshold | successThreshold | timeout |
|----------|------------------|------------------|---------|
| **Strict** | 3 | 3 | 20s |
| **Balanced** | 5 | 2 | 30s |
| **Lenient** | 10 | 2 | 60s |
| **High-throughput** | 10 | 5 | 10s |

### When to Disable

Disable circuit breaker when:
- Webhook endpoint is extremely reliable (e.g., Telegram Bot API)
- You want guaranteed delivery attempts regardless of failures
- Testing/debugging webhook issues

```json
{
  "circuitBreaker": {
    "enabled": false
  }
}
```

## Rate Limiting

Token bucket rate limiting to prevent API overload.

### Configuration

```json
{
  "notifications": {
    "webhook": {
      "rateLimit": {
        "enabled": true,
        "requestsPerMinute": 10
      }
    }
  }
}
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `enabled` | boolean | `true` | Enable rate limiting |
| `requestsPerMinute` | integer | `10` | Maximum requests per minute |

### Algorithm

**Token Bucket:**

1. Bucket starts with `requestsPerMinute` tokens
2. Each request consumes 1 token
3. Tokens refill at rate of `requestsPerMinute / 60` per second
4. Requests without available tokens return `ErrRateLimitExceeded`

### Behavior

**Non-blocking:** Rate limiter immediately returns error when no tokens available, it does NOT wait.

**Burst support:** Allows initial burst up to `requestsPerMinute` tokens.

**Example (requestsPerMinute=10):**
- Can send 10 requests immediately
- Then limited to 1 request every 6 seconds
- Tokens accumulate if idle (up to 10)

### Platform Limits

| Platform | Official Limit | Recommended Config |
|----------|----------------|-------------------|
| **Slack** | ~1 msg/sec | `requestsPerMinute: 20` |
| **Discord** | 5 msg/2sec | `requestsPerMinute: 30` |
| **Telegram** | 30 msg/sec | `requestsPerMinute: 60` |
| **Lark/Feishu** | ~1 msg/sec | `requestsPerMinute: 20` |
| **Custom** | Varies | Match endpoint limit |

### Recommendations

| Scenario | requestsPerMinute |
|----------|-------------------|
| **Conservative** | 10 |
| **Balanced** | 30 |
| **Aggressive** | 60 |
| **High-throughput** | 120 |

### When to Disable

Disable rate limiting when:
- Your endpoint has no rate limits
- You have very few notifications
- Testing/debugging

```json
{
  "rateLimit": {
    "enabled": false
  }
}
```

## Complete Examples

### Minimal Configuration

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "slack",
      "url": "https://hooks.slack.com/services/..."
    }
  }
}
```

### Development Configuration

Fast failures, less aggressive retry:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "slack",
      "url": "https://hooks.slack.com/services/...",
      "retry": {
        "enabled": true,
        "maxAttempts": 2,
        "initialBackoff": "500ms",
        "maxBackoff": "2s"
      },
      "circuitBreaker": {
        "enabled": false
      },
      "rateLimit": {
        "enabled": false
      }
    }
  }
}
```

### Production Configuration (Balanced)

Good balance of reliability and performance:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "slack",
      "url": "https://hooks.slack.com/services/...",
      "retry": {
        "enabled": true,
        "maxAttempts": 3,
        "initialBackoff": "1s",
        "maxBackoff": "10s"
      },
      "circuitBreaker": {
        "enabled": true,
        "failureThreshold": 5,
        "successThreshold": 2,
        "timeout": "30s"
      },
      "rateLimit": {
        "enabled": true,
        "requestsPerMinute": 20
      }
    }
  }
}
```

### Production Configuration (High Reliability)

Maximum reliability, slower recovery:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "slack",
      "url": "https://hooks.slack.com/services/...",
      "retry": {
        "enabled": true,
        "maxAttempts": 5,
        "initialBackoff": "2s",
        "maxBackoff": "30s"
      },
      "circuitBreaker": {
        "enabled": true,
        "failureThreshold": 10,
        "successThreshold": 3,
        "timeout": "60s"
      },
      "rateLimit": {
        "enabled": true,
        "requestsPerMinute": 30
      }
    }
  }
}
```

### High-Throughput Configuration

Optimized for frequent notifications:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "discord",
      "url": "https://discord.com/api/webhooks/...",
      "retry": {
        "enabled": true,
        "maxAttempts": 2,
        "initialBackoff": "500ms",
        "maxBackoff": "2s"
      },
      "circuitBreaker": {
        "enabled": true,
        "failureThreshold": 10,
        "successThreshold": 5,
        "timeout": "10s"
      },
      "rateLimit": {
        "enabled": true,
        "requestsPerMinute": 120
      }
    }
  }
}
```

### Custom Webhook with Auth

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://api.yourservice.com/webhooks/claude",
      "format": "json",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN",
        "X-Service-Name": "claude-notifications"
      },
      "retry": {
        "enabled": true,
        "maxAttempts": 3,
        "initialBackoff": "1s",
        "maxBackoff": "10s"
      },
      "circuitBreaker": {
        "enabled": true,
        "failureThreshold": 5,
        "successThreshold": 2,
        "timeout": "30s"
      },
      "rateLimit": {
        "enabled": true,
        "requestsPerMinute": 60
      }
    }
  }
}
```

## Best Practices

1. **Always enable retry** - Network failures are inevitable
2. **Use circuit breaker in production** - Prevents wasted resources on failing endpoints
3. **Match rate limits to platform** - Respect webhook provider limits
4. **Start conservative, then tune** - Begin with default settings, adjust based on metrics
5. **Monitor webhook metrics** - Track success rates and adjust configuration
6. **Test configuration changes** - Use webhook.site to verify behavior
7. **Document your settings** - Comment why you chose specific values

## Learn More

- [Slack Setup](slack.md)
- [Discord Setup](discord.md)
- [Telegram Setup](telegram.md)
- [Lark/Feishu Setup](lark.md)
- [Custom Webhooks](custom.md)
- [Monitoring & Metrics](monitoring.md)
- [Troubleshooting](troubleshooting.md)

---

[← Back to Webhook Overview](README.md)
