> Historical fixture implementation handoff. Executed G0/G5 qualification now passes all five native platforms; see the current result below. Original pending gates describe the worker handoff only.

# Gemini production artifact driver handoff

The patch owns only `tests/integration/gemini_notifications_e2e.py` and this
handoff. AN input is `571b303d80ff4a723a99f7bc698e732970df0888`. CONTRIBUTING,
the accepted plan and R3 were read; no applicable AN AGENTS.md was found.
Other writers' files are preserved. There are no consumer, installer, helper,
dependency, commit, release or tag changes.

**Provider work is inspection and pure checks only.** Native execution below is
for the trusted orchestrator outside the provider sandbox, with its existing
security guard. No native agent, candidate command, HTTP server, PTY, auth,
profile discovery, environment dump or real project execution was performed
here. `--trusted-orchestrator` is an explicit runner attestation, not a sandbox
bypass or a claim that the driver can detect every execution environment.

## Inspected contracts and the pending API

G0 is the orchestrator-supplied `.research/g0-reference/gemini_native_e2e.py`
and sibling `gemini_native_pty.cjs`, updated from native trials. The driver imports
the normal `tests/integration` sibling first after integration; this workspace
falls back to that reference, or accepts an explicit `--g0-driver`. It reuses
G0's `Fixture`, `Terminal`, `exercise`, `new_lab`, `minimal_env`, bounded process
runner and artifact identity helpers. It adds no terminal/platform framework.
The PTY uses the actual CLI-installed `@lydell/node-pty` 1.1.0 backend.

The final N2 parser `cmd/claude-notifications/gemini_setup.go` has SHA-256
`08a7560511b619073da98a01bb530be43bd5dcc2303a63c0d9b0715591766c57`.
Its install/update/remove/recover and root/artifact/channel flags are used
without calling permission activation or a public-latest downloader.
The driver executes readonly `inspect` after installation and removal and
requires exactly `status`, `registered`, `desktop`, `webhook`, exit zero and
no stderr. Installed inspect must preserve ledger, receipt and settings.
No action/help/unknown flag return 2; successful lifecycle returns 0 with
`Gemini notifications <action> complete`; damaged-receipt remove returns 1.
These contracts come from the actual completed parser and production commands.

N2 `internal/geminiinstall/{setup,receipt,registration,revoke}.go` and the
existing transaction kernel supply the lifecycle checks: `ownership.json`
uses Go field names; `gemini-receipt.json` uses its explicit JSON tags;
consumer ID is `gemini-notifications`; consent is
`agent-notifications.json` → `route.geminiNotifications.desktop/webhook`.
Identical bytes/channels produce no change to generation, installation ID,
binding nonce, receipt, owned settings or policy. Reinstall creates a new nonce.
For a separately supplied changed artifact, update preserves binding/receipt,
native settings and channel policy and advances both ledger generations by one.
Settings are ConfigPaths, not whole-file ledger assets. The UAP planner retains
responsibility for quoting and full-group ownership digests.

N1 is the actual finished source/consumer snapshot supplied as `4c8c48`.
Its generic formatter emits exactly `schema_version`, `status`,
`notification_type`, `agent_source`, `message`, `timestamp`, `session_id`,
`source`, `title`. The exact `source` is `claude-notifications`,
`agent_source` is `gemini`, schema is `1.0`, session is empty and title is
`Gemini CLI`. Bodies are exclusively `Gemini CLI completed a turn` and
`Gemini CLI requested tool permission`. The capture validator rejects any
additional field or enriched copy and returns only the two fixed status classes.
No raw request, hook payload or terminal text is saved in evidence.

## Trusted Mac invocation

Supply physical absolute paths to the actual AN executable, existing
ClaudeNotifier.app, exact installed Gemini CLI 0.62.0 entry point, Node, bash,
UI contract and exact public SDK/installer module trees used for the build.
Artifact/module/CLI roots must be TEST-marked with `.an-gemini-TEST` and must
be outside the source repository and owner HOME. The lab must not exist and
must have an existing non-repository parent. No artifacts are downloaded or
built by this driver. Hashes bind supplied bytes, not an assertion that those
modules were linked; separately attach actual build/module-graph evidence.
To test replacement, also supply `--update-binary` with a distinct, actual built
TEST candidate using the same inspected API. The driver never manufactures a
second binary by appending bytes or substituting a test executable. Without
that input, changed-artifact update remains an explicit external pending gate;
identical-artifact update is still tested.

