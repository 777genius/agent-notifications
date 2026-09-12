# Explicit notification setup

`claude-notifications setup-notifications --help` describes the operator interface
for an **existing managed installation**. It runs before legacy hook logging.
The existing installer owns runtime installation and native artifacts. Explicit
`prepare` composes its existing global configuration core; it installs no runtime.
Only the `existing-installer` owner is supported; portable registration is separate.

Run one operation at a time:

```text
claude-notifications setup-notifications prepare --legacy-config /physical/legacy/config.json --defaults-config /physical/bundle/config.json --expected-generation N
claude-notifications setup-notifications status --json
claude-notifications setup-notifications permission-status --json
claude-notifications setup-notifications request-permission --expected-generation N
claude-notifications setup-notifications register --provider codex --config /physical/client/config.toml --command /physical/runtime/bin/claude-notifications --expected-generation N
claude-notifications setup-notifications enable --app /physical/Applications/Codex.app --team-id ABCDE12345 --allow-unknown-caller true --allow-caller-asserted false --expected-generation N
claude-notifications setup-notifications status
claude-notifications setup-notifications disable --expected-generation N
claude-notifications setup-notifications remove --provider codex --config /physical/client/config.toml --command /physical/runtime/bin/claude-notifications --expected-generation N
```

These are templates: replace paths, verified team ID and `N` with the operator's
selected values. **Read status again before every mutation.** `N` must be that
installation's exact positive generation. Register and enable are separate actions;
there is no atomic transaction spanning them. Setup provisioning can reserve newer
generations before an error without committing enable. The result reports the last
known generation; after any error reread status and use installer recovery if needed.
Do not retry with a fabricated generation or reset state.

`--control-root` defaults to the existing OS config directory's
`agent-notifications` child. `--runtime-root` defaults to the managed ledger runtime
and must match it. `--global-config`, accepted by prepare, status and enable, defaults to the
existing canonical `~/.claude/claude-notifications-go/config.json`. Client config and
command are deliberately explicit: the command never guesses a client profile,
current chat, project scope or executable from the working directory. The command
must be a ledger-owned executable or stable managed alias within that runtime.
Paths must be absolute, clean and physical (including temporary paths on macOS);
only the command's final managed alias may contain ledger-verified links.

For Claude, use `--provider claude` and the exact operator-selected client JSON
config. For Codex, use `--provider codex` and its selected TOML config. Registration
uses the existing clientsetup editor and kernel ownership transaction. It preserves
foreign entries, hooks, user disable flags, disabled tools, environment and timeout
restrictions; altered owned command/args cause a conflict. It does not run
`codex mcp add`. A no-op preserves bytes. Changed JSON may be reformatted by the
existing editor; formatting is not a preservation guarantee for changed files.
Removal operates on this exact registration, leaving unrelated consumers intact.
Disable changes explicit intent and leaves registrations, hooks and global settings
in place, even when the selected app, global settings, journal or native artifact
is unavailable. Managed ownership, writer-floor, recovery and generation checks
still apply.

Only **enable** commits an explicit opt-in. For a local app route it calls the existing setup core with
the real offline `verifyAgentNotifyApplication` verifier (Codex bundle identity and
operator-selected signing team). It neither launches the selected app nor requests
notification permission. To replace a route, supply the app, team and both consent
choices together. Omitting all four preserves the previous route and consent.
Alternatively, explicitly choose `enable --navigation none --allow-unknown-caller true --allow-caller-asserted false --expected-generation N`
for informational notifications without navigation, including standalone Claude use
without a Codex app. Only `none` is supported. `--navigation` is enable-only and
incompatible with `--app` and `--team-id`. Both
`--allow-unknown-caller true|false` and `--allow-caller-asserted true|false`
are required with `--navigation none`; the parser does not imply them.
This writes an explicit route with empty app fields and localRouting false,
clearing any previous app route. It skips app verification, while managed native,
global configuration, journal and eligibility checks still apply. Omitting all
route choices preserves the previous selection, including none; a fresh enable
without a route fails `route_required`. This does not infer client origin or promise
exact Claude terminal navigation. Send informational tool requests with
`navigation: "none"`; the default remains `required`, which rejects without a route.
Never silently downgrade a required request after failure.

Both booleans accept only `true` or `false`; partial route edits are rejected.
Rates and unknown policy fields remain under the existing core's preservation
rules. Ordinary register never changes opt-in.

