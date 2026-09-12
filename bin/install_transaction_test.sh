#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
# Focused runtime promotion regressions. No network, builds, or real profiles.
# Match installer nounset behavior: Bash 3.2 treats empty arrays as unset.
set -eo pipefail
root=$(cd "$(dirname "$0")" && pwd)
sandbox=$(mktemp -d)
trap 'result=$?; if [ "$result" != 0 ]; then echo "FAILED: ${scenario:-utility} (status $result)" >&2; [ ! -f "${case_dir:-}/output" ] || tail -n 25 "$case_dir/output" >&2; fi; rm -rf "$sandbox"' EXIT

assert() {
    local message="$1"
    shift
    if ! "$@"; then
        echo "ASSERTION FAILED: ${scenario:-utility}${phase:+/$phase}: $message" >&2
        return 1
    fi
}

assert_output() {
    local expected="$1" message="$2"
    shift 2
    local actual
    actual=$("$@") || {
        echo "ASSERTION FAILED: ${scenario:-utility}${phase:+/$phase}: $message (command status $?)" >&2
        return 1
    }
    if [ "$actual" != "$expected" ]; then
        echo "ASSERTION FAILED: ${scenario:-utility}${phase:+/$phase}: $message (expected '$expected', got '$actual')" >&2
        return 1
    fi
}
test_env_setup "$sandbox"
sed '/^main "\$@"$/d' "$root/install.sh" > "$sandbox/functions.sh"
for scenario in staged staged_corrupt offline fresh_offline download checksum missing_checksum executable interrupt desktop fresh_desktop success fresh_success optional_interrupt legacy_fallback retained_legacy failed_fallback; do
    case_dir="$sandbox/$scenario"
    mkdir -p "$case_dir"
    (
        export INSTALL_TARGET_DIR="$case_dir"
        source "$sandbox/functions.sh"
        detect_platform() {
            PLATFORM=darwin ARCH=amd64
            BINARY_NAME=claude-notifications-darwin-amd64
            BINARY_PATH="$SCRIPT_DIR/$BINARY_NAME"
            CHECKSUMS_PATH="$SCRIPT_DIR/.checksums.txt"
            FOCUS_HANDLER_NAME='' FOCUS_HANDLER_PATH=''
        }
        detect_platform
        FORCE_UPDATE=true
        [ "$scenario" != desktop ] || FORCE_UPDATE=false
        printf '#!/bin/bash\necho old-version\n' > "$BINARY_PATH"
        chmod +x "$BINARY_PATH"
        mkdir -p "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS"
        printf '#!/bin/bash\necho old-notifier\n' > "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
        chmod +x "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
        ln -s "$BINARY_NAME" "$SCRIPT_DIR/claude-notifications"
        printf utility > "$SCRIPT_DIR/sound-preview"
        if [[ "$scenario" == fresh_* ]]; then
            rm "$BINARY_PATH" "$SCRIPT_DIR/claude-notifications"
            rm -rf "$SCRIPT_DIR/ClaudeNotifier.app"
        elif [ "$scenario" = desktop ]; then
            rm -rf "$SCRIPT_DIR/ClaudeNotifier.app"
        fi
        case "$scenario" in
            legacy_fallback|retained_legacy|failed_fallback)
                # Git Bash recognizes shebang files as executable even after chmod -x.
                printf broken > "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
                chmod -x "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
                if [ "$scenario" = retained_legacy ]; then
                    mkdir -p "$SCRIPT_DIR/terminal-notifier.app/Contents/MacOS"
                    printf '#!/bin/bash\necho legacy-notifier\n' > "$SCRIPT_DIR/terminal-notifier.app/Contents/MacOS/terminal-notifier"
                    chmod +x "$SCRIPT_DIR/terminal-notifier.app/Contents/MacOS/terminal-notifier"
                fi ;;
        esac
        abort_if_wsl_environment() { :; }
        check_required_tools() { :; }
        configure_curl_options() { :; }
        check_github_availability() { OFFLINE_MODE=false; [[ "$scenario" != *offline ]] || OFFLINE_MODE=true; }
        pin_release_urls() { :; }
        eval "$(declare -f download_binary | sed '1s/download_binary/real_download_binary/')"
        MAX_RETRIES=1 RETRY_DELAY=0
        download_checksums() {
            cat > "$SCRIPT_DIR/payload" <<'PAYLOAD'
#!/bin/bash
# agent-notifications-managed-writer-protocol-v1
if [ "$1" = internal-install-runtime ]; then
    stage="" target=""
    shift
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --stage) stage=$2; shift 2 ;;
            --target) target=$2; shift 2 ;;
            --entry|--control-root|--consumer) shift 2 ;;
            --require-native|--refresh|--remove|--purge-native) shift ;;
            *) shift ;;
        esac
    done
    [ -n "$stage" ] && [ -n "$target" ] || exit 2
    for f in "$stage"/*; do
        [ -e "$f" ] || continue
        base=$(basename "$f")
        case "$base" in .install-stage.*|old-*|unusable-*) continue ;; esac
        if [ -d "$f" ]; then
            rm -rf "$target/$base"
            cp -R "$f" "$target/$base"
        else
            cp "$f" "$target/$base"
            chmod +x "$target/$base" 2>/dev/null || true
        fi
    done
    exit 0
fi
echo claude-notifications-new-version
exit 0
PAYLOAD
            [ "$scenario" != executable ] || printf '#!/bin/bash\nexit 1\n' > "$SCRIPT_DIR/payload"
            head -c 1000000 /dev/zero >> "$SCRIPT_DIR/payload"
            [ "$scenario" != missing_checksum ] || return 1
            local sum
            if command -v shasum >/dev/null 2>&1; then
                sum=$(shasum -a 256 "$SCRIPT_DIR/payload")
            else
                sum=$(sha256sum "$SCRIPT_DIR/payload")
            fi
            printf '%s  %s\n' "${sum%% *}" "$BINARY_NAME" > "$CHECKSUMS_PATH"
            [ "$scenario" != checksum ] || printf 'bad  %s\n' "$BINARY_NAME" > "$CHECKSUMS_PATH"
        }
        download_binary() {
            if [ "$scenario" = download ]; then
                curl() { printf 404; return 22; }
                real_download_binary
                return $?
            fi
            cp "$SCRIPT_DIR/payload" "$BINARY_PATH"
            if [ "$scenario" = interrupt ]; then sh -c 'kill -TERM "$PPID"'; fi
        }
        download_terminal_notifier_modern() {
            case "$scenario" in
                success|fresh_success|staged|optional_interrupt) ;;
                *) return 1 ;;
            esac
            mkdir -p "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS"
            printf '#!/bin/bash\necho new-notifier\n' > "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
            chmod +x "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
            printf '{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"test"}\n' > "$SCRIPT_DIR/ClaudeNotifier.app.managed-runtime.json"
        }
        download_terminal_notifier() {
            [ "$scenario" = legacy_fallback ] || return 1
            mkdir -p "$SCRIPT_DIR/terminal-notifier.app/Contents/MacOS"
            printf '#!/bin/bash\necho legacy-notifier\n' > "$SCRIPT_DIR/terminal-notifier.app/Contents/MacOS/terminal-notifier"
            chmod +x "$SCRIPT_DIR/terminal-notifier.app/Contents/MacOS/terminal-notifier"
        }
        download_utilities() {
            # Optional downloads cannot start until the live runtime is complete.
            desktop_runtime_usable
            "$BINARY_PATH" --version | grep -q new-version
            [ "$scenario" != optional_interrupt ] || sh -c 'kill -TERM "$PPID"'
        }
        create_claude_notifications_app() { :; }
        setup_iterm2_venv() { :; }
        if [[ "$scenario" == staged* ]]; then
            download_checksums
            mkdir "$case_dir/assets"
            cp "$SCRIPT_DIR/payload" "$case_dir/assets/$BINARY_NAME"
            cp "$CHECKSUMS_PATH" "$case_dir/assets/checksums.txt"
            export INSTALL_STAGED_ASSETS="$case_dir/assets"
            [ "$scenario" != staged_corrupt ] || printf corrupt >> "$INSTALL_STAGED_ASSETS/$BINARY_NAME"
            # A staged main binary must not be fetched a second time or silently
            # skipped merely because the release server went offline afterward.
            download_binary() { echo 'unexpected binary download' >&2; return 97; }
            download_checksums() { echo 'unexpected checksum download' >&2; return 97; }
            check_github_availability() { echo 'unexpected connectivity probe' >&2; return 97; }
        fi
        main
    ) > "$case_dir/output" 2>&1 && status=0 || status=$?
    binary="$case_dir/claude-notifications-darwin-amd64"
    case "$scenario" in
        staged|success|fresh_success|optional_interrupt)
            "$binary" | grep -q new-version
            [ -x "$case_dir/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern" ]
            assert_output new-notifier 'attested modern notifier must be published' "$case_dir/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
            [ -f "$case_dir/ClaudeNotifier.app.managed-runtime.json" ] ;;
        legacy_fallback|retained_legacy)
            [ "$status" != 0 ]
            assert_output old-version 'existing binary was not preserved' "$binary" ;;
        fresh_desktop|fresh_offline) [ ! -e "$binary" ]; [ "$status" != 0 ] ;;
        *) assert_output old-version 'existing binary was not preserved' "$binary"
           assert_output old-version 'existing binary symlink was not preserved' "$case_dir/claude-notifications" ;;
    esac
    [ "$scenario" != staged_corrupt ] || assert 'corrupt staged binary must fail installation' test "$status" != 0
    assert_output utility 'existing utility was not preserved' cat "$case_dir/sound-preview"
    if [[ "$scenario" != fresh_* && "$scenario" != desktop && "$scenario" != *legacy* && "$scenario" != *fallback && "$scenario" != success && "$scenario" != staged && "$scenario" != optional_interrupt ]]; then
        assert_output old-notifier 'existing notifier was not preserved' "$case_dir/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
    fi
    if [ "$scenario" = failed_fallback ]; then
        [ "$status" != 0 ]
        [ -f "$case_dir/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern" ]
    fi
    transaction_artifacts=$(find "$case_dir" \( -name '.install-stage.*' -o -name '.install.lock' \) -print)
    if [ -n "$transaction_artifacts" ]; then
        echo "ASSERTION FAILED: $scenario: staged temp/lock artifacts were not cleaned:" >&2
        echo "$transaction_artifacts" >&2
        false
    fi
    echo "PASS: $scenario (status $status)"
done

# Exercise the real downloader with a curl stub; never touch a live utility.
scenario=utility_downloader
(
    export INSTALL_TARGET_DIR="$sandbox/utilities"
    mkdir -p "$INSTALL_TARGET_DIR"
    source "$sandbox/functions.sh"
    trap 'result=$?; [ "$result" = 0 ] || echo "UTILITY PHASE FAILED: ${phase:-setup} (status $result)" >&2' EXIT
    utility="$INSTALL_TARGET_DIR/sound-preview-test"
    FORCE_UPDATE=true
    transfer=interrupt
    curl() {
        local output=''
        while [ "$#" -gt 0 ]; do
            if [ "$1" = -o ]; then output="$2"; shift; fi
            shift
        done
        [ "$output" != "$utility" ] || return 99
        printf partial > "$output"
        case "$transfer" in
            interrupt)
                # Avoid command substitution: Bash 3.2 forks an intermediate
                # shell there, making the reported PPID the wrong process.
                sh -c 'kill -TERM "$PPID"'
                return 1 ;;
            fail) return 22 ;;
            short) return 0 ;;
        esac
        printf '#!/bin/sh\necho new-utility\n' > "$output"
        head -c 100001 /dev/zero >> "$output"
    }
    phase=initial_interrupt
    download_utility test "$utility" && status=0 || status=$?
    assert 'initial interrupt must return status 143' test "$status" = 143
    assert 'initial interrupt must not create a live utility' test ! -e "$utility"
    artifacts=$(find "$INSTALL_TARGET_DIR" -name '*.download.*' -print)
    assert_output '' 'initial interrupt temp file was not cleaned' printf %s "$artifacts"
    phase=initial_success
    transfer=success
    download_utility test "$utility"
    assert_output new-utility 'successful download did not install the utility' "$utility"
    cp "$utility" "$INSTALL_TARGET_DIR/expected"
    for transfer in interrupt fail short; do
        phase="replacement_$transfer"
        download_utility test "$utility" && status=0 || status=$?
        assert "$phase must fail" test "$status" != 0
        assert "$phase must preserve the live utility" cmp "$utility" "$INSTALL_TARGET_DIR/expected"
        artifacts=$(find "$INSTALL_TARGET_DIR" -name '*.download.*' -print)
        assert_output '' "$phase temp file was not cleaned" printf %s "$artifacts"
    done
    phase=usable_skip
    FORCE_UPDATE=false
    transfer=fail
    download_utility test "$utility" # usable existing file skips download
    for invalid in partial nonexecutable; do
        phase="repair_$invalid"
        if [ "$invalid" = partial ]; then printf partial > "$utility";
        else head -c 100001 /dev/zero > "$utility"; chmod -x "$utility"; fi
        transfer=success
        download_utility test "$utility"
        assert "$phase must install a usable utility" utility_usable "$utility"
    done
    phase=forced_replacement
    FORCE_UPDATE=true
    printf '#!/bin/sh\necho old-utility\n' > "$utility"
    head -c 100001 /dev/zero >> "$utility"
    chmod +x "$utility"
    download_utility test "$utility"
    assert_output new-utility 'forced replacement did not install the new utility' "$utility"
    # Optional phase must not even request the required Windows focus asset.
    phase=focus_exclusion
    FOCUS_HANDLER_NAME=focus.exe FOCUS_HANDLER_PATH="$INSTALL_TARGET_DIR/focus.exe"
    SOUND_PREVIEW_NAME=sound LIST_DEVICES_NAME=devices LIST_SOUNDS_NAME=sounds
    SOUND_PREVIEW_PATH="$utility" LIST_DEVICES_PATH="$utility" LIST_SOUNDS_PATH="$utility"
    download_utility() { [ "$1" != focus.exe ] || exit 99; }
    create_utility_symlink() { :; }
    download_utilities
    echo 'PASS: real optional downloader interruption, preservation, repair, force, focus exclusion'
)
