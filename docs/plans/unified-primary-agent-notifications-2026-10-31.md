# Unified notifications for Claude Code, Codex CLI and OpenCode

**Date:** 2026-10-31. **Baseline:** main at 06ab7ae317c832e909bc76e998d07e388596292a. **Status:** plan/spikes, not native acceptance. **PR:** one coherent draft; source changes must be proved at its exact head.

## 1. Goal and precise success condition

One consistent delivery policy for three priority agents, using existing cross-platform notification infrastructure and retaining each native event model. Attention/completion alerts and their sounds respect per-agent consent. Clicking an alert returns to the originating terminal window/pane **only when that origin is proven** and supported; exact desktop-chat navigation is a distinct, stronger feature. Unavailable origin leaves a truthful informational banner rather than opening an arbitrary session.

**Three different observations:** notification submitted to OS; physically visible/clickable banner; callback actually focuses the correct live window or pane. None implies the next. Similarly “turn ended” is not guaranteed task success.

**Scope:** Claude Code, Codex CLI, OpenCode V1/V2 compatibility, minimally shared policy/transport, verified local origin, safe sound, focused E2E, README/site changes for proven features. **Non-goals:** Gemini, Cursor, Qoder, Pi (#539 separate); new queue, DB, generic event bus or DI; replacing UAP; refactoring unrelated installer/versioning; adding all possible host event types; inventing exact chat IDs; wholesale conversion of the legacy notifier; cloud/remote focus unless a separate bound client exists.

**Architecture:** Clean Architecture/SOLID with explicit ports, no domain imports of SDKs/OS. Lightweight DDD terms (notification, local origin, target, consent, delivery outcome), but no fictitious aggregate/repository. DRY only where at least two consumers genuinely share the same invariant. Favor composition and static wiring.

## 2. Current source mapping

| Boundary | Existing contract | Retain/adjust |
| --- | --- | --- |
| Claude/Codex ingress | `internal/hooks/{event,claude_source,codex_source}.go`, `hooks.Handler` | Keep host-specific decoding/classification, transcript handling, per-agent policy/dedup |
| OpenCode ingress | `opencode-plugin/*`, `internal/opencodeevent/{admission,composedconsumer}.go` | Keep private native facts, clock admission, installed generation gate, root filtering |
| Shared delivery | `internal/notification/delivery.go`: `Request`, `Receipt`, `DeliveryPort`, explicit `Navigation` | Reuse as stable application port; no second universal pipeline |
| Claude/Codex legacy notifier | `internal/notifier/notifier.go`, `focus_*.go`, `internal/daemon`, Swift helper | Preserve real configured cues and terminal focus; adapt only proven gaps |
| OpenCode typed native notifier | `internal/notifier/delivery*.go`, `nativeprotocol` | Keep OS-specific transactional/callback boundaries; not automatically equivalent to legacy focus |
| Exact Codex Desktop chat | `internal/agentnotify/*` and typed `DesktopTarget.ThreadID` | Must keep its explicit installed client identity/authority; never derive from hook session ID |
| Setup/config | `internal/installruntime`, OpenCode setup, UAP | Existing per-agent channel consent and compatibility authority remain |

**Verified gaps:** (a) Ghostty capture is Claude-only and checks a Claude-labelled frontmost tab; Codex raw session ID must not suddenly be persisted. (b) Both OpenCode consumers set `Navigation=None`, `Silent=true`, `SoundEnabled=false`; merely changing booleans cannot create a trusted callback or custom cue. (c) Current OpenCode server plugin can receive events from remote/multiple TUI clients, without knowing the originating local terminal window. (d) Linux/Windows modern native delivery is informational; only supported, typed callback routes may promise navigation. (e) website support table is independently hardcoded from README.

## 3. Required spikes before enabling capabilities

**S1: Claude/Codex terminal origin.** Compare actual installed native hook payload and environment for Stop, permission, notification; check Ghostty, Warp, WezTerm, kitty/tmux and Windows Terminal. Same project in two windows is a mandatory negative test. Test whether exact pane identity can be obtained from owned terminal environment, or only a best-effort window. Retain Codex hashed state: never store raw Codex ID to copy Claude implementation. If no trusted identity, keep existing focus precision and say so.

**S2: OpenCode V1/V2 locality.** Server-side plugin facts have session/turn but not TUI ownership. Investigate actual installed V2 CLI TUI plugin surface, local route/tabs APIs and `context.attention.notify` for sound; distinguish released V2 from upstream dev specification. Proof requires one local TUI, server/remote case and two clients using the same session. A host-owned attention notification is not evidence that clicking returns to a terminal; an additional native callback with durable origin binding might be required. Until proven, keep `Navigation=None` for server events and do not misroute.

**S3: sound capabilities.** Verify Claude/Codex configured custom audio still obeys volume/device/DND. OpenCode modern `Silent` flag controls default OS notification sound, not configured audio files; V2 TUI host offers sound API but may duplicate our banner/cue. Choose exactly one sound owner per route. Missing explicit opt-in, unsupported host or double sound -> remain silent. No arbitrary sound file paths from native events.

**S4: native callback lifecycle.** For any newly advertised click test installed producer, real visual banner and **physical click after producer exits** on the claimed OS. Native helper signature, origin identity, update/uninstall revocation, OS authorization prompts, duplicate clicks, wrong-window attempts and unknown outcomes are critical. Do not retry after an OS submission of unknown effect. OS acceptance is not click success.

**S5: isolated regression.** Existing focused Go tests, OpenCode Node plugin tests, real pinned CLI with loopback provider and TEST configuration. No unrelated user project, live credentials, transcript text or broad new test framework. Each native receipt records source SHA, CLI version, OS/arch, binary/helper identity, real click/visual/cue and cleanup.

## 4. Decision after spikes

Use existing `notification.Request`, `PolicySnapshot`, `DesktopTarget`, `DeliveryPort` and `Receipt` for cross-agent delivery. The legacy Claude/Codex notifier is already a desktop adapter; do not replace its tested host-specific behavior merely to reduce LOC. Extract a **small pure function** for common sound/navigation admission only if two actual callers share it; no sprawling `UniversalAgentEvent` structure.

A terminal-origin target, if proven, is a separate typed variant from Codex Desktop `ThreadID`. The OS adapter must validate the binding, not the prompt or notification content. Never derive target from `cwd`, app title, session name, model output or process PID alone. Distinguish required (reject before effects if no route), best-effort (may show non-navigable banner with reason) and none (no route lookup). Retain the same immutable deadline and source generation.

Do not call native OS implementations from the domain/hook source. Notification use cases own **what/when**; shared delivery port owns **submission**; platform adapter owns **which verified target**; hook and OpenCode ingestion own **their event protocol**. Webhooks cannot carry local click actions. Single attempt and unknown-outcome semantics remain.

**OpenCode V2 variant:** only on a qualified TUI-local plugin API may the host own local notification/sound or explicitly bind a native callback to the selected TUI. Preserve current V1/V2 server-observer behaviour otherwise. Do not auto-enable native host attention alongside Agent Notifications; avoid false task-success semantics and duplicate notifications.

## 5. Small implementation plan

1. Baseline exact `main` and existing PRs (#539 Pi, #549 docs); open one draft PR for this plan. Do not merge documentation PR blindly into implementation branch.
2. Add characterization tests for missing/untrusted origin, default sound off, disabled channels, root/subagent event, unknown delivery and same-directory two-window ambiguity. No global test framework.
3. Complete S1. Patch Claude/Codex shared terminal-origin capture **only** if it is secure and tested; preserve raw Codex privacy and current OS focus behavior. If negative, do not expand Click-to-Focus claim.
4. Complete S2/S3. If released V2 TUI sound is qualified, wire it via one explicit per-agent opt-in and independently suppress competing alerts; if not, keep OpenCode server notifications silent. Never convert server `sessionID` into a click URL.
5. Add smallest neutral delivery-policy shim **only after** verified shared use. Preserve independent event classification, source decoding, consent and cancellation. Change no ledger/schema or installer semantics unless unavoidable and proved by a focused test.
6. For each newly enabled OS/terminal, add a test for the exact callback target + physical click after sender exit, and failure/unknown variant. Do not advertise unsupported exact pane/chat precision.
7. Update README, support matrix, landing based on **qualified** agent/OS/arch and channel evidence, not aspirations. Use a single typed data record for new claims if appropriate; no codegen.
8. Three sequential plan review rounds (architecture; trust/privacy; E2E/scope), record every finding and correction below. Then implementation code review and resolved PR comments; rerun affected gates only plus required exact-head CI.
9. Mark draft ready only after tests and native proofs. Merge after review and green checks; verify merged main. A blocked native feature remains disabled and separately documented rather than claiming finished E2E.

## 6. High-signal acceptance matrix

| Case | Expected result |
| --- | --- |
| Claude Stop, question, permission, subagent | Same event classification, one alert, existing audio/focus unchanged unless a proved fix |
| Codex Stop, permission, question and subagent | One correct alert, no untrusted chat target, no raw ID persistence, no wrong-window focus |
| OpenCode V1/V2 root idle/question/permission/error | One qualifying attempt, one channel per consent; no nested false positive or altered webhook payload |
| Remote/headless/multiple OpenCode clients | No invented local origin; no wrong TUI focus or duplicate cue |
| Disabled desktop, webhook, click, sound | Independent settings work; no implicit upgrade, no fallback retry |
| Native accept/unknown/reject | Distinct receipt and truthful support claim; never replay unknown effect |
| macOS permission denied; unsupported Linux Wayland; Windows pane unavailable | Explicitly reduced precision; no arbitrary command or selected wrong window |
| Installed click after source exits on each newly supported OS | Correct origin raised; source/callback generation and permission preserved |
| Install/update/remove | Existing per-agent consent retained, foreign files untouched, old callback cannot redirect |
| Exact source CI | Go vet/fmt/race, OpenCode Node tests, required native workflow, review and merge all pass |

**Native qualification gates** must be scoped to actual new support claims, but are not replaced by mocked callback unit tests. Linux X11/Wayland, macOS and Windows are independent evidence layers. No unconditional feature flag defaults or unqualified marketing promises.

## 7. Risks and rollback

| Risk | Go/no-go |
| --- | --- |
| OpenCode server is not a reliable local TUI origin | No server-side Click-to-Focus; use separately qualified local TUI API or keep informational |
| Codex state currently hashes session ID | Reject raw Codex ID persistence; preserve existing state model |
| Same-folder terminals cannot be distinguished | Window-level best effort only if existing implementation documents it; never exact pane |
| New native sound duplicates host attention | Single owner or disable new audio |
| Callback target may change between publish and click | Capture immutable trusted generation or refuse |
| OS asks for Accessibility/Screen Recording permission | Respect deny; never bypass or grant automatically |
| Concurrent Pi #539 or Windows navigation #423/#424 | Do not depend on unmerged branch; rebase/requalify if main changes |
| Broad refactor grows code/test LOC | Cut unrelated abstractions, avoid speculative target/plugin registry |
| CI green but physical click not observed | Feature stays unqualified; don't merge with a false support claim |

**Rollback:** revert only new policy/adapter files or disable opt-in and preserve old registration/state, never rewrite existing user settings. Existing OpenCode informational notifications, Claude/Codex delivery and explicit Codex Desktop navigation must continue functioning.

## 8. Three critical reviews (must be filled after real review)

### R1: SOLID, Clean Architecture, DDD/DRY — completed (self-critical, not an external subagent)
**Challenge A (accepted):** “one delivery mechanism” is not synonymous with making the legacy hook notifier call the modern structured port. Existing Claude/Codex callback and OpenCode native lease have different security/admission lifetimes. Revised decision: share *semantic decisions* and tested lower-level OS services, keep compatibility adapter until verified migration. No large new architecture package.
**Challenge B (accepted):** a new support manifest/code generator would add tooling without improving native qualification. Revised decision: update the existing README/site entries from measured source evidence; if divergence appears, add one narrow static consistency test rather than a generator.
**Challenge C (accepted):** a DDD aggregate/repository around one-shot notifications adds no business invariant. Revised decision: small value types and ports already in internal/notification are sufficient; no persistence layer.
**Challenge D (accepted):** sound support is an *effect*, not an event; don't blend source-specific event classification with one generic status analyzer.
**Scope correction:** first production change will repair a concrete shared legacy notifier defect, with a regression test: macOS multiplexer detection currently runs even when clickToFocus=false and can produce executable click actions. Then proceed to any newly proven OpenCode V2 local route. A plan alone does not qualify the new route.

### R2: authorization, privacy and lifecycle
Pending: adversarial event-origin ambiguity, callback lifetime, version binding, OS permissions, dedup and sound duplicate findings.

### R3: executability and E2E qualification
Pending: inspect actual released OpenCode V2 features, test feasibility, current CI, narrow scope, rollback, merge conditions. Negative spikes must remain negative.

## 9. Definition of done

All three priority agents retain installed event flows and settings; common delivery semantics are demonstrably reused where safe, and any new sound/focus feature has independent real OS evidence. Unverified local TUI focus stays unavailable. No unauthorized/misdirected click, hidden double alert or unbounded retry. Exact PR head tests and implementation review pass, documentation matches facts, merge commit confirmed in main.

**Sources:** repository paths in §2; [existing navigation plan](notification-navigation-adapters-plan-2026-10-03.md), [support audit PR #549](https://github.com/777genius/agent-notifications/pull/549), [Claude hooks](https://code.claude.com/docs/en/hooks), [Codex hooks](https://learn.chatgpt.com/docs/hooks), [OpenCode server plugins](https://opencode.ai/docs/plugins/), [OpenCode V2 TUI plugins](https://opencode.ai/v2/docs/build/plugins/cli/).