For app routing, unknown-caller consent extends the selected local Codex route to callers whose
interface cannot be distinguished, including shared CLI/Desktop registrations.
Caller-asserted consent separately permits caller-supplied CLI context; it does not establish
trusted current-chat identity. The chosen route attempts the supplied chat ID in
the current local Codex profile; it cannot promise the original window, distinguish
all hidden/remote callers, or detect profile/store incompatibility. Claude MCP
registration alone does not establish exact-session navigation.

Success distinguishes explicit intent, managed runtime eligibility, client
activation and permission. Global desktop/sound/focus opt-outs remain effective.
Status reads the existing runtime status adapter without creating/repairing state,
opening journals, launching native probes or requesting permission. Its offline
capability is an observation, not proof of delivery, tool visibility or a GUI click.
A registered client is **activation-required**: refresh/restart it separately and
retain its existing authorization controls. Status cannot confirm that activation.
Existing operations leave permission `not_checked` and never prompt.
`permission-status` reads native OS settings using the current exact managed snapshot;
it accepts no expected generation. `request-permission` requires the exact positive
`--expected-generation` and explicitly authorizes a request. Both work before enable
and without client registration; a valid managed artifact and owner are mandatory.
Neither operation changes policy, ledger, journal, client activation or explicit intent.
They do not require a selected application or global config. The production adapter
calls `notifier.SetupPermission` with the exact verified snapshot and request flag.
The native core validates capability and requests at most once, only if undetermined.
It never enters legacy send/permission flow or sends a notification.

Permission results are `allowed`, `denied`, `undetermined` or `unavailable`.
Allowed does not mean enabled or activated. For denied, review the managed notifier
in macOS System Settings > Notifications. Undetermined is an honest current state,
not authorization. Errors, cancellation and uncertain results are unavailable;
do not automatically repeat authorization. A read-only `permission-status` follow-up
may clarify the result. Native work holds a verified disabled-capable setup lease
for at most 130 seconds within the total three-minute setup budget. Cancellation or
command completion does not prove that an already displayed OS prompt has gone away.
No model-facing setup tool is provided.

Exit codes are 0 for a completed operation/help, 2 for invalid arguments, and 1 for
an operation, cancellation, output or composition failure. `--json` returns a small
result with reason/generation and known status fields, never full config, paths,
credentials or raw core errors. Error results omit unproven enable/activation
claims; permission-operation errors explicitly report permission `unavailable`. Common reasons include `generation_changed`, `registration_conflict`,
`managed_runtime_required`, `installation_invalid`, `configuration_required`,
`configuration_invalid`, `application_identity_invalid`, `recovery_required` and
`canceled`; setup core reason codes are retained. Other kernel failures remain
`mutation_failed` or `policy_commit_failed`; inspect/recover through the installer
rather than assuming a whole-configuration rollback. A successful registration
followed by an unreadable policy returns `registration_saved_status_unavailable`.

Parsing is bounded to 32 tokens, 16 KiB total and 4096 bytes per token. Unknown,
duplicate, misplaced, incomplete and control-character flags fail before runtime
access. Help must stand alone or follow one operation. Setup has a three-minute
budget and handles interrupt/termination cancellation. Production output reuses the
existing owned stdio adapter, including one-second pipe write deadlines and restoring
shared descriptor flags. File/null redirection is supported; OS filesystem syscalls
cannot be promised interruptible. A callable adapter's output writer is borrowed
and must provide its own bounded writes. This interface does not initialize the
legacy global logger.

Hosted tests use real setup/clientsetup/kernel temporary fixtures, an injected
clock/verifier, an inert native qualification fixture and a private permission
function seam. Unit tests never execute fixture/native apps. The production binding
is asserted without invoking it. Production has no
platform/environment bypass. Linux tests do not qualify macOS codesigning,
permission, installed client activation, native delivery or GUI routing. Those
remain separate installer/native gates.

Caller consent is stored compatibly under the shared `route` policy but does not
grant a navigation target. For Claude informational MCP requests, explicitly use
`enable --navigation none --allow-unknown-caller true --allow-caller-asserted false --expected-generation N` and send `navigation: "none"` with a stable, high-entropy `request_id` unique to the new event. Without a session ID, these callers share an anonymous deduplication scope and rate bucket; recurring labels can replay or conflict with another task. Reuse an ID only for the same operation.
Claude metadata (`claudecode`, `toolUseId`, `progressToken`) does not establish a
trusted session or local GUI. Unknown callers need explicit consent even for none;
caller-asserted context needs its separate flag. Known remote/headless callers
remain rejected. Switching app to none clears the app/team and uses the supplied
consent pair (default false/false), never inherited consents.