For example, substitute the orchestrator's actual physical paths in this
command **outside the provider sandbox**:

```sh
python3 tests/integration/gemini_notifications_e2e.py \
  --trusted-orchestrator --desktop \
  --binary /private/tmp/TEST-artifacts/claude-notifications-darwin-arm64 \
  --native-app /private/tmp/TEST-artifacts/ClaudeNotifier.app \
  --gemini-executable /private/tmp/TEST-cli/node_modules/@google/gemini-cli/bundle/gemini.js \
  --cli-install-root /private/tmp/TEST-cli \
  --node-executable /physical/path/to/node \
  --hook-shell /physical/path/to/bash \
  --ui-contract /private/tmp/TEST-cli/permission-ui.json \
  --sdk-module-root /private/tmp/TEST-modules/sdk \
  --installer-module-root /private/tmp/TEST-modules/agentplugins \
  --lab-root /private/tmp/TEST-production-g5 --timeout 240
```

The example paths are placeholders, not qualified installations. Use the
actual entry point path identified by G0. The UI contract is the same G0
contract from actual CLI source/native trials: permission pattern, isolated
approve/deny keys, cancel key and source SHA-256. Native text entry and Enter
are separate by 150 ms; choices are isolated keystrokes. The CLI session has
the G0 watchdog (60–240 s). Each candidate command is bounded to 100 s,
version/help checks to 12 s, each native turn to 25 s, and child/server/spool
shutdown has bounded joins. Kill operations belong only to the G0-owned child
or its newly created process group. No owner process is enumerated or killed.

G0 constructs a fresh minimum environment: HOME, USERPROFILE and
GEMINI_CLI_HOME all select TEST profile; system/default/trust settings,
XDG and temp roots are TEST-owned. Synthetic key and forced file storage
avoid stored-key/keychain lookup. Settings retain gateway `useExternal`,
fixed `gemini-2.5-flash`, `tools.useRipgrep=false`, dotenv sentinels, empty
memory and the TEST git boundary. There is no inherited auth/proxy/header
environment. Startup qualification remains the separate G0 evidence: this
driver does not infer that qualification from path strings.

## What native mode verifies

1. G0 installs only its TEST recorder groups with an empty probe map. G5
   renames their `an-TEST-*` identity to `foreign-an-TEST-*` before production
   setup and retains unknown/policy fields. Production setup installs its own
   two groups. There is no recorder-to-consumer invocation or hook stdin injection.
2. Real explicit webhook/desktop consent precedes writing schema-v2 AN config
   at TEST `an-control/config.json`. A hard link in the actual legacy AN
   selector under synthetic TEST HOME refers to that same inode. This avoids
   adding a forbidden environment override to G0's PTY allowlist. Global channels
   are disabled and `agents.gemini` explicitly enables selected channels;
   sentinel status titles/payloadFields must not appear in captured delivery.
3. Install, repeat, identical-artifact update and clean-state recover verify
   current ledger/receipt identities and owned artifact hashes. Repeat and
   identical update must leave nonce/binding/generation and settings unchanged.
   This checks clean recovery, not interrupted-transaction crash recovery.
   TEST control policy starts with foreign route/unknown fields and disabled
   portable enablement, all of which must survive lifecycle operations.
4. The same live native session executes G0 plain/equal turns and actual
   ToolPermission approve/deny/cancel/recovery cases. Foreign recorder facts,
   rendered permission UI and actual TEST write-file effects are checked.
   Production fixed-payload deliveries must match observed completion and
   permission classes. Cancel completion is whatever the CLI actually emits;
   the driver never fabricates it. Equal text does not collapse two turns.
   If `--update-binary` is supplied, the same session stays alive across real
   replacement; binding stays unchanged, generation advances, and a new native
   turn must deliver through the updated artifact.
5. One extra native turn disables only the completion webhook channel in the
   per-agent profile. No capture may arrive. Desktop independence is claimed
   only when a correlated, fixed-copy desktop acceptance is actually observed
   during that turn; otherwise it remains unverified.
