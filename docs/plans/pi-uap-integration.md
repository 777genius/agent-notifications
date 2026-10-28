# Pi notifications through Universal Agent Plugins

Status: revision 3 after both critic rounds and the targeted round 2 correction. Owner
scope: full Pi support on macOS, Linux and Windows, including
configured sound and a notification click returning to the originating terminal.
All implementation and review workers use GPT-6.1 Sol with xhigh reasoning.

## Outcome and scope

Pi becomes a selectable product in the existing Agent Notifications setup. UAP
materializes its managed package directly in Pi's native extension directory.
Install, update, repair, inspection and removal use the existing source snapshots,
binding identity, directory ownership/recovery and runtime handoff. Normal Pi
activity reaches the existing notification configuration, rendering, sound and
terminal navigation paths. README and landing present Pi as supported when this
work is complete. Publication of a release is a later owner action.

This change does not consolidate Cursor or Qoder, reopen the frozen VS Code
implementation, change legacy YAML authoring, or add a second installer. The
compatibility Go module paths retain `github.com/777genius/plugin-kit-ai`.

## Research baseline and sources

- Agent Notifications baseline: `b75ccdadb86b90ea4bafa13a6eb342adbacba5d3`.
- UAP baseline: `e713efae20f5e7e14b0478479b0abc391a9a6ad0`. Notifications currently
  pins integrationctl/agentplugins to `4284bfe31c05` and SDK to `v1.3.0`; the
  newer local checkout is research input, not an implicit dependency update.
- Pi baseline: official `earendil-works/pi` v1.4.0,
  `4a080252f06caf63a525d7873c5d94d3c23887e0`.
- Pi native contract: [extension discovery and execution][pi-loader],
  [agent directory selection][pi-config], [extension event types][pi-types],
  [settlement implementation][pi-session] and [extension guide][pi-guide].
- Local authority: both repositories' `AGENTS.md`, UAP
  `docs/AUTHORING_CAPABILITY_PRESERVATION.md`, and current owner decisions in
  `docs/STANDARD_FIRST_AUTHORING_ENGINE_IMPLEMENTATION_PLAN.md`.
- UAP architecture evidence: `clients/{planning,staging,lifecycle,identity}.go`,
  `clients/all/registry.go`, `domain/{clients,traits,state}.go`,
  `planner/{planner,compatibility}.go`, `providers/stager_sanitize.go`, and the
  Gemini native ownership/lifecycle implementation. Those paths below are
  relative to `install/integrationctl/agentplugins/` unless stated otherwise.
- Notifications composition evidence: `internal/agentnotify/portablesetup/uap.go`,
  `internal/agentnotify/setupwizard/wizard.go`, `cmd/claude-notifications/setup_products*`,
  existing Claude notifier/navigation, and OpenCode/Gemini event adapters.

Pi discovers direct `.ts`/`.js` files and extension subdirectories containing
`index.ts` or `index.js`. Its user directory defaults to `~/.pi/agent`; nonempty
`PI_CODING_AGENT_DIR` selects the agent directory itself. A complete owned
subdirectory avoids editing settings. Actual v1.4.0 standalone CLI discovery
skips dot directories in its package-manager resource path, although the lower
extension loader also accepts explicitly selected hidden paths. A running
session needs resource reload or restart to load a changed observer.

Pi v1.4.0 has an explicit `agent_settled` event after continuations finish.
`turn_end` and `agent_end` are insufficient completion boundaries. The latest
`agent_before_settle` outcome distinguishes completed, aborted and error;
`agent_settled.aborted` takes precedence. UI prompts have kinds but no native
prompt identifier. `hasUI` is true for RPC as well as TUI; `ctx.mode` is the
terminal eligibility signal.

Disposable Linux research with the verified official standalone confirms a
successful turn settles once, whereas HTTP 400 can exit zero and settle with
`aborted:false` while the boundary outcome is error. Retry and queued followup
each produce two provider requests/agent ends and one final settlement. RPC UI
prompts occur while idle with `hasUI:true`; abort can settle without a preceding
boundary outcome. These observations support the event rules below; permanent
candidate tests must reproduce them after both plan reviews.

## ADR: direct managed materialization and one bounded Pi namespace

Add a Pi adapter using the normal UAP directory transaction. Do not add a second
native copy/swap transaction, native intent protocol, namespace executor or shell
installer. It interprets `com.universal-agent-plugins.pi` in `plugin.json`:

