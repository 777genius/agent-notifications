# Linux selected-client notification handoff TEST contract

Status: source preparation only. No notification, client launch or native callback has been performed for this slice. The existing renderer preflight does not qualify this handoff.

## Selected entry point

The frozen TEST package declares `Exec=chatgpt %U`, `StartupNotify=true` and the Codex URI handler. `/usr/bin/chatgpt` selects the vendor launcher under `/usr/lib/chatgpt`. It does not declare `DBusActivatable`; that omission does not prove absence of a runtime D-Bus interface. The adjacent desktop-declaration evidence retains the package, entry and launcher hashes.

Use the exact vendor launcher with one canonical synthetic `codex://threads/<TEST UUID>` argument and the explicit Wayland switch. Do not consult a default URL handler or copy an authenticated user profile. A signed-out guest can establish handoff and activation, but cannot establish visible selection of an authenticated chat.

## Callback authority and effects

`navigation_linux_client_callback_test.py` runs only as UID 1000 in a fresh offline guest with a read-only ISO TEST seed, root-owned callback specification, private session directories, owned cgroup and live private bus/compositor peers. Its source and selected installation must match the frozen hashes. It reconstructs the child environment rather than inheriting the operator environment.

One genuine Gio action supplies one matching TEST target and a nonempty bounded activation token. The callback publishes an exclusive handoff intent before launching. Any missing result after that intent remains unknown and must never trigger a retry. The token is consumed before the attempt; public records contain only its hash. The deadline is checked immediately before launch, after persistence and log creation.

The callback intentionally records `activationQualified=false` and `navigationQualified=false`. Its return code, a launcher PID, mutable argv or its own JSON record cannot establish client execution, URI consumption, focus or chat selection. Private Wayland traces can contain the opaque token; retain them as private raw evidence and publish a bounded redacted projection.

## Required guest-controller proof before a native attempt

1. Create one new offline TEST overlay, fixed read-only seed and fresh private session. Keep the shipped client sandbox and AppArmor profile enabled. Bound the entire VM attempt externally.
2. Start a private bus, owned Sway compositor, notification daemon and qualified portal frontend/backend. Record kernel process identity and retain pidfds; establish each bus owner and socket peer. Plain distro GTK is an already observed token-omission negative control, not the qualified backend.
3. Install the exact callback source in the ISO and root-owned specification after the private peer identities are known. D-Bus service activation must inherit the TEST cgroup and private environment. Prove the callback is absent before the sole click.
4. Send exactly one native notification, collect the sender exit and prove it is gone before clicking. Resolve the notification ID from the owned provider's matching request/reply. Use one owned Wayland pointer action; never call the callback action directly to substitute for a click.
5. Observe exactly one provider action and its typed platform data. Prove the cold callback birth follows collected sender exit and that its token hash equals the native token hash. Then corroborate the client handoff with kernel identity for the selected executable and observed protocol behavior.
6. Treat client activation separately: correlate the actual client connection, `xdg_activation_v1.activate` token, owned toplevel and compositor focus evidence. A request to activate is not proof of focus. Leave exact-chat qualification false in the signed-out guest.
7. Preserve partial failure evidence. Kill only the inherited owned cgroup, collect every tracked incarnation, prove the cgroup is empty, shut down the guest and collect QEMU. Cleanup uncertainty prevents a pass. Never replay an uncertain attempt.

This is a checklist for the next controller implementation, not a claim that these steps already passed. The current source checkpoint contains the callback, one-shot portal sender and pure invocation tests; controller and native qualification remain outstanding. The sender uses the same guarded context and submits only one synthetic notification after portal Registry registration. Both the AddNotification intent and sender entry are exclusive. A synchronous typed reply is sufficient to know the request was sent; there is no unbounded flush afterward. Application registration still requires the external controller deadline. Sender exit and absence of its bus owner must be observed before the click, rather than inferred from a submission record.

## Review and tests

The pure invocation contract independently expects the fixed vendor argv and canonical synthetic URI, and rejects substituted targets and invalid environment tokens. It imports no Gio and performs no native effects. Run `PYTHONDONTWRITEBYTECODE=1 python3 scripts/test-navigation-linux-client-callback.py`.

Round 1 identified a deadline gap after intent persistence and log creation; the callback now rechecks immediately before `Popen`. Round 2 identified that a bus-address prefix check admitted a fallback transport. The callback now requires exactly the fixed private socket and a 32-hex GUID; pure negative cases reject fallback transports, additional options and different paths. Round 3 independently reviewed the callback authority and effects. Rounds 2 and 3 accepted the corrected callback source. Any subsequent sender/controller changes require their own review before native execution.

Sender review: round 1 removed an unbounded flush after the typed AddNotification reply. Rounds 2 and 3 checked authority reuse, singleton ownership, publication-before-effect and unknown/no-retry outcomes. Final-source verification follows the flush removal.

## Guest pointer entry

The container pointer entry retains its original TEST-container guard. Both entries use `navigation_wayland_pointer_core.h` for the same one-motion, gated CLICK press/release and DONE protocol. Docker build context staging includes that header.

The guest Python entry validates the same offline seed/cgroup/private-session authority as the callback, then a root-owned 0444 `pointer-spec.json` with exactly `nonce`, `y`, `entrySHA256` and `librarySHA256`. The nonce must match the session; integer `y` must fit the fixed 1280x720 output. The entry source and shared library reside in the read-only ISO and must match their frozen hashes.

The entry retains the compositor pidfd and validates the peer of the actual connected Wayland socket before loading the library. It publishes an exclusive pointer intent, then transfers that fd directly to the shared native effect. The library does not resolve an ambient/default display. Its return code cannot qualify a notification click; the future controller must bind pointer enter/button/serial/token protocol evidence and collect the process. The native effect has a 20-second alarm; error, alarm or missing acknowledgement remains unknown and must not be replayed.

