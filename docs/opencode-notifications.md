# OpenCode notifications

Agent Notifications adds a global OpenCode plugin for **root-session completion,
questions, permission requests and terminal errors**. Alerts contain generic text;
they are silent and do not navigate to a terminal or session when clicked. It does
not provide Claude's plan/review events, contextual messages or sound controls.

The tested host is **OpenCode 1.18.33**. OpenCode V2 is not supported. The public
installer rejects V2 and reports the detected V1 version; that diagnostic does not
qualify every V1 release. Setup installs notifications, never OpenCode itself, and
does not start an agent session.

## Platforms and observed delivery

Native release targets: macOS arm64/amd64, Linux arm64/amd64 and Windows amd64.
For native Windows shell installation use **Git Bash**, not WSL or PowerShell.
Desktop delivery uses the signed macOS helper, the Linux desktop notification
service or Windows toasts. Linux needs an available desktop session/D-Bus service.

- The user confirmed a visible completion banner on macOS arm64.
- Linux amd64 X11/dunst rendered all four real OpenCode events; see the
  [captured banners](evidence/opencode-1.18.33-x11-notifications.png).
- Native lifecycle/webhook checks passed on all five targets. Headless CI does
  not establish visible macOS Intel, Linux ARM64 or Windows banners, or universal
  compatibility with every desktop environment.

## Install or update

