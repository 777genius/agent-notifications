---
description: Initialize notification resources and create missing shared configuration
disable-model-invocation: true
allowed-tools: Bash, AskUserQuestion
---

# Initialize Agent Notifications

Use the recorded adapter installation and existing resource discovery to locate the platform executable and installer. `PLUGIN_ROOT`, `CLAUDE_PLUGIN_ROOT` and `INSTALL_TARGET_DIR` locate resources only; never derive notification config from them. Binary and plugin names remain unchanged.

Before running any installer/update, use the verified config-capable helper's read-only preflight from the supported top-level setup flow. It must inspect recorded active bundle/custom-root candidates before any cache update, uninstall or refresh. Never guess the newest cache or automatically copy its config. If the helper/preflight is unavailable, stop and retain the working runtime; do not download/run a legacy installer as a fallback. These instructions require the coordinated config-capable runtime/installer; no downloadable bridge release is promised.

For personalized/unknown cache-only settings, stop before the updater. Explain source and selected destination paths without showing contents. Stop old writers and obtain an explicit choice of recovery source. With `NOTIFICATIONS_BIN` set to the verified new helper and `RECOVERY_SOURCE` to the chosen file:

```bash
"$NOTIFICATIONS_BIN" config path --json
"$NOTIFICATIONS_BIN" config inspect --json
# Only after explicit recovery approval and a confirmed missing destination:
"$NOTIFICATIONS_BIN" config init --from "$RECOVERY_SOURCE"
```

`--from FILE` validates and imports raw settings into a missing selected destination only. It cannot overwrite valid/invalid existing config, migrate L to N, or reset settings. Resolve invalid existing files explicitly with stopped writers and private recovery copies. Never use bundle copies or links for compatibility. Retry preflight after successful recovery before updating.

After successful resource installation and registration through the supported top-level flow, initialize shared configuration once (a repeated call is safe):

```bash
"$NOTIFICATIONS_BIN" config init
"$NOTIFICATIONS_BIN" config inspect --json
```

Existing valid config is a byte/mode/mtime-preserving no-op. Existing invalid config is an error. Missing automatic config is created at N; existing L remains selected; explicit E selects only that file. See [OS paths and environment rules](../docs/CONFIGURATION.md#manual-configuration). Hooks never initialize config.

If assets/registration succeeded but init failed, report partial success and the safe error/path. Retry only `config init` after resolving the cause; do not repeat downloads or registration. Do not claim success after a failed command. Do not add config writes to low-level asset installers.

Then run `/claude-notifications-go:settings` for [private revision-checked edits](settings.md). Save diagnostics privately; never print raw configuration or expanded secrets.
