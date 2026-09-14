# Release Checklist

Step-by-step guide for publishing a new version.

## 0. Pre-release risk checklist

Blocking items for the first release that ships Codex support; steps 1-4 stay useful for every
later release that touches the hook pipeline.

1. **Assets before the bump.** Follow the release-branch order in steps 4-5: tag and publish
   assets first, land the bump on `main` last. Rationale in the callout under step 4.
2. **Canary the draft binary** (step 5): `version` must print the new version, and synthetic
   Claude and Codex `Stop` payloads must reach local recording sinks. A binary that cannot report its version
   is the one failure the auto-updater cannot recover from.
3. **Codex sandbox smoke.** With a throwaway `HOME` *and* `CODEX_HOME` (both are honored:
   Codex resolves the marketplace root from `HOME`/`USERPROFILE`, so the real `~/.agents` and
   `~/.codex` stay untouched): install the draft binary into its matching bundle, run
   `setup-codex --plugin-root BUNDLE`, complete the `/hooks` trust review, and confirm a real
   turn reaches a local recording sink. Test repeated setup and an existing Claude config.
   Record desktop banner/sound checks separately from webhook delivery. Do not also register
   the native plugin: duplicate registration can deliver twice. Destroy the sandbox afterwards.
4. **Release notes must state**, in user-facing wording:
   - Codex support is **beta**;
   - the `permission_request` status requires this version or newer (older binaries reject it
     in `suppressFilters` validation);
   - both products share one config file, so an existing webhook now also receives Codex
     notifications;
   - Windows support for the Codex route is not declared yet.
5. **Watch issues for the first day** after publishing.

Rollback: a bad binary is fixed forward (revert the code, ship a patch tag — the wrapper
re-installs on any version mismatch, so a downgrade under a higher version works). Bad scripts
or JSON are reverted on `main`; user caches pick that up on their next refresh.

## 1. Bump version

> **Frozen Claude Code identity:** do not rename any `claude-notifications-go` `name` in
> `.claude-plugin/plugin.json` or `.claude-plugin/marketplace.json`. These values identify the
> installed plugin, marketplace registration, cache, updater, and slash-command namespace.
> Product branding belongs in `displayName`, descriptions, and repository URLs. See
> [Claude plugin identity compatibility](CLAUDE_PLUGIN_IDENTITY.md).

Update the version string in **4 files** (5 occurrences total):

| File | Location | Count |
|------|----------|-------|
| `cmd/claude-notifications/main.go` | `const version = "X.Y.Z"` | 1 |
| `.claude-plugin/plugin.json` | `"version": "X.Y.Z"` | 1 |
| `.claude-plugin/marketplace.json` | `"version": "X.Y.Z"` | 2 |
| `.codex-plugin/plugin.json` | `"version": "X.Y.Z"` | 1 |

Quick check — all occurrences should match:

```bash
grep -rn '1\.[0-9]\+\.[0-9]\+' cmd/claude-notifications/main.go .claude-plugin/plugin.json .claude-plugin/marketplace.json .codex-plugin/plugin.json
```

`TestCodexManifestContract` in `cmd/claude-notifications` fails the build when any
of the five occurrences drift apart.

Codex note: `CN_CODEX_MIN_VERSION` in `bin/codex-hook-wrapper.sh` must equal the
version of the FIRST release that ships Codex support and must never change after
that release. If the first Codex release number changes during planning, update
that constant in the same release.

## 2. Update CHANGELOG.md

