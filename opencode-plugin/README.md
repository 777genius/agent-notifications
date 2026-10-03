# OpenCode plugin bundle

`npm ci && npm run build` installs the published UAP observer version pinned by
`package-lock.json` and creates a self-contained local OpenCode plugin under
`internal/opencodeplugin/dist`. The product
installer replaces `__AGENT_NOTIFICATIONS_EXECUTABLE__` with a trusted absolute
binary path. This package does not install or enable the plugin.

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
bounded to 4096 bytes. Process startup and OpenCode shutdown remain best effort.

Use Node 22.18 or newer. `npm run check` runs the pinned TypeScript compiler,
builds the bundle and runs both the existing JavaScript tests and typed tests
with Node's supported native TypeScript stripping.
