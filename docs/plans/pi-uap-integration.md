# Pi notifications through Universal Agent Plugins

Status: initial plan for two independent critic rounds; implementation has not
started. Owner scope: full Pi support on macOS, Linux and Windows, including
configured sound and a notification click returning to the originating terminal.
All implementation and review workers use GPT-6.1 Sol with xhigh reasoning.

## Outcome and scope

Pi becomes a selectable product in the existing Agent Notifications setup and
receives a native observer extension through the Universal Agent Plugins (UAP)
engine. Install, update, repair, inspection and removal use that engine's source
snapshots, binding identity, ownership, journals and runtime handoff. Normal Pi
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

Pi auto-discovers direct `.ts`/`.js` files and extension subdirectories containing
`index.ts` or `index.js`. Its user directory defaults to `~/.pi/agent`; nonempty
`PI_CODING_AGENT_DIR` selects the agent directory itself. A complete owned
subdirectory therefore avoids editing Pi settings and avoids a new arbitrary
file-deletion API. A running session needs Pi's resource reload or restart.

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

## ADR: a concrete Pi projection, with one bounded namespace

Add a Pi native-config adapter to UAP. Do not build a general namespace executor
or shell-based installer. The adapter interprets one documented namespace,
provisionally `com.universal-agent-plugins.pi`, in a standard `plugin.json`:

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

Version 1 installs one selected extension directory at
`<physical-pi-agent-dir>/extensions/<declared-plugin-name>/`. `source` is a
portable package-relative directory with a real `index.ts` or `index.js`.
Require contained real directories/files; reject traversal, absolute paths,
links, reserved projection paths, ambiguous entrypoints and a nested Pi
`package.json` manifest that could override the admitted entrypoint. The package
name supplies destination identity; namespace data cannot select another
profile or executable. UAP copies and verifies code but does not execute it.
Unknown namespaces remain opaque and unsupported in every existing client.

The current planner unconditionally emits `extension_semantics_not_established`.
Add only the read-only adapter capability needed to validate a specific namespace
and derive its component support. Both install planning and compatibility must
call the same Pi validator; a client-wide `ExtensionSupport: native` is not an
exemption for arbitrary namespaces. Keep namespace validation with the Pi client,
and leave the standard conformance decoder responsible only for standard object
validity. Record this ADR in UAP with the concrete contract and unsupported data.

## UAP changes and boundaries

Add `ClientPi`, display identity/aliases and a registry row in `domain/clients.go`;
user scope, native extension support, automatic native-config lifecycle and no
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
- `Projector` and `ActiveNativeProjector`: produce and reconstruct the same
  selected directory ownership from a sealed package; reserved descriptors are
  digest-covered, and active reconstruction is read-only.
- `RegistryInspector`, `ActivationPreflighter`, `AutomaticActivator`,
  `ReadOnlyVerifier` and `Lifecycle`: install/verify/remove only the exact
  directory and digest recorded for this binding and physical profile.

Use `NativeObjectOwnership` with a Pi directory kind, object ID, logical plugin
name, exact destination, source-relative path and managed digest. Use existing
path policy, file-tree copy/digest, `shared.RenameDirectoryExclusive`, native
effect states and `shared.CompleteNativeConfigActivation`. Common UAP owns the
outer operation journal and runtime handoff. Pi owns the bounded directory swap,
backup and exact rollback. The project-components helper cannot be reused
verbatim: it assumes skill/MCP pairs and a managed package inside its project
root; do not weaken those invariants merely to fit Pi.

Initial adoption must refuse foreign or merely byte-identical unowned content.
Update must preserve user edits and other Pi extensions. Repair may restore a
missing owned extension but may not overwrite edits. Removal does not require
the Pi CLI to remain installed; it requires the persisted physical root and
ownership. A crash after a possible native write must retain uncertain evidence
and require exact recovery, never infer ownership from a matching pathname.

Add a typed Pi process-event source in `sdk/pi`, wired through `App.Pi()` and the
existing descriptor/registrar generator. Keep this a small read-only notification
carrier; no tool execution, approval response, continuation or transcript API.
Generate registry/support outputs from descriptors rather than hand-editing
generated files. Do not claim the legacy authoring import/generate lane for Pi.

## Observer and runtime event contract

The canonical Notifications package ships one Pi extension directory, using
type-only Pi imports and Node built-ins. A digest-covered reserved UAP descriptor
binds the extension to the committed package and plugin-data roots. Notifications
resolves its executable and binding locator through its existing receipt/facts
contract; the generic Pi provider has no Notifications-specific executable or
policy knowledge. Descriptor materialization occurs only in the owned staging
tree. It cannot contain shell text or arbitrary args. Freeze the exact resolver
before implementation; copied extension code must not guess HOME or follow
source-tree links to find a runtime.

The provisional v1 frame has `schema_version`, `event`, `observer_id`,
`observation_id`, `session_id`, `cwd`, `mode` and `pi_pid`, plus exactly one of:
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
End events and shutdown cancel pending producer work using synthetic identities;
nested UI prompts must be matched without inventing native prompt IDs.

Invoke the real helper directly with asynchronous `execFile`, `shell:false`, no
detachment, inherited terminal environment, `cwd:ctx.cwd` and hidden Windows
console. This preserves the live Pi parent chain for terminal/multiplexer hints.
One observation gets one invocation and no automatic resend. Child failures do
not throw into Pi or change its result. Frame/stdout/stderr limits are 64 KiB.
Provisional budgets: Go delivery context at most 15 seconds and producer timeout
16 seconds, including cancellation/reaping; critics must validate latency and
whether these limits are sufficient for the existing OS focus handoffs.
The existing shared observation budget is four seconds; a Pi budget must be a
separate bounded helper and must not weaken that contract. Alternatively freeze
four/five seconds for Pi if the native handoffs qualify within those limits.

