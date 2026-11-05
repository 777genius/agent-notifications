# Notification delivery and focus boundaries — scoped refactor plan

Date: 2026-11-06
Baseline: main @ `6161d011f5ef2f4abb32f30efe6493d1a411be3c`
Status: **reviewed design proposal; code and CI qualification not yet complete**.

## Purpose

Reduce remaining concentration of unrelated responsibilities in the notification *presentation and delivery* path, with explicit ownership and executable compatibility evidence. This is a source-organization refactor **not** a new installer, desktop protocol, model or release.

A request allowance of 10–20k changed lines is **not a quota**. A line moved counts as one deletion and one addition. If fewer changes suffice, stop. Do not remove behavior, tests, compatibility paths or packaged artifacts because they appear old.

Two large adjacent refactors have already landed:
- #590 (+15,092/-8,771) established product-owned event policy, neutral desktop installation mechanics, honest partial installation results and application-owned setup.
- #612 (+5,765/-4,597) reorganized installer policy, native staging, orphan recovery, configuration and setup CLI. Its inherited behavioral findings are tracked by #616.
- #606 and #608 then fixed narrowly-scoped setup result and group-membership defects.

This proposal **does not repeat** their work, absorb their follow-ups, or touch their storage schemas. Native Windows callback work (#423/#424/#577), SDK group target work (#623/#595), Qoder replay work (#625/#600) and release verification (#624/#619) remain separate. Rebasing must recheck overlap before every slice.

## Measured source evidence (at baseline)

| Package/file | Observed source | Mixed responsibilities | Decision |
| --- | --- | --- | --- |
| `internal/notifier/notifier.go` | 915 lines | Public `Notifier` / `SendDesktop`; macOS helper execution/argument construction; click-to-focus script construction; beeep fallback; audio player lifecycle; legacy message extraction | **S1**: group by existing responsibilities **within the same package**. Preserve all public/unexported names, state fields, hook function seams and fallback ordering. |
| `internal/daemon/focus.go` | 855 lines, `//go:build linux` | Focus fallback order; exact X11/title handling; GNOME, wlroots, KDE selection; WezTerm pane handling; candidate ranking | **S2**: separate Linux-specific focus strategies into same-package files and retain the single ordered focus entry point. |
| `internal/summary/summary.go` | 675 lines | Status-specific text; JSONL extraction; duration and tool counts; Markdown cleanup and defaults | **S3**: separate presentation, extraction/actions, and sanitization. Keep exact output and API. |
| `cmd/claude-notifications/main.go` | 935 lines | CLI bootstrap/dispatch; hook routing; Windows lazy update; `play-sound`; window focus CLI; usage | **S4**: thin entrypoint with same-package command adapters, **only** if S1–S3 succeed. No new CLI framework. |
| `internal/webhook/webhook.go` | 445 lines | Retry/request lifecycle and payload building already coexist with separate `formatters.go`, `runtime.go`, `retry.go` | **Out of scope**: no evidence that a new split would pay for its churn. Queue/delivery semantics tracked in #596. |
| `internal/installruntime/transaction.go` | 51,645 bytes | Durable transaction ownership and recovery fences | **Out of scope**: high-risk security/recovery kernel with active overlapping Windows work. |

This is a reviewability and change-isolation diagnosis, **not** evidence that every long file has a user-visible bug. No behavioral fix will be hidden inside movement. Any suspected bug gets a failing baseline regression and separate explicit patch or follow-up.

## Architecture and nonnegotiable constraints

Existing Go package boundaries already express bounded contexts:
1. Agent source adapters and event admission classify input and own session keys/claims. They choose what to notify; no transport implementation owns product policy.
2. `internal/summary` produces a body/actions representation from parsed transcript data. It has no OS focus, installation or network side effects.
3. `internal/notifier` owns delivery presentation and effects: native helper, beeep fallback, sound. It consumes existing `config`, `notification.DeliveryPort` and context contracts.
4. `internal/daemon` owns Linux click-to-focus policy and invocation of established native desktop mechanisms. The session context is captured in the hook process, never re-guessed from the daemon environment.
5. CLI entrypoints are adapters: parse/dispatch/exit; do not become alternative transaction or presentation engines.
6. `internal/desktopinstall`, `installruntime` and `agentnotify/setupwizard` retain native registration, durable ownership and application setup decisions.

DDD applies as *domain ownership boundaries*, not as a requirement to invent entities, aggregates or repositories around every Go function. SOLID/DRY are met through responsibility locality and existing seams, not a new DI container. `get-modular` is **not** introduced: changing module infrastructure would require a cross-repo design and touches unrelated Go runtime and release contracts.

Hard preservation rules:
- No module/dependency upgrade, feature flag, new Go package, public API rename or plugin/skill deletion.
- No persisted/config JSON change; no AUMID, desktop-entry, bundle ID, binary name, permission or activation scheme change.
- Preserve `SendDesktop` path preference and ordering: native macOS, native error classification and uncertain-effect refusal, Linux/Windows click-to-focus, beeep fallback; silent-by-default macOS; sound independent of toast result when already specified.
- Preserve `beeep.AppName` differences between Windows and Linux/macOS and restore behavior; do not change global concurrency semantics as part of a move.
- Preserve exact `-group`, `-threadID`, `-nosound`, `-execute`, and flag-value interpretation. The legacy macOS helper is a compatibility lane, not proof of dead code.
- Preserve focus sequence: Warp/ID/title, ordered compositor fallbacks, then Zellij/WezTerm pane handling, including the exact GNOME activation-token delay and observed errors; never silently change fail-open/fail-closed behavior.
- Preserve all summary output bytes, truncation, JSONL recency heuristics, action counts, defaults and status-specific paths.
- Preserve CLI help text, stdout/stderr, exit codes, Windows updater throttling, and non-TTY behavior.
- Every new Linux-only file retains `//go:build linux` **before** package declaration. No build-tag edits to other platforms, cgo files, native scripts or binaries.

## Implementation sequence (one branch, one draft PR)

### S0 — Baseline, plan and evidence

- Anchor review to exact main SHA and current open PRs. Record affected functions and package imports. Audit existing tests before moving any code.
- Commit this plan first, request external review on the draft, and record actual reviewer feedback separately from internal analytical passes.
- Three critical perspectives (architecture, native safety, test evidence) review scope; implement only changes supported by the reviewed plan.
- Before each following slice, verify main/head divergence, check for new overlapping PRs, and avoid force pushes or rewriting others' history.

### S1 — Desktop notifier delivery responsibility

- Keep `Notifier`, its state and public `New`/`SendDesktop` as the package entrypoint.
- Move the existing macOS helper execution and argument construction into `terminal_notifier.go`; keep helper path selection and invocation order unchanged.
- Move click-to-focus shell construction into `terminal_focus_script.go`; preserve quoting, executable resolution and explicit opt-outs.
- Move beeep/one-off fallback into `fallback.go`, with the existing platform identity and Windows toast false-positive rules.
- Move detached/inline audio, player init and `Close` into `sound.go`; no new goroutine, lifecycle or retry behavior.
- Move the old message-prefix extraction helper only if it improves locality, otherwise leave it untouched.
- Keep all functions, test-overridable globals and comments. Recompute imports per file, use gofmt, and verify declaration multiset and body equivalence from the baseline. No functional modification in this slice.

### S2 — Linux focus responsibility

- Retain `FocusHints`, ordered `GetFocusMethods`, `TryFocus* ` entry points and the exact WezTerm/Zellij post-focus semantics in `focus.go`.
- Move exact X11 ID/window-title handling to `focus_exact_linux.go`.
- Move compositor-specific GNOME/KDE/wlroots adapters to `focus_compositor_linux.go`, with existing function names.
- Move xdotool candidate search/ranking to `focus_x11_linux.go`; retain lookup order and process/project matching.
- Keep helper functions whose consumers span groups in their existing file or an explicitly named small helper file. No generic strategy registry or new interfaces.
- Every moved declaration receives the Linux build constraint. Prove no files were dropped or included on the wrong OS.
- Compare order-sensitive fallback and failure output against baseline fixtures; tests for GNOME, Wayland, X11, JetBrains, WezTerm, Warp and Zellij remain unchanged.

### S3 — Summary presentation responsibility

- Keep `GenerateFromMessages`, `GenerateFromMessagesStructured`, `GenerateFromTranscript`, `GenerateSimple`, `GetDefaultMessage` and their signatures.
- Split status-specific body extraction, transcript/tool action accounting, and Markdown/truncation cleanup into cohesive same-package files.
- Keep exact regexes, constants, text ordering, emoji and limits; no new dependencies, data model or translated output.
- Run `internal/summary` and `internal/hooks` tests, including snapshot-like existing expectations that would catch subtle differences.

### S4 — Thin CLI entrypoint (conditionally)

- Only after S1–S3 are green, move hook argument routing, Windows lazy-updater, play-sound/focus-window command adapters and static usage into focused files in the **existing** `main` package.
- Keep `main()` and initialization order in `main.go`. No new command framework, no registration system or side effect in `init()`.
- Skip this slice if open PR overlap appears or if evidence shows platform-sensitive command packaging cannot be checked. This is an explicitly permitted stop, not a failed obligation to reach a line count.
- Preserve all flag validation and printed error strings; test both hook-event invocation and human CLI routes.

## Validation contract — required for completion

1. **Structural before/after proof** for each slice: all top-level declarations retained, no duplicate/missing symbols, verbatim non-comment Go body tokens for moves; directives, build tags, package names and annotations preserved. Correct the changes if preservation fails.
2. **Fast checks**: gofmt, `go vet`, `go test` and `go test -race` on affected packages, `go build ./cmd/claude-notifications`, with Go >= 1.25.8 and frozen dependencies. Go 1.23 is not an admissible build environment.
3. **Cross-product**: `./internal/hooks`, `./internal/summary`, `./internal/notifier`, `./internal/daemon`, `./internal/webhook`, `./cmd/claude-notifications`, and the relevant `./tests/integration` fixtures; no disabling tests.
4. **Windows**: Windows CI Go/race and native supported lanes on exact head. Non-Linux build excludes new daemon Linux files; Windows executable and sound command compile.
5. **macOS**: macOS Go/race, terminal helper argument/flag tests, sound, native/structured delivery and existing real platform tests. No claim of physical banner/click correctness from a fixture-only pass.
6. **Linux**: Ubuntu Go/race, real daemon focus fixture paths, Linux notification D-Bus and native-supported lanes; `go test ./internal/daemon` must execute, not merely compile.
7. **CLI E2E**: public hook routing, basic delivery with stubs, config/usage/exit-code behavior, and Windows lazy update command compatibility. Existing installer E2E is retained as a regression gate but not claimed to be reimplemented.
8. **CI truthfulness**: collect each current-head status and relevant job logs. A cancelled, skipped, red, stale-head or runner-unqualified test is not success. Rerun only legitimate infrastructure failures; do not bypass native qualification policies.
9. **Review**: at least two completed review rounds of the implemented diff, including one independent external bot/human review if available. Classify each finding as actionable, inherited with link, or false positive with evidence. Fix actionable findings, then rerun affected tests and review the final integrated head.
10. **Merge gate**: draft until all applicable exact-head gates pass, there is no unresolved blocking review, and no release frozen-source conflict. On merge, verify new `main` head and postmerge workflows; do not publish a release automatically.

## Risks and rollback

| Risk | Severity | Control |
| --- | --- | --- |
| Linux build tag omitted from moved focus helper | High | Explicit tags + Linux test + non-Linux build |
| Lost test-overridable global / incorrect import grouping | Medium | Symbol/token inventory and compilation |
| Changed uncertain native effect retry/fallback | Critical | Identical body movement; native current-head E2E |
| CLI exit/usage/flag divergence | High | CLI fixtures and output equality |
| CI false-green due to skipped native filter | Critical | Job/log inspection, test name audit and exact SHA |
| Concurrent security/installer PR overlap | High | No edits to kernel/setup or active PR files; recheck before merge |
| Noise-only source move without benefit | Medium | Review each split against single responsibility; revert weak splits |

Rollback is a normal revert per slice. Because no new persistent format, plugin identity or native protocol is introduced, rollback needs no data migration. This plan authorizes **no release**.

## Pre-implementation criticism and disposition (scope review, not external model execution)

**Pass A — architectural critic.** An initial idea to introduce a new `delivery` abstraction and reuse `get-modular` crosses package and language boundaries without a second actual caller. **Rejected.** Existing `notifier`/`daemon`/`summary` packages remain the boundaries; only declaration locality changes.

**Pass B — platform/security critic.** Treating macOS terminal-notifier and Windows toast errors as replaceable legacy risks duplicate effects and changed focus identity. **Rejected.** Preserve fallback/error/permission semantics byte-for-byte; do not touch registered native callback, transaction or install code.

**Pass C — test/complexity critic.** A nominal 20,000-line diff can be achieved by relocating unrelated tests or code, without reducing debt. **Rejected.** Require measurable responsibility separation and preserved declaration sets; make S4 conditional, leave webhook and installer out of scope, do not use extra layers. Do not count a skipped or unrelated native CI job as E2E.

**External reviews:** not yet performed. Request through the draft PR; distinguish automated review suggestions from completed validation.

## Execution log

- 2026-11-06: verified live main, the already-merged #590/#612, open #623/#625 and #616's inherited follow-ups; inspected major remaining source files, Go 1.25.8 requirement and three OS CI workflows.
- Current environment has only Go 1.23.2 and cannot reach github.com directly via shell; all repository writes go through the authorized GitHub connection and executable verification must come from GitHub Actions. This is **not** a completed local test.
