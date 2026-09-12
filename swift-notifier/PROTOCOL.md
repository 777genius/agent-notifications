# Native protocol v1 (PR1 + PR3, inactive by default)

This is the native delivery/action lane. No installer, hook or feature enablement
is changed. Structured actions support `none` and `desktop_thread_v1`; legacy CLI
and persisted ClickAction activate/execute/combined data retain their existing
codec and execution meaning. Structured input cannot manufacture shell actions.

`--capabilities-json` must be the entire argv. It emits one JSON object plus LF,
without creating NSApplication, UNUserNotificationCenter, a delegate, permission
probe, sound or callback executor. It declares versions `[1]`, actions `["none", "desktop_thread_v1"]`,
receipt support and backend `macos.usernotifications`; this is protocol support,
not authorization or a claim that a banner will appear. Future caller must verify
the offline managed artifact fingerprint/protocol floor first, then run a private
direct probe with a one-second deadline. Never probe an unknown/old binary.

Structured invocation (through LaunchServices, fresh instance):

```
--send-json --request-file /private/.../<correlation UUID>/<nonce UUID>.request --receipt-file /private/.../<correlation UUID>/<nonce UUID>.receipt -launchedViaLaunchServices
```

Paths must be absolute and free of symlink components (on macOS canonicalize
`/tmp` to `/private/tmp`, `/var` to `/private/var`). Resolve the trusted spool
root using `realpath` / Go `filepath.EvalSymlinks` before creating the attempt;
Foundation `resolvingSymlinksInPath` can retain the `/var` alias. The native
reader still rejects symlink components instead of resolving request paths.
The caller exclusively owns a fresh 0700 directory and
0600 regular request file, both owned by effective UID. Names are never reused.
Files are bounded to 16 KiB before allocation and decoding, locked nonblocking,
read through pinned directory descriptors, and consumed once before any effect.
An exclusive empty `<nonce>.claim` marker gives only one native instance receipt
ownership, including after body consumption. A competing or abandoned claim
exits without publishing a false rejection while another instance may submit.
The marker stays with the attempt for caller cleanup; it is not a replay journal.
Symlinks, hardlinks, special files, permissive modes and existing receipts fail
closed. File ownership isolates other users; it does not authenticate a caller
against malicious same-UID processes. The copied body is removed after reading;
invalid oversized/unowned files are left to the caller, never submitted.

Required request keys: schemaVersion (integer 1), correlationID (directory UUID),
nonce (filename UUID), bootID (`kern.bootsessionuuid`), notAfter (seconds from
`mach_continuous_time` converted with mach_timebase_info), title, body, category
(`info|attention|progress`), action (`"none"` or the typed object below), silent (boolean). Optional subtitle
is a single-line string or null. No unknown or duplicate keys, invalid UTF-8 or
unpaired surrogate escapes. UTF-8 limits: title/subtitle 256 bytes, body 4096;
Unicode control scalars (general category Cc, including NUL/ESC) are rejected
except LF/TAB in body. Format scalars (Cf), including ZWJ/ZWNJ and emoji tags,
are preserved without stripping or normalization. The Go producer must use
the same policy (`unicode.IsControl`, not the broader Cf category).
Text is never truncated.
Title/subtitle reject Unicode line separators as well. All text is literal.
Category does not enable time-sensitive interruption.

The sender inherits the original admission deadline, including validation,
locks, launches and sleep, with at most 15 seconds remaining. Boot mismatch,
expiry or a larger budget rejects before permission lookup. Permission is read
without requesting authorization; undetermined means activation_required, denied
means permission_denied. Deadline is rechecked after lookup and completion. A
serialized owner ignores repeated/late callbacks. Only positive UN `add`
completion yields submitted; OS error yields rejected, deadline after handoff
unknown. Timeout before handoff is rejected/expired. No retry, fallback or bell.

