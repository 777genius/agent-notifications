# OpenCode plugin bundle

This candidate consumes the actual **unpublished 0.3.0** npm pack at
`vendor/universal-agent-plugins-opencode-events-0.3.0.tgz`, with the genuine
npm-generated relative file lock. The published 0.2.0 root API stays in the SDK;
AN imports the strict `/v1` and `/v2` factories. esbuild remains 0.28.2.

After the parent provisions Node/npm and bootstraps the locked dependencies,
`npm run check:candidate` byte-verifies the archive, installed packed files and
lock. `npm run check` builds the self-contained ESM asset and runs focused Node
tests. Neither command starts OpenCode, installs the plugin or publishes npm.

The default export is exactly `{id, server, setup}`. The retained named
`AgentNotifications(input)` routes through the same memoized V1 startup as
`default.server(input)`: discovering both exports cannot acquire two registries
or observers. V2 uses its actual direct native subscription and public RPC.

The renderer must replace exactly one quoted token each for executable,
control root and origin. Origin comes from the existing E1 registration; JS
never creates or repairs it. One max-four registry holds profile probes at
weight two and clock/event children at weight one through actual close.
Missing inner reaping proof permanently retains both profile reservations.

Production clock selection is **unverified**. `createLinuxClock()` exposes the
actual synchronous proc reader for parent qualification; describing a closed
manifest cannot register it. Only compiled reviewed evidence may populate
`clock-qualification-data.mjs`, with the same independently selected Go policy. The fixed
candidate budget is R=103ms/T=430ms, not a measured maximum or today's grant.

Private protocol 1 wraps unchanged neutral wire 1. Native IDs/timestamps and
original source ingress/anchor/deadline remain private; outbound delivery is
owned by Go admission. No native content, paths, prompts, errors or diagnostics
are printed by the plugin. Failed and unqualified cells stay silent. Synthetic
composition tests and ordinary Node proc checks do not qualify native Bun,
installed E2E, suspend behavior or any of the five packaged platform cells.
