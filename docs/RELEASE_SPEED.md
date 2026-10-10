# Basic release preparation and recovery

Use this alongside [RELEASE.md](RELEASE.md). The basic profile still requires
current-source CI, five native artifact proofs, same-source macOS signing and
separate downloaded-draft macOS/Codex checks. This procedure does not authorize
an eleven-cell, physical Intel, live-provider, visible-desktop or cold-start claim.

## Prepare once, run independent work together

Before freezing candidate C, prepare the entire version/changelog change and the
promotion diff P: channel rows, source snapshot S, the platform channel guide,
README links or explicit versions, manual translations and browser expectations.
P must reference the actual C/S, never its own head as C.
The operator O is the workflow revision executed by Actions, not necessarily C.

Run the cheap candidate/promotion preflight before long CI. Once C is frozen,
start its CI and signing in parallel. Prepare P and start its current-head CI as
soon as its metadata is final; do not wait for draft downloads or local E2E.
Keep P unmerged and channels inactive until assets are publicly verified.
Changes to C invalidate affected source/artifact evidence. Changes to P require
P's current-head CI, but do not rebuild unchanged C artifacts.

Use one watcher for each live run, recording its process/session handle:

```sh
gh run watch RUN_ID --repo 777genius/agent-notifications --exit-status --interval 30
node scripts/release-progress.mts watch-pr 777genius/agent-notifications PR HEAD 'EXPECTED CHECK' 'OTHER EXPECTED CHECK'
```

The PR helper checks the head before/after one gh watcher and verifies every
explicitly expected check is successful. Supply the complete repository check
set; success of one workflow alone is insufficient. A missing/failed/skipped
expected check fails closed. Observation timeout does not permit restarting CI.

## Keep evidence and timings

```sh
node scripts/release-progress.mts start /private/tmp/TEST-release-progress.json C O vX.Y.Z signing
# After inspecting the real result; evidence path must be a nonempty regular file:
node scripts/release-progress.mts pass /private/tmp/TEST-release-progress.json C O vX.Y.Z signing /private/tmp/TEST-signing-run.json
```

Record phases `source-ci`, `signing`, `seal`, `draft-upload`, `macos-canary`,
`codex-smoke`, `promotion-ci`, `publication`, `pages` and `public-verification`.
A failure uses `fail`, with the retained failure receipt. The journal binds exact
C/O/version, timestamps and SHA256 of receipts; it does not interpret receipts
or grant qualification. No implicit restart overwrites an existing phase. A new
explicit recovery observation uses a separate phase name and preserves failures.

Before accepting an existing receipt, verify its digest again and independently
join source, operator, version, platform/arch, toolchain, harness, asset hashes,
signing run/attempt and scope against current inputs. A matching filename,
version label, similar tree or previous green run is insufficient. Use
`scripts/codex-release-gate.sh --diff BASE HEAD` to select conservative source
risk: unknown/runtime/installer/build/hook/native/harness paths require the gate.
Its documentation-only exemption does not itself prove artifact identity.

A documentation-only P with unchanged C/assets can use the same artifact proofs;
P still needs its own loader/parser/docs CI. A new C, including a version-only
change, needs new bytes and baseline artifact checks. Never copy old success into
a new source report. Preserve real Rosetta/GUI failures and actual closure.

## Recover effects without rebuilding

Build each candidate artifact once. Seal the complete 29-file set and its
provenance before creating a tag. Create the unpublished draft independently of
uploads. An explicit resume selects the original run/attempt and immutable seal;
it may upload missing assets only after reconciling tag, draft identity and all
existing remote names, sizes, hashes and upload states. Unexpected or mismatched
assets require investigation, never `--clobber`. An uncertain upload is followed
by a remote read before a bounded retry. Never retry an uncertain agent turn.

Publish only after the complete fan-in of C/P CI, independent review, signing,
verified draft assets, canaries and scoped local E2E. Public assets come first,
source branches before channel activation, main promotion after qualification.
Pages deploys from the allowed main context; do not weaken environment policy
for a release-tag workflow. Verify Latest, all public asset checksums, five
channels, public loader version/controller SHA and retained legacy routes.

## Measure before further optimization

Initial target is at most 60 minutes from frozen reviewed C to a publicly verified
installer for an ordinary basic release, not a guarantee. For v1.48.5, frozen C
to successful Pages deployment took 1:41:53; the separate public verification
receipt has no completion timestamp. Candidate CI took 28:55, final promotion
CI 28:09, signing 9:29, basic
artifacts 4:25; overlapping intervals must not be added. A failed draft step and
late metadata fixes introduced recovery and another head's CI.

After three releases, compare median, maximum, first-pass success and specific
failure/queue causes. Do not claim p95 from that sample. Optimize artifact-test
fan-out or dependency caches only after measuring the remaining critical path;
never cache qualification of new bytes under a filename/version key. The 30-45
minute stretch target depends on real runner queues and source CI duration.

## Local commands