Receipt keys: schemaVersion=1, correlationID, nonce, notificationID (same as
correlationID), status, reason, retrySafe=false. Atomic exclusive publication
never overwrites another receipt or recreates a removed directory. Receipt has
no title/body. Structured send stdout is empty; exit 0 means a receipt was
written, not that delivery succeeded. Missing/invalid/mismatched receipt after
handoff means unknown. Invalid argv or unsafe directory cannot safely receive a
receipt and exit 1. The consumer must validate version, correlation, nonce and
status/reason, not just exit code. Suppressed is reserved for the future policy
layer and not emitted by native.

| Reason | Native status | Meaning |
| --- | --- | --- |
| malformed_request | rejected | JSON, fields, limits or correlation invalid |
| unsupported_version | rejected | unsupported schema |
| unsupported_action | rejected | action kind unavailable |
| invalid_file | rejected | unsafe/missing/bounded file read failed |
| expired | rejected | boot/deadline invalid or elapsed before handoff |
| activation_required | rejected | permission undetermined; setup needed |
| permission_denied | rejected | permission denied |
| unsupported_notifier | rejected | bundle/LaunchServices/boot clock unavailable |
| os_rejected | rejected | negative OS completion |
| os_accepted | submitted | positive OS completion, not banner/read/click |
| timeout | unknown | handoff could have occurred; never automatically retry |

The caller's future spool/admission owner must impose total byte/count limits,
retain attempts while LaunchServices may still launch, clean its expired orphans
with a bounded scan on notify/setup, and remove directories only after
terminal receipt plus sender exit, or original deadline under its ownership
protocol. PR1 creates no spool or request files and implements no journal,
installer, global cleanup scan or daemon. This per-attempt native reader neither
recreates a missing attempt nor depends on the caller's cwd. There is no promise
of cleanup at a fixed time after every owner has crashed. These caller gates are
required before activating a producer in later PRs.

Tests are pure codecs/parser/state-machine spies and isolated filesystem tests.
Eight legacy action fixtures characterize current source behavior, including
its intentional ignoring of extra schemaVersion; they are reconstructed here,
not copied from inaccessible historical spike artifacts. Run the complete Swift
suite on macOS. Linux source review cannot qualify AppKit/UN or macOS filesystem
APIs. Native permission, delayed LaunchServices, sleep, atomic receipt, old
artifact rejection and installed UI behavior remain macOS integration gates.

## Desktop action contract

`Tests/Fixtures/desktop-thread-v1.actions.json` and `$defs.desktop_thread_v1` in
`native-v1.schema.json` are shared adapter fixtures. The action object contains
exactly `type="desktop_thread_v1"`, `schemaVersion=1`, `threadID`,
`routeKind="codex_thread"`, `bundleID="com.openai.codex"`, `teamID`,
`applicationPath`, and `correlationID`. The action correlation must match the
request correlation and the eventual UN notification identifier. Unknown fields,
versions, duplicate keys and malformed Unicode fail closed.

Thread ID is opaque, nonempty, at most 256 UTF-8 bytes, single line, without Cc
controls; `.` and `..` are rejected. Cf scalars remain exact bytes. The encoder
percent-encodes every byte except RFC3986 unreserved bytes into one path segment
of `codex://threads/<segment>`; slash, percent, query and fragment characters
cannot alter route structure. No raw URL, shell or alternate scheme is accepted.

The trusted future Go adapter constructs this object from per-call metadata and
explicit operator routing configuration; it must never expose these fields in
the model tool schema. A private request file does not authenticate a same-UID
caller. Startup environment, cwd, parent guessing and credentials are not target
sources. The complete action JSON is serialized into UN userInfo under
`desktop_thread_v1` before add. No request/cache/workspace path is needed by a
later click; `applicationPath` refers only to the selected installed application.

Setup must obtain an explicit canonical application location and expected signing
team from an operator or trusted distribution source. Team IDs are ten uppercase
ASCII letters/digits, with no universal team constant. On every click the selected
existing directory must retain its canonical location, bundle ID and valid Apple
Developer ID signature for that team across all architectures. Ad-hoc/unsigned
builds and other distribution types are unsupported until qualified. A missing,
moved or mismatched selected application fails closed with a bounded reason. This
implementation checks at most 16 registered candidates on macOS 12+ only to
classify a missing configured location as missing, moved or ambiguous; it never
opens a replacement candidate. Older systems report missing. Setup must refresh
moved locations. Multiple installed copies do not cause first-candidate selection.

