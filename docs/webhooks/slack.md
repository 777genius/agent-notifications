# Slack Webhook Integration

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


Send Claude Code notifications to Slack channels with color-coded attachments.

## Overview

Slack integration uses Incoming Webhooks to post messages directly to channels. Messages are formatted with color-coded attachments for easy visual identification of notification types.

## Setup

### 1. Create Incoming Webhook

1. Go to https://api.slack.com/apps
2. Click **"Create New App"** → **"From scratch"**
3. Give your app a name and select your workspace
4. Click **"Create App"**

### 2. Enable Incoming Webhooks

1. In your app settings, navigate to **Features → Incoming Webhooks**
2. Toggle **"Activate Incoming Webhooks"** to **On**
3. Click **"Add New Webhook to Workspace"**
4. Select the channel where notifications will be posted
5. Click **"Authorize"**

**Note:** For private channels, you must join the channel before adding the webhook.

### 3. Copy Webhook URL

Your webhook URL will look like:
```
https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXX
```

**Keep this URL secure!** Anyone with this URL can post to your channel.

### 4. Configure Plugin

Edit the shared file selected by `config path`:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "slack",
      "url": "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXX"
    }
  }
}
```

### 5. Test

```bash
echo '{"session_id":"test","tool_name":"ExitPlanMode"}' | \
  bin/claude-notifications handle-hook PreToolUse
```

Check your Slack channel for the notification!

## Message Format

### Color Coding

Messages use color-coded vertical bars for visual identification:

| Status | Color | Hex | Description |
|--------|-------|-----|-------------|
| Task Complete | Green | #28a745 | Task finished successfully |
| Plan Ready | Blue | #007bff | Plan ready for review |
| Question | Yellow | #ffc107 | Claude has questions |
| Review Complete | Cyan | #17a2b8 | Review completed |
| Session Limit | Orange | #ff9800 | Session limit reached |

### Example Message

```
┌─────────────────────────────┐
│ ✅ Task Completed           │
│                             │
│ [bold-cat] Created new      │
│ authentication system with  │
│ JWT tokens                  │
│                             │
│ Session: abc-123            │
└─────────────────────────────┘
```

### Technical Details

Messages use Slack's **Attachments API**:

```json
{
  "attachments": [
    {
      "color": "#28a745",
      "title": "✅ Task Completed",
      "text": "[bold-cat] Created new authentication system with JWT tokens",
      "footer": "Session: abc-123 | Agent Notifications",
      "ts": 1729353045
    }
  ]
}
```

**Note:** Slack now considers attachments a **legacy feature** and recommends using [Block Kit](https://api.slack.com/block-kit) for new integrations. However, attachments continue to work and are simpler for basic notifications. This plugin uses attachments for compatibility and ease of use.

## Configuration Examples

### Basic Configuration

```json
{
  "notifications": {
    "desktop": {
      "enabled": true,
      "sound": true
    },
    "webhook": {
      "enabled": true,
      "preset": "slack",
      "url": "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
    },
    "suppressQuestionAfterTaskCompleteSeconds": 12
  }
}
```

### With Retry & Circuit Breaker

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "slack",
      "url": "https://hooks.slack.com/services/YOUR/WEBHOOK/URL",
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

## Troubleshooting

### Webhooks Not Arriving

1. **Verify webhook URL:**
   ```bash
   curl -X POST -H 'Content-type: application/json' \
     --data '{"text":"Test message"}' \
     YOUR_WEBHOOK_URL
   ```

2. **Check logs:**
   ```bash
   tail -f notification-debug.log | grep webhook
   ```

3. **Verify config:**
   ```bash
   "$NOTIFICATIONS_BIN" config inspect --json
   ```

### Wrong Channel

You cannot change the target channel via configuration. To change channels:
1. Go to your Slack app settings
2. Features → Incoming Webhooks
3. Delete the old webhook
4. Create a new webhook for the desired channel
5. Update your `config.json` with the new URL

### Rate Limiting

Slack rate limits vary by workspace tier:
- **Free:** ~1 message per second per webhook
- **Pro/Business:** Higher limits

If you hit rate limits:
```json
{
  "webhook": {
    "rateLimit": {
      "enabled": true,
      "requestsPerMinute": 20
    }
  }
}
```

## Best Practices

1. **Use dedicated channels** - Create a `#claude-notifications` channel
2. **One webhook per channel** - Don't reuse webhooks across projects
3. **Secure your URL** - Never commit webhook URLs to git
4. **Enable retry** - Handle transient network failures
5. **Monitor metrics** - Track delivery success rates
6. **Test before deploying** - Use webhook.site for testing

## Security

- **Never commit webhook URLs** - Use environment variables or config files
- **Rotate webhooks periodically** - Create new webhooks every few months
- **Limit access** - Only share webhooks with team members
- **Use HTTPS** - Slack webhooks are HTTPS-only (enforced)
- **Monitor unusual activity** - Check for unexpected messages

## Learn More

- [Configuration Options](configuration.md) - Retry, circuit breaker, rate limiting
- [Monitoring](monitoring.md) - Metrics and debugging
- [Troubleshooting](troubleshooting.md) - Common issues

## Official Documentation

- [Slack Incoming Webhooks](https://api.slack.com/messaging/webhooks)
- [Message Attachments](https://api.slack.com/reference/messaging/attachments)
- [Block Kit Builder](https://api.slack.com/block-kit) (for advanced formatting)

---

[← Back to Webhook Overview](README.md)
