# Custom Webhook Integration

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


Integrate Claude Code notifications with any webhook-compatible service.

## Overview

Custom webhooks allow you to send notifications to any HTTP endpoint that accepts JSON payloads. This is useful for:
- Custom internal APIs
- Webhook services (Zapier, n8n, Make)
- Monitoring systems (PagerDuty, Datadog)
- Chat platforms not supported by presets
- Custom notification routing

## Basic Setup

### Configuration

Edit the shared file selected by `config path`:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://your-webhook-endpoint.com/notifications",
      "format": "json",
      "payloadFields": {}
    }
  }
}
```

**Key fields:**
- `preset: ""` - Empty string for custom webhooks
- `url` - Your webhook endpoint
- `format: "json"` - Payload format (currently only JSON supported)

### Payload Format

Custom webhooks receive a JSON payload:

```json
{
  "schema_version": "1.0",
  "status": "task_complete",
  "notification_type": "task_complete",
  "agent_source": "claude",
  "message": "[bold-cat] Created new authentication system with JWT tokens",
  "session_id": "abc-123",
  "timestamp": "2026-04-13T12:34:56Z",
  "source": "claude-notifications",
  "title": "✅ Completed"
}
```

**Fields:**
- `schema_version` (string) - Payload contract version, currently `"1.0"`. Bumped only on breaking changes (field removed/renamed/retyped); new optional fields can be added without a bump.
- `status` (string) - One of: `task_complete`, `review_complete`, `question`, `plan_ready`, `session_limit_reached`, `api_error`, `api_error_overloaded`, `permission_request`
- `notification_type` (string) - Same enum and value as `status`. Kept as a separate field so it can be relied on for identity purposes even if `status` ever takes on request/response nuance beyond a plain type tag.
- `agent_source` (string) - Which agent produced the event: `claude` or `codex`. More values will be added as more agents are supported; treat unknown values as forward-compatible rather than erroring.
- `message` (string) - Notification message with session name
- `session_id` (string) - Unique session identifier
- `timestamp` (string) - RFC3339 timestamp
- `source` (string) - Always `claude-notifications` (identifies the plugin, not the agent — see `agent_source` for that)
- `title` (string) - Status title from config

The formal contract also lives as a JSON Schema at [`docs/webhooks/payload-schema.json`](payload-schema.json), for consumers that want to validate payloads programmatically.

Slack, Discord, Telegram, and Lark presets also now include the agent name (`Claude Code` / `Codex`) in their footer/author/username so it is visible to humans reading the chat message, not just to machine consumers of the custom JSON format. Discord shows it once, via the top-level `username` field, rather than repeating it in the footer.

`schema_version`, `status`, `notification_type`, and `agent_source` are reserved: `payloadFields` cannot override them, even if your config sets one of those keys. Any such entry is dropped (with a warning in the debug log) instead of silently corrupting the identity contract other systems rely on.

## Dynamic Fields

You can inject runtime values into header values and extra JSON payload fields.

### Example

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://your-webhook-endpoint.com/notifications",
      "format": "json",
      "headers": {
        "Authorization": "Bearer ${{env.WEBHOOK_TOKEN}}",
        "X-Git-Branch": "${{git.branch}}"
      },
      "payloadFields": {
        "context": {
          "git": {
            "userEmail": "${{git.user.email}}",
            "commit": "${{git.commit.short_hash}}"
          },
          "environment": "${{env.DEPLOY_ENV}}"
        },
        "session_name": "${{session_name}}",
        "sent_at_unix": "${{time.unix}}"
      }
    }
  }
}
```

### Supported Templates

- `${{status}}`, `${{title}}`, `${{message}}`
- `${{agent_source}}` - originating agent, `claude` or `codex`
- `${{session_id}}`, `${{session_name}}`
- `${{cwd}}`, `${{folder}}`
- `${{time.rfc3339}}`, `${{time.unix}}`, `${{time.unix_ms}}`
- `${{env.MY_VAR}}`
- `${{git.branch}}`
- `${{git.user.name}}`, `${{git.user.email}}`
- `${{git.commit.hash}}`, `${{git.commit.short_hash}}`
- `${{git.commit.author.name}}`, `${{git.commit.author.email}}`

