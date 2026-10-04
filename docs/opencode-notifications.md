# OpenCode notifications

Agent Notifications adds a global OpenCode plugin for **root-session completion,
questions, permission requests and terminal errors**. Desktop completion uses
`✅ [Native session title]` when available, and questions show their actual text
as `❓ Actual question?` with the session title as context. macOS uses a native
subtitle; Linux and Windows include that context above the body. Missing context
falls back to generic OpenCode copy. Alerts remain silent and do not navigate to
a terminal or session when clicked. Plan/review events and sound controls are
outside this integration.

The dual-API release candidate uses **one installed plugin** for OpenCode V1 and
[V2](https://opencode.ai/v2/docs): V1 calls `server`, V2 calls `setup`. Native
qualification targets are **1.18.33, 1.18.34 and 2.0.21**. The candidate installer
accepts stable V1 >= 1.18.29 and V2 >= 2.0.0; this range does not qualify every
release. Prereleases and unknown future API generations are rejected.
Setup installs notifications, never OpenCode itself, and does not start an agent session.

**Publication pending:** this candidate requires the separately reviewed
`universal-agent-plugins-opencode-events@0.3.0` package. Qualification consumes its
exact local tarball; publishing that package and validating a clean registry install
are separate release steps. These changes do not upgrade an existing installation.

## Platforms and observed delivery

Native release targets: macOS arm64/amd64, Linux arm64/amd64 and Windows amd64.
For native Windows shell installation use **Git Bash**, not WSL or PowerShell.
Desktop delivery uses the signed macOS helper, the Linux desktop notification
service or Windows toasts. Linux needs an available desktop session/D-Bus service.

- The user confirmed a visible completion banner on macOS arm64.
- Linux amd64 X11/dunst rendered all four real OpenCode events; see the
  [captured banners](evidence/opencode-1.18.33-x11-notifications.png).
- V1 native lifecycle/webhook checks passed on all five targets. Headless CI does
  not establish visible macOS Intel, Linux ARM64 or Windows banners, or universal
  compatibility with every desktop environment.
- Retained V2 native completion observations provide corroboration, but full
  current-candidate qualification remains pending. Installed lifecycle and delivery
  qualification are still owed on the native targets; workflow lanes alone do not
  establish that they passed. V2 visible banners are not claimed.

**Windows V1 limitation:** stock OpenCode V1 events do not always allow the
original event age to be verified independently. A delayed completion can notify
once, and the same completion can notify again after its 24-hour deduplication
claim expires. Root-session and workspace filters, origin-bound provenance,
deduplication within that claim lifetime, and lookup and IPC limits still apply.
This accepted limitation does not qualify every Windows V1 version or establish
that the current candidate has completed native qualification.

## Dual candidate evidence boundary

The installed dual candidate fixture in `scripts/opencode-native-e2e.py` is source
preparation only. Its eleven native cells cover V1 1.18.33 and V2 2.0.21 on all
five platform pairs, plus Linux amd64 V1 1.18.34. No cell has been executed or
qualified by this preparation. Exact bundle/SDK/image custody, authoritative
managed configuration, production parent/profile and complete clock qualification
must precede delivery cases. Historical V1 evidence below does not qualify this
candidate or V2. Missing prerequisites report unqualified and stop business phases.

## Install or update

Install OpenCode first. In the [guided installer](https://777genius.github.io/agent-notifications/#install),
select **OpenCode**. The guided command enables desktop notifications; webhook destinations and delivery can be configured later. You can also select Claude or Codex CLI in the same setup. The copied command uses one loader, for example:

```bash
(set -o pipefail; curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --products claude,opencode --desktop)
```

Channel flags grant consent only for OpenCode; webhook URLs still need separate configuration. Claude/Codex setup runs first. If a later setup fails, earlier successful installations remain installed; fix the error and rerun the command.
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
[installed OpenCode settings](#edit-installed-opencode-settings), enable the desired
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
placement. On Windows, OpenCode placement paths must be clean native absolute
paths with backslashes, without `..` or a trailing separator. In Git Bash use
`cygpath -w` and quote the result, especially for `OPENCODE_CONFIG_DIR`,
`XDG_CONFIG_HOME`, `--opencode-config-dir` and `--home`; `cygpath -m` emits forward
slashes and does not satisfy this strict placement contract. For example:

```bash
export OPENCODE_CONFIG_DIR="$(cygpath -w /absolute/path/to/opencode-profile)"
```

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

## Edit installed OpenCode settings

Use the executable from the managed runtime to select OpenCode explicitly:

```bash
"$NOTIFICATIONS_BIN" config path --target opencode --json
"$NOTIFICATIONS_BIN" config inspect --target opencode --json
```

This selects `agent-notifications.json` in the managed control directory, the
same file the installed event consumer reads. Selection verifies the existing
ownership ledger, platform command, origin-bound plugin and its control-root
binding. The default control location follows the platform paths above. If setup
used `--control-root`, pass that same existing directory to **every** config
command, for example `config inspect --target opencode --control-root
/absolute/path/to/control --json`. A control root is accepted only with matching
installed metadata; it is not an arbitrary config-file path.

Copy the opaque `revision` from that inspection. Submit only supported config
leaf edits through private stdin. For example, configure the webhook destination
without persisting an expanded secret:

```bash
printf '%s\n' '{"set":{"/notifications/webhook/enabled":true,"/notifications/webhook/url":"${AGENT_NOTIFICATIONS_WEBHOOK_URL}"}}' |
  "$NOTIFICATIONS_BIN" config edit --target opencode --stdin --expect-revision 'REVISION_FROM_INSPECT'
```

Replace `REVISION_FROM_INSPECT` with the inspected revision and make
`AGENT_NOTIFICATIONS_WEBHOOK_URL` available to OpenCode's environment before
starting it. Managed OpenCode webhook URLs support only this environment token
(or a literal URL); other URL tokens are rejected with
`ConfigOpenCodeWebhookEnvUnsupported`. The value is passed only to event delivery,
never clock or profile helpers, and the saved document retains the token. Keep any saved patch and
inspection private. For a custom control root, include `--control-root` on the
edit as well. Inspect again after success. Settings are read on subsequent events;
a settings-only edit does not replace the loaded plugin or require a restart.

The editor uses the existing config leaf validation and raw-value preservation,
then the managed transaction's policy CAS, component/config locks and generation
publication. Unedited policy fields, other agents' route settings, setup channel
consent and the OpenCode origin remain intact. No-op edits retain the generation.
A setup/update/removal or byte change invalidates the revision; inspect again and
review the intended edit after `ConfigConflict`. If a commit is uncertain, inspect
and resolve any reported recovery before retrying. Interrupted managed transactions
use the existing `setup-opencode recover` lifecycle.

OpenCode channel consent still comes from the explicit setup `--desktop` and
`--webhook` flags. Config edits can restrict delivery and configure endpoints;
they do not grant setup consent or change registrations. Missing, corrupt,
unrecognized, recovery-pending or differently bound installations fail instead
of creating a policy or falling back to shared settings. Use the matching installed
executable; a plugin from a different embedded release must be updated through
setup first. Managed config `init`, imports and `preflight-update` are unsupported.
The managed policy retains its existing schema 1; schema 2 agent-profile migration
is outside this command.

Omitting `--target`, or using `--target shared`, preserves ordinary config
selection and its `AGENT_NOTIFICATIONS_CONFIG` override. The OpenCode target
ignores that override and `AGENT_NOTIFICATIONS_CONTROL_ROOT`; use the verified
control-root selection above.

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
```

On macOS/Linux, use `"$NOTIFICATIONS_BIN" setup-opencode remove`. On Windows,
use the installer's printed **Remove** command: it runs a temporary copy because
a running managed `.exe` cannot delete itself. The equivalent Git Bash command
is below; set `NOTIFICATIONS_BIN` to the installed executable:

```bash
(
  set -eu
  stage=$(mktemp -d "${TMPDIR:-/tmp}/agent-notifications-remove.XXXXXX")
  trap 'status=$?; rm -rf "$stage"; exit "$status"' EXIT
  cp "$NOTIFICATIONS_BIN" "$stage/remover.exe"
  "$stage/remover.exe" setup-opencode remove
)
```

A trusted release executable outside the managed runtime may also run
`setup-opencode remove` directly on Windows. Temporary cleanup preserves the
remover's exit status; a reported ownership conflict must still be resolved.

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

The shared observer sends content-free facts. The product plugin adds an optional
bounded desktop display envelope with a freshly verified native session title
and an immutable snapshot of the exact request's question text. Multiple questions
appear in their original order. Headers, answer options, prompts, paths and native
error bodies are excluded. Invalid or oversized metadata falls back to generic
copy. Turning off `notifications.desktop.showSessionLabel` hides session names
while keeping concrete question text. Webhook messages and receipts remain
generic; configuring a webhook intentionally sends those generic events to your
selected endpoint. Each request rechecks current
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
click-to-focus and plan/review alerts are outside this integration.

V2 completion means one positively verified final answer per native busy period.
Queued or steered inputs can share that period. A tool step, retry, compaction
summary or interrupted execution does not itself produce completion. The observer
checks the root session and its owning directory/workspace before delivery, since
V2 plugin event subscriptions also receive events from other locations.

Lookup and IPC limits apply per plugin instance. Soft lookup timeouts retain their
capacity until the native promise settles, including on 2.0.0 where abort signals
are ignored. Cleanup suppresses late verification without waiting indefinitely;
there is no delivery replay after an uncertain result. V2 2.0.0 needs a server
restart to reload the plugin; 2.0.21 supports location reload.

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
