# Agent support: capabilities, evidence and expansion

Navigation capability reviewed against [#629](https://github.com/777genius/agent-notifications/pull/629) on 2026-11-06. Event and audio evidence below retains its own earlier scope. **Implemented guarded Linux/X11 focus is not the same as end-to-end qualification of a real authenticated CLI turn and its notification click.** Platform/version evidence remains in the linked guides.

The v1.50.0 artifacts are frozen at [7da3329d (C5)](https://github.com/777genius/agent-notifications/commit/7da3329d625eae9cdb29ce665f413e9fb9d03d3a). This source includes Qoder's `StopFailure` handler and five bundled MP3 additions from [#558](https://github.com/777genius/agent-notifications/pull/558), covered by source/package/runtime tests. Native audible qualification remains pending in [#562](https://github.com/777genius/agent-notifications/issues/562); packaged assets and unit checks do not establish full Qoder desktop acceptance.

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
| Qoder CLI | Yes: Stop | Yes | Yes | Yes: generic StopFailure and separate rate-limit classification since v1.50.0 | Plan yes; review no | Limited: opt-in SubagentStop; idle teammates |
| Pi | Yes: final successful settlement | Limited: blocking extension UI | No dedicated permission event | Yes: final failed settlement | No dedicated plan/review | No intermediate/aborted completion |

Sources: [Claude/Codex/Qoder product event boundary](../internal/hooks/event.go), [shared hook handler](../internal/hooks/hooks.go), [OpenCode consumer](../internal/opencodeevent/consumer.go), [Gemini consumer](../internal/geminievent/consumer.go), [Cursor consumer](../internal/cursorevent/consumer.go); [Qoder](QODER.md), [Codex](CODEX.md), [OpenCode](opencode-notifications.md), [Gemini](gemini-notifications.md), [Cursor](cursor-notifications.md).

## Implemented integrations: channels and navigation

| Agent | Desktop | Webhooks | Configured sounds | Click: local terminal/window | Click: exact desktop chat |
| --- | --- | --- | --- | --- | --- |
| Claude Code | Yes | Yes | Yes | Supported, OS/terminal-dependent; pane precision varies | Not established |
| Codex CLI | Yes | Yes | Yes | Legacy terminal focus where supported | Separate verified, configured Codex-desktop navigation path; platform/installation qualification matters |
| OpenCode | Yes | Yes | Opt-in OS-default `systemSound` cue since v1.50.0 | No: `Navigation=None` | No |
| Gemini CLI | Yes | Yes | Opt-in OS-default `systemSound` cue since v1.50.0 | **Guarded opt-in local Linux/X11 window**; authenticated Gemini-to-click E2E not yet qualified | No |
| Cursor CLI | Yes (qualified hosts only) | Yes | Opt-in OS-default `systemSound` cue since v1.50.0 | **Guarded opt-in local Linux/X11 window** on eligible hosts; authenticated Cursor-to-click E2E not yet qualified | No |
| Qoder CLI | Yes | Yes | Configured cues; five MP3s bundled, native audible proof pending (issue #562) | **Guarded opt-in local Linux/X11 window**; authenticated Qoder-to-click E2E not yet qualified | No |
| Pi | Local TUI only | Yes, explicit consent | Yes, configured local TUI | Originating terminal; Linux desktop suite, owner-observed Mac banner/click; full macOS/Windows native acceptance pending | No |

“Desktop” means an implemented submission path, not visible UI acceptance on every OS. **Guarded** means the code can offer a window-focus action only with per-agent schema-2 consent and a verified local X11 terminal origin; **qualified** additionally requires the genuine installed agent's authenticated event → banner → click → correct window evidence. PR [#629](https://github.com/777genius/agent-notifications/pull/629) verified the shared strict daemon with Xvfb/dunst/Xterm and a synthetic `xdotool` click using an isolated Go sender, **not** three authenticated CLI turns. Unverified origins, Wayland, remote/headless and macOS/Windows have informational notifications only for these three agents. [Detailed consent and focus rules](CLICK_TO_FOCUS.md#guarded-local-cli-routing-qoder-gemini-cli-cursor-cli).

Pi's process/lifecycle suite uses 27 cases per OS on Linux, macOS and Windows
with isolated TEST profiles. This scope does not establish full native
macOS/Windows desktop acceptance; see [#561](https://github.com/777genius/agent-notifications/issues/561).

Implementation boundary: Qoder's **managed** installed hooks use [the guarded terminal target](../internal/hooks/hooks.go); Gemini and Cursor bind the same source-derived target through their installed observer consumers. The pre-existing Claude-only Ghostty capture is a different route. Focus is limited to the proven local X11 window (not a pane or exact desktop chat); consent, installation generation and X11 ownership/lifetime are rechecked on click.

Modern observer consumers retain narrow native facts and separate installation/channel gates: [OpenCode](../internal/opencodeevent/consumer.go), [Gemini](../internal/geminievent/consumer.go), [Cursor](../internal/cursorevent/consumer.go). OpenCode stays `Navigation=None`. Gemini and Cursor attach `Navigation=BestEffort` plus an immutable terminal origin **only when the strict Linux/X11 consent and origin checks pass**; otherwise they remain informational (`Navigation=None`). Optional `systemSound` is independent from navigation and never grants focus. The internal Copilot-in-VS-Code integration remains out of the public agent list.

## Additional implementations and candidates (not advertised as shipped)

| Integration | Current repository evidence | Public support status |
| --- | --- | --- |
| Copilot in VS Code | [Local Stop observer](../internal/copilotvscodeevent/consumer.go) and [SDK source](../internal/copilotvscodesource/source.go) exist; informational, `Navigation=None`, optional OS-default cue in source | Internal code exists; not part of the seven-agent public installer/README selection. Native qualification and product exposure need an explicit decision |
| GitHub Copilot CLI | Native hooks include `agentStop`, `subagentStop`, `errorOccurred`, `permissionRequest` and `notification` | Research / new integration; separate from Copilot in VS Code |
| Kiro CLI | Official hooks provide agent-stop and tool events | Research / new integration; qualify real CLI hooks and local origin first |

Source references: [GitHub Copilot hooks](https://docs.github.com/en/copilot/reference/hooks-reference), [Pi extension events](https://pi.dev/docs/latest/extensions), [Kiro hooks](https://kiro.dev/docs/hooks/).

## Next high-value event improvements

| Agent | Native event opportunity | Guardrail |
| --- | --- | --- |
| Qoder | Qualify visible StopFailure delivery and audible cues; investigate documented `PermissionDenied` | Distinguish blocked workflow vs noisy per-tool failures; do not include raw error, prompts or tool arguments |
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

## Historical pre-#629 engineering estimates

The figures below are **historical pre-#629 planning estimates**, not current missing-code or remaining-effort estimates. The guarded Linux/X11 terminal-origin route for Qoder/Gemini/Cursor has since been implemented. Additional authenticated installed-CLI click qualification and expansion beyond local X11 are separate work.

| Workstream | Production changed LOC | Focused tests changed LOC |
| --- | ---: | ---: |
| Shared trusted terminal-origin capture, navigation/sound policy and reusable contracts | 300–700 | 450–900 |
| Qoder: reliable origin and qualified focus, direct error event | 100–260 | 180–420 |
| OpenCode: sound, local terminal focus, careful nested event semantics | 200–500 | 300–700 |
| Gemini: sound, local terminal focus, existing event classification | 150–360 | 250–550 |
| Cursor CLI: differentiated Stop, sound, trusted focus and extra events if qualified | 200–550 | 300–700 |
| Copilot in VS Code: decide public scope, local-window focus and sound | 200–480 | 300–650 |

Historical subtotal: roughly **1,150–2,850 production LOC**, **1,780–3,920 test LOC**, plus documentation and native E2E harnesses; **not** a forecast of work left after #629. Exact desktop-chat navigation and cross-OS authorization remain separate, higher-risk efforts.

A **new** GitHub Copilot CLI or Kiro CLI installer/adapter is each a different effort, provisionally **1,500–4,000 additional changed LOC** including tests/install qualification. Native upstream compatibility and sandbox restrictions can invalidate these ranges. Pi's native validation harness illustrates how full OS-specific acceptance can dominate adapter code.

## Acceptance before upgrading “Guarded” to fully qualified support

- Actual installed product + pinned public CLI version on the claimed OS and architecture.
- Correct native event and suppression of abort, continuation, duplicate and unrelated/subagent cases.
- Independent desktop and webhook consent; installation update/disable/remove preserves unrelated integrations.
- Visible banner and audible cue when promised, including muted, permission-denied and fallback cases.
- Real installed **authenticated CLI event → OS banner → click** after the producer exits, with focus confirmed on the correct originating window/pane. The strict CI's synthetic X11 click from a Go fixture validates shared mechanism but does not satisfy per-CLI qualification.
- Clean review, source-bound native test evidence and CI at the exact candidate revision. Unknown/failed native outcomes are not successes.

This document is a **planning/support audit**. It deliberately changes no runtime capability flags or installation policies.
