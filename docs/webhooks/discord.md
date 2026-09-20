# Discord Webhook Integration

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


Send Claude Code notifications to Discord channels with rich embeds.

## Overview

Discord webhooks allow you to send automated messages to channels without requiring a bot user to be online. Messages are formatted as rich embeds with color-coding, timestamps, and session information.

## Setup

### 1. Create Webhook

1. Open your Discord server
2. Go to **Server Settings** (gear icon)
3. Navigate to **Integrations** tab
4. Click **"Webhooks"** (or **"Create Webhook"** if none exist)
5. Click **"New Webhook"** button

### 2. Configure Webhook

1. **Name:** Set a name (e.g., "Agent Notifications")
2. **Channel:** Select the target channel from dropdown
3. **Avatar:** (Optional) Upload a custom icon
4. Click **"Copy Webhook URL"**

Your webhook URL will look like:
```
https://discord.com/api/webhooks/1234567890123456789/AbCdEfGhIjKlMnOpQrStUvWxYz-1234567890
```

### 3. Configure Plugin

Edit the shared file selected by `config path`:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "discord",
      "url": "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_WEBHOOK_TOKEN"
    }
  }
}
```

### 4. Test

```bash
echo '{"session_id":"test","tool_name":"ExitPlanMode"}' | \
  bin/claude-notifications handle-hook PreToolUse
```

Check your Discord channel for the notification!

## Message Format

### Rich Embeds

Messages are sent as rich embeds with:
- **Title:** Notification type (e.g., "✅ Task Completed")
- **Description:** Message content with session name
- **Color:** Status-based color coding
- **Footer:** Session ID and plugin attribution
- **Timestamp:** Message creation time

### Color Coding

| Status | Color | Hex | Discord Decimal |
|--------|-------|-----|-----------------|
| Task Complete | Green | #28a745 | 2,664,261 |
| Plan Ready | Blue | #007bff | 32,767 |
| Question | Yellow | #ffc107 | 16,761,095 |
| Review Complete | Cyan | #17a2b8 | 1,548,984 |
| Session Limit | Orange | #ff9800 | 16,750,592 |

### Example Message

```
Claude Code
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✅ Task Completed

[bold-cat] Created new authentication
system with JWT tokens

Session: abc-123 | Agent Notifications
2025-10-19 15:30:45
```

### Technical Details

Messages use Discord's **Embeds API**:

```json
{
  "username": "Claude Code",
  "embeds": [
    {
      "title": "✅ Task Completed",
      "description": "[bold-cat] Created new authentication system with JWT tokens",
      "color": 2664261,
      "footer": {
        "text": "Session: abc-123 | Agent Notifications"
      },
      "timestamp": "2025-10-19T15:30:45Z"
    }
  ]
}
```

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
      "preset": "discord",
      "url": "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_TOKEN"
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
      "preset": "discord",
      "url": "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_TOKEN",
      "retry": {
        "enabled": true,
        "maxAttempts": 3,
        "initialBackoff": "500ms",
        "maxBackoff": "5s"
      },
      "circuitBreaker": {
        "enabled": true,
        "failureThreshold": 3,
        "successThreshold": 2,
        "timeout": "20s"
      },
      "rateLimit": {
        "enabled": true,
        "requestsPerMinute": 30
      }
    }
  }
}
```

## Troubleshooting

### Webhooks Not Arriving

1. **Verify webhook URL:**
   ```bash
   curl -X POST -H 'Content-Type: application/json' \
     --data '{"content":"Test message"}' \
     YOUR_WEBHOOK_URL
   ```

2. **Check logs:**
   ```bash
   tail -f notification-debug.log | grep webhook
   ```

3. **Verify webhook exists:**
   - Go to Server Settings → Integrations → Webhooks
   - Ensure your webhook is listed and not deleted

### Wrong Channel

To change the target channel:
1. Go to **Server Settings → Integrations → Webhooks**
2. Click on your webhook
3. Change the **Channel** dropdown
4. Click **"Save Changes"**

No need to update `config.json` - the URL stays the same!

### Rate Limiting

Discord rate limits:
- **5 requests per 2 seconds per webhook**
- **Burst:** Up to 5 requests immediately, then rate limited

If you hit rate limits (429 errors), configure rate limiting:
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

### Webhook Not Found (404)

If you get 404 errors:
- Webhook may have been deleted
- URL may be incorrect (check for typos)
- Token portion of URL may have been regenerated

**Solution:** Create a new webhook and update your config.

## Security

⚠️ **CRITICAL SECURITY WARNING**

Discord webhooks are a **common attack vector**. If an attacker gets your webhook URL, they can:
- Post messages impersonating your notifications
- Spam your channel
- Send malicious links to your team

### Best Practices

1. **Never commit webhook URLs to git**
   - Use config files that are .gitignored
   - Use environment variables for CI/CD

2. **Guard webhook URLs closely**
   - Treat them like passwords
   - Don't share in public channels
   - Don't paste in screenshots

3. **Limit webhook permissions**
   - Only give webhook creation rights to trusted members
   - Review webhooks regularly in Server Settings

4. **Rotate webhooks if compromised**
   - Delete old webhook
   - Create new webhook
   - Update config immediately

5. **Monitor for unusual activity**
   - Watch for unexpected messages
   - Check webhook metrics for anomalies

6. **Use role permissions**
   - Restrict who can create webhooks in Server Settings → Roles

## Best Practices

1. **Dedicated channel** - Create an `#agent-notifications` channel
2. **Custom avatar** - Upload the Agent Notifications logo for easy identification
3. **Enable retry** - Handle transient network failures
4. **Monitor rate limits** - Discord has stricter limits than Slack
5. **Test thoroughly** - Use webhook.site before production
6. **Secure webhook URLs** - See security section above

## Advanced Features

### Custom Username

Override the webhook username per message:
```json
{
  "username": "Claude Code - Production",
  "embeds": [...]
}
```

Note: This plugin uses a fixed username "Claude Code".

### Mentions

Discord webhooks support mentions in embeds:
- Users: `<@USER_ID>`
- Roles: `<@&ROLE_ID>`
- Channels: `<#CHANNEL_ID>`

Note: This plugin doesn't include mentions by default.

## Learn More

- [Configuration Options](configuration.md) - Retry, circuit breaker, rate limiting
- [Monitoring](monitoring.md) - Metrics and debugging
- [Troubleshooting](troubleshooting.md) - Common issues

## Official Documentation

- [Discord Webhooks Guide](https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks)
- [Discord Webhook API](https://discord.com/developers/docs/resources/webhook)
- [Discord Embeds Documentation](https://discord.com/developers/docs/resources/channel#embed-object)

---

[← Back to Webhook Overview](README.md)