`prepare` requires explicit `--legacy-config`, `--defaults-config` and a matching
positive `--expected-generation`. The optional `--global-config` retains the stable
canonical default above. All selected paths must be clean physical absolute paths.
Only this preparation core may create missing global parents. It preserves explicit
false desktop/sound/focus choices and unknown raw fields, fills missing booleans,
and rejects malformed canonical data without falling back to legacy or defaults.
Results report `source` (canonical, legacy, defaults), `changed` and `ready` without
configuration content or paths. Global settings are mutable, never ledger assets.
Preparation does not register clients, opt in, verify applications, open journals,
probe/request permission or send notifications. It uses the config core lock/CAS,
without nesting component locks. A stale initial generation causes no mutation;
a concurrent installer change can produce `generation_changed` after preparation
has persisted, with readiness and the newly observed generation. Reread status;
preparation and registration are not an atomic transaction.

Codex register optionally accepts `--skill-destination /physical/user/skills/agent-notify/SKILL.md`.
Explicit setup safely creates missing physical destination parents; read-only inspection does not create them. Source comes
exclusively from the authoritative ledger runtime's `skills/agent-notify/SKILL.md`,
which must be ledger-owned. The selected copy must be outside runtime/control.
Explicit selection refreshes through the clientsetup ownership transaction; omission
preserves a recorded copy. Same bytes are a no-op. Foreign, tampered, relative or
symlink destinations conflict without overwrite. Claude and remove reject this flag;
remove uses the stored projection and preserves other consumers. Client config and
command remain explicit; no client home, plugin inventory or cache is guessed.

Status independently reports `globalConfiguration` as `configured`,
`configuration_required` or `configuration_invalid`, even when `configuration` is
`disabled`. `desktopEnabled` appears only after a valid strict bounded read; it is
never an invented false value. Missing/invalid global settings return exit 1, with
the disabled observation retained. Inspection is read-only and permission remains
`not_checked`. Disable, removal and permission operations do not read global config.

### Unified explicit configure

After installing, run `claude-notifications setup-notifications configure
--provider codex|claude|both` with an explicit fresh route (`--navigation none`
and both consent flags, or the complete app/team/consent tuple). Route omission preserves existing shared
policy; adding Claude does not clear a Codex route. Claude informational calls use
request-level `navigation: "none"` and an explicit request ID.

Bootstrap, Claude init, and `setup-codex` enable agent-notify by default
(`--agent-notify`, with `--navigation none --allow-unknown-caller true --allow-caller-asserted false` when no route is supplied).
Pass `--skip-agent-notify` to keep hooks-only setup. If agent-notify
configure fails after a successful install, the installer reports the
error and leaves hooks/plugin in place; retry
`setup-notifications configure` after resolving the cause. Bootstrap
configures once after all selected installs succeed. Ordinary update still
preserves intent after the MCP entry exists. `--request-permission` is optional and explicit; when requested, only an allowed
result continues to enable. Otherwise setup reads permission status without prompting
and reports it separately from saved intent. No notification is sent. Registration preserves client disable
and tool restrictions and still requires client activation.

Global settings always live at `$HOME/.claude/claude-notifications-go/config.json`.
Claude MCP lives at `$HOME/.claude.json`, or absolute `$CLAUDE_CONFIG_DIR/.claude.json`.
Codex home inputs must be absolute. Configuration uses the ledger primary runtime.
Failures report saved stages and last observed intent/generation without rolling
back foreign edits. Retry explicitly after resolving the reported conflict.

Inventory is bounded to selected user configuration and exact own installed
packages. Unsupported or ambiguous installed inventories fail closed; it does not
scan projects or use authored Codex plugin sources as effective cache authority.
Installed client discovery and Desktop delivery remain separate qualification gates.

Production low-level `--global-config` accepts only the canonical global path.
The Codex 0.152.0 and 0.153.4 no-skill cache layouts are version-qualified; unsupported versions
remain unknown. Bootstrap acquires into a disposable bundle, then setup-codex
commits the stable installation before the final configure call.
