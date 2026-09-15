#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -eo pipefail
root=$(cd "$(dirname "$0")" && pwd)
sandbox=$(mktemp -d)
trap 'rm -rf "$sandbox"' EXIT
sed '/^main "\$@"$/d' "$root/install.sh" > "$sandbox/functions.sh"
source "$sandbox/functions.sh"
# Only launcher creation is exercised. All mutations stay in a disposable bundle.
SCRIPT_DIR="$sandbox/bundle"
mkdir -p "$SCRIPT_DIR"
guard_install_paths() { :; }
PLATFORM=linux
BINARY_NAME=claude-notifications-linux-amd64
BINARY_PATH="$SCRIPT_DIR/$BINARY_NAME"
printf '#!/bin/sh\nprintf "fixture-binary\\n"\n' > "$BINARY_PATH"
chmod +x "$BINARY_PATH"
create_symlink
for name in agent-notifications claude-notifications; do
    launcher="$SCRIPT_DIR/$name"
    if [ -L "$launcher" ]; then
        [ "$(readlink "$launcher")" = "$BINARY_NAME" ]
    else
        # Git Bash may implement ln as a copy when native symlinks are disabled.
        # The installer also explicitly supports copying on such filesystems.
        [ -f "$launcher" ]
        cmp -s "$BINARY_PATH" "$launcher"
    fi
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*)
            # This fixture is a POSIX script, not a native Windows executable.
            [ "$(sh "$launcher")" = fixture-binary ]
            ;;
        *) [ "$("$launcher")" = fixture-binary ] ;;
    esac
done
PLATFORM=windows
BINARY_NAME=claude-notifications-windows-amd64.exe
create_symlink
for name in agent-notifications claude-notifications; do
 grep -F '"%SCRIPT_DIR%claude-notifications-windows-amd64.exe" %*' "$SCRIPT_DIR/$name.bat"
 grep -F "set AGENT_NOTIFICATIONS_LAUNCHER=$name" "$SCRIPT_DIR/$name.bat"
done
echo 'PASS: dual launchers preserve the platform binary and Windows identity'