```json
{
  "extensions": {
    "com.universal-agent-plugins.pi": {
      "version": 1,
      "source": "com.universal-agent-plugins.pi"
    }
  }
}
```

`TargetLayout` returns anchor `<physical-agent-dir>` and target root
`<physical-agent-dir>/extensions`; common planning chooses its existing
`<physicalArtifactID>` child. `StagingLayout` stages under the exact agent-dir
parent of the watched root, using the existing reserved staging prefix. Existing
dirswap journals own replacement, backup, quarantine, rollback and removal.

Version 1 has one package-relative `source` directory and exactly one real
`index.ts` or `index.js`. Require contained real directories/files; reject
traversal, absolute paths, links, conflicting root `index.ts`/`index.js`, reserved
projection files and root/nested `package.json` Pi entrypoint overrides. Generate
a root `index.ts` factory that checks its physical location against bound
`ActivePath` before dynamically importing the admitted source. Retained copies,
backup/quarantine or an explicitly selected hidden copy must do nothing, including
no source top-level side effects. Compare the real current location to the frozen
physical `ActivePath` literal; never re-resolve the expected path through a new
symlink. Actual Pi research passes active, retained-copy, explicit-hidden and
misbound/symlink cases with source top-level effects only in the admitted active
tree. This is research evidence; candidate tests must reproduce it.
UAP itself never executes extension code.
Unknown namespaces remain opaque and unsupported in every existing client.
Before writes, preflight also refuses foreign `extensions/index.ts`, `index.js`
or a watched-root `package.json` Pi entrypoint manifest that masks child discovery;
preserve the foreign bytes and report the conflict instead of installing inertly.

The current planner unconditionally emits `extension_semantics_not_established`.
Add only the read-only adapter capability needed to validate a specific namespace
and derive its component support. Both install planning and compatibility must
call the same Pi validator; a client-wide `ExtensionSupport: native` is not an
exemption for arbitrary namespaces. Keep namespace validation with the Pi client,
and leave the standard conformance decoder responsible only for standard object
validity. Record this ADR in UAP with the concrete contract and unsupported data.

## UAP changes and boundaries

Add `ClientPi`, display identity/aliases and a registry row in `domain/clients.go`;
user scope, native extension support, directory-only lifecycle and no
unqualified MCP/app/skill claims. Do not add project scope without evidence and
separate acceptance. Add `clients/pi/` to `clients/all/registry.go` and implement
the existing capabilities actually required:

- `HostDetector`, `ProfileResolver`, `VersionProbeEnvironment` and
  `ProfileBindingValidator`: resolve `pi` and physical agent directory, honor
  `PI_CODING_AGENT_DIR`, bind explicit authority and prevent fallback on invalid
  supplied roots. No credentials or model requests during discovery/versioning.
- `PlanPrecondition`, namespace qualification and `PlanRefiner`: admit the
  concrete extension schema, validate identity and report automatic installation
  with reload guidance. Do not equate installed bytes with runtime activation.
- `TargetLayout`, `StagingLayout`, `Projector` and `ActiveNativeProjector`: place
  the full managed package in native discovery, generate the guarded entrypoint
  and descriptor, and reconstruct desired ownership read-only.
- `RegistryInspector`, `ActivationPreflighter`, `AutomaticActivator`,
  `ReadOnlyVerifier` and `Lifecycle`: install/verify/remove only the exact
  managed package and digest; activation observes installed discovery bytes and
  requests reload, without another native write or live-runtime claim.

Retain `managed_package_directory` ownership and existing file-tree/directory
proofs. Declare Pi `LifecyclePrepared`, plus small explicit policies for owned
plugin data and read-only native-directory verification. Add those declarations
to the existing `packageNeedsPluginData` and `ShouldReadOnlyVerifyForScope`
decisions; only Pi opts in, only an admitted extension needs data. Do not invent
an MCP selection, reinterpret CLI registration or use `LifecycleNativeConfig` to
obtain verification. Existing domain/native intent and VS Code source stay intact.

Initial adoption refuses foreign or merely byte-identical unowned content.
Update must preserve user edits and other Pi extensions. Repair may restore a
missing owned extension but may not overwrite edits. Removal does not require
the Pi CLI to remain installed; it requires the persisted physical root and
ownership. Crash recovery uses the existing directory transaction proof, never
pathname/digest coincidence as new ownership. Test retained backup and quarantine
against actual Pi loading as well as directory recovery.