Add a new section at the top following [Keep a Changelog](https://keepachangelog.com/) format:

```markdown
## [X.Y.Z] - YYYY-MM-DD

### Added
- ...

### Changed
- ...

### Fixed
- ...
```

## 3. Run tests

```bash
make test-race
make lint
sh scripts/codex-release-gate_test.sh
```

Run `sh scripts/codex-release-gate.sh --diff PREVIOUS_TAG HEAD`, replacing
`PREVIOUS_TAG` with the previous reachable release tag. Use `--first-release` when
there is no previous tag. A `required` result requires the disposable Codex checks
and their evidence on the release PR; the selector itself does not run or prove E2E.

## 4. Commit, push, and wait for CI

> [!IMPORTANT]
> **Publish assets BEFORE the version bump reaches `main`.**
>
> Installed plugins refresh their cache from `main` and compare `plugin.json` with the
> installed binary. The moment `main` carries the new version, every user's next hook run
> tries to download release assets for it. If those assets do not exist yet, each hook run
> retries the download inside the 30s hook budget, so users get repeated stalls until the
> release finishes building.
>
> Prepare the bump on a release branch, tag that exact commit (`release.yml` triggers on the
> tag, not on `main`), qualify the draft, obtain explicit approval to publish that version,
> publish its assets, and only then fast-forward
> `main` to the same SHA. The tag stays valid because the SHA is unchanged, and the
> asset-missing window is zero.

```bash
git switch -c release/vX.Y.Z
git add -A
git commit -m "chore(release): vX.Y.Z"
git push -u origin release/vX.Y.Z
```

**Wait for ALL CI checks to pass before tagging:**

```bash
gh run list --limit 5          # check status
gh run watch <run-id>          # wait for a specific run
```

All three workflows must be green: Ubuntu CI, macOS CI, Windows CI. If any fail — fix, push again, and wait. Do NOT create the tag until CI is green.

## 5. Prepare and qualify a draft, then request publication

```bash
git tag vX.Y.Z                 # on the release branch commit
git push origin vX.Y.Z
gh run watch                   # wait for release.yml to finish
```

The workflow creates a **draft**, never an automatically published release. Inspect it with
`gh release view vX.Y.Z --json isDraft,assets` and download the assets into a disposable
test directory with `gh release download vX.Y.Z --dir TEST_DIRECTORY`.

Run the canary smoke on the downloaded draft assets, rather than a local build. This
catches a binary so broken it cannot self-heal: the wrapper only re-installs when it can read
both versions, so a binary that fails `version` leaves users stuck on it.

```bash
# in a scratch dir, with a sandboxed HOME
./claude-notifications-<platform> version                     # must print vX.Y.Z
# Exercise synthetic Claude and Codex payloads with local recording sinks.
# Keep HOME, CODEX_HOME and platform config/cache/temp directories in the sandbox.
```

After qualification, obtain the owner's explicit approval for **this version** before
publishing the draft with `gh release edit vX.Y.Z --draft=false`. Previous release
approvals do not carry forward. Verify the public assets and checksums, then land the
exact same commit on `main`:

```bash
git switch main && git merge --ff-only release/vX.Y.Z && git push origin main
```

## ClaudeNotifier.app (macOS)

ClaudeNotifier.app is **automatically built, signed, and notarized** by the `release.yml`
workflow as a `build-notifier` job. It runs in parallel with Go binary builds and the
resulting `ClaudeNotifier.app.zip` is included in the same GitHub Release.

The CI workflow:
1. Imports the Apple Developer certificate from GitHub Secrets
2. Builds a universal binary (arm64 + x86_64)
3. Signs with **Developer ID Application** + hardened runtime
4. Notarizes via `xcrun notarytool` and staples the ticket
5. Uploads `ClaudeNotifier.app.zip` as a release asset

### Required GitHub Secrets

| Secret | Description |
|--------|-------------|
| `APPLE_CERTIFICATE` | Base64-encoded .p12 export of Developer ID Application cert |
| `APPLE_CERTIFICATE_PASSWORD` | Password for the .p12 file |
| `APPLE_ID` | Apple ID email for notarization |
| `APPLE_PASSWORD` | App-specific password for notarization |
| `APPLE_TEAM_ID` | Apple Developer Team ID |

### Local build (optional)

```bash
make build-notifier                                      # ad-hoc or local cert signing
cd swift-notifier && bash scripts/build-app.sh --ci      # Developer ID + notarization (needs env vars)
```

## 6. Update release description

The auto-generated release description is minimal. Edit it with a human-readable summary:

```bash
gh release edit vX.Y.Z --notes "$(cat <<'NOTES_EOF'
## Bug Fixes

### Title ([#N](link))
Description of what was broken and how it was fixed.

## New Features

### Title ([#N](link))
Description of what was added and why.

---

📦 **[Installation](https://github.com/777genius/agent-notifications/blob/main/docs/INSTALLATION.md)** · 🔄 **[Updating](https://github.com/777genius/agent-notifications/blob/main/docs/INSTALLATION.md#updating)**

**Full Changelog**: https://github.com/777genius/agent-notifications/compare/vPREV...vX.Y.Z
NOTES_EOF
)"
```

## 7. Notify relevant issues/PRs

Comment on fixed issues and merged PRs with a link to the release:

```bash
gh issue comment N --body "Fixed in [vX.Y.Z](https://github.com/777genius/agent-notifications/releases/tag/vX.Y.Z)."
gh pr comment N --body "Released in [vX.Y.Z](https://github.com/777genius/agent-notifications/releases/tag/vX.Y.Z)."
```

## How auto-update works

Users don't need to manually download binaries after a plugin update:

1. User updates the plugin via `/plugin` menu
2. This updates `plugin.json` with the new version
3. On the next hook invocation, `bin/hook-wrapper.sh` compares the installed binary version with `plugin.json`
4. If versions differ, it runs `install.sh --force` to download the matching binary from GitHub Releases
5. User sees a `[claude-notifications] Updated to vX.Y.Z` message
