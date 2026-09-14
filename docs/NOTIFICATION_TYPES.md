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
