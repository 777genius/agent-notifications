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

Claude/Codex installation can also add a **local** `agent-notifications` MCP tool and skill. Restart or reload the agent to discover newly installed tools. Only invoke the tool after it appears in the active agent session. Tool availability and event coverage vary by runtime; never claim a sent alert merely because a command was suggested.

The local tool does not make this public website an MCP server, and a cloud-only agent cannot access the user's localhost without an authorized local executor.

## 4. Verify and troubleshoot

Use the [installation guide](https://github.com/777genius/agent-notifications/blob/main/docs/INSTALLATION.md) and [troubleshooting docs](https://github.com/777genius/agent-notifications#troubleshooting). Confirm notification permission and the chosen agent's hooks before troubleshooting sound/focus. Never print provider tokens, user webhook destinations or private config files.

- [Website](https://agent-notifications.com/)
- [Repository](https://github.com/777genius/agent-notifications)
- [Auth and consent boundaries](https://agent-notifications.com/auth.md)
