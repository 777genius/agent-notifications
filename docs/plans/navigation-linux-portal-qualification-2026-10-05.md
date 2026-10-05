# Linux portal callback qualification

Status: owned X11 callback and cold TEST activation passed; production navigation remains unqualified.

This fixture tests the public notification portal using a finite sender and a cold D-Bus-activated TEST application. It does not open Codex or another installed client. The frontend is xdg-desktop-portal 1.22.1 at commit `1d20fadc304f6601452b5db65ed91197dba77041`, built from the publisher archive with SHA256 `d4879ddb3d65ff1a8f19187497e6f13dc5d267bcac404a5d501218be355753d3`. The archive's 198 relevant tracked files were compared to the commit without differences. Meson 1.12.1 is pinned; the image records the actual Ubuntu package candidates and installed package versions.

## Isolation and observable contract

- A fresh owned container has a private session bus, Xvfb, HOME and XDG directories. No host bus, display, application profile or Docker socket is mounted. Runtime networking and capabilities are disabled. The caller's UID owns the private evidence mount.
- Dunst, the GTK backend and the frontend must own their expected bus names before activating API calls. Readiness uses non-activating D-Bus driver queries; a foreign owner fails the experiment.
- The finite Gio sender registers its fresh TEST app ID with the public host Registry, calls AddNotification exactly once and exits. Its successful waitpid and absent bus owner must precede the click.
- An actual visible dunst window is discovered by class, then bound to the owned live dunst PID through server-derived XRes client identity. An unmapped owned resource checks XRes >=1.2 and local PID support before submission. Window ownership is checked again before the single XTest mouse click; no action signal or ActivateAction call is synthesized.
- The cold callback must have a different PID, kernel birth after collected sender exit, the captured helper hash, a live matching bus owner, the bounded TEST target and exactly one exclusive effect. Code-entry time is recorded separately from kernel birth.
- Owned processes are reaped; container removal requires matching owner token and exact container ID, followed by absence checks for both ID and name. Unknown submission or cleanup outcomes fail and do not trigger resend.

## Retained prerequisite failures

The first private run built the frontend successfully but could not read the 0700 evidence mount after capabilities were dropped. Running as the mount owner's UID fixed that prerequisite. The next run detected an autoactivated second frontend, before submission; ordered bus-owner readiness fixed that race.

The subsequent run sent a genuine Notify through GTK, collected sender exit 0 and received AddNotification success, but did not click: searching by `_NET_WM_PID` timed out. Dunst 1.9.2 does not set that property. The XRes check replaces that unsupported property assumption while retaining window ownership. Old evidence remains failed and unchanged; the revised scenario uses a fresh container and nonce.

The XRes scenario then produced actual ActionInvoked and ActivateAction, but its cold helper rejected a redundant `--gapplication-service` argument. Explicit Gio `IS_SERVICE` already selects service mode; removing that argument fixed the helper. A further run obtained one matching callback and effect but failed the process-disappearance assertion. Its orphan/zombie cause was not recorded and remains a hypothesis. Docker `--init` supplies an orphan reaper; the mandatory exit assertion was retained, with identity-checked failure diagnostics.

## Successful source-bound result

[Sanitized evidence](../evidence/navigation/2026-10-05/native-linux-portal-callback.json) records the final separate scenario. Sender PID 93 was collected with exit 0 before the single click. XRes bound the visible window to owned dunst PID 16. The portal cold-activated callback PID 109; its kernel birth lower bound followed collected sender exit. The callback received the matching bounded target, created one effect, exited, and its process disappeared. The native monitor recorded exactly one Notify, ActionInvoked and ActivateAction. Tracked processes were reaped and exact container ID/name absence was verified. Both native and outer reports passed; source snapshots remained unchanged.

The final runner hash is `a535859f375a3ecd5cd1e32e469fcbfb489b944c409a93bdacf2ddaf9f98ddae`; helper hash is `1de7a028a31a193b34a3095f8f9de9ea4cb23714b5399446f0e7c2b129ad1269`. Evidence includes the Dockerfile hash, actual GTK 1.15.1/dunst 1.9.2/libXres 1.2.1 package versions, raw artifact hashes and the previous failed execution without init. Inspected libXres 1.2.3 headers describe the 1.2 API; the native run records its actual installed library and extension version. Failed runs were not relabeled as passed.

## Reproduction and remaining qualification

Prepare the official archive and source provenance with `gh`, then run `scripts/navigation-linux-portal-callback-probe.py --build-and-execute-test --portal-source-archive ARCHIVE --portal-source-sha256 SHA256 --portal-source-provenance MANIFEST`. Use `--docker-via-sudo` only when required by the isolated host's Docker configuration. The manifest requires the exact commit, version, archive hash and preparation command; the runner preserves captured source, commands, logs and failed outcomes in a fresh TEST evidence directory.

This result qualifies only the owned X11 callback and cold TEST activation. It does not establish human-visible chat navigation, selected installed client identity, Wayland activation, backend or notification-server restart, nor the complete P5l adapter. The production Linux route remains unchanged until those contracts are demonstrated.

## Atomic evidence publication review fix

The callback helper now writes and fsyncs JSON in a same-directory temporary file, then publishes it with exclusive `os.link`. Polling readers cannot observe an empty or partial receipt, and duplicate effect publication still raises `FileExistsError` without replacing existing bytes. A source-captured deterministic filesystem experiment paused serialization: the old helper exposed a partial target, while the revised helper hid the target until complete JSON was available. Duplicate, serialization-failure and fsync-failure cases preserved the target and removed temporary files. This verifies atomic visibility, not power-loss durability.

The revised helper hash is `aa6bf02da8203abc13cee890c869cbd44392986e261f76a2271e20e170015199`; operator hash is `d195171e5397f91a4542928a3b9e75b5c2ec9bf3494a6a40837fc62ffd581764`. The native result above remains bound to its original helper snapshot; it is not relabeled as a run of the revised helper.
