# Local Development And E2E Testing

This project has three separate local-testing workflows. Use the smallest one that matches the change you are making.

## Configuration isolation and qualification

Notification configuration follows [E / L / N](../README.md#manual-configuration), not CLAUDE_HOME or CODEX_HOME. Before tests, create a private disposable root and isolate HOME, USERPROFILE, APPDATA, LOCALAPPDATA, all XDG CONFIG/CACHE/DATA/STATE/RUNTIME directories, CODEX_HOME, CLAUDE_CONFIG_DIR, CLAUDE_HOME and TMP/TEMP/TMPDIR. Clear E (`AGENT_NOTIFICATIONS_CONFIG`) for automatic resolver fixtures or set it to a unique absolute native fixture file. Child processes should receive an allowlist environment. Do not read real profiles/configs or invoke real agents/notifications for source-only checks.

Inspect via the existing executable's `config inspect --json`; never attach raw JSON, expanded webhook values or unscreened logs. Keep diagnostic files private. Tests of Store CAS/unknown-field preservation and native Windows locks/replacement/ACLs, macOS/Linux crash/concurrency and frozen-old-reader compatibility must run separately in disposable environments. This documentation change does not establish those gates; cross-compilation is not native Windows E2E evidence.

## Recommended Workflow

1. Use `scripts/dev-local-plugin.sh` first when you are changing install/update behavior.
2. Use `scripts/e2e-real-claude.sh` when you need a real `claude` process to execute hooks.
3. Use `scripts/dev-real-plugin.sh` only when you must validate behavior inside your real Claude environment.
4. Switch your real Claude setup back to the remote marketplace after finishing manual tests.

## Quick Start

### 1. Build the binary

```bash
make build
```

### 2. Isolated local marketplace testing

This isolates Claude metadata under `~/.claude-dev/claude-notifications-go`; it does not by itself isolate notification config, HOME, XDG or APPDATA. Apply the isolation requirements below before running it.

```bash
scripts/dev-local-plugin.sh install
scripts/dev-local-plugin.sh bootstrap
scripts/dev-local-plugin.sh status
```

Useful commands:

```bash
scripts/dev-local-plugin.sh update
scripts/dev-local-plugin.sh reset
```

Use this workflow for:

- `bootstrap.sh` changes
- marketplace/install/update fixes
- plugin manifest/version checks
- local binary/init flow validation

## Real Claude E2E

Use this when the change must be exercised by a real `claude` process with hooks enabled.

### Platform support

- Smoke modes are intended for local `macOS` and `Linux` environments.
- Manual click modes are intended for local desktop sessions only.
- Manual click modes are disabled in CI/headless environments.
- Windows is currently not a supported target for this harness.
- Run `scripts/e2e-real-claude.sh status` to see what the current machine supports before running a mode.

### Manual desktop notification debugging

Use this when you need to debug desktop notification delivery itself on any supported OS. This bypasses the real-`claude` harness and sends a hook event directly to the built binary, which is enough to answer:

- did our hook handler run
- did the notifier return success or error
- did the OS show the banner immediately, only in notification history/center, or not at all

Build the binary from the repo root:

macOS / Linux:

```bash
go build -o bin/claude-notifications ./cmd/claude-notifications
./bin/agent-notifications version
```

Windows PowerShell:

```powershell
go build -o bin/claude-notifications.exe ./cmd/claude-notifications
.\bin\claude-notifications.exe version
```

Trigger a direct desktop notification with a minimal `PreToolUse` payload:

macOS / Linux:

```bash
echo '{"session_id":"local-debug","tool_name":"ExitPlanMode"}' | ./bin/agent-notifications handle-hook PreToolUse
```

Windows PowerShell:

```powershell
'{"session_id":"win-debug","tool_name":"ExitPlanMode"}' | .\bin\claude-notifications.exe handle-hook PreToolUse
```

Windows Git Bash:

```bash
go build -o bin/claude-notifications.exe ./cmd/claude-notifications
echo '{"session_id":"win-debug","tool_name":"ExitPlanMode"}' | ./bin/claude-notifications.exe handle-hook PreToolUse
```

What to collect:

1. Whether the notification banner appears immediately.
2. Whether it appears only in the OS notification history / center.
3. The last lines from `notification-debug.log` in the repo root.
4. The output of the built binary's `version` command.
5. Relevant OS notification settings:
   - macOS: `System Settings > Notifications > Agent Notifications`
   - Linux: desktop-environment notification settings and whether the session is local desktop vs headless/remote
   - Windows: `Settings > System > Notifications > Claude Code Notifications`
6. On macOS / Linux, if click-to-focus is part of the report, whether clicking the notification activates the expected window.

Interpretation:

- If the log says the desktop notification was sent successfully but no banner appears, the problem is likely in the OS notification layer or app notification settings rather than in Claude hook parsing.
- If the log shows a notifier-specific error such as `beeep.Notify failed`, we likely have a platform integration bug.
- If the direct command works but notifications from Claude Code are still delayed or missing, the next place to inspect is the hook invocation path rather than notification delivery.

### Smoke test against the currently installed plugin

```bash
scripts/e2e-real-claude.sh smoke-installed
```

This runs a real `claude -p` command, forces a `Read` tool call against `.claude-plugin/plugin.json`, captures stdout/stderr, writes a separate Claude debug log, and checks the plugin log delta for hook activity.

### Smoke test via `--plugin-dir`

```bash
scripts/e2e-real-claude.sh smoke-plugin-dir
```

Use this when you want a real Claude run against the repo checkout without changing the installed marketplace/plugin source.

### Manual click-to-focus validation

```bash
scripts/e2e-real-claude.sh manual-click-installed
scripts/e2e-real-claude.sh manual-click-plugin-dir
```

This still requires a human check. Native desktop notification click handling is OS/window-manager specific, so the harness can trigger the notification and collect logs, but it cannot reliably assert that the correct window was focused after the click.

What to verify manually:

1. A desktop notification appears.
2. Clicking it focuses the exact Claude terminal/window that triggered the hook.
3. On Linux, verify it does not jump to a stale Terminator/X11 window.
4. On macOS, verify the right app/window becomes frontmost.
5. In Warp (v0.2026.05.27+): with two Warp tabs/panes, the click must land on the originating pane (and its agent chat), not merely bring Warp to the front. From a Warp pane:

```bash
scripts/warp-focus-check.sh notify
```

Then switch to another Warp tab and click the notification. `scripts/warp-focus-check.sh open` only checks Warp's URL handler, not the notification click path.

### Status / targeting

```bash
scripts/e2e-real-claude.sh status
```

By default the script targets `~/.claude`. To target another Claude config:

```bash
REAL_CLAUDE_HOME="$HOME/.claude-dev/claude-notifications-go" scripts/e2e-real-claude.sh status
```

## Switching Your Real Claude Between Local And Remote

When you must test the actual installed plugin inside your normal Claude environment, use:

```bash
scripts/dev-real-plugin.sh local
scripts/dev-real-plugin.sh status
scripts/dev-real-plugin.sh remote
```

There is also a convenience toggle:

```bash
scripts/dev-real-plugin.sh toggle
```

Recommended rule:

- Use `local` only for active validation.
- Use `remote` when you are done.

## Best Practices

- Prefer `scripts/dev-local-plugin.sh` before touching your real Claude environment.
- Prefer `scripts/e2e-real-claude.sh smoke-plugin-dir` when you need real hook execution but do not need to mutate the installed marketplace.
- Use `scripts/dev-real-plugin.sh local` only for end-to-end checks that depend on the real installed plugin path.
- Keep `make build` up to date before testing install/init flows so the plugin can reuse the local `bin/` binary.
- Treat click-to-focus as a smoke-plus-manual test: automate hook execution, then manually verify the actual focus behavior.
- Do not rely on shell startup hacks like exporting `WINDOWID` in `.bashrc` as the primary fix. Prefer runtime detection inside the plugin so we do not couple behavior to one shell profile.

## Suggested Validation Matrix

For changes to install/update logic:

1. `scripts/dev-local-plugin.sh install`
2. `scripts/dev-local-plugin.sh bootstrap`
3. `scripts/dev-local-plugin.sh status`

For changes to hooks/notifier/runtime behavior:

1. `scripts/e2e-real-claude.sh smoke-plugin-dir`
2. `scripts/e2e-real-claude.sh smoke-installed`

For click-to-focus changes:

1. `scripts/e2e-real-claude.sh smoke-plugin-dir`
2. `scripts/e2e-real-claude.sh manual-click-plugin-dir`
3. If needed, switch the real environment to local source and repeat with `manual-click-installed`

## Logs

- Plugin runtime log: `<plugin-root>/notification-debug.log`
- Claude process debug log: printed by `scripts/e2e-real-claude.sh` for each run

If a smoke test fails, keep both logs and the command output together when opening an issue or PR.

The checked-in `bin/agent-notifications` symlink delegates to `bin/claude-notifications`; building the legacy development target also makes the primary command available. Installation replaces both launchers with links to the same platform executable.
