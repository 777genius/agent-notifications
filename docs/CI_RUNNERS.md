# CI runner routing

Ordinary Linux and Apple Silicon jobs use the installed Ubicloud and Namespace
integrations. Runner routing is separate from semantic `matrix.os`, platform,
architecture, check names and artifact identities. Windows and Intel Macs retain
their original GitHub runners.

| Original runner | Repository variable | Configured managed runner |
| --- | --- | --- |
| `ubuntu-latest`, `ubuntu-24.04` | `CI_LINUX_RUNNER` | `ubicloud-standard-4` |
| `ubuntu-22.04` | `CI_LINUX_2204_RUNNER` | `ubicloud-standard-4-ubuntu-2204` |
| `ubuntu-24.04-arm` | `CI_LINUX_ARM_RUNNER` | `ubicloud-standard-4-arm` |
| `ubuntu-22.04-arm` | `CI_LINUX_ARM_2204_RUNNER` | `ubicloud-standard-4-arm-ubuntu-2204` |
| `macos-15` | `CI_MACOS_15_RUNNER` | `namespace-profile-macos-15` |
| `macos-26` | `CI_MACOS_26_RUNNER` | `namespace-profile-macos-26` |

Ubicloud labels follow its [official runner types](https://www.ubicloud.com/docs/github-actions-integration/runner-types).
The routing expression retains the original runner when a variable is empty or
removed. A pull request whose head repository differs from `github.repository`,
including a missing/unknown head repository, always uses the original runner.
Same-repository PRs, pushes and dispatches use the configured runner.

## Coverage and evidence

Routing covers ordinary Ubuntu/macOS CI, landing checks/deployment, Claude SDK
blackbox E2E, selector bootstrap, install recovery (including root/APFS tests),
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
No extra PowerShell installation or test-step changes are needed for SDK jobs.
Provider/OS evidence is distinct from application E2E or signing evidence; the
first routed application runs must still pass their existing checks.

## Deliberate runner exceptions

These contracts require new qualification before changing provider:

- `ci-ubuntu.yml`: `cursor-mkdir-diagnostic` and `cursor-installed-e` retain
  `ubuntu-24.04`. Their frozen carrier, pinned checkout and qualified physical
  machine/runner evidence are outside ordinary CI routing.
- `ci-ubuntu.yml`: ordinary `test` matrix Go 1.26 also retains stock
  `ubuntu-24.04`, because `Require qualified Cursor installed physical contracts`
  runs only on that cell. The managed run at source `f31f2fa` rejected its
  positive materializer with `accepted Ubuntu24.04/kernel tuple required`.
  Matching Ubuntu release/architecture in the provider canary does not qualify
  the frozen kernel/physical tuple. Go 1.25 continues using managed routing;
  qualification guards and test commands remain unchanged.
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

## Concurrency and rollback

Active PR workflows group by workflow, event and PR number, cancelling only
superseded runs of the same PR. Other events use the unique run ID, so dispatches,
pushes and tags remain independent. Core `main` pushes are deliberately retained:
they may provide exact-source release evidence, and removing queued PR work does
not require invalidating it. Disabled ReviewRouter keeps its existing concurrency.

Remove the applicable repository variable to restore the original GitHub runner
without editing semantic matrices. Workflow changes affect future runs only;
existing frozen tags and the already-created `v1.48.1` release run 37519336110 are
not restarted, cancelled or republished by this migration.
