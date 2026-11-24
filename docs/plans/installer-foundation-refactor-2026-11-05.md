# Installer foundation refactor — 2026-11-05

Status: **Draft / in progress**. Baseline: `54387fd0923e0d8ccd76bb527c141176e11655e1` (`main`). This plan records the narrow, reviewable foundation work after merged #590; it does **not** supersede #590 or its closed-out validation.

## Diagnosis

The current installer already has a durable transaction kernel with strong ownership, CAS, recovery and cross-platform tests. Its next maintenance hazard is not the absence of an abstraction framework: it is **policy and lifecycle authority co-located with unrelated mechanics**. The largest remaining source files keep orphan selection, physical observation, recovery, and policy-specific admission in long mixed-responsibility units. New agent adapters risk accidentally broadening another adapter's removal authority or changing failure ordering.

Evidence on the exact baseline:

- `internal/installruntime/orphan_recovery.go`: 949 lines. Owns selection, path/asset observation, mutation, native-record validation and reversal in one compilation unit.
- `internal/installruntime/policy.go`: 584 lines. Mixes snapshots, JSON mutations and agent-specific revocation predicates (OpenCode, Gemini, Copilot, Cursor, Local).
- `internal/installruntime/native.go`: 724 lines. Mixes staging, external evidence verification, callback artifact lifecycle and immutable generation identities.
- `cmd/claude-notifications/setup_wizard.go`: 1,625 lines. Generic argument parsing and product-specific Cursor authorization share one file. No new engine is warranted.
- `internal/config/config.go`: 793 lines. Configuration schema, defaults, loading, validation and reads share a file.

The pending Windows callback PR #423 also changes `internal/installruntime/transaction.go`, `recovery.go`, locks and writer protocol. **Do not rewrite those files in this pass**. The separate materializer membership race #608 and multistage result loss #606 require observed behavior and narrow regressions, not blind changes under a move-only refactor.

## Architectural decision

Preserve the current package/API shape and all existing authority seams. Improve *responsibility locality* first; then make only evidence-backed, bounded correctness changes.

1. **Orphan lifecycle:** separate request/projection, filesystem observations, transactional mutation, replay validation and reversal into cohesive same-package source files. All durable structs, error codes and exported APIs remain unchanged.
2. **Policy:** separate authoritative snapshot/JSON handling from product-specific revocation classification. Keep strict before/after checking and same raw byte preimage. No new policy registry or generic adapters.
3. **Native artifact lifecycle:** separate staging and verification from durable generation identity and alias/discard operations. Preserve exact schemas, attestation hash, paths, path anchors and OS-specific implementations.
4. **CLI/config seam:** separate flag/TTY parsing from Cursor-specific authorization and configuration parsing from immutable defaults and validation. These are packaging moves unless an independently reproduced bug requires more.
5. **Focused correctness:** add only narrow tests around existing interfaces and explicit known failure cases; do not change transactional order merely to reduce line counts.

### Non-negotiable invariants

- Exact on-disk ledger, policy, transaction envelope, native generation, command output and JSON schemas.
- Ownership/CAS and fresh physical observation **before** any mutation; no auto-adoption, replay or retry of ambiguous effects.
- Fail-closed behavior for corrupt, unknown or foreign state; retain all clients' recorded identities, lease/lock ordering, and no-follow protections.
- Windows COM/installed generation, Darwin signed native bundle and Linux session semantics remain platform-owned.
- No new dependencies, feature flags, DI container, event bus, generic provider registry, release tags or deployment/publishing.
- No edits to #423's concurrent kernel files until merged and re-reviewed against a fresh baseline.

## Sequencing & acceptance

1. Move the small, cohesive pieces at compilation-unit granularity with **zero intended runtime behavior change**. Each resulting file has one responsibility, and imports are explicit. Use Go formatting and Go test/build feedback to correct compile errors.
2. Preserve and extend the existing public lifecycle regressions for orphan preview/commit, native evidence, policy revocation and setup paths only where the move reveals a gap.
3. Review the diff in at least three distinct passes: (a) omission/behavior-order preservation, (b) security/admission/recovery invariants, (c) complexity/SOLID/DRY/no unnecessary abstraction. Invite available bot/human review on the Draft PR and address concrete comments; don't mark bot review as completed without evidence.
4. Run focused Go tests (including race tests where supported), then CI on Linux/macOS/Windows and the native lifecycle lanes required by changed files. A source-only cross-compile is not evidence of COM, graphical delivery or signed-bundle E2E.
5. Keep the PR **Draft** until exact-head checks and comments are resolved. No merge or release from this refactor.

### Critic checklist / stop conditions

- If any moved definition changes an exported API, durable bytes, platform build-tag participation, error type or mutation ordering, stop and revert the move.
- If a split introduces a new abstraction with no second real caller, discard it.
- Do not pursue an arbitrary 10–15k-line diff target by moving unrelated code. Estimate changed-line totals honestly; favor a smaller high-confidence refactor over a large, unqualified rewrite.
- Treat #423 conflicts, #606, #608 and native sign-off as independent workstreams, not silently absorbed into this PR.

## Evidence log

- 2026-11-05: audited exact `main@54387fd0923e0d8ccd76bb527c141176e11655e1`, open PR #423 overlap, merged #590 scope, AGENTS.md packaging rule and current Go source structure.
- Code, validation and reviewer evidence: pending; update alongside actual commits/CI, not predictions.