6. Real remove executes while the CLI remains alive. Both consents must be false,
   both own groups and registration/receipt removed, and foreign settings/groups
   preserved. A further native turn must still reach the foreign recorder in
   that same session and produce no new webhook, including a five-second
   observation window. This does not assume whether Gemini caches hook commands.
7. A separate reinstall/session verifies nonce renewal and genuine delivery,
   then damages only the ledger-owned TEST receipt. Remove must conflict while
   revoking both channels. Settings and the installed binary must remain intact.
   A further native turn with those retained production groups/binary must
   produce no webhook for five seconds. That is native evidence for the stale
   installed-command gate; it is not a theoretical post-delete claim. Only the
   driver's own exact damage is restored, then real removal cleans up. On
   failure, shutdown and cleanup are attempted and any uncertainty is reported.

Mac desktop uses the existing notifier library/native helper and
`gemini-native-spool`. A bounded read-only watcher validates actual owner,
request and receipt schemas, nonce/correlation binding, fixed title/body,
silent delivery and absent navigation. Only fixed classes/statuses and whether
fixed copy was observed survive in the manifest. Spool files may be deleted
before observation; missing evidence remains unverified. Receipt `submitted`
with `os_accepted` proves OS acceptance only, never a visible banner.
An observed rejected/unknown receipt makes the driver fail; it is not quietly
reclassified as missing evidence.

## Evidence and remaining qualification

Native mode writes `<TEST lab>/production-evidence.json` and emits a short
result with the exact path. It records candidate/module/helper/CLI/package/
PTY/backend/Node/driver hashes and current-state lifecycle results. Per-OS
levels are separate: `build/contracts`, `native_cli/provider_substitute`,
`OS_API`, `visual`. Unexecuted Windows/Linux records remain unverified.
Build/contracts require independent actual build/test evidence; hashes alone
do not turn them green. A successful driver reports only implemented scenarios
with external gates pending. It does not claim complete G5 qualification.

The foreign recorder's neutral stdout describes that recorder alone.
Production neutral stdout requires separate actual contract evidence. G0 debug
output is its inspected fixed classification allowlist; observer rows are
validated against a strict known evidence field set. Production raw stdout,
stderr, native extra fields, provider bodies and terminal text are not persisted.

Still external: actual native run of this driver,
exact candidate build/public module-graph and contract results, G0 startup/native
qualification evidence, and visible Mac banner evidence supplied by the owner
or trusted observer. Changed-artifact update also needs an actual second build.
Interrupted recovery, blocking-hook continuation,
resume/restart and nested/subagent modes remain explicitly unqualified.
No visual evidence is auto-promoted. No Windows/Linux native or OS proof is
claimed without execution on those hosts. This is a local artifact driver,
not public-channel installation or live Google model-service qualification.

## Provider-side checks

```sh
python3 -B tests/integration/gemini_notifications_e2e.py --self-test
git diff --check
```

The pure suite checks fixed-class capture/privacy rejection, lifecycle identity
drift, foreign/policy preservation, correlated receipt rejection and artifact/
runner/action guards. It uses no subprocess, HTTP, PTY, fake production hook,
installer mock, or source-grep passing gate. Results from this workspace are
recorded in the final handoff response; native execution remains unverified.

## Executed qualification, 2026-10-01

GitHub run `36859247789` at exact AN
`cee81a77d8aef5369456cc24ad4e9be5184b3264` passed Windows amd64,
Linux amd64/arm64 and macOS arm64/Intel, with actual native Gemini 0.62.0
and production AN binaries. All six native cases and the documented lifecycle,
changed executable update, foreign-hook preservation and two live-session
revocation cases passed. Both native children exited zero without forced cleanup.
Public SDK `2d2a26bf372a` and agentplugins `11a842f55d19` were linked with
`GOWORK=off` and no module replacements.

The owner's Mac separately passed production desktop delivery using the existing
signed helper, ten submitted receipts (seven completion, three permission),
including desktop delivery with webhooks disabled. The owner confirmed both fixed
banner texts in the earlier native run. Its visual attestation retains its original
artifact binding; final-graph delivery is separate evidence. See
`gemini-native-harness-evidence.md` for exact binary identities and limitations.

All CI jobs use a deterministic loopback model provider. Windows/Linux/Intel
notification API/visual desktop and live Google model service remain unqualified;
public installation awaits a compatible release. Native CLI/webhook qualification
is complete and does not imply those separate capabilities.
