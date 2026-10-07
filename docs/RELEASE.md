# Release Checklist

Step-by-step guide for publishing a new version.

For a release that skips a platform, promote only its qualified
[platform release channels](PLATFORM_RELEASE_CHANNELS.md). Keep skipped-platform
rows and global Latest on the previously qualified version. Publish source
branches before activating the immutable source SHAs in the channel index.

v1.48.1 is a Linux amd64/arm64 and Windows amd64 partial release. macOS stays
on v1.46.1, and GitHub global Latest stays v1.46.1. There are no Darwin binaries,
portable packages or ClaudeNotifier.app assets in v1.48.1.

The earlier v1.48.0 draft and tag are retained unpublished: a repeated Windows
clock tick exposed a floating-point admission-budget bug. v1.48.1 fixes it
without extending the four-second limit; qualification must use the new binary.

## 0. Pre-release risk checklist

Run these checks for releases that touch the hook pipeline.

1. **Assets before promotion.** Follow the release-branch order in steps 4-5: tag and publish
   qualified assets first. Fully qualified all-platform releases land the version bump
   on `main` last. Partial releases follow the platform-channel procedure; v1.48.1
   keeps all five legacy versions on `main` at 1.46.1. See the callout under step 4.
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
4. **Release notes must state**, when relevant to the update:
   - minimum runtime versions required by new statuses or settings (older binaries may reject
     unknown statuses in `suppressFilters` validation);
   - both products share one config file, so an existing webhook now also receives Codex
     notifications;
   - any concrete platform or hook limitations introduced or changed by the release.
5. **Watch issues for the first day** after publishing.

Rollback: a bad binary is fixed forward (revert the code, ship a patch tag — the wrapper
re-installs on any version mismatch, so a downgrade under a higher version works). Bad scripts
or JSON are reverted on `main`; user caches pick that up on their next refresh.

## 1. Bump version

> **Frozen Claude identity:** do not rename any `claude-notifications-go` `name` in
> `.claude-plugin/plugin.json` or `.claude-plugin/marketplace.json`. These values identify the
> installed plugin, marketplace registration, cache, updater, and slash-command namespace.
> Product branding belongs in `displayName`, descriptions, and repository URLs. See
> [Claude plugin identity compatibility](CLAUDE_PLUGIN_IDENTITY.md).

Update the version string in **4 files** (5 occurrences total):

| File | Location | Count |
|------|----------|-------|
| `internal/config/runtime.go` | `var ConsumerVersion = "X.Y.Z"` | 1 |
| `.claude-plugin/plugin.json` | `"version": "X.Y.Z"` | 1 |
| `.claude-plugin/marketplace.json` | `"version": "X.Y.Z"` | 2 |
| `.codex-plugin/plugin.json` | `"version": "X.Y.Z"` | 1 |

Quick check — all occurrences should match:

