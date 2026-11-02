# Authentication and permissions — Agent Notifications

Agent Notifications is a free local open-source tool. **No Agent Notifications website account, product password, OAuth login, credit card or subscription is required** to use its supported desktop notification integrations.

The user must install the chosen AI agent CLI and may need to authenticate that CLI separately with its own provider. Such provider login does not authenticate against agent-notifications.com.

Installation/update requires local filesystem access and relevant terminal permissions; request explicit user approval before running an installer or changing agent plugins/hooks. On Windows use native Git Bash, not WSL. Some platforms also require OS notification permission.

Optional outbound webhooks (Slack, Discord, Telegram, Lark/Feishu and custom endpoints) require a destination configured by the user. Webhook tokens/URLs and provider credentials must stay in local/private configuration, never in public prompts or messages.

This website has **no public REST notification API**, remotely accessible MCP endpoint, or authorization-server discovery metadata. The optional MCP server installed for Claude/Codex runs locally within the user's agent setup.

- Install instructions: https://github.com/777genius/agent-notifications/blob/main/docs/INSTALLATION.md
- AI assistant guide: https://agent-notifications.com/agents.md
- Supported integrations: https://github.com/777genius/agent-notifications#supported-agents
