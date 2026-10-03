# E2a process ports (frozen)

Base: `6ee0d84705d721219ecc56ef8cab6ff2de53becb`. Module: `opencode-plugin/process-registry.mjs`. This is the owned-process prerequisite for E2. E0 owns clock authority; E1 owns admission/install/kernel; D owns the SDK job/token lifecycle. Those integrations remain separate.

## Closed interface

`createProcessRegistry({ executable, privateCwd, controlRoot, osEnv = {}, deliveryEnv = {} })` returns one frozen object with exactly `clock`, `event`, `status`, `drain`, `dispose`. Configuration rejects unknown keys. All paths must pass native absolute-path validation; the executable also needs `.exe` on Windows. The caller must supply the installation-owned executable/control root and an existing private directory (0700 on POSIX), and keep their ownership binding valid. This module checks path syntax, not installation authority or directory ownership.

Construct exactly one registry per loaded plugin instance, shared by the two consumers. Do not construct per callback, helper invocation or event, or add a forwarding pool. No spawn implementation, platform override, argv, executable override per operation, queue, retry, shell or inherited environment port exists.

| Call | Exact accepted argument object | Return |
| --- | --- | --- |
| `clock(options)` | `{ isCurrent, signal?, deadline? }` | `Promise<{ status, output }>` |
| `event(options)` | `{ frame, isCurrent, signal?, deadline? }` | `Promise<{ status, output }>` |
| `status()` | None | `{ accepting, disposed, occupied, unresolved }` |
| `drain(options?)` | `{ timeoutMs? }`, default 3000, range 0..3000 | `Promise<{ accepting, disposed, occupied, unresolved, reaped, status }>` |
| `dispose()` | None | Same bounded observation result as `drain()` |

`isCurrent` is a required synchronous callback returning exactly `true` for live, eligible work; exceptions fail closed. E2 must combine the actual SDK tokens, original freshness and observer eligibility here. `signal` is an optional native `AbortSignal`. `deadline` is an optional finite absolute timestamp in this JS process's `performance.now()` millisecond domain: the immutable earlier command stop deadline, not a duration or wall timestamp. It only shortens the default stop deadline. It is not a cross-process/Go clock field.

`frame` is a Buffer of at most 4096 bytes, copied before acquisition/spawn. E2 supplies the complete compatible encoded frame including its original private provenance; this port neither invents nor validates wire field names. `clock` takes no frame. Unknown operation option keys are rejected with `invalid_request`; invalid factory/environment/drain arguments throw fixed content-free TypeErrors.

Both operations spawn only the configured executable, with exact argv `['opencode-clock', '--protocol', '1']` or `['opencode-event', '--protocol', '1']`, `shell: false`, `windowsHide: true`, private cwd and piped stdin/stdout/stderr. Clock stdin is closed empty; event stdin is ended with the copied frame.

`absoluteNativePath(value, platform = process.platform)` is also exported as a pure boolean syntax guard. Passing a target platform to this guard cannot override the registry's native platform.

| Explicit environment input | Allowed keys |
| --- | --- |
| POSIX `osEnv` | None |
| Windows `osEnv` | `SystemRoot`, `WINDIR` |
| POSIX `deliveryEnv` | `AGENT_NOTIFICATIONS_CONFIG`, `HOME`, `XDG_CONFIG_HOME`, `DBUS_SESSION_BUS_ADDRESS`, `XDG_RUNTIME_DIR` |
| Windows `deliveryEnv` | `AGENT_NOTIFICATIONS_CONFIG`, `USERPROFILE`, `HOMEDRIVE`, `HOMEPATH`, `APPDATA`, `LOCALAPPDATA`, `TEMP`, `TMP` |

Values are copied strings, at most 8192 characters each, without NUL. Both commands get fixed private HOME/XDG on POSIX or private profile/appdata/temp on Windows, plus `osEnv`. Only event gets delivery overrides and the fixed `AGENT_NOTIFICATIONS_CONTROL_ROOT`. No ambient PATH, Node flags, supervisor/debug, proxy or authentication environment is read or inherited. Legitimate OS delivery inputs must be selected explicitly by composition.

## Ownership and settlement

