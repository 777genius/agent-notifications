# Release acceleration implementation plan

Date: 2026-10-10. Goal: remove avoidable release recovery and sequential operator
work, with end-to-end evidence for the next basic release. This is tooling work,
not a new consumer release or permission to publish unqualified bytes.

## Observed problem

v1.48.6 took about 2h11m. Native source CI cells took roughly 25 minutes; source
and promotion revisions, failures and manual recovery lengthened the critical
path. The sealed build succeeded, but the published-tag API returned 404 for its
valid draft. An explicit resume repeated the same lookup failure. Publication
then required manual reconciliation of the original 29-file seal and manual
public delivery verification. Do not sum overlapping job durations or claim a
measured per-cause breakdown that was not recorded.

## Implementation order and acceptance

1. Fix authenticated draft discovery in the existing basic operator. Enumerate
   complete bounded release pages, find one exact tag, re-read its immutable ID,
   and validate draft/target/state. Reject ambiguity and uncertain reads before
   writes. A complete resume only scans assets once at entry and once at final
   verification; actual missing uploads keep their before/after conflict checks.
   E2E: published-only tag endpoint returns 404 for an existing draft; create is
   not repeated. Lost accepted upload resumes to exactly 29 immutable assets.
2. Expose early preparation through `make release-prepare`, reusing candidate
   and prepared-promotion preflight. Keep `make preview-installer` as the single
   interactive local installer preview, before freezing C. Preview is not
   signed candidate/draft/native qualification and does not submit agent turns.
3. Add a conservative release-tools CI mode with an exact file allowlist.
   Changed runtime/build/installer/hooks/native/version manifests, unknown paths,
   invalid events, unsafe tree modes, deletions and renames retain full CI.
   `ci:full` forces full. Never label skipped native jobs as native success.
   Changed Makefile/workflow/classifier logic also retains full CI. Validate the
   classifier with actual temporary Git commits and hostile path/mode fixtures.
4. Add `make release-wait` to start C CI, P CI and signing observers together,
   then join all three once. Reuse the existing fenced PR watcher. Require exact
   PR heads, explicit complete expected check sets, owner signing identity,
   source/run/attempt and success. Persist an exclusive timestamped receipt for
   pass or failure; no automatic rerun, publication or model turn. E2E proves
   concurrent watcher starts and that failure still joins every live observer.
5. Keep sealed29 explicit resume, build once and immutable tag/assets semantics.
   Do not create a second recovery framework or reconstruct a seal. Preserve
   the original failed result and reuse only the exact original bytes/provenance.
6. Add `make release-verify` for one final read-only public verification: original
   seal, public29 and Latest, release/source tags, branches, all five channels,
   exact main/controller, successful same-main Pages verify/deploy and exact
   public loader bytes. Emit one JSON receipt only after all checks. E2E uses a
   local HTTP server and strict read-only gh fixture; then run the same command
   against the actual published v1.48.6. Installer application and visible
   delivery remain separate evidence, never inferred from HTTP/API metadata.

## Scope boundaries

A new candidate C, even a version-only change, still requires full current-source
CI, same-source signing, all five new artifact checks and the existing separate
macOS/Codex checks. Prepare P and start its CI while those independent lanes run.
The new tools-only lane does not exempt arbitrary promotion P or installer code.
A future cheap P lane needs exact product-tree equality P-to-C plus authenticated
full C evidence and current P loader/browser/preflight checks. This plan does not
introduce that additional evidence protocol or count old CI as testing new bytes.

Release order remains qualified public assets, immutable source refs, then main
channels/version promotion and Pages. No reduced clocks, reader/session/disposal
budgets, signature checks, permission requirements or semantic oracles.

## Verification and delivery

Run strict TypeScript checks, workflow expression validation, classifier Git E2E,
existing release tools contracts, resume subprocess E2E, concurrent watcher E2E
and local-HTTP public verifier E2E. The live published-release verifier is read-only
and cannot create tags/releases, upload assets, install apps or run providers.
Independent review must cover the final diff and verify this list against actual
results. Merge only after exact-head mandatory CI succeeds.

Target: remove recovery/manual-operator time; 30-45 minutes for an ordinary basic
release is a stretch target dependent on queue times and source CI, not a measured
promise. Record existing phase durations and compare the next three real releases.

## Local evidence

- Existing candidate and prepared-promotion preflights passed through the new
  `make release-prepare` entry point against frozen v1.48.6 C and its channel
  promotion.
- Draft regression failed before the fix; final nine subprocess recovery tests
  passed, including 29 unique asset uploads and no duplicate draft creation.
- Concurrent C/P/signing CLI fixtures passed, including failed/missing/skipped/
  changed-head observations, joining remaining watchers and exclusive receipts.
- Public verifier subprocess/local-HTTP E2E passed 18 cases. The same command
  passed against actual published v1.48.6: 29 assets, Latest, five channels,
  source refs, exact-main Pages/controller/loader and retained legacy setup.sh.
- Six disposable-Git release-scope E2Es and 14 existing macOS scope tests passed.
  P-from-C scoping intentionally remains full; unknown changes fail closed.
- Pinned actionlint validates Ubuntu/Windows and release workflows. Existing
  macOS native `parallel` syntax is newer than its parser; baseline has the same
  two diagnostics. Normalizing only those existing blocks for a separate read-only
  syntax check validated the changed macOS job expressions; real macOS CI remains
  required for this workflow-policy change.

Exact-head CI and final independent review are delivery requirements, recorded
on the PR rather than represented by the local fixture results above.