Add a typed Pi process-event source in `sdk/pi`, wired through `App.Pi()` and the
existing descriptor/registrar generator: one `OnObservation` handler, fixed
`pi-event` argv invocation and typed stdin JSON union for the two events below.
Keep this a small read-only notification
carrier; no tool execution, approval response, continuation or transcript API.
Generate registry/support outputs from descriptors rather than hand-editing
generated files. Do not claim the legacy authoring import/generate lane for Pi.

## Observer and runtime event contract

The canonical package ships one Pi source directory using type-only Pi imports
and Node built-ins. A reserved, digest-covered UAP descriptor carries schema and
bootstrap versions, bound `active_path`, `package_root`, `data_root` and canonical
source tree digest. It is generated in staging and encoded in the guarded
bootstrap, so a loaded observer captures it once rather than rereading changed
files. Final whole-projection digest cannot be embedded in itself. Notifications
resolves its binding and managed runtime from existing receipt/facts under these
bound roots; UAP has no product-specific executable or policy code. No HOME
guessing, external source links, shell text or arbitrary args enter this contract.

The v1 frame has `schema_version`, `event`, `observer_id`, `observation_id`,
`session_id`, `cwd`, `mode`, `pi_pid` and captured loaded-revision facts, plus one of:
`settled: {aborted, boundary_outcome?}` or `prompt: {kind}`. Event values are
`agent_settled` and `ui_prompt_start`. Observer and observation UUIDs are
explicitly synthetic. Session ID and cwd come from Pi's extension context; PID
comes from the Pi process. Never include prompt titles, text, responses, tool
arguments, transcript, tokens, provider error strings or user-provided targets.

The extension caches the latest `agent_before_settle` outcome for one activity,
without returning a continuation or otherwise changing Pi. Repeated agent starts
before a final settlement do not create duplicate activities. Completion requires
non-aborted settlement and a latest completed outcome; abort suppresses completion,
error maps to fixed `api_error`, and missing/inconsistent outcome fails closed.
`ui_prompt_start` maps to question/waiting-for-input, not permission approval.
End events cancel pending producer work using synthetic identities;
nested UI prompts use a bounded synthetic stack. Idle prompts are independent of
agent activity. Event handlers synchronously snapshot context/state and return
`undefined`; activity/prompt handlers never await a child. Pi awaits settlement
handlers, so an async transport handler would stall its final idle transition.

Invoke the real helper directly with asynchronous `execFile`, `shell:false`, no
detachment, inherited terminal environment, `cwd:ctx.cwd` and hidden Windows
console. This preserves the live Pi parent chain for terminal/multiplexer hints.
One observation gets one invocation and no resend. Track at most two children;
drop excess work with no queue. Child failures cannot throw into Pi or change its
result. Frame/stdout/stderr limits are 64 KiB. Go has a 15-second context budget
through all capture, policy and delivery work. A separate Pi budget leaves the
existing four-second shared observation contract unchanged. Producer lifetime is
bounded to 16 seconds: TERM by 15.5 seconds, 250 ms grace, then hard kill and one
join by the deadline. Prompt end uses the same cancellation/reaping path.
Shutdown with `reason:quit` in JSON/print/RPC (including RPC stdin close) is the
exception: the awaited handler drains only already admitted terminal completion/error
webhook children to their original deadlines, with no new work or budget reset;
TERM/KILL/join retain the same bound. Questions, TUI shutdown and every non-quit
reason (`reload`, `new`, `resume`, `fork`) cancel promptly. One-shot webhook drain
must not block TUI settlement or shutdown.
Context-aware command runners must terminate/reap their descendants on all OSs.
Never retry or choose another desktop backend after a possible handoff, and never
claim cancellation retracts a notification or audio already delivered.

At initialization, the admitted observer resolves the existing authoritative
binding receipt and captures its final projected-artifact digest, canonical
digest/bootstrap identity, binding ID, runtime root and primary executable
SHA-256. The descriptor fences which package that receipt can describe; no final
digest is embedded in its own projection. Every frame carries this captured
revision, not newly read values. Under the existing lease, Go validates it against
the current sealed UAP binding and current runtime before policy and immediately
before sending. Changed projection/runtime, disable or removal denies old loaded
observers until reload; an old observer cannot upgrade itself by rereading facts.
Do not add generation to immutable `portable.Binding` or alter other integrations.