Four reservations/returned actual handles maximum, combined across helper/event for this instance. Capacity returns `capacity_suppressed` immediately without spawning, queueing or later retrying. Validity/signal are checked before acquisition and immediately before spawn; checked again after spawn before stdin and at actual close. SDK/E2 must also recheck after every other await in their own workflows. Aborted or stale work never returns usable output.

Any returned ChildProcess, including asynchronous spawn error without a pid, retains its slot and operation promise until its actual `close` event. `exit`, child/stream error, timeout, abort, stream overflow or a successful/failed `kill()` never settles the operation or releases the slot. Only synchronous spawn throw with no returned handle releases immediately. Each entry retains its handle through the termination closure.

Internal AbortController invalidation precedes TERM on every failure path. Dispose synchronously disables acquisition and invalidates all internal controllers before sending any termination signals. It aborts only this registry's returned handles; it never scans PIDs, kills groups or terminates descendants/host/services. SDK cancellation must invalidate its own external tokens before aborting its signal.

Clock stdout and event receipt stdout retain at most 1024 bytes while alive. Stderr is discarded, counted with a saturating over-limit sentinel, and terminated above 1024 bytes; no raw stderr/error text is returned. Stream errors and bounds start termination. Further output is discarded after failure. Only successful zero-exit actual close with current validity returns the bounded opaque stdout Buffer. E0/E2 still own strict response/receipt decoding and privacy handling.

Operation statuses are `ok`, `invalid_request`, `invalidated`, `registry_unavailable`, `capacity_suppressed`, `spawn_failed`, `stream_error`, `output_limit`, `aborted`, `deadline`, `exited`, `ipc_termination_unproved`. Unproved termination takes precedence; otherwise expired validity takes precedence over other operation outcomes. All non-`ok` outputs are empty Buffers. Objects are frozen; the returned Buffer belongs to the caller. No status includes argv, pid, error text, stderr or frame data.

## Deadlines and shutdown proof

Event TERM timer is anchored immediately before the original spawn at +22s maximum; KILL is scheduled within 1s of termination, proof expires within another 2s, capped at original +25s. A caller's earlier deadline moves the command stop and corresponding +3s proof bound earlier. Go's original maximum 20s command context, independent clock verification and actual provider deadlines remain E2/E1 composition requirements.

Helper's usable response bracket expires at original spawn +2s, with no receive/activity reset. Timeout clears its output and invalidates it immediately; its retained operation promise waits for actual close. Termination has the same separate maximum 1s escalation / 3s close-proof allowance (helper timeout therefore has at most a +5s proof bound). This cleanup allowance cannot make a late helper response usable or extend clock calibration's 2s bracket. Abort, bounds and errors terminate immediately with the shorter remaining proof bound.

At a missed proof bound, `unresolved` increases, `accepting` becomes false for the rest of the instance lifetime, the actual slot remains occupied and its operation promise remains pending. Late actual close releases the slot and finally settles `ipc_termination_unproved`; it does not reopen acquisition. A late close detected before timer dispatch also records the missed proof.

`drain` only observes; it never authorizes, cancels or kills. It resolves when all handles close, any proof becomes unresolved, or its bounded observation timeout expires. Observation status is `drained` only with occupied=0/reaped=true, `ipc_termination_unproved` with unresolved handles, otherwise `pending`/reaped=false. `dispose` is idempotent and bounded; it cannot claim reaping from kill's return. Repeated `status`/`drain` can observe eventual close. The SDK emit callback must await the operation promise through actual close; the separate bounded dispose result is not permission to release that SDK job early.

## Platform limits and outstanding composition

Windows Node maps these supported termination signals to forceful process termination; the TERM/KILL calls are not a POSIX graceful/escalation handshake there. The same actual-close rule still applies. Windows path tests cover canonical drive/UNC and reject root-relative, drive-relative, device namespaces, NUL and noncanonical paths. Linux fixtures do not qualify Windows or macOS reaping, nor native cross-platform clock authority.

No existing IPC/plugin/consumer/bundle/SDK/clock/store/install/kernel file is composed here. Final E2 must wire these ports to E0's qualified clock API, D's actual callback lifetime and E1's compatible frame/admission/context ports, then qualify the installed candidate and each platform. This prerequisite makes no installed-candidate pass claim.
