# Explicit notification runtime

`New(Options) (*Backend, error)` creates one process-lifetime service and its
existing two-submit limiter. `Backend.Notify(ctx, payload, origin, deadline)`
implements the transport Backend contract. `Backend.Clock()` supplies the native
boot-continuous clock for capturing the transport's original <=15-second budget.
`Backend.Status(ctx)` returns read-only configuration, explicit intent, desktop
opt-out, offline capability and `permission: not_checked`; it does not promise OS
permission or delivery. `Backend.Close(ctx)` refuses future calls and drains all
in-flight calls, including replay lookups; repeat calls can finish a timed-out
close. No resource is opened at construction and no background worker is owned.

Main wiring must create one Backend, pass that same backend and clock to CLI/MCP,
and call Close after the MCP SDK transport has drained. Translate runtime Status
to each adapter's presentation schema there. Runtime has no SDK dependency and
accepts no path fields in notification payloads.

## Stable setup contract

Production defaults:

- Control: `os.UserConfigDir()/agent-notifications`.
- Explicit intent/routes/rates: `control/agent-notifications.json` (kernel owned).
- Durable journal: `control/state/journal`.
- Private native spool: `control/state/native-spool`.
- Global desktop policy: `~/.claude/claude-notifications-go/config.json`.

All four roots/file locations are constructor overrides for trusted process
composition and private-root tests. Explicit policy location is overridden through
ControlRoot because the kernel owns that location and its locks. No notification
caller may select them. Paths must be absolute. Setup must provision private
state parents, the journal directory and initialize the journal exactly once;
provision the native spool and its existing lock using the notifier setup contract.
This package never creates, repairs, migrates, initializes or collects these roots.
Notify uses `journal.Open` only, keeping the returned namespace for lookup,
admission and outcome finalization. Missing/corrupt state fails closed.

Example authoritative explicit policy (schema version 1):

```json
{
  "schemaVersion": 1,
  "enabled": true,
  "route": {
    "localRouting": true,
    "allowUnknownCaller": false,
    "allowCallerAsserted": false,
    "applicationPath": "/Applications/Codex.app",
    "teamID": "OPERATOR_VERIFIED_TEAM_ID"
  },
  "rates": {"sessionPerMinute": 6, "runtimePerMinute": 30, "burst": 3}
}
```

Missing explicit policy means disabled; present policy requires version 1 and a
boolean enabled. Omitted route booleans are false; unknown and caller-asserted
contexts are never opted in implicitly. Omitted individual rates merge with
6/30/3 before journal Normalize validates them. Zero/null/malformed supplied rates
are invalid. Setup must use kernel transactions for generation/intent changes.
Intent alone does not establish installed eligibility.

The global file must contain these three present booleans when explicit intent
is enabled; unrelated config fields are accepted without rewriting:

```json
{"notifications":{"desktop":{"enabled":true,"sound":true,"clickToFocus":true}}}
```

Missing global config yields `configuration_required`; unreadable, malformed,
duplicate-key, null or missing-boolean config yields `configuration_invalid`.
There is no legacy loader or enabled fallback. Desktop false suppresses; sound
false makes delivery silent; clickToFocus false prevents target construction.
Global config is bounded to 64 KiB and must be a regular non-symlink file.

Each fresh request reads `installruntime.ReadPolicySnapshot` once, after replay
lookup, then reads global config once. Captured fields and Installation remain
request-local. Readiness and delivery share one StructuredDelivery and the exact
ManagedInstallation Expected snapshot. Readiness releases the kernel lease before
journal admission; delivery reacquires the same expected generation. A generation
change/disable cannot be refreshed into a handoff. Replay loads no policy/global
config and reconstructs no target.

The production clock bridges `notifier.SystemBootClock`; unavailable platforms
return a no-effect `unsupported_platform` receipt. Journal retention uses the
separate `journal.PlatformClock` seam. Status is bounded to one second, only reads
kernel configuration/installed fingerprints and global configuration, and never
opens journal history/spool, probes permission, launches native, or cleans files.

Tests use actual private filesystem roots, the real Service and durable journal,
injected clocks and fake OS delivery. A real kernel transaction fixture verifies
both disable and generation update between released readiness and delivery.
Actual native/GUI execution and external E2E are outside this bounded composition.
Remaining work belongs to main/setup/CLI/MCP wiring; no such source is changed here.