Notes:
- `payloadFields` is merged into the generated JSON payload
- Missing template values are skipped instead of failing the webhook
- `payloadFields` is not supported with `format: "text"`

## Authentication

### Bearer Token

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://api.yourservice.com/webhooks/claude",
      "format": "json",
      "headers": {
        "Authorization": "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
      }
    }
  }
}
```

### API Key

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://api.yourservice.com/webhooks",
      "format": "json",
      "headers": {
        "X-API-Key": "your-api-key-here",
        "X-Service-Name": "claude-notifications"
      }
    }
  }
}
```

### Basic Auth

Encode credentials as `base64(username:password)`:

```bash
echo -n "username:password" | base64
# Output: dXNlcm5hbWU6cGFzc3dvcmQ=
```

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://api.yourservice.com/webhooks",
      "format": "json",
      "headers": {
        "Authorization": "Basic dXNlcm5hbWU6cGFzc3dvcmQ="
      }
    }
  }
}
```

### Multiple Headers

You can include multiple custom headers:

```json
{
  "headers": {
    "Authorization": "Bearer YOUR_TOKEN",
    "X-API-Key": "your-api-key",
    "X-Service-Name": "claude-notifications",
    "X-Environment": "production",
    "Content-Type": "application/json"
  }
}
```

**Note:** `Content-Type: application/json` is automatically added, you don't need to include it.

## Configuration Examples

### Minimal Configuration

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://webhook.site/unique-url-here",
      "format": "json"
    }
  }
}
```

### With Authentication

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
        "X-API-Key": "your-api-key-here"
      }
    }
  }
}
```

### High-Throughput Configuration

For environments with frequent notifications:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "",
      "url": "https://api.yourservice.com/webhooks/claude",
      "format": "json",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN"
      },
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

### Production Configuration

Balanced for reliability and performance:

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
        "X-Service-Name": "claude-notifications",
        "X-Environment": "production"
      },
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
        "requestsPerMinute": 60
      }
    }
  }
}
```

## Common Integrations

### ntfy.sh
_push notifications to Android/iOS/browsers/etc., FOSS_