The opener exclusively uses `NSWorkspace.open(_:withApplicationAt:configuration:
completionHandler:)`, with activation enabled. It neither invokes a shell nor
uses the default URL handler. `open_requested` means the explicit application
open completed positively, not that the visible chat was verified. Profile,
account, exact window/Space, sign-out and archived/deleted chat detection are
unavailable. Prior default-open exit 0/logs did not switch visible chat; its root
cause remains unknown. Prior explicit-app observations are not this patch's
native qualification.

Callback acceptance and completion run on the main queue. A process-wide owner
counts all in-flight callbacks, invalidates idle watchdog generations on accept,
and completes each callback once on completion or its separate ten-second
deadline. An idle-only watchdog expires ten seconds after the last callback
finishes and all owned children are reaped; stale timers cannot terminate active
callbacks or abandon owned children. Only default click and
registered `OPEN` navigate; dismiss and unknown buttons complete without action.
Late completion cannot decrement the counter twice. No unconditional startup or
half-second callback termination remains. Callback timeout does not prove that
an already issued OS open was cancelled and never permits retry.


### Callback work ownership and diagnostics (review fixes)

Configured-location discovery, registered-candidate lookup, canonicalization and
Security verification run off the main queue. One process-wide admission gate
allows at most two outstanding preflights, including scheduled work and pending
result delivery; a full gate completes the new callback as `open_unknown` without
queueing a job. A timeout invalidates a lock-protected token immediately, completes
the callback, and cannot release a slot still occupied by an uncancellable system
call. Workers check that token before subsequent discovery/verification steps and
after blocking calls. Results return to the main queue and recheck the original
deadline before issuing an open. No worker reads the lifecycle dictionary or waits
for the main queue. Security itself has no cancellation/deadline API; a permanently
blocked call can occupy a slot for the remaining process lifetime.

Legacy `/bin/sh -c <unchanged command>` uses `posix_spawn` with
`POSIX_SPAWN_SETPGROUP` and pgroup zero, establishing its own process group before
exec. Deadline cleanup sends TERM only to that owned group, then KILL after 250 ms.
Normal leader exit is observed with Darwin `waitid(WEXITED | WNOHANG |
WNOWAIT)`, without consuming its status. While this unreaped leader reserves the
PGID, a `sysctl(KERN_PROC_PGRP)` snapshot must contain only that zombie leader
to prove ordinary same-group work drained. Other members, even zombies, retain
ownership conservatively. Failed, truncated or oversized snapshots
are unknown and retain ownership until another poll or deadline cleanup. A live
helper may finish normally within the original callback deadline; a command with
no helper can reap on its first exit observation, without waiting ten seconds.
On timeout the direct child remains unreaped until the final group signal,
preventing PID reuse during escalation. Only proven normal group drain or completed
TERM/KILL escalation permits nonblocking `waitpid` to reap the direct child; idle
exit remains inhibited until this barrier completes, even though the notification
completion has already fired. No fixed kernel-reap latency is promised. Ordinary
same-group shell descendants receive cleanup signals; commands deliberately
creating a different session/group are outside group ownership. Successful command
completion still precedes combined activation. Deliberate `setsid`/process-group
escape is explicitly unsupported; no cached bare PID is signalled after reap.
The single main-queue owner must remain the only consumer of this child's wait
status. Forced notifier death remains outside this ownership guarantee.

Legacy activation retains its running-application-first strategy, followed by
bundle-ID lookup/open when no running target exists or activation fails. Both
running-app discovery and LaunchServices lookup use off-main work admitted through
the same two-slot gate as typed preflight. A held call keeps its slot through main
result delivery, even after timeout. The original callback token/activity guard is
carried through command completion, discovery and fallback; main rechecks it before
every activation/open. Timeout and a second typed callback remain independent of
a held legacy lookup. Native asynchronous opens cannot
be cancelled; timeout remains unknown and never implies a visible target.

