# CI runner routing

Ordinary Linux jobs use GitHub-hosted runners. Apple Silicon jobs retain the
installed Namespace integration. Runner routing is separate from semantic
`matrix.os`, platform,
architecture, check names and artifact identities. Windows and Intel Macs retain
their original GitHub runners.

| Original runner | Repository variable | Configured runner |
| --- | --- | --- |
| `ubuntu-latest`, `ubuntu-24.04` | `CI_LINUX_RUNNER` | `ubuntu-24.04` |
| `ubuntu-22.04` | `CI_LINUX_2204_RUNNER` | `ubuntu-22.04` |
| `ubuntu-24.04-arm` | `CI_LINUX_ARM_RUNNER` | `ubuntu-24.04-arm` |
| `ubuntu-22.04-arm` | `CI_LINUX_ARM_2204_RUNNER` | `ubuntu-22.04-arm` |
| `macos-15` | `CI_MACOS_15_RUNNER` | `namespace-profile-macos-15` |
| `macos-26` | `CI_MACOS_26_RUNNER` | `namespace-profile-macos-26` |

The Linux variables select GitHub-hosted labels; preserve the explicit Ubuntu
22.04 and ARM mappings when updating them. The generic Linux override is pinned
to Ubuntu 24.04 to preserve the previous Ubuntu baseline as `ubuntu-latest` moves.
The routing expression retains the original runner when a variable is empty or
removed. A pull request whose head repository differs from `github.repository`,
including a missing/unknown head repository, always uses the original runner.
Same-repository PRs, pushes and dispatches use the configured runner.

## Coverage and historical migration evidence

Routing covers ordinary Ubuntu/macOS CI, landing verification, selector
bootstrap, install recovery (including root/APFS tests),
qualification candidate builds, signing smoke, and future release version/build,
notifier, publishing and artifact E2E jobs. Existing commands, permissions,
certificate/keychain handling, matrix coverage and release custody are retained.
The profiles must continue supporting sudo/APFS and Apple security/keychain tools;
this change grants no permissions or entitlements.