1. Install [any app](https://ntfy.sh/)
2. Subscribe to `your_topic_name`
3. Configure Agent Notifications:

```json
"webhook": {
    "enabled": true,
    "preset": "",
    "url": "https://ntfy.sh/your_topic_name",
    "format": "json",
    "headers": {
        "X-Message": "{{.message}}",
        "X-Title": "{{.title}}",
        "X-Priority": "{{if or (eq .status \"task_complete\") (eq .status \"review_complete\")}}3{{else}}4{{end}}",
        "X-Template": "yes"
    }
},
```

In the example above templates are used to interpret this plugin's default json, see the docs for more.
You can also use ntfy <ins>as middleware transformer for other webhooks</ins> or relay to channels like email, etc.

### Zapier

1. Create a **Webhook by Zapier** trigger
2. Copy the webhook URL
3. Configure Agent Notifications:

```json
{
  "webhook": {
    "enabled": true,
    "preset": "",
    "url": "https://hooks.zapier.com/hooks/catch/YOUR_HOOK_ID/",
    "format": "json"
  }
}
```

### n8n

1. Add **Webhook** node to workflow
2. Set method to `POST`
3. Copy webhook URL
4. Configure Agent Notifications with the URL

### Make (formerly Integromat)

1. Create scenario with **Webhooks** module
2. Add **Custom webhook**
3. Copy webhook URL
4. Configure Agent Notifications with the URL

### PagerDuty

Use PagerDuty's Events API v2:

```json
{
  "webhook": {
    "enabled": true,
    "preset": "",
    "url": "https://events.pagerduty.com/v2/enqueue",
    "format": "json",
    "headers": {
      "Authorization": "Token token=YOUR_INTEGRATION_KEY"
    }
  }
}
```

**Note:** You'll need to transform the payload format. Consider using Zapier/n8n as middleware.

### Microsoft Teams

Teams uses a different JSON structure. Consider using the webhook as a bridge:

```json
{
  "webhook": {
    "enabled": true,
    "preset": "",
    "url": "https://your-middleware.com/teams-adapter",
    "format": "json"
  }
}
```

Your middleware should transform the payload to Teams' format.

## Testing

### webhook.site

Perfect for testing webhook payloads:

1. Go to https://webhook.site/
2. Copy your unique URL
3. Configure Agent Notifications:

```json
{
  "webhook": {
    "url": "https://webhook.site/your-unique-url"
  }
}
```

4. Trigger a notification
5. Check webhook.site for the payload

### RequestBin

Alternative to webhook.site:

1. Go to https://requestbin.com/
2. Create a request bin
3. Use the endpoint URL in your config
4. View captured requests

### curl Testing

Test your endpoint manually:

```bash
curl -X POST https://your-webhook-endpoint.com/notifications \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "status": "task_complete",
    "message": "[test-session] Test notification",
    "session_id": "test-123",
    "timestamp": 1729353045
  }'
```

## Response Handling

### Success (2xx)

Any HTTP status code in the 2xx range is considered successful:
- `200 OK`
- `201 Created`
- `202 Accepted`
- `204 No Content`

### Retry (5xx, 429)

These status codes trigger automatic retry:
- `500 Internal Server Error`
- `502 Bad Gateway`
- `503 Service Unavailable`
- `504 Gateway Timeout`
- `429 Too Many Requests`

### No Retry (4xx)

Client errors don't trigger retry (except 429):
- `400 Bad Request` - Check payload format
- `401 Unauthorized` - Check authentication
- `403 Forbidden` - Check permissions
- `404 Not Found` - Check URL

## Troubleshooting

### Webhooks Not Arriving

1. **Test endpoint with curl** (see Testing section)
2. **Check logs:**
   ```bash
   tail -f notification-debug.log | grep webhook
   ```
3. **Verify URL** - Check for typos
4. **Test with webhook.site** - Isolate issue

### Authentication Failing

1. **Check header format:**
   - Bearer: `Authorization: Bearer TOKEN`
   - API Key: `X-API-Key: KEY`
   - Basic: `Authorization: Basic BASE64`

2. **Verify credentials** - Test with curl

3. **Check token expiration** - Refresh if needed

### Timeout Errors

1. **Check endpoint response time** - Should respond < 10s
2. **Increase retry backoff:**
   ```json
   {
     "retry": {
       "initialBackoff": "5s",
       "maxBackoff": "30s"
     }
   }
   ```

### Rate Limiting

If you're hitting rate limits:

```json
{
  "webhook": {
    "rateLimit": {
      "enabled": true,
      "requestsPerMinute": 30
    }
  }
}
```

## Best Practices

1. **Always use HTTPS** - Never use plain HTTP for webhooks
2. **Implement authentication** - Use API keys or bearer tokens
3. **Enable retry** - Handle transient failures
4. **Monitor webhook health** - Track success rates
5. **Use circuit breaker** - Prevent cascading failures
6. **Rate limit appropriately** - Match your endpoint's capacity
7. **Test thoroughly** - Use webhook.site before production
8. **Rotate credentials** - Update tokens/keys regularly
9. **Log webhook activity** - Track all requests/responses
10. **Handle all status codes** - Return appropriate HTTP codes

## Security Considerations

- **Use HTTPS only** - Encrypt data in transit
- **Authenticate requests** - Verify sender identity
- **Validate payloads** - Check structure and content
- **Rotate credentials** - Change tokens periodically
- **Limit IP access** - Whitelist if possible
- **Monitor for abuse** - Watch for unusual patterns
- **Use short-lived tokens** - JWT with expiration
- **Log all requests** - Audit trail for security

## Learn More

- [Configuration Options](configuration.md) - Retry, circuit breaker, rate limiting
- [Monitoring](monitoring.md) - Metrics and debugging
- [Troubleshooting](troubleshooting.md) - Common issues

---

[← Back to Webhook Overview](README.md)
