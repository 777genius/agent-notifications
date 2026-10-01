# Gemini CLI notifications (unreleased candidate)

Public Agent Notifications **1.46.1 does not contain Gemini support**. This guide
covers the candidate integration; it requires a compatible release or an exact,
checked candidate bundle supplied for qualification. No release version is assigned
here. The candidate was tested with exact **Gemini CLI 0.62.0** on macOS arm64/Intel, Linux amd64/arm64 and Windows amd64. Tests use the real CLI and its permission UI with a deterministic local
provider; they do not call the live Google model service. See qualification by OS below.

## Two fixed alerts

| Native event | Alert meaning | Planned fixed notification body |
| --- | --- | --- |
| `AfterAgent` | Gemini completed a turn | `Gemini CLI completed a turn` |
| `Notification` with `notification_type: ToolPermission` | Gemini requests tool permission | `Gemini CLI requested tool permission` |

`AfterAgent` does not prove task success or a final answer. Continuation can follow.
There is no promise of final-only delivery or root/nested-agent parity. Gemini
notifications do not include prompts, tool arguments, transcripts or model answers.
The candidate integration observes these signals; it does not approve tools or change answers.

Desktop alerts are silent. Webhooks require explicit consent and separately
configured endpoints. Sound, click-to-focus, questions, errors, plans, reviews,
session-limit alerts, MCP notification tools and the `agent-notify` skill are not
Gemini capabilities. Claude/Codex capabilities and OpenCode limits remain separate.

## Candidate setup and compatible-release setup

Install Gemini CLI first. For candidate qualification, use the orchestrator's checked
bundle and its direct built-candidate setup instructions in an isolated TEST profile.
Do not run the public loader or a local `bin/setup.sh` to qualify candidate bytes:
those paths resolve public releases. The checked artifact and dependency hashes must match its qualification record.
The macOS test uses our existing signed ClaudeNotifier helper and delivery backend.

After a compatible release is available, the planned public selector is:

```bash
curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product gemini --desktop
```

**This command does not install Gemini support from current release 1.46.1.** For
webhook-only delivery, replace `--desktop` with `--webhook`; for both, specify both.
Choose at least one channel. Webhook URLs must be configured separately.

The planned combined selector is one pipeline, in canonical product order:

```bash
(set -o pipefail; curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --products claude,codex,opencode,gemini --desktop --webhook)
```

Desktop/webhook choices apply to **each selected observer agent**, Gemini and
OpenCode, with consent persisted separately per agent. They do not grant or change
Claude/Codex notification channels. `--skip-agent-notify` affects only Claude/Codex
portable MCP/skill setup. Legacy `--product both` remains Claude + Codex.
Combined-selector parsing and candidate installation require installer qualification;
command generation alone does not establish that the published loader accepts them.

## Settings, duplicates and lifecycle

Gemini's experimental [built-in notifications](https://geminicli.com/docs/cli/notifications/)
can duplicate desktop alerts from Agent Notifications. Choose one desktop source
manually, or use Agent Notifications for webhooks only. Setup does not switch off
Gemini's built-in notifications or modify their settings.

Use the candidate installer's reported configuration path and Gemini guide rather
than the Claude/Codex portable MCP wizard. The candidate `agents.gemini` profile
controls the two supported statuses and allowed delivery channels. Shared sound or
focus settings cannot add those capabilities. Channel consent belongs to Gemini;
OpenCode consent is separate. Webhook consent alone does not configure an endpoint.

Restart Gemini after setup or update so its effective hooks are reloaded. Use the
checked candidate's printed inspect, channel-change, remove and recovery commands;
installation, repeat, update, inspect and removal were exercised on all five native CI targets.
Removal revokes Gemini consent and removes only owned hooks/runtime files, preserving foreign settings and
other agents. An ownership conflict may retain disabled files for inspection rather
than overwrite another writer's edits.

## Qualification status

| Evidence layer | Status |
| --- | --- |
| Exact Gemini CLI 0.62.0 native hooks and permission UI | Passed on macOS arm64/Intel, Linux amd64/arm64 and Windows amd64, with a local provider fixture |
| macOS arm64 desktop and visual test | Real CLI delivered completion and tool-permission alerts through our signed helper; macOS accepted both and the owner confirmed seeing both |
| Windows amd64 native shell / notification API / visual desktop | Native CLI, production lifecycle and webhook passed; notification API and visual desktop unverified |
| Linux amd64/arm64 native shell and webhook / desktop | Native CLI and production install/update/remove/webhook passed; desktop API and visual desktop unverified |
| macOS Intel native CLI | Native CLI, production lifecycle and webhook passed; visual desktop unverified |
| Compatible public Agent Notifications release | Pending; 1.46.1 excludes Gemini |

A macOS observation will not imply Windows/Linux GUI verification. Injected
contracts, actual CLI with a local provider fixture (`native_cli/provider_substitute`)
and live model-service tests are reported separately. Native tests covered equal
turns, permission approval/denial/cancellation, recovery to another turn, changed
executable updates and removal while Gemini remained running. They also verified
that a damaged receipt revokes consent, blocks new webhook delivery and preserves
foreign hooks. macOS additionally verified desktop delivery while webhooks were
disabled. Interrupted crash recovery, resume and nested sessions remain unqualified.
