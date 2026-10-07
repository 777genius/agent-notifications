#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$PROJECT_DIR")"

BINARY_NAME="terminal-notifier-modern"
APP_BUNDLE_NAME="ClaudeNotifier"
BUILD_DIR="${PROJECT_DIR}/.build"
APP_BUNDLE="${PROJECT_DIR}/${APP_BUNDLE_NAME}.app"
ICON_SRC="${REPO_ROOT}/claude_icon.png"
ENTITLEMENTS="${PROJECT_DIR}/entitlements.plist"
CI_ENTITLEMENTS="${PROJECT_DIR}/entitlements-ci.plist"

CI_MODE=false
SKIP_NOTARIZE=false
REGISTER_APP=true
for arg in "$@"; do
    case "$arg" in
        --ci) CI_MODE=true ;;
        --skip-notarize) SKIP_NOTARIZE=true ;;
        --no-register) REGISTER_APP=false ;;
        *)
            echo "Unknown argument: $arg"
            exit 1
            ;;
    esac
done

echo "Building ${BINARY_NAME}..."
if [ "$CI_MODE" = true ]; then
    echo "  Mode: CI (Developer ID + hardened runtime + notarization)"
else
    echo "  Mode: Local (ad-hoc signing)"
fi

cd "$PROJECT_DIR"

echo "Building for arm64..."
swift build -c release --arch arm64 2>&1
ARM64_BINARY="${BUILD_DIR}/arm64-apple-macosx/release/${BINARY_NAME}"
if [ ! -f "$ARM64_BINARY" ]; then
    echo "Error: arm64 binary not found at ${ARM64_BINARY}"
    exit 1
fi

echo "Building for x86_64..."
swift build -c release --arch x86_64 2>&1
X86_BINARY="${BUILD_DIR}/x86_64-apple-macosx/release/${BINARY_NAME}"
if [ ! -f "$X86_BINARY" ]; then
    echo "Error: x86_64 binary not found at ${X86_BINARY}"
    exit 1
fi

BINARY="${BUILD_DIR}/${BINARY_NAME}-universal"
lipo -create \
    "${ARM64_BINARY}" \
    "${X86_BINARY}" \
    -output "$BINARY"

echo "Universal binary built successfully: ${BINARY}"
file "$BINARY"

echo "Assembling .app bundle..."

rm -rf "$APP_BUNDLE"
mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Resources"

cp "$BINARY" "${APP_BUNDLE}/Contents/MacOS/${BINARY_NAME}"
cp "${PROJECT_DIR}/Resources/Info.plist" "${APP_BUNDLE}/Contents/"

if [ -f "$ICON_SRC" ]; then
    echo "Generating app icon..."
    ICONSET_DIR="$(mktemp -d)/AppIcon.iconset"
    mkdir -p "$ICONSET_DIR"

    sips -z 16 16 "$ICON_SRC" --out "$ICONSET_DIR/icon_16x16.png" 2>/dev/null || true
    sips -z 32 32 "$ICON_SRC" --out "$ICONSET_DIR/icon_16x16@2x.png" 2>/dev/null || true
    sips -z 32 32 "$ICON_SRC" --out "$ICONSET_DIR/icon_32x32.png" 2>/dev/null || true
    sips -z 64 64 "$ICON_SRC" --out "$ICONSET_DIR/icon_32x32@2x.png" 2>/dev/null || true
    sips -z 128 128 "$ICON_SRC" --out "$ICONSET_DIR/icon_128x128.png" 2>/dev/null || true
    sips -z 256 256 "$ICON_SRC" --out "$ICONSET_DIR/icon_128x128@2x.png" 2>/dev/null || true
    sips -z 256 256 "$ICON_SRC" --out "$ICONSET_DIR/icon_256x256.png" 2>/dev/null || true
    sips -z 512 512 "$ICON_SRC" --out "$ICONSET_DIR/icon_256x256@2x.png" 2>/dev/null || true
    sips -z 512 512 "$ICON_SRC" --out "$ICONSET_DIR/icon_512x512.png" 2>/dev/null || true

    ICNS_PATH="${APP_BUNDLE}/Contents/Resources/AppIcon.icns"
    if iconutil -c icns "$ICONSET_DIR" -o "$ICNS_PATH" 2>/dev/null; then
        echo "App icon generated successfully"
    else
        echo "Warning: could not generate app icon (iconutil failed)"
    fi

    rm -rf "$(dirname "$ICONSET_DIR")"
else
    echo "Warning: icon source not found at ${ICON_SRC}, skipping icon generation"
fi

# Covered by the bundle resource seal. Old bundles lack this marker and must
# never be invoked with a capability flag by the managed installer.
cat > "${APP_BUNDLE}/Contents/Resources/managed-runtime.json" <<'MANIFEST'
{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}
MANIFEST

