# Telegram Webhook Integration

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


Send Claude Code notifications to Telegram chats via bot.

## Overview

Telegram integration uses the Bot API to send HTML-formatted messages directly to chats or groups. Messages include session names, status indicators, and are delivered reliably through Telegram's infrastructure.

## Setup

### Step 1: Create Bot with BotFather

1. Open Telegram and search for **@BotFather**
2. Start a chat and send `/newbot`
3. Follow the prompts:
   - **Bot name:** Choose a display name (e.g., "Agent Notifications")
   - **Username:** Choose a unique username ending in `bot` (e.g., `agent_notify_bot`)
4. BotFather will give you an **API token** like:
   ```
   123456789:ABCdefGHIjklMNOpqrsTUVwxyz
   ```
5. **Save this token securely!**

### Step 2: Get Chat ID

#### For Personal Chat:

1. Start a chat with your bot
2. Send any message to the bot (e.g., "hello")
3. Visit this URL in your browser (replace `<YOUR_BOT_TOKEN>`):
   ```
   https://api.telegram.org/bot<YOUR_BOT_TOKEN>/getUpdates
   ```
4. Find the `chat` object in the JSON response:
   ```json
   {
     "chat": {
       "id": 123456789,
       "first_name": "Your Name",
       "type": "private"
     }
   }
   ```
5. Your **chat ID** is the `id` value (e.g., `123456789`)

#### For Group Chat:

1. Add your bot to the group
2. Send a message in the group (mention the bot)
3. Visit the same getUpdates URL
4. Find the `chat` object with `type: "group"` or `type: "supergroup"`
5. Your **chat ID** will be negative (e.g., `-987654321`)

**🆕 2025 Update:** If `getUpdates` returns empty results for groups, you may need to disable privacy mode:
1. Go to @BotFather
2. Send `/mybots`
3. Select your bot
4. Choose **"Bot Settings"** → **"Group Privacy"**
5. Select **"Disable"**
6. Try getting updates again

### Step 3: Configure Plugin

Edit the shared file selected by `config path`:

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "telegram",
      "url": "https://api.telegram.org/bot<YOUR_BOT_TOKEN>/sendMessage",
      "chat_id": "123456789"
    }
  }
}
```

**Important:**
- Replace `<YOUR_BOT_TOKEN>` with your actual bot token
- Set `chat_id` to your chat ID (can be positive or negative)

### Step 4: Test

```bash
echo '{"session_id":"test","tool_name":"ExitPlanMode"}' | \
  bin/claude-notifications handle-hook PreToolUse
```

Check your Telegram chat for the notification!

## Message Format

### HTML Formatting

Messages use Telegram's HTML formatting:
- **Bold:** `<b>text</b>`
- **Italic:** `<i>text</i>`
- **Code:** `<code>text</code>`

### Example Message

```
✅ Task Completed

[bold-cat] Created new authentication
system with JWT tokens

Session: abc-123
```

### Technical Details

Messages are sent via `sendMessage` method:

```json
{
  "chat_id": "123456789",
  "text": "✅ <b>Task Completed</b>\n\n[bold-cat] Created new authentication system with JWT tokens\n\nSession: abc-123",
  "parse_mode": "HTML"
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
      "preset": "telegram",
      "url": "https://api.telegram.org/bot123456789:ABCdefGHIjklMNOpqrsTUVwxyz/sendMessage",
      "chat_id": "123456789"
    },
    "suppressQuestionAfterTaskCompleteSeconds": 12
  }
}
```

### With Retry & Rate Limiting

```json
{
  "notifications": {
    "webhook": {
      "enabled": true,
      "preset": "telegram",
      "url": "https://api.telegram.org/bot123456789:ABCdefGHIjklMNOpqrsTUVwxyz/sendMessage",
      "chat_id": "123456789",
      "retry": {
        "enabled": true,
        "maxAttempts": 3,
        "initialBackoff": "1s",
        "maxBackoff": "10s"
      },
      "circuitBreaker": {
        "enabled": false
      },
      "rateLimit": {
        "enabled": true,
        "requestsPerMinute": 30
      }
    }
  }
}
```

**Note:** Circuit breaker is disabled for Telegram since the API is highly reliable.

## Troubleshooting

### Bot Token Invalid (401)

Error: `{"ok":false,"error_code":401,"description":"Unauthorized"}`

**Solutions:**
- Verify bot token is correct (no spaces or typos)
- Check that token includes both parts: `123456789:ABCdef...`
- Ensure bot wasn't deleted in BotFather

### Chat Not Found (400)

Error: `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`

**Solutions:**
- Verify chat ID is correct
- Ensure you've started a conversation with the bot (send at least one message)
- For groups, verify bot is still a member

### Bot Can't Initiate Conversation (403)

Error: `{"ok":false,"error_code":403,"description":"Forbidden: bot can't initiate conversation with a user"}`

**Solution:** You must start the conversation first. Send any message to the bot via Telegram before sending webhook notifications.

### getUpdates Returns Empty

**Solution (🆕 2025):** Disable privacy mode:
1. Open @BotFather
2. Send `/mybots`
3. Select your bot
4. **Bot Settings** → **Group Privacy** → **Disable**

### Rate Limiting

Telegram rate limits:
- **30 messages per second** per bot (across all chats)
- **1 message per second** per chat (for groups)

Configure rate limiting:
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

1. **Dedicated bot** - Create a separate bot for Agent Notifications
2. **Secure token** - Never commit bot token to git
3. **Private chats** - Use personal chat or private groups for sensitive notifications
4. **Enable retry** - Telegram is reliable but network issues can occur
5. **Monitor rate limits** - Stay under 30 messages/second
6. **Test with @username** - Send test messages before production use

## Security

- **Protect bot token** - Treat like a password, never share publicly
- **Use private chats** - Don't send notifications to public channels
- **Revoke compromised tokens** - If leaked, use @BotFather to revoke
- **Disable unused bots** - Delete bots you're not using via @BotFather
- **Monitor bot activity** - Check for unexpected messages

## Advanced Features

### Send to Multiple Chats

To send notifications to multiple chats, configure multiple webhook instances (currently requires code changes).

### Custom Keyboards

Telegram supports custom keyboards and inline buttons. This plugin currently sends text-only messages.

### Markdown vs HTML

This plugin uses HTML formatting (`parse_mode: "HTML"`). Telegram also supports Markdown, but HTML is more reliable for complex messages.

### Silent Messages

To send notifications without sound/vibration, add to the request:
```json
{
  "disable_notification": true
}
```

Note: This plugin doesn't currently support silent messages.

## API Limits

### Message Limits:
- **Text:** 4096 characters per message
- **Caption:** 1024 characters
- **Messages per second:** 30 (per bot)
- **Messages per minute (group):** ~60

### File Size Limits:
- **Photos:** 10 MB
- **Documents:** 50 MB (bots)

This plugin only sends text messages, so file limits don't apply.

## Learn More

- [Configuration Options](configuration.md) - Retry, circuit breaker, rate limiting
- [Monitoring](monitoring.md) - Metrics and debugging
- [Troubleshooting](troubleshooting.md) - Common issues

## Official Documentation

- [Telegram Bot API](https://core.telegram.org/bots/api)
- [BotFather Tutorial](https://core.telegram.org/bots/tutorial)
- [sendMessage Method](https://core.telegram.org/bots/api#sendmessage)
- [HTML Formatting](https://core.telegram.org/bots/api#html-style)

---

[← Back to Webhook Overview](README.md)
