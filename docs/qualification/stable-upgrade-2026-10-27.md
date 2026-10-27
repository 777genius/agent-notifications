# Published stable upgrade qualification

The Linux amd64 installer/release gate now exercises real published bytes, real
Claude Code and Codex CLIs, and the candidate's actual portable archive. It
requires a clean installation, a previous-stable upgrade, and a repeated
candidate installation. The existing macOS LaunchServices TEST fixture remains
the separate operating-system registration gate.

## Reproduced before the fix

The published v1.49.8 release (release ID `424253661`, published
`2026-10-26T13:04:11Z`) installed successfully into an isolated TEST profile and
wrote schema 4. Claude Code `2.1.311` and Codex `0.162.0` independently listed
the product and a separately installed foreign plugin as enabled. No client
stub was used.

Updating that profile with source commit
`270f3f0b32167dea5124d9891ae1f242c80f6446` failed with exit 1 and
`activation_incomplete`. The reported next action was:

> state schema 4 requires explicit migration; run agentplugins migrate-state --dry-run, then agentplugins migrate-state

The immutable release IDs, asset hashes, old state hash, native listings, and
observed failure are recorded in
`stable-upgrade-schema4-base-2026-10-27.json`. The baseline executable was built
with `-trimpath -ldflags='-s -w'`; stripping matches release packaging and stays
within the managed runtime's existing payload limit.

Latest was v1.49.10 at preparation, release ID `425286051`, published
`2026-10-27T10:14:21Z`. It writes schema 5. The gate therefore retains the real
v1.49.8 history case alongside automatic previous-stable selection, so a newer
Latest cannot erase schema-4 upgrade coverage.

## What the gate checks

`scripts/stable-upgrade-e2e.py prepare` resolves Latest once, rejects drafts and
prereleases, downloads by immutable asset ID, and checks both GitHub's published
SHA-256 digest and the release's checksum file. It writes `stable-pin.json` with
the exact tag, release ID, asset IDs and hashes. `run` verifies the local hashes
and never discovers or downloads a release.

Each clean/upgrade case uses a new TEST profile and an allowlisted environment.
The old release installs through its real runtime publication adapter and public
setup wizard. Before upgrading, the harness seeds user settings, a separately
native-installed foreign plugin, unrelated data, and user data in the existing
product's owned data directory. It records the old state's exact bytes.

The candidate must preserve installation identity and user/foreign data, produce
exactly one byte-identical legacy backup when a schema migration is needed,
install its own executable into the managed runtime, and appear enabled exactly
once in each native client's plugin listing. The portable archive must embed
the exact candidate executable. Repeating the candidate must preserve state,
registrations and config bytes and create no additional migration backup.

The recovery workflow runs this gate in its existing Linux recovery job. The
three release workflows run it against their downloaded release artifacts before
release creation can proceed. Each run preserves pins, reports, stdout/stderr,
and the pre-upgrade state, without uploading duplicate executable payloads.

## LaunchServices and qualification boundaries

`bin/e2e_launchservices_macos_test.sh` executes only on an ephemeral GitHub macOS
runner. Its unique TEST bundle ID verifies that private acquisition stages never
register, both published callback generations remain registered after an
update, and disposable acquisition has no registration side effect. It also
creates a second owned TEST app with the same ID outside the managed runtime,
records the actual preferred path before and after the update, and checks that
the foreign app's bytes and registration survive. A remaining preference
conflict is reported as an observation requiring explicit reconciliation. The
test never resets the LaunchServices database or removes registrations outside
its owned TEST paths.

Native plugin listings prove registration and enablement. These checks do not
claim a model turn, MCP tool invocation, application restart, visible desktop
delivery, or a queued callback click. The LaunchServices check requires macOS;
a Linux skip supplies no macOS evidence. Restricted `/proc` environments can
reject the native process containment prerequisite, and such a refusal is a
failure rather than substituted client evidence.

## Running locally

Use Linux amd64, Python 3.11+, the candidate executable/portable ZIP, and actual
Claude and Codex executable paths. Prepare once, then reuse that frozen input:

```sh
python3 -B scripts/stable-upgrade-e2e.py prepare --output /tmp/TEST-stable-pin
python3 -B scripts/stable-upgrade-e2e.py run \
  --pin /tmp/TEST-stable-pin/stable-pin.json \
  --candidate-binary /absolute/path/claude-notifications-linux-amd64 \
  --candidate-package /absolute/path/agent-notify-portable-linux-amd64.zip \
  --claude /absolute/path/claude --codex /absolute/path/codex \
  --output /tmp/TEST-stable-upgrade
```

For the permanent published schema-4 case, prepare with `--stable-tag v1.49.8`.
`--cases upgrade` selects only the upgrade case when reproducing a regression;
CI and release gates use the default clean and upgrade cases, each with its
unchanged-repeat check.
