# OpenCode plugin bundle

`npm ci && npm run build` installs the UAP observer version pinned by
`package-lock.json` and creates a self-contained local OpenCode plugin under
`internal/opencodeplugin/dist`. The product
installer replaces `__AGENT_NOTIFICATIONS_EXECUTABLE__` with a trusted absolute
binary path. This package does not install or enable the plugin.

The plugin sends content-free UAP facts to `opencode-event --protocol 1`. It
does not send native event bodies, prompts, question text, errors, or project
metadata. Process startup and OpenCode shutdown remain best effort.

One default definition exposes V1 `server` and V2 `setup`. The V1 observer stays
unchanged; V2 uses a location-aware, bounded event reader and verifies native
session context before completion. Completion refers to a settled busy period,
which may include several queued prompts. Restart after install/update; V2
2.0.21 also supports native plugin reload, while 2.0.0 requires restart.

The candidate pins `universal-agent-plugins-opencode-events@0.2.0`, which is not
published yet. Qualification stages its exact reviewed tarball in a temporary
package/lock overlay. After separately authorized SDK publication, clean registry
`npm ci` must reproduce the qualified bundle before product merge/release.
