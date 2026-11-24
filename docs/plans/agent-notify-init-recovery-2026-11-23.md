# Agent-notify initialization/recovery hardening (2026-11-23)

## Problem
Follow-up to #585 (v1.51.4): Claude's `/init` can report a ready preflight and then fail the enable stage with `initialization_recovery_required`. A failed configure stage prevents portable MCP/skill setup. Old cache entries may be retained if another consumer still needs them.

## Scope / boundaries
- Preserve all existing owner/CAS/transaction/journal guards. Never reset/delete user state or automatically adopt a foreign file.
- Make read-only preflight detect provably inconsistent initialization state, using existing safe owner/namespace/lock checks; report its real reason before any mutation.
- Preserve a recoverable interrupted setup for explicit owner recovery; avoid silently declaring a fresh install.
- Keep `/init` partial-success guidance precise and make wizard inspection distinguish retained opt-out vs damaged owned materialization.
- Add red/green Go regressions (missing marker with enabled policy, stale private state, healthy first install, healthy retry/legacy); check Linux native installer/wizard paths and shared old-cache consumers.
- Document actionable diagnostics without printing private journal or ownership contents.

## Failure modes / critiques
1. A simplistic `os.Stat(state)` check misses symlinks, insecure ownership, mismatched namespace: use the existing verified open/check helpers instead.
2. Checking only `enabled` would incorrectly flag a valid first install: permit exact empty disabled state; require owner proof before claiming recovery.
3. Eager journal initialization in preflight violates read-only semantics: no writes, no initialization, no journal reset.
4. Old cache entries with retained consumers are intentional: do not remove without verified last-consumer transition.
5. Do not turn an absent skill into an implicit consent to reinstall; report exact inspection/next action.

## Verification
- `go test ./internal/agentnotify/setup ./cmd/claude-notifications ./internal/installruntime ./internal/agentnotify/setupwizard`
- Existing installer and managed portable E2E, no skipped failures counted.
- Cross-platform CI on exact pushed commit; physical KDE/Wayland verification requires an authorized runner.

## Estimates
~150–280 LoC production + 200–360 LoC tests, complexity 6/10, reliability target 9/10, confidence 7/10.