All three independent source reviews accepted the pointer authority boundary and unchanged container effect. Both native entries compiled on the TEST Linux server with `-std=c11 -Wall -Wextra -Werror`; the guest library also used `-shared -fPIC`. The exact owned compiler container exited 0 and was removed, and source before/after hashes matched. The adjacent `linux-guest-pointer-compile.json` retains that evidence and the preceding no-effect preflight failure. No pointer function was invoked during the compiler check. The full guest controller/native attempt remains outstanding.

## Dormant guest-controller checkpoint

The controller source now composes the guarded sender/callback/pointer, private bus owners,
kernel incarnations, shipped AppArmor profile, native token chain and peer-verified Sway
focus observation. It retains partial evidence and collects the owned cgroup and pidfds
before publishing a result. This source has not run in a guest.

The controller refuses inside its result/cleanup/shutdown boundary before creating a
native child or sending a notification. Its server-side join now binds the observed
connection handle and live toplevel to the selected client and focused Sway view in
source. The refusal remains until complete host/seed assembly and the join pass renewed
integration review. Client stderr plus matching PID/focus alone is insufficient.
The host factory, seed assembly and host reader for `NAVIGATION_TEST_HANDOFF_V1` also
remain outstanding. Never run the dormant scaffold as a native qualification attempt.

The existing container lane and the dormant guest controller share one native-chain
decoder. Six pure tests use independent wire fixtures and reject foreign providers,
notification IDs, button serials, seat/surface mismatches, lost surfaces, duplicate actions
and a callback before the native action. The reordered-action case failed on the previous
decoder and passed after enforcing token event < native action < typed callback. These
fixtures do not qualify a native click. Final qualification flags derive from successful
collection/publication and observed evidence; the missing join keeps activation false.

Three independent controller reviews accepted only this dormant source checkpoint.
Their fixes closed expired-predicate admission, late start/click deadlines, cleanup and
publication gaps, premature qualification, missing main-process AppArmor attachment
and callback/native-action ordering. The remaining missing join caused the explicit
early refusal. Removing that refusal requires renewed review of the complete assembly
and server connection/toplevel proof; this acceptance cannot authorize native execution.

## TEST compositor observer preparation

The proposed observer attaches the public Wayland protocol logger only to owned TEST
Sway. It must never be preloaded into the selected client. Its private append-only log
fd is passed explicitly to that compositor, validated as an empty root-owned 0600
regular file, then marked CLOEXEC. A read-only ISO TEST marker and UID 1000 are required.
The surrounding guest authority still owns the offline/cgroup/session checks.

For each relevant connection, the observer obtains `SO_PEERPIDFD` from the actual
Wayland socket, checks its fdinfo PID against socket credentials and retains its live
incarnation. There is no numeric `pidfd_open` fallback. Missing kernel support rejects
the fixture. The identity belongs to the connector; it does not identify every possible
writer through an inherited socket. The root controller must independently bind this
handle and birth to the selected launched client through the still-live compositor.

Requests and actual server resource creation/destruction are separate records. The
decoder requires both, preserves resource generations, and rejects closed/recycled
surface IDs, duplicate activation and multiple live selected toplevels. Its returned
`kernelBound` and `focusQualified` remain false. Bounds are cumulative: 128 connections,
256 resource generations and 512 records. Observer errors, lost logging or a second
display terminate only the owned TEST Sway with exit 74; a valid truncated prefix
cannot leave a healthy compositor. The consumer must verify compositor liveness
through the final observation window.

Seven pure decoder tests pass. A new resource-bound regression test failed on the
previous decoder for both 257 live resources and reuse after 256 creations, then passed
after the cumulative check. Strict server compilation passed for the frozen C source;
`linux-server-observer-compile.json` retains source/output/raw hashes and collected
container exit/cleanup. The resulting library was never loaded.

Three independent source reviews accepted this checkpoint after fixing logging failure
handling, second-display admission, output-fd inheritance and the cumulative decoder
bound. Independent compiler evidence review passed 50 source/archive/binary/operator
and lifecycle binding assertions. Neither acceptance qualifies native integration.

This remains preparation: no manifest staging, Sway loading, retained-handle validation,
before/after stable focus-node observation or native attempt has occurred. Resource allocation
does not prove completed mapping, token acceptance, focus or chat navigation. The early
guest refusal remains in place until the complete integration passes renewed review.

Public API references: [Wayland server API](https://wayland.freedesktop.org/docs/html/apc.html)
and [Linux SO_PEERPIDFD introduction](https://github.com/torvalds/linux/commit/7b26952a91cf65ff1cc867a2382a8964d8c0ee7d).

## Dormant observer integration

The guest manifest now freezes the observer library and decoder as two additional
seed entries. Only the Sway process receives the preload and explicitly passed private
log fd. The root controller checks the observer's socket-derived pidfd through the live
compositor against its own retained selected-client incarnation and executable.

The decoded unique live role, resource generations and selected-record fingerprint must
remain identical before and after the peer-verified IPC focus query. The focused node
must be a native `con`/`xdg_shell` view; XWayland nodes are rejected. The same surface
snapshot is checked again before final collection. Matching activation is selected by
the expected token hash. Final provider waits use the remaining deadline, and expiry
is checked before the final surface snapshot and immediately before PASS.

Three independent source reviews accepted this integration after closing the late-PASS
deadline gap. The early refusal still precedes cgroup/native child creation. This is
source preparation, not observed kernel/focus qualification, token causation or chat
selection. Complete host factory, runtime staging and frame-reader review remain next.
