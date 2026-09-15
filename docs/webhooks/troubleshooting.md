# Webhook Troubleshooting Guide

Use the installed config-capable executable as `$NOTIFICATIONS_BIN`. Locate settings with `config path` and use [revision-checked leaf edits](../../commands/settings.md). JSON examples below illustrate fields, not whole-file replacements. Keep unrequested fields and literal environment templates unchanged. `config inspect --json` is the safe support output; it intentionally omits URLs, headers, payloads, free-form sounds and unknown fields. Never share raw config or assume omitted values are unset. Keep diagnostic files private and review logs for credentials before sharing.


Common issues and solutions for webhook notifications.

## Table of Contents

- [Webhooks Not Sending](#webhooks-not-sending)
- [Circuit Breaker Issues](#circuit-breaker-issues)
- [Rate Limiting](#rate-limiting)
- [Authentication Errors](#authentication-errors)
- [Timeout Issues](#timeout-issues)
- [Platform-Specific Issues](#platform-specific-issues)
- [Debugging Workflow](#debugging-workflow)

## Webhooks Not Sending

### Symptom
No messages appearing in Slack/Discord/Telegram, no errors in logs.

### Checklist

1. **Verify webhook is enabled:**
   ```bash
   "$NOTIFICATIONS_BIN" config inspect --json
   ```
   Inspect reports `valid` and per-status `settings.statuses.<status>.webhookEnabled`. It does not expose the global webhook object; do not infer omitted fields are unset.

2. **Check logs for errors:**
   ```bash
   tail -f notification-debug.log | grep webhook
   ```

3. **Test webhook URL manually:**

   **Slack:**
   ```bash
   curl -X POST -H 'Content-type: application/json' \
     --data '{"text":"Test message"}' \
     YOUR_SLACK_WEBHOOK_URL
   ```

   **Discord:**
   ```bash
   curl -X POST -H 'Content-Type: application/json' \
     --data '{"content":"Test message"}' \
     YOUR_DISCORD_WEBHOOK_URL
   ```

   **Telegram:**
   ```bash
   curl -X POST "https://api.telegram.org/botYOUR_TOKEN/sendMessage" \
     -d "chat_id=YOUR_CHAT_ID&text=Test message"
   ```

4. **Verify config is valid JSON:**
   ```bash
   "$NOTIFICATIONS_BIN" config inspect --json
   ```
   If error, fix JSON syntax

5. **Check for missing fields:**
   - Slack/Discord: `preset`, `url`
   - Telegram: `preset`, `url`, `chat_id`

### Solutions

**If manual curl works but plugin doesn't:**
- Check log file for errors
- Verify config path is correct
- Restart Claude Code

**If manual curl fails:**
- Webhook URL may be invalid/expired
- Recreate webhook in platform settings
- For Telegram: verify bot token and chat ID

## Circuit Breaker Issues

### Symptom
Webhooks work initially, then stop. Logs show "circuit breaker opened".

### Understanding the Issue

Circuit breaker opens after multiple consecutive failures to protect the system from wasting resources on a failing endpoint.

**Log message:**
```
[webhook] Circuit breaker opened after 5 consecutive failures
[webhook] Webhook blocked by circuit breaker (state: open)
```

### Solutions

#### 1. Wait for Auto-Recovery

Circuit breaker automatically transitions to `half-open` after timeout (default: 30s), then tests recovery.

**Watch logs:**
```bash
tail -f notification-debug.log | grep "Circuit breaker"
```

**Expected flow:**
```
Circuit breaker opened
... wait 30s ...
Circuit breaker half-open, testing recovery
Circuit breaker closed after 2 successful requests
```

#### 2. Increase Failure Threshold

If circuit opens too easily, increase threshold:

```json
{
  "circuitBreaker": {
    "failureThreshold": 10
  }
}
```

#### 3. Increase Timeout

Give more time for endpoint to recover:

```json
{
  "circuitBreaker": {
    "timeout": "60s"
  }
}
```

#### 4. Disable Circuit Breaker

For debugging or extremely reliable endpoints:

```json
{
  "circuitBreaker": {
    "enabled": false
  }
}
```

**⚠️ Warning:** Disabling circuit breaker means all requests will be attempted even if endpoint is down.

### Root Cause Analysis

**Check why circuit opened:**
```bash
grep "webhook failed" notification-debug.log | tail -10
```

Common causes:
- **503 Service Unavailable** - Endpoint overloaded
- **Timeout** - Endpoint too slow
- **DNS failure** - Network issues
- **401/403** - Authentication problems

Fix the underlying issue before re-enabling circuit breaker.

## Rate Limiting

### Symptom
Logs show "rate limit exceeded", some webhooks not delivered.

### Log Message
```
[webhook] Rate limit exceeded: requests=11 limit=10/min
[webhook] Webhook blocked by rate limiter
```

### Solutions

#### 1. Increase Rate Limit

```json
{
  "rateLimit": {
    "requestsPerMinute": 30
  }
}
```

**Platform recommendations:**
- Slack: 20-60
- Discord: 30-60
- Telegram: 30-60
- Custom: Match your endpoint's limit

#### 2. Reduce Notification Frequency

If you're hitting limits frequently, consider:
- Increasing cooldown periods
- Disabling certain notification types
- Combining multiple events into single notification

#### 3. Disable Rate Limiting

For testing or unlimited endpoints:

```json
{
  "rateLimit": {
    "enabled": false
  }
}
```

**⚠️ Warning:** May violate platform rate limits and cause 429 errors.

### Check Rate Limit Impact

```bash
# Count rate limited requests
grep -c "Rate limit exceeded" notification-debug.log

# View rate limit events
grep "Rate limit" notification-debug.log
```

## Authentication Errors

### Symptom
Logs show 401 Unauthorized or 403 Forbidden errors.

### HTTP 401 Unauthorized

**Cause:** Invalid credentials or missing authentication.

**Solutions:**

1. **Verify authentication header format:**

   Bearer token:
   ```json
   {"Authorization": "Bearer YOUR_TOKEN"}
   ```

   API Key:
   ```json
   {"X-API-Key": "YOUR_KEY"}
   ```

   Basic auth:
   ```json
   {"Authorization": "Basic BASE64_ENCODED_CREDENTIALS"}
   ```

2. **Check for typos:**
   - Extra spaces in token
   - Missing "Bearer" prefix
   - Incorrect header name

3. **Verify credentials haven't expired:**
   - Regenerate tokens if needed
   - Check token expiration date

4. **Test with curl:**
   ```bash
   curl -X POST YOUR_URL \
     -H "Authorization: Bearer YOUR_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"test":"data"}'
   ```

### HTTP 403 Forbidden

**Cause:** Valid credentials but insufficient permissions.

**Solutions:**

1. **Check API permissions:**
   - Ensure API key has webhook permissions
   - Verify service account has correct roles

2. **IP allowlist:**
   - Add your server IP to allowlist
   - Check firewall rules

3. **Platform-specific:**
   - **Slack:** Verify webhook is still active in app settings
   - **Discord:** Check bot has channel permissions
   - **Telegram:** Ensure bot was started by user

### Telegram: Bot Can't Initiate Conversation

**Error:**
```json
{"ok":false,"error_code":403,"description":"Forbidden: bot can't initiate conversation with a user"}
```

**Solution:** Start conversation with bot first:
1. Open Telegram
2. Search for your bot
3. Send `/start` or any message
4. Try notification again

## Timeout Issues

### Symptom
Logs show timeout errors, webhooks taking too long.

### Log Message
```
[webhook] Webhook failed: timeout after 10s
[webhook] Context deadline exceeded
```

### Solutions

#### 1. Check Endpoint Performance

Your webhook endpoint may be slow:

```bash
# Test response time
time curl -X POST YOUR_WEBHOOK_URL -d '{"test":"data"}'
```

**Expected:** < 2s
**Acceptable:** < 5s
**Problem:** > 10s

#### 2. Increase Retry Backoff

Give more time between retries:

```json
{
  "retry": {
    "initialBackoff": "5s",
    "maxBackoff": "30s"
  }
}
```

#### 3. Optimize Endpoint

If endpoint is under your control:
- Add caching
- Optimize database queries
- Use async processing
- Return 202 Accepted immediately

#### 4. Use Intermediate Service

If endpoint is slow, use fast proxy:
```
Claude → Fast webhook receiver → Queue → Slow processor
```

## Platform-Specific Issues

### Slack

#### Wrong Channel
**Problem:** Messages going to wrong channel.

**Solution:**
- Can't change via config
- Must recreate webhook in Slack app settings:
  1. Features → Incoming Webhooks
  2. Delete old webhook
  3. Add new webhook to desired channel
  4. Update URL in config

#### Attachments Not Showing
**Problem:** Messages appear as plain text.

**Solution:**
- Ensure `preset: "slack"` is set
- Check logs for errors
- Verify webhook URL is for Incoming Webhook (not legacy URL)

### Discord

#### Webhook Not Found (404)
**Problem:** `{"message": "Unknown Webhook", "code": 10015}`

**Solution:**
- Webhook was deleted
- Check Server Settings → Integrations → Webhooks
- Create new webhook and update config

#### Rate Limited (429)
**Problem:** `{"message": "You are being rate limited.", "retry_after": 1.234}`

**Solution:**
- Discord limits: 5 requests per 2 seconds
- Enable rate limiting:
  ```json
  {"rateLimit": {"requestsPerMinute": 30}}
  ```

### Telegram

#### Chat Not Found
**Problem:** `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`

**Solutions:**
1. Verify chat ID is correct
2. Start conversation with bot
3. For groups: ensure bot is member

#### Bot Token Invalid
**Problem:** `{"ok":false,"error_code":401,"description":"Unauthorized"}`

**Solutions:**
1. Verify token format: `123456789:ABCdef...`
2. Check for spaces/typos
3. Ensure bot not deleted in BotFather

#### getUpdates Empty (🆕 2025)
**Problem:** Can't get chat ID, getUpdates returns `{"ok":true,"result":[]}`

**Solution:** Disable privacy mode:
1. Go to @BotFather
2. Send `/mybots`
3. Select your bot
4. **Bot Settings** → **Group Privacy** → **Disable**
5. Try getUpdates again

## Debugging Workflow

### Step 1: Enable Verbose Logging

```bash
export CLAUDE_NOTIFICATIONS_DEBUG=1
```

### Step 2: Check Configuration

```bash
# Inspect validity without printing the document
"$NOTIFICATIONS_BIN" config inspect --json

# Inspect safe status/channel flags; credentials are intentionally omitted
"$NOTIFICATIONS_BIN" config inspect --json
```

### Step 3: Test Webhook URL

Use webhook.site for isolated testing:

1. Go to https://webhook.site/
2. Copy unique URL
3. Update config:
   ```json
   {
     "webhook": {
       "preset": "",
       "url": "https://webhook.site/YOUR-UNIQUE-URL"
     }
   }
   ```
4. Trigger notification
5. Check webhook.site for payload

### Step 4: Check Logs

```bash
# Watch logs in real-time
tail -f notification-debug.log | grep webhook

# Find errors
grep -i error notification-debug.log | grep webhook

# Count successes vs failures
echo "Success: $(grep -c 'webhook sent successfully' notification-debug.log)"
echo "Failures: $(grep -c 'webhook failed (final attempt)' notification-debug.log)"
```

### Step 5: Test Manually

```bash
# Create test payload
echo '{
  "session_id": "test-123",
  "transcript_path": "/tmp/test.jsonl"
}' > test-payload.json

# Test hook
cat test-payload.json | bin/claude-notifications handle-hook Stop

# Check logs
tail -20 notification-debug.log | grep webhook
```

### Step 6: Isolate Issue

**Network issue?**
```bash
ping api.telegram.org
curl -I https://hooks.slack.com
```

**Config issue?**
```bash
# Read-only validity and selected-path diagnostics; never dump raw config.
"$NOTIFICATIONS_BIN" config inspect --json
```

**Platform issue?**
```bash
# Check platform status pages
# Slack: status.slack.com
# Discord: discordstatus.com
# Telegram: telegram.org
```

## Getting Help

If you're still stuck after trying these solutions:

1. **Gather information:**
   - Relevant logs from `notification-debug.log`
   - Safe `config inspect --json` output (never raw config)
   - Platform (Slack/Discord/Telegram/Custom)
   - Error messages

2. **Check existing issues:**
   - GitHub Issues: https://github.com/777genius/agent-notifications/issues

3. **Create detailed issue:**
   - Include reproduction steps
   - Attach logs
   - Specify platform and version
   - Describe expected vs actual behavior

## Learn More

- [Configuration Reference](configuration.md) - All config options
- [Monitoring & Metrics](monitoring.md) - Track webhook health
- [Slack Setup](slack.md)
- [Discord Setup](discord.md)
- [Telegram Setup](telegram.md)
- [Custom Webhooks](custom.md)

---

[← Back to Webhook Overview](README.md)