sign_with_entitlements() {
    local entitlements_path="$1"
    local label="$2"

    local flags=(--force --timestamp --options runtime)
    if [ -n "$entitlements_path" ]; then
        flags+=(--entitlements "$entitlements_path")
        echo "Using entitlements: ${entitlements_path}"
    else
        echo "Using entitlements: none"
    fi

    echo "Code signing with: Developer ID Application (hardened runtime) [${label}]"
    codesign "${flags[@]}" --sign "Developer ID Application" "${APP_BUNDLE}"
    codesign --verify --verbose "${APP_BUNDLE}"
    echo "Signature verified"

    if ! open -W -n "${APP_BUNDLE}" --args -launchedViaLaunchServices -help >/dev/null 2>&1; then
        echo "Error: signed app failed LaunchServices smoke check (-help) [${label}]"
        return 1
    fi

    return 0
}

if [ "$CI_MODE" = true ]; then
    FULL_ENTITLEMENTS=""
    SAFE_ENTITLEMENTS=""
    if [ -f "$ENTITLEMENTS" ]; then
        FULL_ENTITLEMENTS="$ENTITLEMENTS"
    fi
    if [ -f "$CI_ENTITLEMENTS" ]; then
        SAFE_ENTITLEMENTS="$CI_ENTITLEMENTS"
    fi

    EFFECTIVE_ENTITLEMENTS="$FULL_ENTITLEMENTS"
    EFFECTIVE_LABEL="full"

    if ! sign_with_entitlements "$EFFECTIVE_ENTITLEMENTS" "$EFFECTIVE_LABEL"; then
        if [ -n "$SAFE_ENTITLEMENTS" ] && [ "$SAFE_ENTITLEMENTS" != "$FULL_ENTITLEMENTS" ]; then
            echo "Warning: full entitlements failed runtime smoke check, retrying with CI-safe entitlements"
            EFFECTIVE_ENTITLEMENTS="$SAFE_ENTITLEMENTS"
            EFFECTIVE_LABEL="ci-safe"
            sign_with_entitlements "$EFFECTIVE_ENTITLEMENTS" "$EFFECTIVE_LABEL"
        else
            exit 1
        fi
    fi
else
    echo "Code signing .app bundle (ad-hoc)..."
    codesign --force --deep --sign - "$APP_BUNDLE" 2>/dev/null || {
        echo "Warning: code signing failed (notifications may require manual permission)"
    }
fi

if [ "$CI_MODE" = true ] && [ "$SKIP_NOTARIZE" != true ]; then
    echo ""
    echo "Notarizing ${APP_BUNDLE_NAME}.app..."

    if [ -z "${APPLE_ID:-}" ] || [ -z "${APPLE_PASSWORD:-}" ] || [ -z "${APPLE_TEAM_ID:-}" ]; then
        echo "Error: APPLE_ID, APPLE_PASSWORD, and APPLE_TEAM_ID must be set for notarization"
        exit 1
    fi

    NOTARIZE_ZIP="${BUILD_DIR}/${APP_BUNDLE_NAME}-notarize.zip"
    ditto -c -k --keepParent "${APP_BUNDLE}" "${NOTARIZE_ZIP}"

    xcrun notarytool submit "${NOTARIZE_ZIP}" \
        --apple-id "${APPLE_ID}" \
        --password "${APPLE_PASSWORD}" \
        --team-id "${APPLE_TEAM_ID}" \
        --wait

    rm -f "${NOTARIZE_ZIP}"

    echo "Stapling notarization ticket..."
    xcrun stapler staple "${APP_BUNDLE}"

    echo "Verifying notarized bundle..."
    codesign --verify --verbose "${APP_BUNDLE}"
    spctl --assess --type execute --verbose "${APP_BUNDLE}"
    echo "Notarization complete!"
fi

# Hash final signed bytes outside the bundle's own resource seal. The release
# archive transports this attestation alongside the app; it is not a publisher
# certificate, and unmanaged existing bundles are never probed on its authority.
cat > "${APP_BUNDLE}.managed-runtime.json" <<MANIFEST
{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"$(shasum -a 256 "${APP_BUNDLE}/Contents/MacOS/${BINARY_NAME}" | awk '{print $1}')"}
MANIFEST

LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
# Packaging and isolated qualification must not register a temporary bundle as
# the user's callback handler. Registration belongs to the stable installed path.
if [ "$REGISTER_APP" = true ] && [ -x "$LSREGISTER" ]; then
    "$LSREGISTER" -f "$APP_BUNDLE" 2>/dev/null || true
    echo "Registered with Launch Services"
fi

echo ""
echo "Build complete!"
echo "  Binary: ${APP_BUNDLE}/Contents/MacOS/${BINARY_NAME}"
echo "  Bundle: ${APP_BUNDLE}"
if [ "$CI_MODE" = true ]; then
    echo "  Signed: Developer ID Application (hardened runtime)"
    if [ "$SKIP_NOTARIZE" != true ]; then
        echo "  Notarized: yes"
    fi
fi
echo ""
echo "To install into plugin bin/:"
echo "  cp -R ${APP_BUNDLE} ${APP_BUNDLE}.managed-runtime.json ${REPO_ROOT}/bin/"