Each callback emits two bounded JSON lines to stderr: `callback_received` and one
`callback_terminal` with a fixed `outcome`. Both contain `correlationID`, the
validated typed action UUID matching the notification identifier. Legacy, missing,
or malformed target data instead receives a fresh event UUID shared by its two
lines. Valid typed dismissals retain correlation while taking no action. No body,
thread ID, path, shell text or raw userInfo is logged. Late/double completions do
not emit another terminal event. These events report callback-stage evidence;
production does not emit visible-target confirmation.

`CallbackWorkTests` exercises the production `CallbackHandler`, executors and
lifecycle using held discovery/Security and owned-child spies. A syscall seam also
exercises the production child owner's signal/escalation/reap ordering without OS
effects, including failed spawn and cancellation before spawn. The harmless real
child/reap test is macOS-only qualification, explicitly enabled with
`NOTIFIER_TEST_OWNED_CHILD=1`; ordinary unit tests do not launch children or apps.

`LegacyFinalTests` adds production-owner leader-exits-first drain/timeout fixtures,
no-helper immediate completion, guarded running-first/fallback delivery, and a held
legacy lookup alongside a typed callback with zero late legacy opens. The actual
Darwin leader-exits-first natural-drain and deadline-cleanup fixture is opt-in via
`NOTIFIER_TEST_LEADER_EXITS_FIRST=1`; it runs only disposable `/bin/sleep` groups,
without app UI. Darwin syscall compilation/runtime qualification must be performed
on macOS; Linux source inspection does not establish those results.

### Read-only permission readiness (PR4 pre-admission)

The existing six-field `--capabilities-json` response is unchanged and does not
initialize UserNotifications. A verified protocol-floor-1 helper may additionally
be invoked directly with the exact ordered grammar:

```
--capabilities-json --permission-status --correlation-id <uuid> --nonce <uuid>
```

This returns a separate five-field envelope (not capabilities and not a receipt):
`{"schemaVersion":1,"correlationID":"<uuid>","nonce":"<uuid>","backend":"macos.usernotifications","permission":"allowed"}`.
Permission is `allowed` (authorized/provisional), `undetermined`, `denied`, or
`unavailable` (future status or native timer expiry). Grammar/UUIDs and app bundle
are checked before querying settings. The probe only calls
`getNotificationSettings`, uses no NSApplication/LaunchServices, and emits one
JSON line via a single stdout write, then exits. A global one-second timer races
settings through a locked single-completion gate; no late second response exists.
No authorization request, notification add, category registration, sound, or open
occurs on this mode. Invalid grammar/bundle exits 1 with no protocol output.

Old verified PR1 helpers already reject extra capability-mode arguments before
OS initialization. A standalone unknown flag is unsafe and must never be used.
Unknown/unverified installations get zero executions, including discovery.
Capabilities and permission each have a one-second direct-process timeout within
the original transport budget; Go kills/reaps only that child. Native output is
bounded and validated strictly; errors contain no response bytes.

`notification.ReadinessPort.CheckReadiness(ctx, request)` returns `ready` only
when shared request/policy/navigation validation, verified lease/capability, and
permission checks pass. Undetermined maps to `activation_required`, denied to
`permission_denied`, unreadable/unknown to `readiness_unavailable`. Other failures
retain shared delivery reasons. It never accesses the spool. The lease is released
before return. Readiness is a snapshot: PR4 must recheck journal key/counters in
its subsequent atomic admission transaction, then call Deliver with the SAME
original boot-continuous deadline (15 seconds from complete transport frame).
Deliver reacquires/verifies the current generation and repeats both probes before
spool/launch; native send still checks permission immediately before add. Ready
is neither a reservation nor an OS-acceptance guarantee.

Read-only status may supply an ephemeral valid request/UUID/deadline without
journal state. NativeInstallation.Acquire must use only existing ownership/lock
state and perform no setup/cleanup. The current ManagedInstallation opens the
existing lock with create=false. PR2 common Acquire composition remains the
coordinator's integration responsibility; preserve this interface seam.