Install OpenCode first. In the [guided installer](https://777genius.github.io/agent-notifications/#install),
select **OpenCode** and explicitly allow desktop notifications, webhooks or both.
The bootstrap requires Agent Notifications **v1.46.0 or newer** and acquires the
checksum-verified executable for your platform from the accepted release. On Mac,
desktop setup also acquires the signed native helper and its attestation sidecar
from the same release. The loader pins installer source to the release's exact
commit.

```bash
(set -o pipefail; curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product opencode --desktop)
```

Use `--webhook` instead of `--desktop` for webhook-only consent, or supply both.
Webhook consent alone does not configure a destination: add your endpoint to the
[shared settings](CONFIGURATION.md#manual-configuration), enable the desired
webhook and status channel, and restart OpenCode. Saved settings can further
restrict authorized delivery; setup does not enable portable MCP notifications.
The OpenCode installer does not register Claude marketplace plugins or Codex hooks.

Run the same command to update. It uses the idempotent `setup-opencode install`
action and reuses the runtime recorded in an existing ownership ledger. Each run
sets the OpenCode channels to the flags you explicitly select. **Restart OpenCode
after installation or update** to load the current global plugin bytes.

## Installed locations and manual setup

The managed control directory is `~/Library/Application Support/agent-notifications`
on macOS, `$XDG_CONFIG_HOME/agent-notifications` (otherwise
`~/.config/agent-notifications`) on Linux, and `%APPDATA%\agent-notifications` on
Windows. A fresh public install puts the runtime under its `runtime` directory;
an existing shared component keeps its recorded runtime location. Check
`ownership.json` in the control directory for the actual runtime; do not publish
this file unredacted.

The plugin goes to `OPENCODE_CONFIG_DIR/plugins/agent-notifications.js` when set,
otherwise `$XDG_CONFIG_HOME/opencode/plugins/agent-notifications.js`, otherwise
`~/.config/opencode/plugins/agent-notifications.js`. Use the same environment when
setting up and starting OpenCode. The CLI also supports `--opencode-config-dir`,
`--xdg-config-home`, `--home`, `--control-root` and `--runtime-root` for explicit
placement. On Windows pass native absolute paths (Git Bash `cygpath -m` can
convert them).

For manual/offline setup, obtain the native `claude-notifications-OS-ARCH` release
binary (`.exe` on Windows) and verify it against that release's `checksums.txt`.
The executable embeds the plugin; no npm installation or project dependency is
required. Below, `NOTIFICATIONS_BIN` means that verified binary or the executable
in your managed runtime:

```bash
"$NOTIFICATIONS_BIN" setup-opencode install \
  --binary /absolute/path/to/verified-native-binary \
  --runtime-root /absolute/path/to/new-managed-runtime \
  --desktop
```

On **macOS desktop**, also extract the checksum-verified `ClaudeNotifier.app.zip`
and pass `--native-app /absolute/path/ClaudeNotifier.app`. Keep the adjacent
`ClaudeNotifier.app.managed-runtime.json` sidecar. Setup verifies the helper's
sealed protocol, attestation and code signature before enabling desktop consent.
Webhook-only setup does not require the native helper. Existing installations
may omit `--runtime-root` to use their authoritative recorded location.

## macOS notification permission

Setup prepares the managed helper; event delivery never requests permission.
Use the installed executable for these explicit actions:

```bash
"$NOTIFICATIONS_BIN" setup-opencode permission-status
"$NOTIFICATIONS_BIN" setup-opencode request-permission
```

Allow **ClaudeNotifier** in the macOS prompt and in **System Settings >
Notifications**. Select banners or alerts and check Focus/Do Not Disturb if no
banner is visible. Permission or an accepted delivery receipt alone is not proof
that the OS displayed a banner. OpenCode notifications remain silent.

## Change channels, remove or recover

The lower-level update action requires an existing OpenCode registration and a
verified local executable source. Choose the channels on every update:

```bash
"$NOTIFICATIONS_BIN" setup-opencode update --binary /absolute/path/to/verified-native-binary --webhook
"$NOTIFICATIONS_BIN" setup-opencode remove
```

To enable macOS desktop on an update, supply the verified `--native-app` source
unless the existing managed helper already meets the required protocol. Removal
first revokes OpenCode consent, then removes owned registration and artifacts.
Other consumers and saved shared settings are preserved. **Restart OpenCode after
removal** to unload the old plugin. A loaded old plugin cannot admit new delivery
after revocation; a delivery already admitted before removal can still finish.
If interruption leaves a transaction journal, run `setup-opencode recover`,
then repeat the intended action. Recovery does not create channel consent.
Foreign, symlinked or edited plugin files are preserved and cause setup to stop;
resolve the reported ownership conflict explicitly rather than deleting blindly.

## Privacy, duplicate notifications and limits

The observer sends content-free facts to the local owned executable. It does not
forward prompts, question text, native error bodies or project metadata. Desktop
and webhook messages use generic copy; configuring a webhook intentionally sends
those generic events to your selected endpoint. Each request rechecks current
consent, registration and owned plugin/executable identities. See
[configuration](CONFIGURATION.md) for channel and status restrictions.

OpenCode's own desktop notifications and other notification plugins may create
duplicates. In OpenCode 1.18.33's desktop app, **Settings > General > Notifications**
has **Agent**, **Permissions** and **Errors** switches: disable overlapping native
notifications if Agent Notifications should be your notification source. Also
check other plugins in your OpenCode profile.

One-shot `opencode run` may exit before asynchronous delivery completes. Delivery
at host shutdown is best effort; notification delivery after process exit is not
guaranteed. Root-session events only are covered; nested subagent events, audio,
click-to-focus, plan/review alerts and OpenCode V2 are outside this integration.

## Linux amd64 qualification

On 2026-09-29, a fresh disposable Linux amd64 project and isolated OpenCode
profile loaded the bundle from Notifications commit
`2da9d192b1863f9d2220e760ad3fc47177ae15d2`.
The test used OpenCode 1.18.33 and a locally built Linux amd64 binary
(`sha256:5a83226dedb208c80c2597b5fa48de745bbbf6ddb5f7afdf4b7d95f7c111ee54`).
The bundle source hash was
`sha256:0148e31bbd1b9bca49c674689a5f621a26d72142974b9cc663b8a96f72461b81`.
The operator retained test data and logs in the disposable sandbox. No agent
command ran in a product checkout.

- A loopback webhook received `task_complete`, `question`,
  `permission_request`, and `opencode_error` from the loaded OpenCode plugin.
  The question came from a native `question.asked` triggered through the
  OpenCode SDK with the question tool enabled. The error came from a test
  request for a nonexistent model. Captured payloads contained none of the
  test prompt markers.
- With a private D-Bus session and test implementation of
  `org.freedesktop.Notifications`, the loaded plugin made one `Notify` call
  for a completed turn and one for a terminal error. Each call used generic
  product copy; no duplicate call was observed in those runs.
- `install`, `update` between desktop and webhook consent, and `remove`
  completed. While the OpenCode server still held the old plugin in memory,
  a new error after `remove` produced no webhook request.

One-shot `opencode run` can exit after printing an answer before asynchronous
notification delivery finishes. The persistent OpenCode server runs above
verified both loaded desktop outcomes. This MVP is best effort at process
shutdown; it does not promise delivery after the host exits.

### Visible desktop check (2026-09-30)

A second disposable, Git-initialized test project used the merged Notifications
binary (`sha256:a1a70f35a6fbdd40ac50123dca5582c6fb4fe6e88724427bfc5f7e8d0460e7aa`)
and OpenCode 1.18.33. `setup-opencode install --desktop` created a mode-0755
managed executable and a mode-0600 global plugin. The loaded OpenCode config
listed only that plugin. In a private D-Bus session with a real `dunst` daemon
and Xvfb display, native completion, question, permission, and error events each
rendered one visible system banner (`dunstctl count displayed` was 1 at each
capture). The banners used generic text and omitted the test prompt markers.
The [four captured banners](evidence/opencode-1.18.33-x11-notifications.png)
show the actual X11 pixels, in that order. This qualifies the Linux X11/dunst
path, not every desktop environment or the OpenCode desktop app's own switches.
