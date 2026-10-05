# Native navigation evidence, 2026-10-05

This directory stores generated evidence, not handwritten production code. It does not establish fast-path qualification or cross-platform completion.

- `native-typed-callback.json`: actual unique test notifier -> native click callback -> full static validation -> installed Codex handoff. User confirmed the specified synthetic chat opened with delay. One sample; no instrumented physical-click or visible-render timestamp.
- `native-none-callback.json`: separate native click with no application target. It verifies callback delivery only.
- `preflight-samples.json` / `preflight-summary.json`: 50 same-process read-only installed static-verifier samples through production callback/executor boundaries, with a spy opener. No application activation.
- `native-probes.json`: compiled source snapshot and binary hashes, two live same-ID fixture copies, selected-only GURL and stale PSN failure after restart. The signed-resource mutation probe also observes fresh static failure with fresh dynamic success while its own child remains alive. No production client activation, Developer ID signing-binding qualification, update/PID-reuse proof or Codex resource qualification.
- `runner-failure-regression.json`: real compiler-failure injection in a disposable copied source tree. Before the fix the runner incorrectly exited 0/passed=true; after it exits 1/passed=false and retains the failed phase output.
- `installed-resource-observation.json`: read-only metadata, fuses, ASAR header and unpacked executable JavaScript manifest of the tested installed distribution. No user-profile contents.

Reproduce the disposable API fixtures on macOS:

```sh
python3 scripts/navigation-native-probes.py --execute-test-fixtures
```

Read-only installed measurement requires explicit path/team opt-ins:

```sh
NOTIFIER_TEST_INSTALLED_PREFLIGHT=1 NOTIFIER_TEST_INSTALLED_APP=/Applications/ChatGPT.app NOTIFIER_TEST_INSTALLED_TEAM=2DC432GLL2 NOTIFIER_TEST_PREFLIGHT_SAMPLES=50 swift test --package-path swift-notifier --filter NativePreflightMeasurementTests
```

Native notification clicks require user interaction and a separately registered unique test notifier. They are not automatically replayed by either command. The user explicitly authorized this Mac and provided the target test-chat URI. No agent/runtime commands were performed in real user projects.
