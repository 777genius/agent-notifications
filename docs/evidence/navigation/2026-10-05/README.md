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

`managed-cli-permission-rejection.json` records a negative isolated full-CLI precondition experiment. The fixed product signing identifier and unique TEST bundle identifier were rejected by macOS notification authorization. No notification was submitted, and this is not full CLI E2E acceptance. The experimental harness is not shipped because this OS identity composition is unsupported.

`native-sender-death-callback.json` correlates a submitted macOS TEST notification, an exact executable/structured-send PID observed running then absent before the click request, and a different callback PID 768.5s after launch. The user confirmed the test chat opened. This proves the observed callback can run after the sender disappears and the old 14s submission budget elapses; it does not collect the helper's exit status or physical-click/render/startup timing.

`native-linux-connection-callback.json` retains both the initial version-format failure and the successful pinned Ubuntu24.04 dunst run. A private D-Bus monitor recorded actual daemon emissions, including a callback addressed to the disconnected sender's unique name. A replacement received no old callback and received its own new callback. Only the connection lifecycle is covered; no sender subprocess death, GUI, Wayland, server restart, Codex or latest dunst qualification is claimed. All owned daemon/display/bus PIDs were observed absent after cleanup.

Linux reproduction: extract Ubuntu24.04 packages `dunst=1.9.2-1build2` and `libxss1=1:1.2.3-1build3` into a new private `navigation-dbus-test-*` root. Do not install a global daemon. Start an authenticated owned Xvfb with TCP disabled and a fresh `dbus-run-session`; start only the extracted daemon with an explicit TEST config and its private library path. Write `.owned-test-root.json` containing `purpose="TEST navigation Linux callback"`, the exact private `busAddress`, exact `display`, and `xvfb=true`. Pass absolute paths belonging to that root:

```sh
AGENT_NOTIFY_NAVIGATION_LINUX_E2E=1 /usr/bin/python3 scripts/navigation-linux-callback-probe.py --root "$TASK_PROBE_ROOT" --dunst "$TASK_PROBE_ROOT/package/usr/bin/dunst" --dunstctl "$TASK_PROBE_ROOT/package/usr/bin/dunstctl"
```

The probe requires system Python dbus/GLib modules, validates daemon identity/version through its real bus owner and `/proc`, emits JSON evidence with explicit qualification limits, and refuses to overwrite an existing report. The caller owns private daemon/Xvfb/bus cleanup. Do not point this fixture at a user session bus or display.

`native-resource-envelope.json` records the separate resource-aware candidate experiment, including the original run and the replay after independent review tightened assertions and outcome labels. All fixtures are newly signed ad-hoc TEST applications. Text-resource and CodeResources changes are mandatory integrity controls; executable-byte mutation is an optional observation, not fast-path qualification. Fresh full validation rejected the in-place signed executable change with -67061 while fresh dynamic validation and resource-aware validation succeeded with the child alive. This is a concrete non-equivalence observation, not a latency improvement or a production change.

```sh
python3 scripts/navigation-resource-envelope-probe.py --execute-test-fixtures
```

The runner retains its captured source, build/command output hashes and evidence in a new private root, including failures. It does not send notifications, open Codex or mutate installed applications. The optional executable observation may be unavailable if the OS refuses modification or kills the fixture. A successful mandatory control run does not imply that this weaker validation can replace the product's strict/all-architecture verifier.

`installed-addressed-transport.json` records one reviewed, explicitly approved public PSN GURL request to the already-running installed Codex. Full Developer ID validation and guest/path/PSN checks succeeded before the sole send; the OS accepted it and the user separately confirmed the intended chat opened or became active. Full verification still took 3.359s. The fixed-route operator harness and captured sources are retained privately rather than publishing the private test route or adding an automatic replay command. This does not prove atomic exec binding, deadline enforcement inside the existing bridge, races, cold launch or latency improvement.

`native-same-pid-exec.json` records one source-bound, disposable ad-hoc A-to-B exec experiment. The PID and PSN remained unchanged. A fresh guest check authenticated B and rejected A, while the cached guest still authenticated A. The sole GURL event addressed to the old PSN was received by B. All owned children were stopped and reaped; no installed client was activated. This is a counterexample to treating PSN as a code-identity-bound endpoint across same-PID exec, not a production qualification or a fix.

```sh
python3 scripts/navigation-exec-binding-probe.py --execute-test-fixtures
```

The fixture retains captured sources, hashes, build output and event records in a new private TEST root. Its `passed` result means the experiment and observation completed, including cleanup; `atomicBindingQualified` remains false. The production verifier and opener are unchanged.
