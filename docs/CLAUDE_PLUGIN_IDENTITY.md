# Claude Plugin Identity Compatibility

The public product and repository are named **Agent Notifications** and
`agent-notifications`. The following Claude Code identifiers intentionally retain the legacy
name and must not be changed as part of branding or routine cleanup:

- `.claude-plugin/plugin.json` -> `name`
- `.claude-plugin/marketplace.json` -> top-level `name`
- `.claude-plugin/marketplace.json` -> `plugins[].name`
- the resulting `/claude-notifications-go:*` command namespace

Use `displayName: "Agent Notifications"`, descriptions, and the current repository URL for
user-facing branding.

## Why the identifiers are frozen

Claude Code keys installations and updates by `plugin-name@marketplace-name`. This was also
verified end to end on Claude Code 2.1.265 on 2026-09-10 using an isolated config and a
throwaway project:

- Renaming the marketplace, marketplace entry, and plugin manifest left the existing install
  enabled but unresolved: `Plugin claude-notifications-go not found in marketplace
  claude-notifications-go`.
- Renaming only the plugin manifest allowed an update, but operations using the new internal
  name were inconsistent: disable reported `already disabled` while the installed plugin was
  enabled.
- Keeping all three legacy `name` values and changing only `displayName` updated cleanly and
  rendered as `Agent Notifications (claude-notifications-go)`.

There is no documented alias or atomic rename mechanism that migrates existing marketplace
registrations, installed-plugin records, cache paths, enabled state, and command namespaces.
The GitHub repository URL may change independently and does not require changing these IDs.

## Repository renames still break existing installs

Even though the marketplace/plugin *name* stays `claude-notifications-go`, users who declared
that marketplace before a repository rename (e.g. the `claude-notifications-go` ->
`agent-notifications` rename) get stuck: Claude Code stores the declared source
(`extraKnownMarketplaces` in settings) and refuses to silently re-point an existing declaration
at a different repo, failing `marketplace add` with `its network source differs from the one
declared for it in settings`. There is no officially documented transparent migration for a
marketplace *source* change (the `renames` map in `marketplace.json`, added in Claude Code
2.1.193, only covers plugin name changes within a marketplace, not the marketplace's own
repo). Confirmed against the official docs and reproduced end to end on 2026-09-11.

`marketplace update` (the path used by the in-app "Update marketplace" action and Claude
Code's periodic background check) is unaffected — it pulls the existing clone via git, which
follows GitHub's redirect from the old repo name. Only `marketplace add` — which
`bin/bootstrap.sh` always tries first, including on repeat runs — hits the conflict. `setup_marketplace()`
in `bin/bootstrap.sh` detects this specific error, confirms the currently declared repo is one
of our own retired names (`LEGACY_MARKETPLACE_REPOS`), and re-registers the marketplace
(`remove` + `add`) automatically. This only resets the marketplace/plugin *registration*; the
user's saved notification settings live in a separate config file and are untouched, and the
rest of the script reinstalls the plugin right after. Any future repository rename must add the
old repo slug to `LEGACY_MARKETPLACE_REPOS` in `bin/bootstrap.sh`.

## Rule for a future migration

Do not change these identifiers unless a dedicated migration ships with disposable-profile
E2E coverage for existing installs, update/rollback, enable/disable/uninstall, duplicate-hook
prevention, and both old and new command namespaces. A new identifier must be treated as a
new plugin identity, not as a routine rename.
