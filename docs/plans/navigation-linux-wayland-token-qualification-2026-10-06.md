# Wayland native token forwarding TEST

This fixture implements the plan's click-time activation-token checkpoint. Native execution is pending; source review and syntax checks do not qualify the platform.

The first native execution at source 0b965fb6261e768e8f08e0af12fd4cf3a939f1a4 compiled GTK and the strict C driver, started the private 1280x720 compositor and submitted one notification after registering the TEST sender. The sender's exit was collected. Mako configured a 600x52 surface, while the original fixed pointer coordinate used y=60. The pointer-enter gate timed out before CLICK authorization; the captured trace has no button events, the bus has no ActivateAction, and no callback/effect files exist. Passed remained false and exact owned-container removal/absence checks succeeded. Its pointer pipe acknowledgement was not retained, so no MOVED-output claim is made for this run.

The corrected fixture derives Y from the actual configured height, still requiring the same-pointer enter before authorizing a press. Failure cleanup also retains remaining pointer-pipe output. This correction awaits a separate fresh native experiment; the failed evidence is not relabeled.

Each run owns a private headless Sway compositor, Mako notification server, D-Bus session, immutable TEST application files and container. It collects the finite sender's exit before one virtual-pointer click. Direct Mako protocol tracing ties the entered surface/pointer, pressed serial, token setup and compositor response. Actual D-Bus signals and typed ActivateAction arguments are compared with the cold helper's before_emit observation and exactly one TEST effect.

The evidence class is owned_process_protocol_trace. The compositor socket peer and notification D-Bus owner are checked, but Sway does not expose a public server-derived layer-surface PID contract. No surface-identity, token acceptance, focus, installed Codex client or visible chat claim follows from this fixture.

Use separate fresh runs for GTK 1.15.1 and 1.15.3. The older backend is expected to receive the native token and omit it from platform data; that is a successful negative experiment with tokenForwardingQualified=false. The newer backend must preserve both activation-token and desktop-startup-id, match the helper's token hash, and pass lifecycle/count/immutability checks before tokenForwardingQualified becomes true. Unknown click effects are never retried.

The source-built backend uses publisher GTK 1.15.3 commit 337202d4e7179857bc37b03c1a6d8c9d92e47c44, archive SHA256 47a3743d2419a8601e691db37e85bb5fac5ae4b26842177065cd5f22ada23b37. Its official checksum and all 121 tracked file blobs were verified through gh. The public pointer XML is pinned to swaywm/wlr-protocols commit b010a03648b88d143236de193bddbfea0c08bc84, SHA256 3ff6d540be0bc5228195bf072bde42117ea17945a5c2061add5d3cf97d6bb524. Generated bindings stay inside the TEST image. GTK compilation explicitly selects the verified portal 1.22.1 interface directory.

Invoke the existing portal wrapper with --build-and-execute-test, its verified portal archive/checksum/provenance, and either --wayland-token-test or --wayland-token-negative-test. Both require --gtk-source-archive, --wayland-source-provenance and --virtual-pointer-protocol. All four scenarios are mutually exclusive. A nonroot operator and the existing --docker-via-sudo option provide a nonroot private runtime without weakening the container boundary.

The wrapper retains immutable context/source hashes, build output, runtime package records, partial failures and exact owned-container cleanup evidence. Tokens remain in the private raw collection; public projections must use token hashes and omit private paths and TEST identities. Production dispatch is unchanged.
