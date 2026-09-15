# Lark/Feishu Webhook Integration

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


Send Claude Code notifications to Lark (飞书) with rich interactive cards.

## Overview

Lark integration uses custom bot webhooks to post messages with interactive cards. Messages are formatted with colored headers for easy visual identification of notification types.

Lark is the international version of Feishu (ByteDance's enterprise collaboration platform).

## Setup

### 1. Create Custom Bot

1. Open Lark and go to your target group chat
2. Click **Group Settings** (gear icon) → **Add Bot** → **Custom Bot**
3. For Feishu (Chinese version), click **群设置** → **群机器人** → **添加机器人** → **自定义机器人**
4. Give your bot a name (e.g., "Agent Notifications")
5. Upload an avatar (optional)
6. Click **Add** / **添加**

### 2. Copy Webhook URL

Your webhook URL will look like:
```
https://open.feishu.cn/open-apis/bot/v2/hook/XXXXXXXXXXXXXXXXXXXX
```
or
```
https://open.larksuite.com/open-apis/bot/v2/hook/XXXXXXXXXXXXXXXXXXXX
```

**Keep this URL secure!** Anyone with this URL can post to your group.

### 3. Configure Plugin

Edit the shared file selected by `config path`:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "lark",
      "url": "https://open.feishu.cn/open-apis/bot/v2/hook/XXXXXXXXXXXXXXXXXXXX"
    }
  }
}
```

### 4. Test

```bash
echo '{"session_id":"test","tool_name":"ExitPlanMode"}' | \
  bin/claude-notifications handle-hook PreToolUse
```

Check your Lark group for the notification!

## Message Format

### Color Coding

Messages use colored headers for visual identification:

| Status | Color | Template | Description |
|--------|-------|----------|-------------|
| Task Complete | Green | `green` | Task finished successfully |
| Plan Ready | Blue | `blue` | Plan ready for review |
| Question | Red | `red` | Claude has questions |
| Review Complete | Yellow | `yellow` | Review completed |
| Session Limit | Grey | `grey` | Session limit reached |

### Example Message

```
┌─────────────────────────────────┐
│ ✅ Task Completed               │
│                                 │
│ [bold-cat] Created new          │
│ authentication system with      │
│ JWT tokens                      │
│                                 │
│ ─────────────────────────────   │
│                                 │
│ Session: abc-123                │
└─────────────────────────────────┘
```

### Technical Details

Messages use Lark's **Interactive Card API**:

```json
{
  "msg_type": "interactive",
  "card": {
    "config": {
      "wide_screen_mode": true
    },
    "header": {
      "title": {
        "tag": "plain_text",
        "content": "✅ Task Completed"
      },
      "template": "green"
    },
    "elements": [
      {
        "tag": "div",
        "text": {
          "tag": "plain_text",
          "content": "[bold-cat] Created new authentication system"
        }
      },
      {
        "tag": "hr"
      },
      {
        "tag": "div",
        "text": {
          "tag": "plain_text",
          "content": "Session: abc-123"
        }
      }
    ]
  }
}
```

**Note:** Lark interactive cards support rich formatting including buttons, images, and tables. This plugin uses a simple, clean format optimized for readability.

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
      "preset": "lark",
      "url": "https://open.feishu.cn/open-apis/bot/v2/hook/YOUR/WEBHOOK/URL"
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
      "preset": "lark",
      "url": "https://open.feishu.cn/open-apis/bot/v2/hook/YOUR/WEBHOOK/URL",
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
   curl -X POST -H 'Content-Type: application/json' \
     --data '{"msg_type":"text","content":{"text":"Test message"}}' \
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

### Wrong Group

You cannot change the target group via configuration. To change groups:
1. Delete the existing bot from the current group
2. Go to the target group
3. Create a new custom bot following the setup steps
4. Update your `config.json` with the new URL

### Rate Limiting

Lark/Feishu rate limits for custom bots:
- **Free tier:** ~1 message per second
- **Enterprise tier:** Higher limits

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

### Authentication Errors

If you receive authentication errors:
- Verify the webhook URL is complete and correct
- Check that the bot hasn't been deleted from the group
- Ensure the bot permission includes message sending

## Lark vs Feishu

| Aspect | Lark | Feishu |
|--------|------|--------|
| Domain | `open.larksuite.com` | `open.feishu.cn` |
| Language | English | Chinese |
| Features | Same | Same |
| API | Compatible | Compatible |

Both platforms use the same webhook API format. This plugin works with both.

## Best Practices

1. **Use dedicated groups** - Create an "Agent Notifications" group
2. **One bot per group** - Don't reuse bots across projects
3. **Secure your URL** - Never commit webhook URLs to git
4. **Enable retry** - Handle transient network failures
5. **Monitor metrics** - Track delivery success rates
6. **Test before deploying** - Use webhook.site for testing

## Security

- **Never commit webhook URLs** - Use environment variables or config files
- **Rotate webhooks periodically** - Create new webhooks every few months
- **Limit access** - Only share webhooks with team members
- **Use HTTPS** - Lark webhooks are HTTPS-only (enforced)
- **Monitor unusual activity** - Check for unexpected messages

## Learn More

- [Configuration Options](configuration.md) - Retry, circuit breaker, rate limiting
- [Monitoring](monitoring.md) - Metrics and debugging
- [Troubleshooting](troubleshooting.md) - Common issues

## Official Documentation

- [Lark Bots](https://open.larksuite.com/document/home/introduce-to-bots/custom-bot)
- [Feishu Bots (飞书机器人)](https://open.feishu.cn/document/home/introduce-to-bots/custom-bot)
- [Message Cards](https://open.larksuite.com/document/home/interactive-message-cards)
- [Message Cards (飞书)](https://open.feishu.cn/document/home/interactive-message-cards)

---

[← Back to Webhook Overview](README.md)
