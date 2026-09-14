# Explicit setup core

`Apply(ctx, Options, Request)` is callable by the existing installer after it has
installed and qualified the managed runtime. It does not register a client. The
caller supplies the existing owner, runtime path, consumer, expected generation,
explicit intent, and operator-selected route/consent. The application verification
callback is mandatory for a local route: the installer must supply an offline
identity verifier which does not launch the app or request OS permission. There
is no default that trusts a path or a caller's asserted team ID.

`Enabled: nil` preserves intent; it never turns a disabled repair into opt-in.
An absent route or rate member preserves the corresponding existing value.
Missing individual rates use the runtime reader's 6/30/3 defaults. Supplied zero,
null or malformed rates are invalid. A supplied route replaces its known consent
and identity fields; both caller-consent flags default to false. Unknown policy
members, including nested route/rate members, remain intact. Enable requires a
chosen verified local application; disable is a separate operation that needs no
app, native availability, journal, global config, or supported OS.

The initial supported OS is macOS. `Platform` and `JournalClock` are trusted hosted
test seams, not notification payload fields. `ControlRoot` defaults to
`os.UserConfigDir()/agent-notifications`; journal and spool locations are fixed
under it at `state/journal` and `state/native-spool`. The default global config is
`~/.claude/claude-notifications-go/config.json`. Constructor path overrides belong
to trusted installer composition only. State parents must be physical private
owned directories. Setup rejects links, unsafe ownership/modes/types, hardlinked
private files and pre-existing unowned state; it never adopts or chmods them.

Setup reads the canonical global config strictly before creating setup state or
locks. It revalidates under the canonical config lock before activation, preserves
all global opt-outs, and never writes global config. Missing config returns
`configuration_required`: preparing it belongs to a later explicit existing
installer action. Success means policy configured, not permission granted or a
notification delivered; global desktop/sound/focus opt-outs remain effective.

Initialization ownership is the `setupState` object in the kernel-owned explicit
policy at `control/agent-notifications.json`, durably outside journal/state/cache.
It contains schema, installation ID, random token, directory identity, phase and
namespace. This is setup metadata ignored by the runtime policy reader, not a
second routing policy or a journal schema extension.

The first invocation creates an empty private `.setup-state` staging directory,
then commits its `started` ownership marker through the ordinary generation/CAS
kernel transaction while intent remains disabled. Only that uninterrupted
invocation may call `journal.Initialize`. It provisions the private spool and its
permanent `.spool.lock` without invoking native. Once a real journal Open succeeds,
a second kernel transaction records `ready` and its exact namespace. A no-clobber
same-filesystem rename publishes the stage as `state`, then the final kernel
transaction binds route, rates and intent to the same new generation.

Retries with a marker use `journal.Open` only. A complete interrupted journal can
finish provisioning without changing namespace, history, counters or spool lock.
An incomplete first journal, a lost expected journal or lock, a mismatched
namespace, or ambiguous pre-marker staging returns
`initialization_recovery_required` and preserves evidence. The `started` marker
and retained stage identify the interrupted attempt; this slice deliberately has
no reset or destructive recovery command. Loss of both the ownership policy and
all state is outside what any remaining marker can prove.

Initial provisioning may commit ownership generations before activation.
`Result.Generation` reports the latest successfully committed generation even
when provisioning returns an error. After an error the adapter should refresh
managed status; `Result.Enabled` is meaningful on success. Final transaction I/O
errors use the kernel's existing recovery fence and recovery protocol. Setup
requests use the kernel's restricted `PolicyOnly` mode: they require existing
component/policy lock inodes and refuse all pending installer recovery. An
unrelated pending transaction cannot promote native or rewrite hooks through
setup, including disable; its owning installer must perform that recovery. No setup
code writes authoritative policy directly. Pure disable still checks the writer
floor, registered runtime, expected generation and policy preimage; its exemption
only removes native availability checks.

Lock order is setup serialization, then separate journal work, then component
and config transactions. Journal locks are released before component/config
locks. The final kernel preparation checks the ready state structure without a
journal lock and revalidates global configuration. Manual policy edits are fenced
by an exact byte preimage as well as generation. `PolicySnapshot.Preimage` is
bound to the same bytes parsed into its fields by the kernel CAS reader; it is
never reconstructed with an independent later fingerprint. An interrupted first
setup with inconsistent enabled intent must be explicitly disabled before its
ownership metadata can advance, so metadata repair cannot activate an unpublished
journal. Native delivery and runtime
status retain their existing read-only initialization behavior.

`Error.Reason` is intended for a later setup UI. Common actionable reasons are
`configuration_required`, `configuration_invalid`, `route_required`,
`invalid_route`, `application_verification_required`,
`application_identity_invalid`, `qualified_runtime_required`,
`newer_installer_required`, `generation_changed`, `initialization_interrupted`,
`initialization_recovery_required`, and `policy_commit_failed` (with the kernel's
specific cause). A caller must not interpret an error as permission to reset state.

Hosted tests use real kernel/journal operations, temporary roots, a fake clock,
an inert retained native fixture and an explicit qualification stand-in. They do
not establish macOS signing, permission, app routing, native dispatch, callback,
or GUI behavior. Client registration, installer opt-in UI/activation wiring and
native macOS E2E remain outside this core.
