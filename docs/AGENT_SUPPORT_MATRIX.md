# Agent support: capabilities, evidence and expansion

Audit snapshot: 2026-10-31, `main` at `06ab7ae317c832e909bc76e998d07e388596292a`. This is a **source-and-documentation review**, not fresh native desktop acceptance. For platform/version gates use the linked agent guides and [release channels](PLATFORM_RELEASE_CHANNELS.md).

**Key distinction:** showing an alert, focusing the originating terminal window/pane, and opening the exact originating desktop chat are three separate capabilities. The same notification transport can be shared without pretending an agent session ID is a verified desktop-chat ID.

## Shipped integrations: observed event coverage

Legend: **Yes** = implemented; **Limited** = only under the stated event/host condition; **No** = not provided by the integration. This is a code-capability table, **not** a statement that every platform, CLI version or native click has qualified E2E evidence.

| Agent | Completion / idle | Questions | Permission prompts | Errors / limits | Plan / review | Subagents / teams |
| --- | --- | --- | --- | --- | --- | --- |
| Claude Code | Yes: main Stop | Yes | Yes: notification/interactive hooks | Yes: transcript/status classification | Yes: plan and review classification | Limited: optional subagent and teammate notifications |
| Codex CLI | Yes: turn Stop | Limited: supported question tools and final-text heuristic | Yes: PermissionRequest | Limited: final-text heuristic; not a native error guarantee | No dedicated plan/review classification | Limited: SubagentStop, opt-in |
| OpenCode V1/V2 | Yes: verified root-session idle | Yes | Yes | Yes: terminal error | No | No: root-session filter |
| Gemini CLI | Yes: AfterAgent means a turn ended, **not task success** | No | Yes: Notification/ToolPermission | No distinct error/limit alerts | No | No qualified parity |
| Cursor CLI | Limited: native Stop means loop stopped, including abort/error | No | No | No distinct alert: completion/abort/error use the same informational wording | No | No qualified subagent alerts |
| Qoder CLI | Yes: Stop | Yes | Yes | No in published v1.49.11; unreleased source adds a generic StopFailure event and separate rate-limit class | Plan yes; review no | Limited: opt-in SubagentStop; idle teammates |

Sources: [Claude/Codex/Qoder product event boundary](../internal/hooks/event.go), [shared hook handler](../internal/hooks/hooks.go), [OpenCode consumer](../internal/opencodeevent/consumer.go), [Gemini consumer](../internal/geminievent/consumer.go), [Cursor consumer](../internal/cursorevent/consumer.go); [Qoder](QODER.md), [Codex](CODEX.md), [OpenCode](opencode-notifications.md), [Gemini](gemini-notifications.md), [Cursor](cursor-notifications.md).

## Shipped integrations: channels and navigation