Go also validates bounded strict JSON, event-specific shape and canonical
identity. A typed facts adapter derives fixed Pi
presentation, completion/question/error semantics and native session/cwd. Use
the existing `observation.RecentCache` for per-channel claims keyed by binding,
observer and observation identity.

Reuse `notifier.New(effectivePiConfig).SendDesktop` behavior for sound and terminal
focus, adding a narrow `SendDesktopContext` entrypoint while retaining
the existing wrapper for existing agents. Existing OpenCode/Gemini delivery
explicitly disables sound/navigation and is not a Pi implementation template.
Cancellation must reach terminal capture helpers, multiplexer/Terminator probes,
Linux daemon startup/ping/send and macOS helper launch, not merely wrap a blocking
top-level call. Resolve native assets from the validated managed-runtime receipt;
ambient `CLAUDE_PLUGIN_ROOT` must not redirect Pi to an unrelated helper.
The desktop policy admits only `mode:tui` with a known local terminal. Presence of
`SSH_CONNECTION`, `SSH_CLIENT` or `SSH_TTY` means remote; unknown locality and
RPC/JSON/print deny desktop. Do not add unqualified opt-in overrides. Explicitly
enabled webhooks independently admit native events in all four modes and remote
contexts. Configuration uses established per-agent layering and defaults;
Desktop/Webhook consent remains the existing setup choice. A backend without a
structured OS acceptance receipt cannot claim one.

## Notifications composition and product surface

Add Pi to the portable integration vocabulary, setup product order/labels,
captured environment/scopes, frozen intent validation, discovery, setup summary
and config per-agent schema. Introduce explicit Pi agent-dir/executable options
where the existing product interface needs them. Freeze native qualification to
the official standalone v1.4.0 distribution. On Windows accept its real `pi.exe`;
an npm `pi.cmd` shim is not a directly executable native binary and is refused
with explicit standalone/executable guidance. Adding npm launcher support needs
its own native parity evidence rather than silent shell fallback.

Compose `clients/pi` explicitly in the Notifications materializer and installer
engine. Extend its existing installation/binding/runtime handoff and policy
revoke paths for Pi. The historical compatibility facade may re-export the
contract; it must not acquire an independent Pi implementation. There is no
standalone Pi installer, custom state store, shell copy command or second
update/remove path. Preserve existing clients' configuration and fixtures.

README, landing agent list/cards, support matrix and Pi guide must state the
implemented scope, setup command, event meanings, sound configuration and
terminal-click behavior. Add a Pi asset with appropriate provenance. Update
supported-agent counts and existing documentation tests. Avoid unreleased
disclaimers, release publication, and unrelated wording cleanup.

## Implementation sequence and dependency order

1. Commit/push this concrete plan and open the Notifications draft PR early.
   Complete critic round 1, revise, then independent critic round 2 and revise.
   Record every finding and resolution before implementation.
2. After round 2 approval, run native GUI/audio/terminal preflight first on the
   actual available runners. Record ready/unqualified prerequisites before product
   effects; continue unaffected implementation if a lane is unavailable, but keep
   final acceptance and merge blocked until genuine three-OS evidence exists.
   Open a linked UAP draft PR; implement the bounded Pi namespace/client adapter
   and SDK carrier. Push reviewable stages. Run native lifecycle/SDK gates and
   independent code review, fix findings, and merge UAP first.
3. Pin Notifications integrationctl/agentplugins and SDK to the exact merged UAP
   commit through normal Go dependencies. A temporary local replace may support
   development only; no local replace or unmerged dependency ships. Check all
   affected nested module pins/sums and the external consumer build.
4. Implement observer, typed facts, runtime policy, existing notifier reuse and
   setup/installer composition. Push stages and attach test evidence to draft PR.
5. Implement and execute native Pi and desktop qualification, update docs and
   landing, perform code review, fix all issues, rerun affected required checks,
   and merge the Notifications PR after its exact candidate is green.

## Gates and evidence

