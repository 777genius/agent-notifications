# Using Agent Notifications from an AI assistant

Agent Notifications adds notifications for **locally installed** coding agents. It does not provide a cloud API that an agent can call by reading this page.

## 1. Choose a qualified integration

Check the supported products, events, OS and architecture in the [support matrix](https://github.com/777genius/agent-notifications#supported-agents) and the [installation guide](https://github.com/777genius/agent-notifications/blob/main/docs/INSTALLATION.md). Supported products include Claude, Codex, OpenCode, Gemini, Cursor, Qoder and Pi, with different alert, sound, webhook and click-to-focus coverage.

Confirm the computer OS, installed agent CLIs and whether the user wants desktop notifications and/or outbound webhooks. **Do not assume** that every agent supports the same features.

## 2. Ask permission before installing or changing configuration

The [guided installer](https://agent-notifications.com/#install) supplies a command for the chosen products. A generic interactive install/update command for a user to inspect is:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash)
```

This **downloads and executes** code on the user's machine. Do not run it without explicit consent and review of the official source and release. To install on native Windows, use Git Bash instead of WSL. The installer needs curl, Bash, and either python3 or Node, plus the chosen agent CLI available on PATH.

For noninteractive product selection, **use the exact flags from the [official docs](https://github.com/777genius/agent-notifications#install-or-update)**. Do not silently turn on webhook delivery or override a saved disabled channel. Verify the selected plugin and generated config after installation.

## 3. Connect agent notifications when supported

Claude/Codex installation can also register the **local** `agent-notifications` MCP **server** and its skill. The MCP tools are named `notification_status` (read-only availability) and `notify` (send). Some clients defer tool definitions: use the current client's tool search/discovery for those exact tool names, or `agent_notifications`, before declaring them unavailable. Restart or reload after installing if discovery remains empty. Call the read-only status tool first, and invoke `notify` only when explicitly permitted and warranted; interpret its receipt rather than claiming an OS banner was shown. Tool availability and event coverage vary by runtime.

The local tool does not make this public website an MCP server, and a cloud-only agent cannot access the user's localhost without an authorized local executor.

## 4. Verify and troubleshoot

Use the [installation guide](https://github.com/777genius/agent-notifications/blob/main/docs/INSTALLATION.md) and [troubleshooting docs](https://github.com/777genius/agent-notifications/blob/main/docs/troubleshooting.md). Confirm notification permission and the chosen agent's hooks before troubleshooting sound/focus. Never print provider tokens, user webhook destinations or private config files.

- [Website](https://agent-notifications.com/)
- [Repository](https://github.com/777genius/agent-notifications)
- [Auth and consent boundaries](https://agent-notifications.com/auth.md)