The isolated runner canary in
[ci-runner-sandbox-20261005 run 37521210525](https://github.com/777genius/ci-runner-sandbox-20261005/actions/runs/37521210525)
passed all four Ubuntu ARM 22.04/24.04 and Namespace macOS 15/26 cells. Ubuntu
reported the expected OS/architecture and PowerShell 7.6.5; macOS 15/26 provided
PowerShell 7.6.0/7.6.6. x64 Ubicloud and Namespace had earlier pilot evidence.
These historical canary results apply to the tested images; the current Claude
SDK runner exception is listed below.
Provider/OS evidence is distinct from application E2E or signing evidence; the
first routed application runs must still pass their existing checks.

## Deliberate runner exceptions

These contracts require new qualification before changing provider:

- `claude-sdk-e2e.yml`: `sdk-blackbox` uses its explicit `matrix.runner` labels:
  `ubuntu-24.04`, `macos-15` and `windows-2025`. `CI_LINUX_RUNNER` and
  `CI_MACOS_15_RUNNER` do not apply to this job. It requires `pwsh`; the routed
  Linux runner failed before the SDK checks because PowerShell was unavailable
  ([run 45067168388](https://github.com/777genius/agent-notifications/actions/runs/45067168388/job/137345805680)).
- `landing.yml`: `deploy` uses stock `ubuntu-24.04` because
  `actions/jekyll-build-pages` pulls a Docker image during job preparation.
  `CI_LINUX_RUNNER` does not apply to this job; `verify` keeps ordinary routing.
  The routed Linux runner failed before checkout because Docker was unavailable
  ([run 45079148244](https://github.com/777genius/agent-notifications/actions/runs/45079148244/job/137389364516)).
- `ci-ubuntu.yml`: `cursor-mkdir-diagnostic` and `cursor-installed-e` retain
  `ubuntu-24.04`. Their frozen carrier, pinned checkout and qualified physical
  machine/runner evidence are outside ordinary CI routing.
- `ci-ubuntu.yml`: `cursor-physical` retains stock `ubuntu-24.04` with Go 1.26
  for `Require qualified Cursor installed physical contracts`. The managed run
  at source `f31f2fa` rejected its positive materializer with
  `accepted Ubuntu24.04/kernel tuple required`; the attempted whole-installer
  routing at `4142a20` independently confirmed that refusal. Matching Ubuntu
  release/architecture does not qualify the frozen kernel/physical tuple.
  Both ordinary Go test versions and installer E2E use repository-variable
  routing; the separate physical job preserves all original commands/positive markers and
  is required by both Ubuntu aggregates and native-input evidence.
- `pi-installed-linux.yml`: the installed Pi process now exercises a real
  shared Cursor binding through install, update and Pi-only removal. It uses
  stock `ubuntu-24.04`, matching `cursor-physical` above. Managed Ubuntu runners
  do not qualify Cursor's exact kernel/ext4/profile contract; the runtime guards
  remain unchanged.
- `gemini-native-e2e.yml`: all original runners remain. Its trusted wrapper
  (`scripts/gemini-native-ci.py`) explicitly requires
  `RUNNER_ENVIRONMENT == 'github-hosted'` before side effects. Having PowerShell
  on a managed image does not prove this runner identity; do not spoof that value
  or weaken the guard to migrate it.
- `opencode-clock-native-qualification.yml` and
  `opencode-platform-clock-prequalification.yml`: retain stock image matrices
  used to qualify native clock/loader identity.
- `opencode-native-e2e.yml` and release job `qualify-opencode`: retain their
  original runner matrices and immutable external qualified evidence contract.
- Both `disabled-reviewrouter-*` workflows are unchanged, including runner and
  concurrency configuration.
- Windows workflows and all Intel macOS cells retain their original labels.

## Cursor source qualification kernels

The stock Ubuntu 24.04.5 pool has supplied `6.17.0-1022-azure`,
`7.0.0-1017-azure` and `7.0.0-1014-aws`. Source qualification is specific to each
exact kernel and retains the Linux amd64, Ubuntu release, ext4 ancestry and
original profile token requirements. Unknown kernels continue to refuse.

The historical P1 receipt remains evidence for the original kernel only. The
kernel 7 qualification has its own `source-ci-pr497` identity. Before merging
[PR 497](https://github.com/777genius/agent-notifications/pull/497), its Go 1.26
Ubuntu job must pass the real directory identity/swap, Cursor, materializer,
caller and recovery contracts on that kernel. `NOT_RUN` remains a CI failure;
this source qualification grants no installed native E or consent.

### AWS kernel source qualification

The `7.0.0-1014-aws` entry has its own
`source-ci-aws-20261123` identity. It cannot inherit either Azure receipt.
The kernel policy and identity mapping admit only this exact new string;
neighboring versions, another provider suffix and extra characters remain
refused. An unknown kernel also receives no source evidence identity.

Acceptance requires a complete `cursor-physical` result on an actual
Ubuntu 24.04.5/Linux amd64 host with this AWS kernel: directory identity/swap,
original-token revalidation, selected projection/acknowledgement/data,
positive materializer, caller handoff and matching recovery, and package cleanup.
The existing job commands, positive markers, `NOT_RUN` failures, ext4 ancestry
checks and installed-native/consent boundaries are unchanged. A passing Azure
run cannot qualify the AWS entry.

The source-bound AWS results and review are tracked in
[PR 682](https://github.com/777genius/agent-notifications/pull/682). Its acceptance
checklist requires the complete measurement before merge; an entry in this
source policy alone is not evidence that the measurement passed.

Both attempts of the unchanged source `06734f7` reached the new AWS tuple and
refused the positive materializer at its kernel guard
([first attempt](https://github.com/777genius/agent-notifications/actions/runs/45082159894/job/137436146752),
[bounded retry](https://github.com/777genius/agent-notifications/actions/runs/45082159894/job/137472781097)).
Those partial results do not qualify the missing materializer/caller contracts
or establish ext4 for every ancestor. The candidate must record its own complete
AWS source-bound evidence before merge, as the earlier kernel qualification did
in [PR 497](https://github.com/777genius/agent-notifications/pull/497).

## Concurrency and rollback

Ordinary PR workflows group by workflow, event and PR number, cancelling only
superseded checks of the same PR. Metadata-only edits and manual diagnostics retain
independent groups. Core `develop` push checks may replace earlier checks of that
same branch; they never share a group with PR checks.

Core `main` pushes and `release-tools.yml` main pushes remain independent because
[release qualification](../.github/workflows/basic-release.yml) rejects cancelled
latest checks for the exact candidate SHA,
including checks outside its named native receipt requirements. The installed
Cursor Windows feature-branch qualification also retains every push and manual
run: its native result, evidence and owned cleanup must finish independently.
Release/tag workflows and Pages deployments retain their existing independent
operations. Disabled ReviewRouter keeps its existing concurrency.

Meaningful PR base edits use a run-local merge of the exact event base and head,
rather than GitHub's potentially stale synthetic merge commit. Both edited-event
workflows force full native CI for that pair; title/body-only no-ops retain their
existing isolation. The checkout helper is pinned independently of the checked-out
source, so a stale checkout cannot select its implementation.

A run-local base-edit merge is not release qualification. Those runs cannot use
release-equivalent mode or create immutable native-input receipts. The unchanged
release verifier still requires remotely fetchable exact-SHA evidence from an
ordinary qualified run; skipped evidence is never accepted. Normal PR, push and
manual checkout identities and release pins retain their existing behavior.

Remove the applicable repository variable to restore the original GitHub runner
without editing semantic matrices. Do not restore retired Ubicloud labels.
Workflow changes affect future runs only;
existing frozen tags and the already-created `v1.48.1` release run 37519336110 are
not restarted, cancelled or republished by this migration.

## Intermediate prose/Windows revisions and final CI

Core macOS and install recovery classify every revision on Linux. Only modified
`README.md`, `docs/DO_NOT_DISTURB.md`, and `docs/NOTIFICATION_TYPES.md` are eligible
for a cheap intermediate PR run. Mixed changes, additions, deletions, renames,
unknown paths, qualification/evidence documents, or unavailable Git metadata
retain full native coverage. A draft PR can also use `windows-only` mode when its
entire merge-base diff contains only these Windows App SDK contract paths:
`.github/workflows/navigation-windows-appsdk-build.yml` and
`tests/integration/windows_appsdk_build/{README.md,SDKContract.cpp,SDKContract.vcxproj,packages.lock.json}`.
Regular-file additions, modifications, deletions and renames within that exact
set are eligible; both rename paths must qualify. Symlinks, gitlinks, mixed or
unknown paths and malformed/unavailable Git or PR metadata retain full coverage.
This does not treat arbitrary C++ or Visual Studio projects as Windows-only.
Linux recovery continues in both intermediate modes; all four Mac jobs are
omitted. Ready Windows PRs, pushes and manual runs use full mode; becoming ready
for review also requests full CI. Existing prose eligibility is unchanged.

**A prose- or Windows-only run deliberately fails `Full macOS CI required` and `Full install
recovery required`.** It saves intermediate Mac capacity but is not merge evidence.
Add the `ci:full` PR label before final review/merge and wait for both full gates,
the Go 1.25/macOS 15 and Go 1.26/macOS 26 jobs, Swift tests, and both recovery cells
on the current PR head. Keep the label while editing to validate subsequent heads.
Removing it recomputes eligibility; a previous SHA's success cannot qualify a new
head. Changing the PR base recomputes the comparison. Title/body-only edits create
no canonical checks or native jobs and use independent concurrency, so they cannot
cancel an active full run or replace its gates with skipped results. These
always-present gates also reject failed, skipped or cancelled native jobs. Repository merge policy must require the full gates; without protection,
the merge operator must verify them and the exact current SHA explicitly.

The loader and offline/mock installer suites share a native `parallel` step after
the binary build. Each uses private fixture state, and only the installer binds
its mock HTTP port. The group completion barrier propagates failures before real
network diagnostics. Diagnostics use `--real-network-only` to avoid repeating the
mandatory offline suite; `--real-network` continues to run the combined suite.
Actual provider overlap/barrier and failure propagation were verified in disposable
TEST workflows [37583868400](https://github.com/777genius/ci-runner-sandbox-20261005/actions/runs/37583868400)
and [37584064041](https://github.com/777genius/ci-runner-sandbox-20261005/actions/runs/37584064041).

The Go 1.25 cell uses one race-enabled `go test ./...` invocation and omits only
six audited portable tests by exact, anchored name: `TestWizardInstallOrUpdateBootstrapE2E`,
`TestSetupWizardRepairMixedRevisionsKeepsSiblingE2E`,
`TestSetupWizardTTYMixedRepairPlanShowsPerBindingDigestsE2E`,
`TestGeminiEventTransportTotalBudget`, `TestWizardTTYAddSecondClientKeepProposesNewDefaults`,
and `TestWizardTTYKeepBoundMixedCancels`. Every package and test file still compiles;
unknown/new tests remain enabled. Go 1.26 retains the full suite, including all six.
The selection follows the native Go 1.25 baseline audit whose Go sources match
main `54c24f71e9b4d3af47a3cd2f0da0574b442089ac`; the six test durations totalled
33.80 seconds (the command took 29.08 seconds). This is measured overlap, not a
guaranteed wall-time reduction. Platform-specific/native tests, installer units,
Swift and recovery coverage remain, as do separate opt-in qualification recipes.