UAP: unit/contract tests cover client detection, profile override/canonicalization,
namespace version/schema, unknown namespaces unchanged, malicious paths/symlinks,
foreign/unowned collision, projection/reconstruction parity, cancellation,
read-only verify, idempotence, replacement, missing repair, edited refusal,
confirmed removal and directory transaction recovery. Inject failures before
swap, after backup, after publish, at receipt commit and during rollback/cleanup;
retry/recovery must not delete foreign content. Run affected module suites,
architecture/conformance guards, descriptor generation checks, and public SDK
consumer tests; compile for supported OS/architectures.

Notifications: strict frame tests cover duplicates/trailing JSON, wrong event
shape, invalid modes/outcomes/IDs, private-data exclusion, per-channel dedup,
revoked/stale bindings and effective Pi config. Observer tests cover repeated
starts, successful/error/abort/retry/queued continuation, nested prompts, prompt
end/shutdown cancellation/drain, child error/hang and terminal environment ancestry.
Setup/installer tests cover frozen consent, no write before confirmation,
mixed-agent isolation, update/repair/remove and retained runtime compatibility.
Test `SendDesktopContext` cancellation and legacy-wrapper behavior, preserving
existing agents' rendering/sound/navigation. Run the affected Go and JS suites,
required repository gates, race tests where concurrency changes, landing build
and documentation checks.

Baseline before implementation: Notifications config/notifier/daemon/hooks and
UAP planner/Gemini suites pass. Existing Claude unreadable-directory tests fail
when root bypasses chmod restrictions; rerun as an unprivileged user, do not fix
unrelated Claude source. Some filesystem/socket checks require the proper sandbox.

PR native process lanes use `CI_LINUX_RUNNER` (currently `ubicloud-standard-4`),
`CI_MACOS_15_RUNNER` (currently `namespace-profile-macos-15`) and Windows 2025;
fallbacks are Ubuntu 24.04/macOS 15 only when the variables are absent. Existing
runner names do not prove GUI/audio readiness. Pin official standalone Pi v1.4.0
archives by OS/arch SHA-256 and verify version. Build the release-equivalent
sealed product bundle with `-trimpath`, exact checkout and linked UAP identities,
installed sounds and actual native helpers. macOS uses the pinned verified signed
helper. Windows builds the native callback with `windows_callback_asset`, verifies
the embedded asset and includes the `-H=windowsgui` focus companion. The installer
must consume this receipt-bound bundle; a main-only test binary is insufficient.

Use isolated TEST roots, an environment allowlist and fresh project, without real
credentials. A loopback model fixture drives genuine Pi with
`--offline --no-approve --no-mcp --no-tools`. Product extension loading is automatic,
never `-e`; explicit `-e` is allowed only for the guarded-copy negative test.
Default terminal behavior runs in a real PTY/ConPTY TUI on every OS; JSON/RPC
fixtures independently prove zero desktop and explicitly configured webhooks.
Record provider requests, native events and production delivery against this oracle:

| Case | Requests / agent ends / settled | Required result |
| --- | --- | --- |
| Success | 1 / 1 / 1 | One completion |
| Retry or queued followup | 2 / 2 / 1 | One final completion |
| HTTP 400 | Native error; exit may be 0 | No completion despite non-aborted settlement |
| Abort | Boundary outcome may be absent | Aborted settlement; no completion |
| Idle RPC UI | 0 / 0 / 0 | Question webhook only when enabled; zero desktop |

Keep Pi A alive across real replacement update. Its next native event must be
denied on captured revision mismatch. Fresh/reloaded Pi B must deliver once.
Exercise idempotent reinstall, missing-extension repair followed by fresh Pi,
and CLI-missing removal. Both still-loaded observers and a fresh Pi after removal
must deliver nothing while provider work and a foreign witness extension still
succeed. Preserve foreign extensions/settings throughout. Run a real hung-helper
case: Pi's settlement remains prompt, two-child bound holds, timeout/cancel reaps
the helper and descendants, and there is no resend. Actual JSON/print normal quit
and RPC stdin-close tests assert one authorized terminal webhook survives shutdown;
a hung child drains only to its original deadline. Also prove reload/reset,
questions and TUI shutdown cancel promptly. Cross-compilation is not E2E.

Native desktop qualification is blocking and separate from process-only lanes:

