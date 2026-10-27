#!/bin/bash
# Focused parent orchestration regression; real registry bytes use the Go/E2E suites.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SANDBOX=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-state-migration-TEST-XXXXXX")
SANDBOX=$(cd -P "$SANDBOX" && pwd -P)
trap 'rm -rf "$SANDBOX"' EXIT
test_env_setup "$SANDBOX"
sed '/^main "\$@"$/d' "$ROOT/bin/bootstrap.sh" > "$SANDBOX/functions.sh"

# Parent migration is ordered before both sequential and parallel writers. The
# peers below replace acquisition/client effects only; real main, fixed scalar
# parsing, confirmation handoff, progress capture and failure reporting execute.
for migration_case in sequential parallel current refused old-helper; do (
    source "$SANDBOX/functions.sh"
    export TEST_MIGRATION_ROOT="$SANDBOX/migration-$migration_case"
    export TEST_MIGRATION_CASE="$migration_case"
    mkdir -p "$TEST_MIGRATION_ROOT"
    export TEST_MIGRATION_TRACE="$TEST_MIGRATION_ROOT/trace"
    : > "$TEST_MIGRATION_TRACE"
    cat > "$TEST_MIGRATION_ROOT/helper" <<'MIGRATION_PEER'
#!/bin/bash
set -eu
if [ "$1" = config ]; then printf '%s\n' "$TEST_MIGRATION_ROOT/config.json"; exit 0; fi
printf '%s\n' "$2" >> "$TEST_MIGRATION_TRACE"
case "$2" in
    migration-capabilities)
        [ "$TEST_MIGRATION_CASE" != old-helper ] || exit 2
        printf 'state-migration-v1\n' ;;
    repair-capabilities) exit 2 ;;
    prepare)
        while [ "$#" -gt 0 ]; do
            if [ "$1" = --intent-file ]; then printf '{}\n' > "$2"; break; fi
            shift
        done
        printf 'prepared\n' ;;
    intent-args)
        required=true; [ "$TEST_MIGRATION_CASE" != current ] || required=false
        printf '%s\0' home "$TEST_MIGRATION_ROOT" control-root "$TEST_MIGRATION_ROOT/agent-notifications" \
            global-config "$TEST_MIGRATION_ROOT/config.json" codex-home "$TEST_MIGRATION_ROOT/codex" \
            codex-mcp-config "$TEST_MIGRATION_ROOT/codex/config.toml" codex-executable "$TEST_MIGRATION_ROOT/codex-cli" \
            state-migration-required "$required" ;;
    preflight) exit 0 ;;
    migration-apply)
        if [ "$TEST_MIGRATION_CASE" = refused ]; then
            printf 'setup-products: UAP registry has an unfinished operation. Recover it with the previous installer.\n' >&2
            exit 73
        fi
        if [ "$TEST_MIGRATION_CASE" = current ]; then printf 'unchanged\n'; else printf 'migrated\n'; fi ;;
    *) exit 97 ;;
esac
MIGRATION_PEER
    chmod +x "$TEST_MIGRATION_ROOT/helper"
    select_product() {
        PRODUCT=codex; SELECTED_PRODUCTS=(codex); LEGACY_PRODUCT=codex
        _INTERACTIVE_INTENT=false; _SELECTION_PENDING=false; _CHANNEL_PENDING=false
    }
    stage_config_helper() { _CONFIG_STAGE="$TEST_MIGRATION_ROOT"; _CONFIG_HELPER="$_CONFIG_STAGE/helper"; }
    abort_if_wsl_environment() { :; }; detect_platform() { :; }; require_installer_runtime() { :; }
    resolve_bootstrap_release() { BOOTSTRAP_TAG=v1.99.0; BOOTSTRAP_COMMIT=fixture; BOOTSTRAP_BUNDLE_COMMIT=fixture; }
    check_prerequisites() { :; }; stage_historical_baselines() { :; }; config_preflight() { :; }
    print_header() { :; }; bootstrap_promotion() { :; }
    progress_rich_available() { return 1; }; progress_helper_available() { return 1; }
    parallel_install_available() { [ "$TEST_MIGRATION_CASE" = parallel ]; }
    install_legacy_products() { printf 'writer\n' >> "$TEST_MIGRATION_TRACE"; }
    install_products_parallel() { printf 'parallel-writers\n' >> "$TEST_MIGRATION_TRACE"; _PARALLEL_ACTIVE=true; }
    status=0
    main > "$TEST_MIGRATION_ROOT/stdout" 2> "$TEST_MIGRATION_ROOT/stderr" || status=$?
    python3 -I - "$TEST_MIGRATION_ROOT" "$migration_case" "$status" <<'PY_MIGRATION_PARENT'
import pathlib, sys
root, case, status = pathlib.Path(sys.argv[1]), sys.argv[2], int(sys.argv[3])
trace = (root/'trace').read_text().splitlines()
output = (root/'stdout').read_text() + (root/'stderr').read_text()
writers = [i for i, item in enumerate(trace) if item in ('writer', 'parallel-writers')]
if case == 'refused':
    assert status != 0 and not writers, (status, trace, output)
    assert 'unfinished operation' in output and 'registrations have not been changed' in output, output
    assert 'resume saved activation' not in output and 'config-only retry' not in output, output
elif case == 'old-helper':
    assert status == 0 and len(writers) == 1 and 'migration-apply' not in trace, (status, trace, output)
else:
    assert status == 0 and len(writers) == 1 and trace.count('migration-apply') == 1, (status, trace, output)
    assert trace.index('prepare') < trace.index('preflight') < trace.index('migration-apply') < writers[0], trace
    assert ('registry upgraded; backup saved' in output) == (case != 'current'), output
PY_MIGRATION_PARENT
) || exit 1; done
printf 'PASS confirmed parent registry migration precedes all writers and refuses safely\n'