Go validates bounded strict JSON, event-specific shape, canonical identity and
binding generation before policy. A typed facts adapter derives fixed Pi
presentation, completion/question/error semantics and native session/cwd. Use
the existing `observation.RecentCache` for per-channel claims keyed by binding,
observer and observation identity. Recheck live generation/policy at the actual
send boundary so disable, remove or retained-runtime changes revoke old writers.

Reuse `notifier.New(effectivePiConfig).SendDesktop` behavior for sound and terminal
focus, adding a narrow `SendDesktopContext` entrypoint if required while retaining
the existing wrapper for existing agents. Existing OpenCode/Gemini delivery
explicitly disables sound/navigation and is not a Pi implementation template.
Cancellation must reach terminal capture helpers, multiplexer/Terminator probes,
Linux daemon startup/ping/send and macOS helper launch, not merely wrap a blocking
top-level call. Resolve native assets from the validated managed-runtime receipt;
ambient `CLAUDE_PLUGIN_ROOT` must not redirect Pi to an unrelated helper.
Pi desktop is TUI-only by default; RPC/JSON/print and remote contexts need explicit
policy and independent webhook eligibility. Configuration uses the established
per-agent layering and defaults. Webhook opt-in and Desktop opt-in remain the
existing setup choices. Do not claim an OS acceptance receipt the backend lacks.

## Notifications composition and product surface

Add Pi to the portable integration vocabulary, setup product order/labels,
captured environment/scopes, frozen intent validation, discovery, setup summary
and config per-agent schema. Introduce explicit Pi agent-dir/executable options
where the existing product interface needs them. Resolve Windows `pi.cmd` or
native executables through the actual supported distribution path and never
treat a command shim as a directly executable native binary without proof.

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
2. Open a linked UAP draft PR; implement the bounded Pi namespace/client adapter
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
confirmed removal and uncertain native write recovery. Inject failures before
swap, after backup, after publish, at receipt commit and during rollback/cleanup;
retry/recovery must not delete foreign content. Run affected module suites,
architecture/conformance guards, descriptor generation checks, and public SDK
consumer tests; compile for supported OS/architectures.

Notifications: strict frame tests cover duplicates/trailing JSON, wrong event
shape, invalid modes/outcomes/IDs, private-data exclusion, per-channel dedup,
revoked/stale bindings and effective Pi config. Observer tests cover repeated
starts, successful/error/abort/retry/queued continuation, nested prompts, prompt
end/shutdown cancellation, child error/hang and terminal environment ancestry.
Setup/installer tests cover frozen consent, no write before confirmation,
mixed-agent isolation, update/repair/remove and retained runtime compatibility.
Test `SendDesktopContext` cancellation and legacy-wrapper behavior, preserving
existing agents' rendering/sound/navigation. Run the affected Go and JS suites,
required repository gates, race tests where concurrency changes, landing build
and documentation checks.

Add a PR-triggered native Pi workflow on Ubuntu 24.04, macOS 15 and Windows 2025.
Pin official standalone Pi v1.4.0 archives by OS/arch SHA-256 and check version.
Build the candidate real product binary with `-trimpath` and record checkout and
linked UAP identities. Use isolated TEST roots, environment allowlist and a fresh
project; no real credentials. A loopback model fixture drives genuine Pi with
`--offline --no-approve --no-mcp --no-tools`; the installed extension must be
auto-discovered, never injected with `-e`. Record native event observations,
fixture requests and production webhook delivery. Exercise successful completion,
retry, terminal error, abort, queued continuation and UI prompt semantics, then
idempotent reinstall, real binary replacement update, repair and removal. A final
native turn after removal must deliver nothing, preserving foreign extensions
and settings throughout. Cross-compilation is not native execution evidence.

Desktop qualification is a separate blocking acceptance gate on each OS. Use two
real terminal windows/sessions and real Pi; move focus away, produce the native
event, observe the production banner and configured audio output, click the real
notification and verify the originating session/window becomes foreground.
Record product/Pi versions, OS/session, terminal, process/session identity, audio
capture and click/foreground evidence. Cover completion/question defaults,
sound-off, desktop-off, focus-off, wrong-window isolation, and available
multiplexer behavior. Existing Linux D-Bus receipt, Windows disposable callback
test and macOS intercepted sound arguments do not prove these requirements.
Headless/locked/unavailable desktop or audio must report unqualified, never pass.

## Open decisions for critic round 1

- Confirm the one-source-directory namespace shape/name and smallest shared
  read-only namespace-admission hook; prevent generic execution or scope growth.
- Specify exact crash-recovery ownership across Pi directory publish and the UAP
  receipt; test recoverable uncertainty before choosing implementation details.
- Freeze the reserved UAP package/data descriptor and Notifications receipt
  resolver so generic projection does not acquire product-specific runtime code.
- Freeze SDK frame/handler names, prompt matching/cancellation, mode policy and
  deadline after reviewing actual native ordering and existing notifier latency.
- Establish usable macOS/Linux/Windows desktop/audio/terminal environments and
  their evidence runner before claiming sound and click-to-focus completion.
- Check standalone Windows Pi versus npm shims and the supported distribution
  floor. Native tests must use the same command path users will install.

[pi-loader]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/src/core/extensions/loader.ts
[pi-config]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/src/config.ts
[pi-types]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/src/core/extensions/types.ts
[pi-session]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/src/core/agent-session.ts
[pi-guide]: https://github.com/earendil-works/pi/blob/4a080252f06caf63a525d7873c5d94d3c23887e0/packages/coding-agent/docs/extensions.md