## Explicit permission setup (separate from notification delivery)

The caller MUST verify the managed artifact identity and known base
`--capabilities-json` contract first, then invoke exactly
`--capabilities-json --setup`. Never blindly launch an old or unknown binary with
`--request-permission-json`: older legacy helpers may interpret unknown flags as
callback mode. A known modern helper without setup support rejects the setup
probe nonzero, without permission access. Probe failure, malformed replies, or
unsupported versions fail closed; they do not permit a request or fallback send.

The separate bounded setup response is one newline-terminated JSON object:

```json
{"schemaVersion":1,"permissionRequestVersions":[1],"backend":"macos.usernotifications"}
```

Only this exact two-argument probe grammar is supported (no transport marker).
It needs neither app metadata nor AppKit/UN initialization. Base capabilities
retain their existing schema and output implementation. Setup is neither a
notification action kind nor a send protocol version. Go
`DecodeSetupCapabilities` requires exactly schema 1 and request versions `[1]`;
it rejects unknown, missing, null, duplicate, oversized or unsupported fields.
There is no production Go launcher in this slice; main wires the trusted
installer and explicit user action later. Notify and read-only status must
never initiate setup.

After successful trusted negotiation and an explicit user setup action, use:

```text
--request-permission-json --correlation-id UUID --nonce UUID
```

Order is fixed, both UUIDs must be 36-character UUID strings. An optional single
`-launchedViaLaunchServices` may appear **only at the end**, to accommodate the
bundle-launch transport marker. It is not required for direct bundle execution
and does not establish bundle identity. No other flags, duplicates or values are
accepted. Argument values cannot select another command mode. Invalid grammar
exits nonzero without UN access or an envelope. Valid grammar with unavailable
bundle metadata emits `unavailable` without UN access. Before UN access the
helper requires a nonempty bundle identifier, `.app` bundle URL and `APPL`
package type. Direct bundle execution versus LaunchServices authorization
behavior and stdout capture remain actual Mac qualification tasks; neither
transport is claimed qualified here.

The helper runs an accessory AppKit event loop. It reads current settings;
authorized/provisional returns `allowed`, denied returns `denied`, unknown or
failed settings returns `unavailable`. Only undetermined settings dispatch one
OS authorization request (alert/sound/badge authorization options, matching
legacy permission policy). Grant returns `allowed`, rejection returns `denied`,
and authorization errors return `unavailable`. No notification is constructed
or submitted, no audio is played, no categories or callback delegate are
registered, and no notification receipt is emitted. Legacy send and its
PermissionManager behavior remain unchanged; read-only PermissionProbe still
cannot request authorization.

The terminal output is exactly one newline-terminated existing PermissionEnvelope:

```json
{"schemaVersion":1,"correlationID":"00000000-0000-4000-8000-000000000001","nonce":"00000000-0000-4000-8000-000000000002","backend":"macos.usernotifications","permission":"allowed"}
```

Use Go `DecodePermission` with the pending correlation ID and nonce. The shared
permission vocabulary is `allowed|denied|undetermined|unavailable`; setup normally
resolves undetermined through authorization or the deadline. Raw OS errors are
never printed. The 120-second continuous-clock deadline covers settings and
human authorization response, checked before dispatch and on completion, with
main-loop timeout polling. Timeout emits `unavailable` once; late/duplicate
callbacks cannot emit again or trigger another request. It does **not** cancel
already-dispatched OS UI or prove whether permission was granted. Missing output
or `unavailable` is an unknown grant outcome: never automatically retry
permission or use legacy send as a fallback. A later read-only status check may
observe the resulting permission.

Qualification: focused Swift XCTest seams exercise grammar, mappings, deadlines,
synchronous/duplicate/concurrent callbacks, and read-only behavior without OS
calls or sleeps. Main must compile/run these on Mac and separately qualify
bundle metadata, direct/LaunchServices transport and event loop behavior under
its authorization policy. Hosted Go codec tests are not Mac or E2E evidence.
