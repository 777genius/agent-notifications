# OpenCode plugin bundle

`npm ci && npm run build` installs the published UAP observer version pinned by
`package-lock.json` and creates a self-contained local OpenCode plugin under
`internal/opencodeplugin/dist`. The product
installer replaces `__AGENT_NOTIFICATIONS_EXECUTABLE__` with a trusted absolute
binary path. This package does not install or enable the plugin.

The plugin sends content-free UAP facts to `opencode-event --protocol 1`. It
does not send native event bodies, prompts, question text, errors, or project
metadata. Process startup and OpenCode shutdown remain best effort.