| OS | Preflight and independent observations |
| --- | --- |
| Linux | Private D-Bus, Xvfb/Openbox, dunst, two nonce-identified xterms; PulseAudio null sink/monitor and ALSA-to-Pulse output. A known cue validates capture, real dunst mouse click plus active window ID/Pi PID/TTY proves focus. |
| macOS | Unlocked WindowServer/user session, signed-helper notification permission, selected observer's Accessibility/screen permissions; two Terminal windows/TTYs; ScreenCaptureKit system-audio capture, real notification click and foreground window observation. |
| Windows | Connected interactive WTS/input desktop on Windows Client, Shell/Toast Center/UI Automation, default render endpoint and WASAPI loopback; two terminal HWND/PID pairs, real toast click and foreground HWND. Hosted Server 2025 alone cannot qualify this lane. |

Preflight proves observers with known fixtures before product effects. Production
tests use both completion and a still-pending TUI question: move focus away,
observe the actual banner/configured digital audio, click it, and verify the
originating terminal. Idle RPC questions do not substitute for this. Cover sound-off,
desktop-off, focus-off, wrong-window isolation and available multiplexer behavior.
Record source/Pi/bundle hashes, OS/session, terminal, process identity, screenshot,
audio monitor and click/foreground evidence. Audio capture proves output, not
physical audibility. An aggregate required job accepts only three desktop passes
for the exact candidate SHA/bundle, rejecting missing, skipped, not-run or
unqualified receipts. Headless/locked/unavailable environments cannot pass. No
three-OS desktop environment is claimed available before the preflight executes.

## Critic findings and resolutions

| Finding | Revision 2 decision |
| --- | --- |
| R1-RUNTIME1: extra native copy is unrecoverable | Direct TargetLayout, normal directory journal, guarded bootstrap; no native intent protocol. |
| R1-RUNTIME2: descriptor/data demand are incomplete | Reserved immutable roots/canonical descriptor, existing receipt resolver, explicit owned-data/verification policy, final digest captured after sealing. |
| R1-RUNTIME3: deadlines/contexts are provisional | Mandatory context API through all subprocess/handoff paths; fixed 15/16-second bounds, grace/KILL/join, no fallback after possible handoff. |
| R1-RUNTIME4: modes and old observer are ambiguous | Local TUI-only desktop, independent opted-in webhook; captured projected/runtime revision mismatch denies old code without global Binding generation changes. |
| R1-RUNTIME5: callbacks can stall Pi | Synchronous snapshots/return, activity and prompt state separation, at most two children, cancellation and no queue/resend. |
| R1-E2E1: desktop mechanisms/gate absent | Concrete preflight/observer matrix and exact-SHA aggregate; unavailable lanes remain blockers. |
| R1-E2E2: main-only build is insufficient | Sealed release-equivalent bundle, verified signed macOS helper, embedded Windows callback/focus companion and installed sounds. |
| R1-E2E3: lifecycle misses cached old code | Pi A denied after update; fresh B accepted; repair, CLI-missing remove and foreign-witness negative checks. |
| R1-E2E4: non-TUI fixtures miss normal behavior | Real PTY/ConPTY TUI plus explicit non-TUI desktop-denial/webhook tests. |
| R1-E2E5: insufficient native oracle | Provider/event/delivery count table includes retry/followup, error exit zero, abort without boundary and idle RPC UI. |
| R1-E2E6: hung sender can block settlement | Real native hung-helper case asserts prompt settlement, admission bound and reaped child tree. |
| R1-E2E7: Windows launcher parity is unproven | Official standalone-only floor; refuse npm shims until separately tested. |
| R2-E2E1: normal quit cancels one-shot webhooks | Await only bounded normal-quit drain of already admitted terminal webhook children; preserve original deadlines and prompt TUI/reload/question cancellation. Actual Pi drain research passes; candidate JSON/RPC and hung-child cases are required. |
| R2-E2E2: question UI and watched-root masking need explicit coverage | Qualify actual pending TUI question banner/audio/click; preflight refuses masking foreign root entrypoints/manifests without modifying them. |

Both round 2 critics must verify this targeted correction before code begins.
Actual desktop/audio runner readiness and candidate qualification remain execution
gates, not assumptions resolved by this document. Record any material adjustment
and critic disposition before broadening the implementation scope.

[pi-loader]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/src/core/extensions/loader.ts
[pi-config]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/src/config.ts
[pi-types]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/src/core/extensions/types.ts
[pi-session]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/src/core/agent-session.ts
[pi-guide]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/docs/extensions.md
