[Back to README](../README.md)

# Supported Notification Types

The Claude triggers are listed below. Codex uses a different event mapping, described in [Codex support](CODEX.md).

| Status | Icon | Description | Trigger |
|--------|------|-------------|---------|
| Task Complete | ✅ | Main task completed | Stop/SubagentStop hooks (state machine detects active tools like Write/Edit/Bash, or ExitPlanMode followed by tool usage) |
| Review Complete | 🔍 | Code review finished | Stop/SubagentStop hooks (state machine detects only read-like tools: Read/Grep/Glob with no active tools, plus long text response >200 chars) |
| Question | ❓ | Claude has a question | PreToolUse hook (AskUserQuestion) OR Notification hook |
| Plan Ready | 📋 | Plan ready for approval | PreToolUse hook (ExitPlanMode) |
| Session Limit Reached | ⏱️ | Session limit reached | Stop/SubagentStop hooks (state machine detects "Session limit reached" text in last 3 assistant messages) |
| API Error | 🔴 | Authentication expired, rate limit, server error, connection error | Stop/SubagentStop hooks (state machine detects via `isApiErrorMessage` flag + `error` field from JSONL) |
| Permission Request | 🔐 | Codex is waiting for tool approval | Codex `PermissionRequest` hook (Codex only) |

## Gemini CLI

Released in **[v1.47.1](https://github.com/777genius/agent-notifications/releases/tag/v1.47.1)**
for Linux amd64/arm64 and Windows amd64. macOS remains on **v1.46.1**, which excludes Gemini.
Tested with **Gemini CLI 0.62.0**. [Setup, qualification and limits](gemini-notifications.md).

| Event | Meaning | Fixed body |
| --- | --- | --- |
| `AfterAgent` | A turn completed; not necessarily success or final answer | `Gemini CLI completed a turn` |
| `Notification: ToolPermission` | Tool permission requested | `Gemini CLI requested tool permission` |

Silent desktop and explicitly opted webhooks only. No sound, click-to-focus,
question/error/plan/review/session-limit alerts or nested-agent parity. Built-in
Gemini alerts can duplicate desktop delivery; choose one desktop source manually
or webhook-only. Setup does not change built-in notification settings.