```bash
grep -rn '1\.[0-9]\+\.[0-9]\+' internal/config/runtime.go .claude-plugin/plugin.json .claude-plugin/marketplace.json .codex-plugin/plugin.json
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
> tag, not on `main`), qualify the draft, follow the owner request scope below,
> publish its assets when authorized. Only a release qualified for every supported
> platform may fast-forward `main` to that same SHA. For partial v1.48.1, do not
> fast-forward the version bump to `main`: promote the Linux/Windows source and
> channel controller as described below, retaining all five legacy macOS versions.
> The native tag stays immutable and the asset-missing window is zero.

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

Before creating the v1.48.1 draft, `release.yml` checks the downloaded artifacts
on three native targets with `scripts/release-artifact-e2e.py`: Linux amd64/arm64
and Windows amd64. The workflow has no macOS build, signing or helper upload jobs.
Artifact checks do not establish visible desktop banners.

OpenCode qualification uses the same-run binaries in seven Linux/Windows cells:
V1 1.18.33 and V2 2.0.21 on Linux amd64/arm64 and Windows amd64, plus V1 1.18.34
on Linux amd64. SHA-256-pinned host archives, native executable hashes, exact
release source and binary hashes bind the reports to the candidate. The checks
cover completion delivery, managed update and revocation in disposable projects.
They do not replace the broader semantic matrix or desktop delivery evidence.

For immutable v1.48.1 artifacts, the `release-recovery` plan in
`opencode-native-e2e.yml` reuses the original release run rather than rebuilding.
Run `first-linux-amd64-v2` before `all-seven`. Fresh finite clock and packaged
reader observations must pass semantic validation before the unchanged installed
`business` suite runs. Publication requires successful reports for all seven
cells. This path does not qualify the full native clock, source epoch, time
policy, platform lifetime or visible desktop delivery; the full/smoke denial
gate remains unchanged. Retain the original failure and all raw recovery reports.
If only Windows fails, use `windows-two` to repeat those two cells. The Windows
recovery verifies the original executable's unique canonical LF bundle, then
uses the unchanged registration oracle to check installed raw bytes and the
real ownership hash after setup, update and reinstall. The original Windows
ACL helper uses verified prefetched modules in a private environment with external
Go module fetching disabled; this does not claim general outbound network isolation.
It must successfully set the protected DACL. These preparation receipts do not
qualify the full native gate.

The v1.48.1 package qualification helper accepts lightweight release tags only;
annotated tags fail closed before any package execution.

Same-run custody explicitly selects `--scope linux-windows` and seals only those
seven cells. The general fixture still requires all eleven cells by default;
partial custody rejects skipped-platform requests and missing requested binaries.
Neither this archive nor earlier macOS evidence qualifies a skipped platform.
Selected installed-client navigation and Windows interactive toast/cold callback
qualification remain incomplete; protocol and artifact checks do not close those gaps.

The self-contained bundle incorporates UAP SDK 0.3.0 built from the reviewed
vendored tarball. Preparation verifies
its lockfile integrity and an identical rebuild of the tracked embedded bundle.
Publishing that separate SDK requires separate owner authorization; it is not a
prerequisite for this self-contained consumer release. Adding CI lanes does not
establish that they have passed.

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

After qualification, follow the owner's request for this release:

- A request to **make a release** authorizes publishing the qualified version prepared for
  that request. Publish the draft with `gh release edit vX.Y.Z --draft=false --latest=false`
  for a partial release without asking for a second approval. State the candidate version in a progress update so the owner can
  correct it before publication.
- A request to **make a draft release** authorizes only the draft. Leave it unpublished
  until the owner asks to publish it.
- If there was no request to publish this release, or the candidate version or scope
  materially changed after the request, obtain explicit owner approval for the final version
  before publishing. Approval for an earlier release does not carry forward.

For v1.48.1, publish the qualified partial draft with:

```bash
gh release edit v1.48.1 --draft=false --prerelease=false --latest=false
gh release view v1.48.1 --json isDraft,isPrerelease,assets
gh api repos/777genius/agent-notifications/releases/latest --jq .tag_name
# Latest must still be v1.46.1.
```

Verify public assets and checksums, then promote only the Linux/Windows platform
source branch and its immutable channel-index SHAs in the order documented in
[platform release channels](PLATFORM_RELEASE_CHANNELS.md). Preserve both macOS
rows and global Latest at v1.46.1. Do not use the general `main` promotion below
as a substitute for the partial platform promotion.

For a fully qualified release across all published platforms, land the exact
same release commit on `main`:

```bash
git switch main && git merge --ff-only release/vX.Y.Z && git push origin main
```

## Partial releases without macOS

When macOS signing is unavailable, qualify and publish only the Linux/Windows
assets with `--latest=false`. Keep the macOS channel and GitHub Latest at their
previous qualified version. Promote Linux/Windows source branches and immutable
source tags, then activate only their index rows as described in
[platform channels](PLATFORM_RELEASE_CHANNELS.md).

Keep all five native version occurrences on `main` at the legacy macOS version.
Existing Claude marketplace users on `main` download the version in its manifest;
a newer version without Darwin assets would stall their updater. Integrate the
release and channel PR histories together with the main-version restoration in
one reviewed promotion, so no intermediate bump is published on `main`. The
release tag and Linux/Windows source retain their new version and exact source SHA.
The assets-before-main bump instructions above apply to a full-platform release.

## ClaudeNotifier.app (macOS)

v1.48.1 does not build or publish ClaudeNotifier.app. macOS consumers retain the
signed and notarized helper from their qualified v1.46.1 channel.

Future macOS candidates use the manual `macos-qualification.yml` workflow.
Signing runs only when the repository owner dispatches from
`release/macos-signing`, inside the `macos-signing` GitHub environment. PR and
`smoke-notary-*` tag events do not sign. The qualification caller fails before
building artifacts when the actor or ref is untrusted.

After review and merge, the owner fast-forwards the trusted signing branch to
the exact reviewed `main` SHA. Never force-push this branch or dispatch signing
from an implementation branch. All five candidate version values must match the
input. For example, replace `REVIEWED_MAIN_SHA` and `vX.Y.Z` with verified values:

```bash
git fetch origin main
git merge-base --is-ancestor REVIEWED_MAIN_SHA origin/main
git push origin REVIEWED_MAIN_SHA:refs/heads/release/macos-signing
gh workflow run macos-qualification.yml --ref release/macos-signing -f candidate_version=vX.Y.Z
# Notifier-only verification uses the same trusted branch and environment:
gh workflow run notifier-signing-smoke.yml --ref release/macos-signing -f skip_notarize=false
```

`use_github_runner=true` may be supplied to either workflow when the configured
runner is unavailable. These commands qualify artifacts only; they do not
publish a release or promote platform channels.

The workflow builds the four native Go executables and portable package on each
Darwin architecture, then runs the existing CLI/config/local-webhook artifact
checks in disposable profiles. It also calls the canonical notifier signing
workflow with notarization required: universal Swift build, Developer ID team
`86399583GS`, hardened runtime, API-key notarization and stapled-ticket validation.
Each Mach-O slice must belong to that team. The notifier archive includes its
managed-runtime sidecar and same-run custody report. Uploaded Go artifacts include
source/version records and SHA-256 checksums. These are Actions artifacts only;
this workflow does not create or publish a release or promote any channel.

A green run proves those scoped artifact/signature checks. It does not prove
visible notifications, cold callbacks or the complete macOS OpenCode installed
lifecycle. Complete those candidate-specific checks on disposable test identities
before declaring macOS ready. Then, with publication authorization, download the
same-run artifacts, verify their checksums/custody, upload the Darwin binaries,
portable archives and notifier archive (as `ClaudeNotifier.app.zip`) to the
candidate draft, and publish with `--latest=false` for a macOS-only release.
Promote both macOS source/index rows using the platform-channel procedure only
after public assets are verified. Linux/Windows rows, legacy `main` versions and
global Latest remain unchanged unless a separately qualified release includes them.

### Required macos-signing environment secrets

| Secret | Description |
|--------|-------------|
| `APPLE_CERTIFICATE` | Base64-encoded .p12 export of the new Developer ID Application certificate |
| `APPLE_CERTIFICATE_PASSWORD` | Password for the .p12 file |
| `APPLE_TEAM_ID` | Must equal `86399583GS` |
| `APPLE_API_KEY_BASE64` | Base64-encoded team App Store Connect API .p8 key |
| `APPLE_API_KEY_ID` | API key ID |
| `APPLE_API_ISSUER` | Team API issuer UUID |

Apple ID/password credentials are not used. CI decodes the API key to a private
mode-600 file under `RUNNER_TEMP`, exports only its path, and requires the API key
ID and issuer. Local `build-app.sh --ci --no-register` uses an absolute
`APPLE_API_KEY` path plus `APPLE_API_KEY_ID`, `APPLE_API_ISSUER` and the pinned team.
`APPLE_SIGNING_KEYCHAIN` optionally selects a dedicated certificate keychain.

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
