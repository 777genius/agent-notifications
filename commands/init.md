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

Existing valid config is a byte/mode/mtime-preserving no-op. Existing invalid config is an error. Missing automatic config is created at N; existing L remains selected; explicit E selects only that file. See [OS paths and environment rules](../README.md#manual-configuration). Hooks never initialize config.

If assets/registration succeeded but init failed, report partial success and the safe error/path. Retry only `config init` after resolving the cause; do not repeat downloads or registration. Do not claim success after a failed command. Do not add config writes to low-level asset installers.

Then run `/claude-notifications-go:settings` for [private revision-checked edits](settings.md). Save diagnostics privately; never print raw configuration or expanded secrets.

After a successful plugin install, agent-notify configure runs by default
(`--navigation none --allow-unknown-caller true --allow-caller-asserted false`
unless a route is supplied). Pass `--skip-agent-notify` to
keep hooks-only setup. If agent-notify setup fails, the plugin install still
counts as success; retry `setup-notifications configure` after fixing the cause.

```bash
SKIP_AGENT_NOTIFY=false
SEEN_AGENT_NOTIFY=false
CONFIGURE_ARGS=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --skip-agent-notify) SKIP_AGENT_NOTIFY=true; shift ;;
    --agent-notify) SEEN_AGENT_NOTIFY=true; shift ;;
    --navigation|--app|--team-id|--allow-unknown-caller|--allow-caller-asserted)
      [ "$#" -ge 2 ] || { echo "Missing value for $1" >&2; exit 1; }
      case "$1" in
        --navigation) [ "$2" = none ] || { echo "Invalid navigation: $2" >&2; exit 1; } ;;
        --app)
          case "$2" in /*) ;; *) echo "App path must be absolute." >&2; exit 1 ;; esac
          case "$2" in *..*) echo "App path must be a physical path." >&2; exit 1 ;; esac ;;
      esac
      CONFIGURE_ARGS+=("$1" "$2"); shift 2 ;;
    --request-permission|--json) CONFIGURE_ARGS+=("$1"); shift ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
done
if [ "$SEEN_AGENT_NOTIFY" = true ] && [ "$SKIP_AGENT_NOTIFY" = true ]; then
  echo "--agent-notify and --skip-agent-notify are mutually exclusive." >&2
  exit 1
fi
if [ "$SKIP_AGENT_NOTIFY" = true ] && [ "${#CONFIGURE_ARGS[@]}" -ne 0 ]; then
  echo "Route flags require --agent-notify." >&2
  exit 1
fi
if [ "$SKIP_AGENT_NOTIFY" != true ]; then
  nav="" app="" team="" unknown="" asserted=""
  i=0
  while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
    case "${CONFIGURE_ARGS[$i]}" in
      --navigation|--app|--team-id|--allow-unknown-caller|--allow-caller-asserted)
        i=$((i + 1))
        [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ] || { echo "Missing value for ${CONFIGURE_ARGS[$((i - 1))]}" >&2; exit 1; }
        case "${CONFIGURE_ARGS[$((i - 1))]}" in
          --navigation) nav="${CONFIGURE_ARGS[$i]}" ;;
          --app) app="${CONFIGURE_ARGS[$i]}" ;;
          --team-id) team="${CONFIGURE_ARGS[$i]}" ;;
          --allow-unknown-caller) unknown="${CONFIGURE_ARGS[$i]}" ;;
          --allow-caller-asserted) asserted="${CONFIGURE_ARGS[$i]}" ;;
        esac ;;
      --json|--request-permission) ;;
      *) echo "unknown option: ${CONFIGURE_ARGS[$i]}" >&2; exit 1 ;;
    esac
    i=$((i + 1))
  done
  if [ -z "$nav" ] && [ -z "$app" ] && [ -z "$team" ] && [ -z "$unknown" ] && [ -z "$asserted" ]; then
    CONFIGURE_ARGS+=(--navigation none --allow-unknown-caller true --allow-caller-asserted false)
  elif [ "$nav" = none ]; then
    if [ -n "$app" ] || [ -n "$team" ]; then
      echo "navigation none cannot combine with --app/--team-id." >&2; exit 1
    fi
    case "$unknown" in true|false) ;; *) echo "navigation none requires --allow-unknown-caller and --allow-caller-asserted." >&2; exit 1 ;; esac
    case "$asserted" in true|false) ;; *) echo "navigation none requires --allow-unknown-caller and --allow-caller-asserted." >&2; exit 1 ;; esac
  elif [ -n "$nav" ]; then
    echo "Invalid navigation: $nav" >&2; exit 1
  elif [ -z "$app" ] || [ -z "$team" ] || [ -z "$unknown" ] || [ -z "$asserted" ]; then
    echo "Incomplete route; supply --app, --team-id, and both consent flags." >&2; exit 1
  else
    case "$unknown" in true|false) ;; *) echo "Invalid allow-unknown-caller: $unknown" >&2; exit 1 ;; esac
    case "$asserted" in true|false) ;; *) echo "Invalid allow-caller-asserted: $asserted" >&2; exit 1 ;; esac
  fi
fi
INSTALLER="${CLAUDE_PLUGIN_ROOT}/bin/install.sh"
curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/install.sh -o "$INSTALLER"
chmod +x "$INSTALLER"
"$INSTALLER"
if [ "$SKIP_AGENT_NOTIFY" != true ]; then
  NOTIFY_BIN="${CLAUDE_PLUGIN_ROOT}/bin/claude-notifications"
  if [ ! -x "$NOTIFY_BIN" ]; then
    echo "agent-notify setup skipped; installer binary not found. Plugin install succeeded." >&2
  elif ! "$NOTIFY_BIN" setup-notifications configure --provider claude "${CONFIGURE_ARGS[@]}"; then
    echo "agent-notify setup failed; plugin install succeeded. Retry: \"$NOTIFY_BIN\" setup-notifications configure --provider claude ${CONFIGURE_ARGS[*]}" >&2
  fi
fi
```
