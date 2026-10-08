# Platform release channels

The public installer reads `release-channels.tsv` from one immutable controller
commit. Each row binds an OS/architecture to a stable version tag, the tag's
release commit, an immutable source commit, and its platform branch.

| Platform | Qualified version | Source branch |
| --- | --- | --- |
| Linux amd64 / arm64, Windows amd64 | 1.48.4 | `release/platform-linux-windows` |
| macOS amd64 / arm64 | 1.48.4 | `release/platform-macos` |

All five platform rows, GitHub Latest and main manifests select 1.48.4. This
complete release includes Linux, Windows and macOS native/portable assets and
the signed, notarized macOS helper. A platform channel can also select an explicitly
qualified partial release even when GitHub labels it Pre-release.
No scan for the newest tag or fallback to Latest is used. Invalid/missing rows,
unsupported architectures and failed downloads stop selection before installation.

## Existing installations

Rerun the public install command once to adopt a channel. An ordinary Claude
marketplace checkout on `main` is moved using Claude's native settings writer;
custom repositories, branches and detached/pinned checkouts are retained.
The initial installation uses an immutable `dist/platform-source/SHA` tag and
verifies the checkout against its source SHA. Claude marketplace refs accept
branches/tags, so the tag provides an immutable snapshot without a moving ref. Successful setup then records the
platform branch for subsequent Claude plugin updates. Standalone Codex copies a
complete bundle; update it by rerunning setup. No new background updater is added.

Same-version source refresh temporarily retains the old cache in a private
`.channel-backup.*` directory. Restart Claude after migration; hooks from running
sessions can encounter the short cache replacement interval. A failed refresh
restores the old same-version cache and retains the incomplete copy for diagnosis.
Saved settings, plugin data and disabled state are not removed. Setup never uses
marketplace removal or plugin uninstall for channel migration.

Explicit release/installer URL overrides keep their operator semantics. A local
bundle's repair installer downloads its own manifest version, so an older macOS
release cannot silently downgrade a newer Linux/Windows bundle. A channel older
than an installed Claude version is rejected.

## Promotion order

1. Qualify and publish native assets according to `RELEASE.md`; leave release tags
   immutable. Skipped platforms retain their previously qualified release.
2. Prepare source branches from their respective release commits. Patch only
   distribution scripts as necessary, preserving all source/native manifest and
   `ConsumerVersion` values. Review the exact commits and test installation in new
   isolated TEST homes on the affected platforms. Verify release checksums and
   source provenance; never combine main's older manifest with newer binaries.
3. Publish the qualified platform branch heads and immutable
   `dist/platform-source/SHA` tags at those exact commits. Never move these tags. Source commits in the index must
   refer to those reviewed immutable commits, not moving branch names.
4. Update the complete index in one reviewed commit. Run the parser, loader,
   installer and cache-recovery tests. Deploy the loader pinned to that controller
   commit. Publish source branches before activating the index.
5. Future source promotions should change the plugin version with the native
   release, because Claude's ordinary updater reuses same-version caches. Use
   bootstrap again when adopting a distribution-only change at the same version.

Branches may advance after index selection: installation still uses the indexed
SHA, while subsequent Claude updates follow only qualified branch promotions.
Rollback the index/branch to a qualified snapshot; do not rewrite release tags or
silently downgrade users with a newer installed version.

## Future macOS candidates

`macos-qualification.yml` is a manual, artifact-only preparation workflow. It
builds and checks native Darwin amd64/arm64 packages and the signed, notarized
universal notifier from one selected source ref. It never updates this index,
platform branches, existing release assets or GitHub Latest. Its scoped checks
must be supplemented by the macOS desktop and installed-lifecycle qualification
listed in [the release checklist](RELEASE.md#claudenotifierapp-macos).

After an authorized candidate publication, follow the promotion order above for
both macOS rows only. Retain the currently qualified macOS version until that
candidate's public assets and source provenance are verified; preserve unrelated
Linux/Windows rows and global Latest.