Node 24+ runs the typed operator files directly. Go is also needed for the pinned
workflow expression validator (`actionlint`); YAML parsing alone does not check
which GitHub expression contexts are available at each workflow key. Install the reviewed locked
compiler/types once, then run all side-effect-free tooling contracts:

```sh
npm --prefix scripts/codex-offline-release-smoke ci
make release-tools-check
make release-preflight
make release-preflight RELEASE_PREFLIGHT_ARGS='--mode candidate --version X.Y.Z --source-sha C'
make release-preflight RELEASE_PREFLIGHT_ARGS='--mode prepared-promotion --version X.Y.Z --source-sha C'
make codex-release-smoke SMOKE_PHASE=plan SMOKE_INPUTS=/private/tmp/TEST-inputs.json
```

Use the [offline smoke operator instructions](../scripts/codex-offline-release-smoke/README.md)
for preparation, real TTY trust, exactly one turn and owned-server closure. The
Make target runs one explicit phase, never silently applies an installation or
submits a model turn when asking for a plan. CI runs type/fixture contracts only;
actual signed macOS and installed Codex E2E remains a separately scoped TEST run.

## Executable fast path

The implementation/acceptance plan is
[release-fast-path-2026-10-10.md](plans/release-fast-path-2026-10-10.md).

Before freezing C, use the existing `make preview-installer` in a real terminal
for an isolated installation preview. It uses clean committed source, retains
TEST native profiles and does not execute agent/model turns. Do not introduce a
second preview runner or infer signed/draft delivery qualification from preview.
After preparing C and P, run their cheap preflights together:

```sh
make release-prepare RELEASE_VERSION=X.Y.Z CANDIDATE_SHA=C \
  CANDIDATE_ROOT=/absolute/candidate PROMOTION_ROOT=/absolute/promotion
```

Start C CI and signing independently; prepare P and start its CI immediately.
Use one `make release-wait RELEASE_WAIT_INPUTS=/private/tmp/TEST-wait.json`
process instead of serially watching the three lanes. The input shape is:

```json
{
  "repository": "777genius/agent-notifications",
  "candidateSHA": "C_EXACT_40_CHARACTER_SHA",
  "operatorSHA": "O_EXACT_40_CHARACTER_SHA",
  "version": "vX.Y.Z",
  "candidate": {"pr": "C_PR_NUMBER", "checks": ["ALL_EXPECTED_CURRENT_C_CHECK_NAMES"]},
  "promotion": {"pr": "P_PR_NUMBER", "headSHA": "P_EXACT_40_CHARACTER_SHA", "checks": ["ALL_EXPECTED_CURRENT_P_CHECK_NAMES"]},
  "signing": {"runID": "SIGNING_RUN_ID", "attempt": "SIGNING_ATTEMPT"},
  "receipt": "/private/tmp/TEST-source-signing-promotion-fan-in.json"
}
```

Replace every placeholder and supply the complete expected check sets from the
current workflows, including mandatory aggregate gates. The command validates
exact PR heads and signing identity before/after observing, starts one observer
per lane concurrently, waits for all observers even if one fails, and writes an
exclusive timestamped receipt. It cannot submit an agent turn, rebuild, retry,
merge or publish. A failed receipt is retained; a recovery observation needs a
new receipt path and only the missing phase should be rerun.

An explicitly enumerated release-tools-only PR may use scoped tooling contracts
instead of expensive native runtime CI. Unknown paths, runtime/installer/hook/
native changes, source version changes, Makefile, workflows and classifier
changes keep full CI. `ci:full` always forces full. Scoped checks have distinct
names; skipped native and recovery jobs are not native success and cannot qualify a new C.
Promotion P still has its current-head checks; this optimization does not grant
an arbitrary P permission to reuse C's native qualification.

On a draft/upload failure, use the existing `resume_run`/`resume_attempt` inputs.
Authenticated paginated discovery finds drafts that the published-tag endpoint
cannot return, revalidates their release IDs, and rejects duplicate/conflicting
state. A complete resume checks inventory without traversing 29 already present
uploads; any actual missing upload retains before/after remote conflict checks.
Builds/signing are never repeated merely because publication transport failed.

After the separately qualified assets are public, source refs/main are promoted
and the exact-main Pages run succeeds, perform one final read-only fan-in:

```sh
make release-verify SEALED_DIR=/absolute/original-seal MAIN_SHA=EXACT_MAIN_SHA \
  PUBLIC_LOADER_URL=https://agent-notifications.com/install.sh PAGES_RUN_ID=RUN_ID \
  > /private/tmp/TEST-public-release-verification.json
```

Require exit zero and the success JSON. It verifies the original seal, all public
asset identities/hashes, Latest, source references, all five channel selections,
exact main/controller and Pages jobs, public loader and retained legacy script.
This receipt does not apply an installation or prove native permission, agent
activation, business delivery, banners or callbacks. Keep those separate actual
TEST observations and the independent technical review. A failed verification
requires repairing only the mismatched delivery phase, never rebuilding the
immutable released tag.
