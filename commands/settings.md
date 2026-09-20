---
description: Configure shared notification settings with revision-checked edits
disable-model-invocation: true
allowed-tools: Bash, AskUserQuestion
---

# Agent Notifications Settings

This wizard edits the single shared configuration for Claude, Codex, and future adapters. Follow this recipe only with a runtime supporting the config commands below. Never fall back to direct full-document writes or bundle copies when a command is unavailable.

## 1. Discover the executable and inspect

Use the adapter's recorded installation or `CLAUDE_PLUGIN_ROOT` / `PLUGIN_ROOT` to locate the installed executable and sound resources. Honor the existing `CLAUDE_NOTIFICATIONS_BIN` executable override. Select the binary for the current platform (including the native Windows `.exe`); do not run another platform's binary or scan caches for the newest version. These locations select resources only, never the user config.

Set `NOTIFICATIONS_BIN` to that verified executable, then run:

```bash
"$NOTIFICATIONS_BIN" config path --json
"$NOTIFICATIONS_BIN" config inspect --json
```

Do not install/update automatically if unavailable: explain that a matching config-capable runtime is required, and follow [init](init.md)'s recovery guard first. No downloadable bridge release is promised.

Use the inspect snapshot, not a separately computed path. Its safe response has this shape (values below are illustrative):

```json
{
  "selection": {"path": "/absolute/selected/config.json", "source": "universal", "exists": true, "diagnostics": []},
  "revision": "opaque-token",
  "schemaVersion": 1,
  "valid": true,
  "settings": {
    "desktopEnabled": true,
    "desktopSound": true,
    "volume": 1,
    "statuses": {
      "question": {"enabled": true, "desktopEnabled": true, "webhookEnabled": false}
    }
  }
}
```

On failure `errorCode` may be present. Stop on invalid, inaccessible, unsupported-schema or recovery-required config; never substitute defaults or a bundle. For a genuinely missing destination, explain create-only `config init`, run it only as part of requested setup, then inspect again. Existing valid init is a no-op; invalid existing config is an error. `config init --from FILE` is explicit recovery into a missing destination only, never reset/migration.

Keep the opaque revision from the successful inspect for saving. Free-form sound/device/webhook values and unknown fields are intentionally omitted. Absence in this projection does NOT mean unset. Never serialize inspect as config, print raw config, or read raw secrets merely to populate the wizard. If raw access is truly necessary, explain why and obtain explicit user authorization first; prefer unchanged defaults and new choices.

## 2. Ask only for requested changes

Use AskUserQuestion to identify the settings the user wants to change. Every question defaults to **Keep unchanged**. Skipped or unanswered questions produce no operation. A request for volume and ONE status sound must yield exactly two leaf edits; every other status, channel override, webhook secret/header/payload, filter and future-agent field stays untouched.

- Volume: keep unchanged, or choose 0–100% mapped to a JSON number 0–1 (70% → 0.7; zero is valid).
- Status sound: ask which status, then keep unchanged or choose an available sound. Discover built-in files from the installed resources; macOS can also use `/System/Library/Sounds/*.aiff`. Reject unknown choices instead of silently substituting a sound. Store built-ins literally as `${AGENT_NOTIFICATIONS_ROOT}/sounds/<file>`; resource expansion is for playback only.
- Status enabled: ask separately for each requested status. Never turn an unselected list of statuses into disabled statuses. Set only `/statuses/<status>/enabled` when explicitly requested.
- Desktop enabled/sound, per-status desktop/webhook enabled, and audio device: change only the specific requested leaf. System-default device is an explicit empty string choice, not the default answer.
- Webhooks: keep unchanged by default. An explicit disable changes only `/notifications/webhook/enabled`; it does not reset preset, URL, headers or payload. For new setup collect only the requested supported leaves from the [webhook guide](../docs/webhooks/README.md). Never insert placeholder URLs or replace the entire webhook object. Prefer literal environment references for credentials; never echo credentials in summaries.

Users may request previews at any time (“play”, “preview”, “прослушать”, “проиграть”). Resolve only a known sound resource and invoke the installed `sound-preview` with quoted arguments and `--volume 0.3`. A specifically requested volume preview may use that volume. Preview does not edit settings; never trigger hooks or webhooks as a wizard test. Offer `list-devices` for an explicitly requested audio device change.

## 3. Prepare a private patch and save with CAS

Summarize only requested changes, redacting secret values, and confirm ambiguous choices. No answers means no write. Build a JSON patch, not an expanded Config. Example for exactly volume and question sound:

```json
{
  "set": {
    "/notifications/desktop/volume": 0.7,
    "/statuses/question/sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/question.mp3"
  },
  "remove": []
}
```

Use JSON Pointer escaping (`~0` for `~`, `~1` for `/`) where needed. Set values are raw JSON; `null` is not deletion. `remove` contains only explicitly requested removals. No root replacement, parent-object replacement, overlapping operations or schemaVersion edit. Arrays may only be replaced as a whole when explicitly requested and supported by the CLI validator.

Create a private temporary directory with `umask 077` and `mktemp -d`, disable tracing BEFORE handling input, and write only the requested patch there. Use a JSON serializer through private input for arbitrary answers; never interpolate answers into shell code, command arguments, unquoted heredocs, or `eval`. Never show the patch if it contains credentials. On Windows ensure a current-user/SYSTEM private ACL for temporary input; POSIX mode bits alone do not prove Windows privacy. If a private input location is unavailable, stop without writing.

For the non-secret two-edit example above, this Bash recipe preserves the literal placeholder. `REVISION` is the opaque metadata token from inspect, not a setting value:

```bash
(
  set +x
  umask 077
  PATCH_DIR=$(mktemp -d "${TMPDIR:-/tmp}/agent-notifications-edit.XXXXXXXX") || exit 1
  trap 'rm -rf -- "$PATCH_DIR"' EXIT
  cat > "$PATCH_DIR/patch.json" <<'JSON'
{"set":{"/notifications/desktop/volume":0.7,"/statuses/question/sound":"${AGENT_NOTIFICATIONS_ROOT}/sounds/question.mp3"},"remove":[]}
JSON
  "$NOTIFICATIONS_BIN" config edit --stdin --expect-revision "$REVISION" < "$PATCH_DIR/patch.json"
)
```

Replace this example only with the user's actual requested operations. Do not pass secrets in argv, enable tracing, write the canonical file yourself, or copy it to the bundle. The Store preserves untouched raw fields and literal templates; formatting can normalize on a real edit.

On `ConfigConflict`, keep the user's answers privately, inspect again, and explain that the file or selected path changed. Ask an explicit user decision: apply the retained edits to this new snapshot, revise them, or cancel. Do not automatically retry, rebase, overwrite, or adopt a new token. Only explicit approval permits a new patch submission with the new revision. Repeat this decision for any further conflict. For `ConfigCommitUncertain`, inspect and reconcile the visible state before any further action; never blindly repeat a write. Clean private inputs after success/cancel; retain answers only for the active conflict decision.

## 4. Verify and summarize

After confirmed success, run `config inspect --json` again. Report the selected path and only the requested changes; do not claim untouched fields were reset or infer omitted secret/sound values. Report failures honestly. Sound previews remain optional and user-requested.

See [configuration paths and compatibility](../docs/CONFIGURATION.md#manual-configuration). Resource, permission-marker, venv and state paths, executable names and plugin IDs remain unchanged.

Schema 2 agent overrides are manual-only. This wizard edits global settings only; never submit `/agents` edits or flatten an effective agent profile into the document. Existing agent overrides are preserved by `config edit`.
