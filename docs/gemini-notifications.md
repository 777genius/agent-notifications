# Gemini CLI notifications

Gemini support is available on macOS amd64/arm64, Linux amd64/arm64 and Windows amd64 through the
[platform channels](PLATFORM_RELEASE_CHANNELS.md).

The integration was tested with exact **Gemini CLI 0.62.0** on macOS arm64/Intel, Linux amd64/arm64 and Windows amd64. Tests use the real CLI and its permission UI with a deterministic local
provider; they do not call the live Google model service. See qualification by OS below.

## Two fixed alerts

| Native event | Alert meaning | Fixed notification body |
| --- | --- | --- |
| `AfterAgent` | Gemini completed a turn | `Gemini CLI completed a turn` |
| `Notification` with `notification_type: ToolPermission` | Gemini requests tool permission | `Gemini CLI requested tool permission` |

`AfterAgent` does not prove task success or a final answer. Continuation can follow.
There is no promise of final-only delivery or root/nested-agent parity. Gemini
notifications do not include prompts, tool arguments, transcripts or model answers.
The integration observes these signals; it does not approve tools or change answers.

Desktop alerts are silent. Webhooks require explicit consent and separately
configured endpoints. Sound, click-to-focus, questions, errors, plans, reviews,
session-limit alerts, MCP notification tools and the `agent-notifications` skill are not
Gemini capabilities. Claude/Codex capabilities and OpenCode limits remain separate.

## Installation

Install stable Gemini CLI **>= 0.62.0 and < 0.66.0** and make `gemini` available on `PATH`. The compatibility range is separate from the exact versions used by native tests. Prereleases, older versions and unreviewed minor generations are rejected. On macOS, Linux or Windows, use the public platform installer
shown below. Its source bundle and binaries come from the same qualified release.
The installer selects the matching native assets automatically.

### Compatibility range and tested versions

Stable 0.62.x through 0.65.x share the hook contract used by this integration:
`AfterAgent`, `Notification`/`ToolPermission`, hook settings and the command runner.
The upstream hook sources are unchanged between
[0.62.0](https://github.com/google-gemini/gemini-cli/tree/b460678f3db508407554afd604cc9d6635becb2a/packages/core/src/hooks)
and [0.65.0](https://github.com/google-gemini/gemini-cli/tree/6006c14a4decfe4b891c6263ad6ad5992e404b5b/packages/core/src/hooks).
This supports the bounded compatibility range, not a claim that every patch was
individually tested. A newer minor generation needs a contract review and native
qualification before extending the upper bound.

Native CI keeps exact 0.62.0 baseline coverage on five platforms and adds exact
0.65.0 endpoint coverage on Linux amd64 and macOS arm64. Each run records the
actual version, upstream source and installed package hashes; the historical
results below retain their original scope. A newer compatibility range does not
turn an older test result into evidence for a new CLI version.

To install a version within this range:

```bash
npm install -g '@google/gemini-cli@>=0.62.0 <0.66.0'
```

### Platform installer selectors

On macOS, Linux or Windows, use:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- --product gemini --desktop)
```

For webhook-only delivery, replace `--desktop` with `--webhook`; for both, specify both.
These flags choose channels explicitly; `--webhook` alone turns desktop consent off. Without channel flags, fresh public setup enables desktop only and updates preserve saved choices. Webhook URLs must be configured separately.

The combined selector is one pipeline, in canonical product order:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- --products claude,codex,opencode,gemini --desktop --webhook)
```

Desktop/webhook choices apply to **each selected observer agent**, Gemini and
OpenCode, with consent persisted separately per agent. They do not grant or change
Claude/Codex notification channels. `--skip-agent-notify` affects only Claude/Codex
portable MCP/skill setup. Legacy `--product both` remains Claude + Codex.
The piped loader, interactive menu and all-four installation were qualified
in an isolated Linux TEST profile with real AN installers. Claude registration used
a metadata fixture. The installer evidence records qualification before publication;
current public versions are recorded in the platform channels linked above. This historical installer evidence does not qualify newer release bytes.
See [installer evidence](handoffs/gemini-installer-e2e.md).

## Settings, duplicates and lifecycle

Gemini's experimental [built-in notifications](https://geminicli.com/docs/cli/notifications/)
can duplicate desktop alerts from Agent Notifications. Choose one desktop source
manually, or use Agent Notifications for webhooks only. Setup does not switch off
Gemini's built-in notifications or modify their settings.

Use the installer's reported configuration path and Gemini guide rather
than the Claude/Codex portable MCP wizard. The `agents.gemini` profile
controls the two supported statuses and allowed delivery channels. Shared sound or
focus settings cannot add those capabilities. Channel consent belongs to Gemini;
OpenCode consent is separate. Webhook consent alone does not configure an endpoint.

Duplicate suppression records attempts per channel for a 60-second window, with
at most 256 hashed observation markers. It does not guarantee successful delivery
or exactly-once notification display. Missing native timestamps are scoped to one
invocation. An unsafe, unavailable or slow cache suppresses the affected attempt;
the locked cache transaction budget is 250 ms after root and private temporary-file
preflight. The caller deadline also applies to preflight, so complete claim duration
can exceed 250 ms. There is no automatic delivery retry. A crash
or cache loss can permit duplicates outside this bounded guarantee.

Restart Gemini after setup or update so its effective hooks are reloaded. Use the
installer's printed inspect, channel-change, remove and recovery commands;
installation, repeat, update, inspect and removal were exercised on all five native CI targets.
Removal revokes Gemini consent and removes only owned hooks/runtime files, preserving foreign settings and
other agents. An ownership conflict may retain disabled files for inspection rather
than overwrite another writer's edits.

## Qualification status

The native CLI and production lifecycle checks passed on all five targets for the exact v1.49.1 source in [run 38269416372](https://github.com/777genius/agent-notifications/actions/runs/38269416372). These checks use a local provider and webhooks; they do not establish desktop visibility. The desktop observations below are historical evidence from the integration qualification.

| Evidence layer | Status |
| --- | --- |
| Exact Gemini CLI 0.62.0 native hooks and permission UI | Passed on macOS arm64/Intel, Linux amd64/arm64 and Windows amd64, with a local provider fixture |
| macOS arm64 desktop and visual test | Real CLI delivered completion and tool-permission alerts through our signed helper; macOS accepted both and the owner confirmed seeing both |
| Windows amd64 native shell / notification API / visual desktop | Native CLI, production lifecycle and webhook passed; notification API and visual desktop unverified |
| Linux amd64/arm64 native shell and webhook / desktop | Native CLI and production install/update/remove/webhook passed; desktop API and visual desktop unverified |
| macOS Intel native CLI | Native CLI, production lifecycle and webhook passed; visual desktop unverified |
| Public Agent Notifications availability | macOS amd64/arm64, Linux amd64/arm64 and Windows amd64; see [current platform versions](PLATFORM_RELEASE_CHANNELS.md) |

A macOS observation will not imply Windows/Linux GUI verification. Injected
contracts, actual CLI with a local provider fixture (`native_cli/provider_substitute`)
and live model-service tests are reported separately. Native tests covered equal
turns, permission approval/denial/cancellation, recovery to another turn, changed
executable updates and removal while Gemini remained running. They also verified
that a damaged receipt revokes consent, blocks new webhook delivery and preserves
foreign hooks. macOS additionally verified desktop delivery while webhooks were
disabled. Interrupted crash recovery, resume and nested sessions remain unqualified.
