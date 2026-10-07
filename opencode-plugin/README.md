# OpenCode plugin bundle

The Agent Notifications 1.48.0 bundle consumes the actual **unpublished 0.3.0** npm pack at
`vendor/universal-agent-plugins-opencode-events-0.3.0.tgz`, with the genuine
npm-generated relative file lock. The published 0.2.0 root API stays in the SDK;
AN imports the strict `/v1` and `/v2` factories. The built ESM bundle is self-contained;
separate SDK publication is not a consumer release gate. esbuild remains 0.28.2.
The reviewed archive SHA-256 is
`c3d5aaaf6ecc3116b48ab1ae3f0e00b47720f239df9938f0c499d03f2c21a752`.

After the parent provisions Node/npm and bootstraps the locked dependencies,
`npm run check:candidate` byte-verifies the archive, installed packed files and
lock. `npm run check` typechecks with pinned TypeScript 7.0.2, builds the self-contained ESM asset and runs focused Node
tests. Neither command starts OpenCode, installs the plugin or publishes npm.

The plugin sends content-free UAP facts to `opencode-event --protocol 1`, with
an optional product-owned desktop display envelope containing an exact-session
native title and the actual `questions[].question` text. The shared observer and
SDK remain content-free. Options, headers, prompts, paths and native errors never
enter display metadata. Webhooks and delivery receipts retain generic copy.

Title lookups are fresh, verify the returned session ID, time out after one
second and allow at most eight native requests in flight. Question text is
snapshotted before observer awaits and matched to the exact session/request;
resolution and new turns invalidate pending display context. Invalid or missing
optional data falls back to the neutral notification. The whole wire remains
bounded to 4096 bytes. Display lookups do not change original causal timestamps, metadata deadlines, owned child closure or admission authority. V2 retains neutral display when the V1 session client API is unavailable.


Use Node 22.18 or newer.

The default export is exactly `{id, server, setup}`. The retained named
`AgentNotifications(input)` routes through the same memoized V1 startup as
`default.server(input)`: discovering both exports cannot acquire two registries
or observers. V2 uses its direct native subscription and public RPC source path.
These loader shapes serve both API generations through one installed plugin.
Installed delivery remains bounded by exact-source qualification and host lifetime.

The V1 source requires a live observed user/ordinary assistant association and
fresh root/location metadata. Idle alone, retry, interruption and a manual summary
cannot authorize completion; a final permanent assistant failure is distinct from
an abort. New user identities fence pending work, including equal native times.
V2 binds the original execution-start and terminal envelope identities, validates
native session context and pending attention, and consumes its final checkpoint on
the same reader. Queued input fences obsolete completion; HTTP prompt admission
alone does not establish a settled execution. Fork lineage is distinct from true
child ancestry. These behaviors have focused source tests; release reports bind installed/native
checks to the released bytes. Restart the host after install or update to load the
reviewed bundle; reload support alone is not a delivery qualification.

The renderer must replace exactly one quoted token each for executable,
control root and origin. Origin comes from the existing E1 registration; JS
never creates or repairs it. One max-four registry holds profile probes at
weight two and clock/event children at weight one through actual close.
Missing inner reaping proof permanently retains both profile reservations.

Production clock selection uses reviewed compiled qualification rows, bound to
the exact host image and semantic source. `createLinuxClock()` exposes the
actual synchronous proc reader for parent qualification; describing a closed
manifest cannot register it. Only compiled reviewed evidence may populate
`clock-qualification-data.mjs`, with the same independently selected Go policy. The fixed
policy budget is R=103ms/T=430ms, not a measured maximum or a runtime grant.

Private protocol 1 wraps unchanged neutral wire 1. Native IDs/timestamps and
original source ingress/anchor/deadline remain private; outbound delivery is
owned by Go admission. No native content, paths, prompts, errors or diagnostics
are printed by the plugin. Failed and unqualified cells stay silent. Synthetic
composition tests and ordinary Node proc checks do not qualify native Bun,
installed E2E, suspend behavior or any of the five packaged platform cells.

The installed fixture retains the SOURCE13 driver and eleven custody cells: V1
1.18.33 and V2 2.0.21 on all five platform pairs, plus V1 1.18.34 on Linux amd64.
It stages completion, pending question/permission, permanent error, retry/interrupt,
manual compaction, foreground child, independent roots/fork and install/update/
remove/reinstall scenarios. Missing custody or production clock prerequisites stop
business phases. Inert fixture checks establish source/parser contracts only.

The 1.48.0 tag release gate runs seven Linux/Windows cells: V1 1.18.33 and V2
2.0.21 on Linux amd64/arm64 and Windows amd64, plus Linux amd64 V1 1.18.34.
The three Linux/Windows native artifacts retain canaries. macOS artifacts and
the signed/notarized helper are excluded from 1.48.0. Exact release reports, rather than configured lanes,
establish successful qualification. Intel macOS desktop delivery remains
unqualified; historical macOS ARM business evidence is separate. One-shot host
shutdown remains best effort. Stock Windows V1 may notify a delayed completion
once and again after the 24-hour claim expires when original event age cannot be
independently verified.

The committed generated asset retains its three quoted executable/root/origin
placeholders. Release preparation verifies the reviewed SDK archive and lock,
rebuilds the bundle and requires byte equality with that tracked embedded asset.
This consumer verification does not publish the standalone SDK or grant eligibility
to an unqualified host. See [release qualification boundaries](../docs/opencode-notifications.md#release-qualification-boundary).
