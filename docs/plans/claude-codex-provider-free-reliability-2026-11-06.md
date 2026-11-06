# Provider-free reliability: Claude Code and Codex CLI

**Status:** working implementation plan, not an acceptance receipt  
**Baseline:** `191c6d835ad902cd6c8818abafd9086990b7c64b` (2026-11-06)  
**Scope:** one focused draft PR, provider-free tests, no real Anthropic/OpenAI requests, no agent subscriptions, no new feature flags.

## Goal and boundaries

Increase the reliability of **Agent Notifications**, not the upstream AI agents. Preserve current source decoding, permission prompts, existing webhooks, native delivery, privacy and the previously qualified Claude/Codex routes. Reuse the Go notifier, hook/state/dedup modules and existing CI.

Do **not** overlap PR #633 (Claude permission prompts) or PR #634 (managed cache migration). Do not change Qoder, Gemini, Cursor, Pi or OpenCode behavior deliberately. No new database, queue, event bus, cross-platform framework or broad rewrite.

### Reproduced from source, not speculation

1. **Ghostty no-authority click**: `focusGhosttyWindowWithOptions` already fails closed for an existing stale terminal ID; AppleScript already enumerates windows → tabs → terminals with uniqueness checks. The remaining ordinary macOS `sendWithTerminalNotifier` path still installs an action when `loadStoredGhosttyTerminalID(sessionID)` returns empty, allowing cwd/AXDocument fallback. Do **not** reimplement the already fixed stale-ID path.
2. **Claude Stop replay during desktop grace period** ([#594](https://github.com/777genius/agent-notifications/issues/594)): the event lock expires after 2s; the cross-hook content lock is deliberately released before `notifyDelaySeconds`; last-delivered state is only committed after a successful/queued effect. A replay can get in during a long delay.
3. **Hook deadline** ([#598](https://github.com/777genius/agent-notifications/issues/598)): the desktop delay is capped to 25s, but earlier parsing/git/settling and later delivery/shutdown use the same finite host hook window. The cap is not an end-to-end guarantee.
4. **Codex regressions**: `handleCodexHook` currently keeps stdout/stderr quiet and fails open; native event parsing/permissions, registration and foreign-hook preservation already have tests. Improve targeted negative-path coverage **only if** a real gap is found; avoid duplicating PR #633.

## Implementation order

### A. Ghostty action authority — small safety change

In ordinary macOS Claude **and Codex** notification delivery, when Ghostty is selected and there is **no stored verified terminal ID**, emit a normal notification **without `-execute` or `-activate`**. Keep audio, body, grouping and non-Ghostty behavior unchanged. Existing terminal-ID action continues to be exact and fail-closed on stale IDs. Ensure multiplexers are considered: a verified exact local pane target can remain eligible only if that route is actually proven; otherwise no action.

**Acceptance:** unit/contract cases for known ID, missing ID, stale ID, multiplexers, no-focus policy, and unrelated terminals. Do not claim live Ghostty/Automation permission success from static tests.

### B. Claude Stop in-flight reservation — narrowly scoped

Only for **Claude Stop with a verified turn identity** and delayed desktop-only delivery, reserve that *turn* before releasing the content lock. A second invocation must see the pending reservation even after the old 2s event lock expires. A distinct turn or other agent must remain independent.

Requirements: atomic cross-process claim; finite expiry after crash; marker integrity; no writing successful delivery state before effects; ownership-safe release on definitive no-effect/error and after successful state update; preserve content lock release while waiting. If the existing dedup/state storage cannot provide these guarantees with a small change, do not introduce an ad-hoc unsafe pseudo-lock — document the precise blocker and split into a separately reviewed change.

**Acceptance (narrow delivered change):** delayed desktop-only Claude Stop with a transcript-derived turn timestamp blocks **same-event** replay after the former 2s lock age; separate turns remain eligible. The marker has a bounded 60s lifetime, unknown age fails closed and definite no-effect is released. Tests age a real marker without a 3-second sleep.

**Not yet solved by this narrow reservation:** a Stop with no reliable last-user timestamp uses the legacy short event lock and can replay during the delay. A concurrent **Notification** hook is not the same native Stop event: suppressing all notifications in the turn would incorrectly swallow a distinct blocking question, so cross-hook same-body coordination remains unqualified. Crash after native send but before state commit can produce a second banner after marker expiry; the attempt marker is *not* an exactly-once guarantee or a successful-delivery record. These cases need a separately reviewed shared claim backed by the proven event/body identity before closing issue #594; do not claim it completely fixed here.

### C. Deadline-aware delay — entrypoint-owned budget

Capture the 30s host-hook budgets **separately for Claude and Codex** from binary startup; bound optional Git prework through a contextual 1s lookup with bounded output-pipe drain, and reject desktop delays that cannot leave five seconds for submission and shutdown. Limit webhook shutdown to remaining entrypoint time. Preserve the existing configured cap, webhook-before-desktop ordering, DND/focus recheck *after* the delay and no false sent/cooldown receipt when expired. Codex must preserve its observation-only empty stdout/stderr/exit-0 contract. Do not reuse Claude's timeout as an assumed universal Codex/Qoder timeout.

**Acceptance:** source-bound Git child timeout and output-pipe cleanup; elapsed prework reduces budget; an oversized delay is skipped (not silently shortened) with no desktop-success record; late wake/cancellation and zero budget produce no false receipt; webhooks remain independent. These guards reduce overruns but do **not** prove every possible native close/OS operation always completes before the host kills the process. Prefer fake clocks/injected sleepers for unit tests; reserve real native process deadline for provider-free CI.

### D. Codex negative-path and cross-agent regressions

Extend only uncovered cases around `setup-codex` lifecycle/foreign-hook preservation, invalid input, permission classification, duplicate delivery and fail-open console silence. Run existing `setup_codex_e2e_test.go`, `codex_cli_test.go` and Claude SDK blackbox tests. Do not write unverified new Codex event semantics.

## Decision review: keep scope small

| Candidate | Reliability / 10 (estimated) | Complexity / 10 | Changed LOC including tests (estimate) | Decision |
| --- | ---: | ---: | ---: | --- |
| Gate Ghostty action on verified terminal ID | 9 | 3 | 80–200 | Implement first; guard mux exceptions |
| File-backed Claude Stop reservation | 8 | 7 | 230–550 | Only with cross-process ownership and expiry proof |
| Deadline propagation across hook entrypoint | 9 | 6 | 220–520 | Implement per host, not global timeout |
| Targeted Codex regression tests | 9 | 2 | 60–160 | Do only evidence-backed missing cases |
| Universal session DB / notification service | 5 | 10 | 1,000+ | Reject |
| Synthetic title/PID matching as authority | 2 | 3 | small | Reject: wrong-window risk |

### Critical review checkpoints (three independent perspectives)

1. **Security/privacy:** no guessed Ghostty target, no fallback to an unrelated window, no foreign config mutation, never log prompts/tokens/transcript.
2. **Concurrency/architecture:** pending Stop cannot double-send across processes, crash and failure semantics are explicit; no long-held content lock or unsafe global state; preserve SOLID boundaries.
3. **E2E/release:** provider-free subprocess fixtures are *not* a live authenticated AI turn. Require exact-head CI and independent native macOS/Windows qualification before claiming observed OS click/focus. Review source and tests, resolve actionable CodeRabbit/Codex feedback.

The review passes are a **plan for critical review**, not evidence that external agents have already run.

## Validation and completion gate

- `gofmt` changed Go files; focused `go test -race` for notifier/hooks/dedup and Codex CLI paths.
- Existing provider-free `scripts/claude_sdk_e2e.py` with real product binary, isolated HOME, loopback webhook; `setup-codex` install/update/remove with foreign-hook preservation.
- Run platform CI on the exact draft PR HEAD (Ubuntu, macOS, Windows, lint, static checks). Re-run **only** if source or a proven CI defect changes; inspect exact job logs and review threads.
- Final review of code and OS boundaries. Validate **each new exact HEAD**, including documentation-only commits. Draft PR may skip native macOS/Windows qualification: transition to ready-for-review at the validation gate rather than treating skipped jobs as passed. Merge only after required checks and review acceptance; follow with post-merge verification.
- Explicitly report any missing macOS real-notification/Click-to-Focus proof or authenticated upstream Claude/Codex turn. **Never mark it qualified based solely on mocked input.**

## Review record — updated after initial external and local critical passes

- **External CodeRabbit (plan R1):** explicitly include Codex in Ghostty no-ID gate (implemented in shared notifier); define what happens for overlapping Stop/Notification (excluded from same-event guarantee); document unverified-turn replay and post-delivery crash expiry (limitations above); bound Git prework and pipe drain (implemented); validate after every new HEAD (gate above).
- **Codex remote review request:** attempted; service reported review-usage quota exhausted. No Codex review success is claimed.
- **Security-focused local review R2:** no cookie/session/title/cwd identity is used to *authorize* new Ghostty actions; verified multiplexer action remains explicit. Distinct host budgets and informational/no-effect outcomes do not grant additional navigation.
- **Concurrency-focused local review R2:** existing two-second event lease was extended only for identified Claude Stop turns with delayed desktop-only delivery; the content lock still releases before the delay. Unqualified cross-hook and post-crash exactly-once behavior are documented rather than replaced by a new state framework.
- **Qualification-focused local review R3:** offline CLI and test fixtures can prove Agent Notifications output, install contracts and synthetic OS callbacks, never upstream provider-authenticated turn or user-visible macOS focus without GUI evidence.

**Scope clarification:** #594 and #598 are *reliability improvements*, not claims that every edge case from those issues is solved. Do not close the issues without their separately stated acceptance evidence.
