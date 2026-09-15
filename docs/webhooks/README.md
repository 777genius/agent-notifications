# Webhook Integration Guide

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


**Professional webhook system with enterprise-grade reliability patterns.**

Send Claude Code notifications to Slack, Discord, Telegram, Lark/Feishu, or custom endpoints with built-in retry, circuit breaker, and rate limiting.

## Quick Start

### 1. Enable Webhooks

Edit the shared file selected by `config path`:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "slack",
      "url": "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
    }
  }
}
```

### 2. Test Your Setup

```bash
echo '{"session_id":"test","tool_name":"ExitPlanMode"}' | \
  bin/claude-notifications handle-hook PreToolUse
```

### Per-Status Webhook Control

If you want desktop notifications for a status but do not want webhook noise,
use a per-status webhook override in the shared file selected by `config path`:

```json
{
  "statuses": {
    "question": {
      "webhook": {
        "enabled": false
      }
    }
  }
}
```

This only affects the webhook channel. If you set `statuses.<name>.enabled` to
`false`, both desktop and webhook notifications are disabled for that status.

## Platform Guides

Choose your platform for detailed setup instructions:

### Popular Platforms

- **[Slack](slack.md)** - Color-coded attachments in Slack channels
- **[Discord](discord.md)** - Rich embeds with timestamps
- **[Telegram](telegram.md)** - HTML-formatted messages via bot
- **[Lark/Feishu](lark.md)** - Interactive cards with colored headers

### Other Options

- **[Custom Webhooks](custom.md)** - Integrate with any webhook-compatible service

## Features

- **Platform presets**: Pre-configured formatting for Slack, Discord, and Telegram
- **Custom endpoints**: Support for any webhook-compatible service
- **Dynamic fields**: Add runtime values to headers and JSON payloads via templates
- **Retry mechanism**: Exponential backoff with jitter (1-3 attempts)
- **Circuit breaker**: Automatic failure detection and recovery
- **Rate limiting**: Token bucket algorithm to prevent API overload
- **Rich formatting**: Color-coded messages with platform-specific layouts
- **Request tracing**: UUID-based request IDs for debugging
- **Metrics tracking**: Success/failure rates, latency, circuit breaker state
- **Session names**: Friendly identifiers like `[bold-cat]` for easy tracking

## Documentation

### Configuration
- **[Configuration Reference](configuration.md)** - Retry, circuit breaker, and rate limiting options

### Monitoring & Debugging
- **[Monitoring & Metrics](monitoring.md)** - Track success rates, latency, and system health
- **[Troubleshooting](troubleshooting.md)** - Common issues and debugging tips

## Status Types

The system automatically detects and formats these statuses:

| Status | Title | Emoji |
|--------|-------|-------|
| `task_complete` | Task Completed | ✅ |
| `review_complete` | Review Complete | 🔍 |
| `question` | Claude Has Questions | ❓ |
| `plan_ready` | Plan Ready | 📋 |
| `session_limit_reached` | Session Limit Reached | ⏱️ |

## Best Practices

1. **Always enable retry** - Transient network failures are common
2. **Use circuit breaker in production** - Prevents cascading failures
3. **Set appropriate rate limits** - Match your webhook provider's limits
4. **Monitor metrics** - Track success rates and latency
5. **Use platform presets** - Leverage built-in formatting for better UX
6. **Test with webhook.site** - Verify payloads before production

## Security

- Store webhook URLs in config files (not in code)
- Use HTTPS for all webhook endpoints
- Rotate API keys/tokens regularly
- Use custom headers for authentication when possible
- Monitor for unusual activity in webhook metrics

## Support

For issues, questions, or contributions:
- GitHub Issues: https://github.com/777genius/agent-notifications/issues
- Main Documentation: [README.md](../../README.md)

---

**Built with reliability patterns from production systems.**