| Agent | Desktop | Webhooks | Configured sounds | Click: local terminal/window | Click: exact desktop chat |
| --- | --- | --- | --- | --- | --- |
| Claude Code | Yes | Yes | Yes | Supported, OS/terminal-dependent; pane precision varies | Not established |
| Codex CLI | Yes | Yes | Yes | Legacy terminal focus where supported | Separate verified, configured Codex-desktop navigation path; platform/installation qualification matters |
| OpenCode | Yes | Yes | No in published v1.49.11; unreleased source has optional OS-default `systemSound` opt-in | No: `Navigation=None` | No |
| Gemini CLI | Yes | Yes | No: explicit silent delivery | No: `Navigation=None` | No |
| Cursor CLI | Yes (qualified hosts only) | Yes | No: explicit silent delivery | No: `Navigation=None` | No |
| Qoder CLI | Yes | Yes | Shared notifier supports cues; v1.49.11 standalone bundle lacks built-in MP3s, unreleased source now packages all five (issue #562) | **Not qualified**: Claude-only Ghostty capture; generic legacy window activation must not be advertised as exact/guaranteed | No |

“Desktop” means the implementation provides a submission path; it does **not** mean visible banners or physical click callbacks were observed on every platform. [Terminal / IDE focus precision](CLICK_TO_FOCUS.md) explains window versus pane/tab and the OS permissions.

Important implementation detail: `internal/hooks/hooks.go` and `internal/notifier/notifier.go` share sound, desktop and webhook delivery across Claude, Codex and Qoder. However, terminal-target capture for Ghostty is explicitly Claude-only. Qoder's setup guide currently describes `clickToFocus` as inert. The shared lower-level notifier can attempt generic activation when enabled; its existence is **not** evidence that Qoder has reliable, session-bound navigation. Resolve the documentation/runtime ambiguity with a native test before promoting support.

Modern observer consumers intentionally use narrow fact types and separate registered-consent gates: [OpenCode](../internal/opencodeevent/consumer.go), [Gemini](../internal/geminievent/consumer.go), [Cursor](../internal/cursorevent/consumer.go). All three select `Navigation=None` and do not provide a trusted local terminal origin. The unreleased OpenCode source can explicitly request an OS-default cue, but published v1.49.11 remains silent; Gemini and Cursor remain silent. This is a genuine missing feature, not merely a README omission.

## Additional implementations and candidates (not advertised as shipped)

| Integration | Current repository evidence | Public support status |
| --- | --- | --- |
| Copilot in VS Code | [Local Stop observer](../internal/copilotvscodeevent/consumer.go) and [SDK source](../internal/copilotvscodesource/source.go) exist; silent, informational, `Navigation=None` | Internal code exists; not part of the six-agent public installer/README selection. Native qualification and product exposure need an explicit decision |
| Pi | [Draft PR #539](https://github.com/777genius/agent-notifications/pull/539) adds final completion, blocking extension question, final error, configured sound and local-terminal focus using shared primitives | **Not in main.** Linux native acceptance reported; full macOS/Windows click/sound acceptance pending |
| GitHub Copilot CLI | Native hooks include `agentStop`, `subagentStop`, `errorOccurred`, `permissionRequest` and `notification` | Research / new integration; separate from Copilot in VS Code |
| Kiro CLI | Official hooks provide agent-stop and tool events | Research / new integration; qualify real CLI hooks and local origin first |

Source references: [GitHub Copilot hooks](https://docs.github.com/en/copilot/reference/hooks-reference), [Pi extension events](https://pi.dev/docs/latest/extensions), [Kiro hooks](https://kiro.dev/docs/hooks/).

## Next high-value event improvements

| Agent | Native event opportunity | Guardrail |
| --- | --- | --- |
| Qoder | `StopFailure` provides explicit error categories; `PermissionDenied` is also documented | Distinguish blocked workflow vs noisy per-tool failures; do not include raw error, prompts or tool arguments |
| Cursor CLI | Separate known `stop.status` values (`completed`, `aborted`, `error`) rather than presenting them all as “stopping”; investigate `subagentStop` and tool failures | Verify **Cursor CLI** event dispatch, not merely Cursor IDE hook documentation. Stop status is not proof a task succeeded |
| OpenCode | Native `session.status`, `session.error`, permission events and parent/child sessions offer expansion candidates | Preserve root-only behavior until duplicates, nested ownership and exact event ordering are proven |
| Gemini CLI | Native hooks also include `SessionEnd`, `AfterTool` and more | Session end is **not** task completion; `Notification` currently exposes ToolPermission, not an arbitrary “question” event |
| Copilot CLI | Native `notification` has permission prompts, background shell completion, subagent completion/idle and elicitation; `errorOccurred` is explicit | Local CLI and cloud agent are different surfaces. Cloud hooks cannot directly focus a local window |

Upstream references: [Qoder hooks](https://docs.qoder.com/cli/hooks), [Cursor hooks](https://cursor.com/docs/hooks), [OpenCode plugins](https://docs.opencode.ai/docs/plugins/), [Gemini hooks](https://geminicli.com/docs/hooks/reference/), [Copilot hooks](https://docs.github.com/en/copilot/reference/hooks-reference).

## Reuse boundaries and recommended implementation

- **Keep** host-specific ingress/SDK decoding, authorization/installation gates, dedup ownership and exact event semantics in their current adapters. Do not replace these with one giant universal event model.
- **Share** the minimal stable output: validated notification content/status, immutable trusted origin (local TTY/window/pane or explicitly verified desktop-thread ID), channel/consent snapshot, single deadline, sound preferences and a typed navigation policy.
- **Reuse** `notification.DeliveryPort` and existing native implementations, adding an explicit trusted **terminal-origin navigation route** only when captured and E2E qualified. The current `DesktopTarget.ThreadID` targets Codex desktop; a terminal or arbitrary session ID must not be silently interpreted as one.
- **Separate** `navigation=required` (reject if impossible), `best_effort` (deliver a banner with a truthful unavailable result), and `none`. Do not retry after an unknown native effect; do not change a stored click's destination after installation updates.
- **Differentiate** dispatch success, OS accepted banner, real physical click, window/pane activated and correct chat rendered. None implies the next.
- **Centralize documentation truth** in a typed support data source *after* the actual capabilities and test evidence are qualified; have the landing table and README consume/verify the same record, instead of maintaining independent optimistic booleans.

See the existing [navigation design and review](plans/notification-navigation-adapters-plan-2026-10-03.md). Preserve its privacy, code-signing and native callback-lifetime constraints; avoid rebuilding that subsystem.

## Rough incremental engineering scope

Estimates below are **changed LOC** (not a measurement of currently missing code), against current main, excluding Pi PR #539 and potential large native harnesses. They include validation in the test column but are **not** promises that an unsupported upstream event exists.

| Workstream | Production changed LOC | Focused tests changed LOC |
| --- | ---: | ---: |
| Shared trusted terminal-origin capture, navigation/sound policy and reusable contracts | 300–700 | 450–900 |
| Qoder: reliable origin and qualified focus, direct error event | 100–260 | 180–420 |
| OpenCode: sound, local terminal focus, careful nested event semantics | 200–500 | 300–700 |
| Gemini: sound, local terminal focus, existing event classification | 150–360 | 250–550 |
| Cursor CLI: differentiated Stop, sound, trusted focus and extra events if qualified | 200–550 | 300–700 |
| Copilot in VS Code: decide public scope, local-window focus and sound | 200–480 | 300–650 |

Subtotal: roughly **1,150–2,850 production LOC** and **1,780–3,920 focused-test LOC**, plus **150–300 docs/data LOC** and perhaps **800–2,500 OS native installed-E2E/fixture LOC**; about **3,900–9,600 changed LOC** for this broad scope. Exact desktop-chat navigation for every IDE, cross-OS authorization and a fully repeatable interactive physical-click harness are **separate higher-risk projects** and may exceed those numbers.

A **new** GitHub Copilot CLI or Kiro CLI installer/adapter is each a different effort, provisionally **1,500–4,000 additional changed LOC** including tests/install qualification. Native upstream compatibility and sandbox restrictions can invalidate these ranges. Pi's very large ongoing draft is a reminder that full OS-specific acceptance can dominate adapter code.

## Acceptance before changing a capability from “No” to “Supported”

- Actual installed product + pinned public CLI version on the claimed OS and architecture.
- Correct native event and suppression of abort, continuation, duplicate and unrelated/subagent cases.
- Independent desktop and webhook consent; installation update/disable/remove preserves unrelated integrations.
- Visible banner and audible cue when promised, including muted, permission-denied and fallback cases.
- Real **physical click** after the producer exits: the correct originating window or pane is activated; native target identity and privacy checks survive update/restart. Record weaker accuracy explicitly.
- Clean review, source-bound native test evidence and CI at the exact candidate revision. Unknown/failed native outcomes are not successes.

This document is a **planning/support audit**. It deliberately changes no runtime capability flags or installation policies.
