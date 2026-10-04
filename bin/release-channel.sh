#!/usr/bin/env bash
# Read-only release selection. Network acquisition and mutation belong to callers.
release_channel_platform() {
    local os arch
    case "$(uname -s)" in
        Darwin) os=darwin ;;
        Linux) os=linux ;;
        MINGW*|MSYS*|CYGWIN*|Windows_NT) os=windows ;;
        *) echo 'Unsupported release platform.' >&2; return 1 ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) echo 'Unsupported release architecture.' >&2; return 1 ;;
    esac
    [ "$os/$arch" != windows/arm64 ] || {
        echo 'No qualified Windows arm64 release.' >&2; return 1;
    }
    printf '%s\t%s\n' "$os" "$arch"
}

release_channel_select() {
    # The entire snapshot must be valid before any selected row is returned.
    LC_ALL=C awk -F '\t' -v os="$2" -v arch="$3" '
        BEGIN {
            expected["darwin/amd64"]=1; expected["darwin/arm64"]=1
            expected["linux/amd64"]=1; expected["linux/arm64"]=1
            expected["windows/amd64"]=1
        }
        NR == 1 {
            if ($0 != "# agent-notifications-platform-channels-v1") bad=1
            next
        }
        /^#/ { next }
        {
            key=$1 "/" $2
            if (NF != 6 || !expected[key] || seen[key]++) bad=1
            if ($3 !~ /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/) bad=1
            split(substr($3,2), version, ".")
            for (i=1; i<=3; i++) if (length(version[i]) > 9) bad=1
            if (length($4) != 40 || $4 ~ /[^0-9a-f]/) bad=1
            if (length($5) != 40 || $5 ~ /[^0-9a-f]/) bad=1
            ref=($1 == "darwin" ? "release/platform-macos" : "release/platform-linux-windows")
            if ($6 != ref) bad=1
            if ($1 == os && $2 == arch) row=$3 FS $4 FS $5 FS $6
        }
        END {
            for (key in expected) if (seen[key] != 1) bad=1
            if (bad || row == "") exit 1
            print row
        }
    ' "$1" || {
        echo 'Invalid release channel snapshot or unavailable platform; no latest fallback.' >&2
        return 1
    }
}
