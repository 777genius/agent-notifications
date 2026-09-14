# Plugin Compatibility

Compatible with other Claude Code plugins that spawn background Claude instances.

## double-shot-latte

**[double-shot-latte](https://github.com/obra/double-shot-latte)** — auto-continue plugin that uses a background Claude instance for context evaluation. Notifications are automatically suppressed for the background judge process (via `CLAUDE_HOOK_JUDGE_MODE=true` environment variable).

## For plugin developers

If you're developing a plugin that spawns background Claude instances and want to suppress notifications, set `CLAUDE_HOOK_JUDGE_MODE=true` in the environment before invoking Claude.

To disable this behavior and receive notifications even in judge mode, set in the shared file selected by `config path`:

```json
{
  "notifications": {
    "respectJudgeMode": false
  }
}
```

## Shared settings and older versions

All new adapters use the [same resolver and environment context](CONFIGURATION.md#manual-configuration): E → existing L → N. E must reach every adapter; old binaries ignore it. Existing L with v1-shaped JSON remains readable by old Go readers, but old wizards discard unknown fields and cannot safely write concurrently with the Store. N requires a bridge-aware runtime or explicit stopped-writer recovery to one legacy canonical file. There is no promised downloadable bridge release. Native OS E2E/Windows ACL qualification remains pending for this rollout.

Personalized or unknown cache-only configs require explicit `config init --from FILE` into a missing selected destination before any updater runs; no automatic copying, reset or bundle fallback. Resource paths and plugin IDs do not change.
