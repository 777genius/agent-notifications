#!/usr/bin/env bash
# Full public/candidate installer regression on a disposable Actions VM only.
set -euo pipefail
umask 077

repo=$(cd "$(dirname "$0")/.." && pwd -P)
if ! { [ "${GITHUB_ACTIONS:-}" = true ] && {
    [ "${RUNNER_ENVIRONMENT:-}" = github-hosted ] || {
        [ "${PI_INSTALLER_NAMESPACE:-}" = true ] && [[ "${RUNNER_NAME:-}" = nsc-runner-* ]]
    }
}; }; then
    echo 'This test requires a disposable GitHub-hosted or configured Namespace Actions VM.' >&2; exit 1
fi
[ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] && [[ "$(sw_vers -productVersion)" = 26.* ]] || {
    echo 'This test requires macOS 26 ARM64.' >&2; exit 1;
}
[ -n "${RUNNER_TEMP:-}" ] && [ ! -e "$RUNNER_TEMP/TEST-pi-installer" ] || exit 1
mkdir -m 700 "$RUNNER_TEMP/TEST-pi-installer"
root=$(cd "$RUNNER_TEMP/TEST-pi-installer" && pwd -P)
evidence="$root/evidence"
mkdir -m 700 "$evidence" "$root/downloads" "$root/public" "$root/candidate" "$root/missing"
python=$(command -v python3)
go=$(command -v go)
source_sha=$(git -C "$repo" rev-parse HEAD)
[ "$source_sha" = "${SOURCE_SHA:?exact source SHA required}" ]
[ -z "$(git -C "$repo" status --porcelain --untracked-files=all)" ]
export PI_INSTALLER_TEST_ROOT="$root" PI_INSTALLER_SOURCE_SHA="$source_sha"
# Keep diagnostics bounded and useful in the Actions console as well as artifacts.
failed_logs() {
    local status=$?
    if [ "$status" -ne 0 ]; then
        for log in "$evidence"/*.log; do
            [ -f "$log" ] || continue
            printf '\nFailure evidence: %s\n' "${log##*/}" >&2
            tail -n 80 "$log" >&2
        done
    fi
}
trap failed_logs EXIT
"$python" - "$evidence" "$source_sha" "$repo/scripts/pi-installer-macos-e2e.sh" <<'PYHOST'
import hashlib, json, os, platform, sys
from pathlib import Path
(Path(sys.argv[1]) / 'host-source.json').write_text(json.dumps({
    'source_sha': sys.argv[2], 'script_sha256': hashlib.sha256(Path(sys.argv[3]).read_bytes()).hexdigest(),
    'host_os': platform.system(), 'host_architecture': platform.machine(), 'host_version': platform.mac_ver()[0],
    'runner_environment': os.environ.get('RUNNER_ENVIRONMENT'), 'runner_name': os.environ.get('RUNNER_NAME'),
    'namespace_route': os.environ.get('PI_INSTALLER_NAMESPACE') == 'true', 'run_id': os.environ.get('GITHUB_RUN_ID'),
    'run_attempt': os.environ.get('GITHUB_RUN_ATTEMPT'), 'desktop_qualified': False,
}, indent=2) + '\n')
PYHOST

# Acquire the genuine standalone Pi, before either installer sees its PATH.
pi_archive=$($python -c 'import json; print(json.load(open("scripts/testdata/pi-native/pins.json"))["targets"]["darwin/arm64"]["archive"])')
gh release download v1.4.0 --repo earendil-works/pi --pattern "$pi_archive" --dir "$root/downloads"
"$python" -B "$repo/scripts/acquire-pi-native.py" --archive "$root/downloads/$pi_archive" \
    --platform darwin --arch arm64 --output "$root/pi" --report "$evidence/pi-acquisition.json"
pi="$root/pi/pi/pi"
[ -x "$pi" ]
"$pi" --version > "$evidence/pi-version.txt"

# Save the exact mutable public script bytes used by both baseline attempts.
curl -fsSL --connect-timeout 15 --max-time 120 https://agent-notifications.com/install.sh \
    -o "$evidence/public-install.sh"

run_installer() {
    local profile=$1 selected_path=$2 script=$3 log=$4
    shift 4
    mkdir -p "$profile/home" "$profile/tmp" "$profile/project" "$profile/global"
    env -i HOME="$profile/home" USERPROFILE="$profile/home" TMPDIR="$profile/tmp/" \
        XDG_CONFIG_HOME="$profile/home/.config" XDG_DATA_HOME="$profile/home/.local/share" \
        XDG_CACHE_HOME="$profile/home/.cache" PI_CODING_AGENT_DIR="$profile/home/.pi/agent" \
        AGENT_NOTIFICATIONS_CONFIG="$profile/global/config.json" PATH="$selected_path" \
        TERM=dumb CI=true NO_COLOR=1 BOOTSTRAP_VERBOSE=1 \
        BOOTSTRAP_DIAGNOSTIC_ROOT="$profile/diagnostics" \
        "$@" /bin/bash "$script" --products pi --desktop --plain </dev/null > "$log" 2>&1
}

# The exact reported discovery failure remains independently reproducible.
missing_status=0
run_installer "$root/missing" /usr/bin:/bin:/usr/sbin:/sbin "$evidence/public-install.sh" \
    "$evidence/public-missing-path.log" || missing_status=$?
[ "$missing_status" -ne 0 ]
grep -Fq 'setup-products: pi prerequisite unavailable: not found in selected PATH' "$evidence/public-missing-path.log"
printf '%s\n' "$missing_status" > "$evidence/public-missing-path.exit"

# Retain the original released installation regression separately from candidate
# success. A future corrected stable release may complete this same baseline.
public_status=0
run_installer "$root/public" "$(dirname "$pi"):/usr/bin:/bin:/usr/sbin:/sbin" \
    "$evidence/public-install.sh" "$evidence/public-real-pi.log" || public_status=$?
printf '%s\n' "$public_status" > "$evidence/public-real-pi.exit"
if [ "$public_status" -eq 0 ]; then
    public_outcome=completed
else
    grep -Fq 'concurrent_change' "$evidence/public-real-pi.log"
    public_outcome=failed_concurrent_change
fi
printf '%s\n' "$public_outcome" > "$evidence/public-real-pi.outcome"
if grep -Fq 'pi prerequisite unavailable' "$evidence/public-real-pi.log"; then exit 1; fi

# Download the released inputs once, retain their original manifest, and verify
# original bytes before reusing only the authentic signed native helper.
stable_tag=v1.50.0
assets=(checksums.txt ClaudeNotifier.app.zip claude-notifications-darwin-arm64 \
    sound-preview-darwin-arm64 list-devices-darwin-arm64 list-sounds-darwin-arm64 \
    agent-notify-portable-darwin-arm64.zip)
patterns=()
for name in "${assets[@]}"; do patterns+=(--pattern "$name"); done
gh release view "$stable_tag" --repo 777genius/agent-notifications --json tagName,targetCommitish,url \
    > "$evidence/public-release.json"
gh release download "$stable_tag" --repo 777genius/agent-notifications "${patterns[@]}" --dir "$root/downloads/stable"
cp "$root/downloads/stable/checksums.txt" "$evidence/public-checksums.txt"
"$python" - "$root/downloads/stable" "$evidence" <<'PY'
import hashlib, json, sys
from pathlib import Path
stage, evidence = map(Path, sys.argv[1:])
entries = {}
for line in (stage / 'checksums.txt').read_text().splitlines():
    digest, name = line.split(maxsplit=1)
    name = name.removeprefix('*')
    if name in entries:
        raise RuntimeError('duplicate_original_checksum')
    entries[name] = digest
verified = {}
for path in stage.iterdir():
    if path.name == 'checksums.txt':
        continue
    actual = hashlib.sha256(path.read_bytes()).hexdigest()
    assert entries.get(path.name) == actual, 'original_release_checksum_mismatch'
    verified[path.name] = actual
(evidence / 'verified-original-assets.json').write_text(json.dumps(verified, indent=2) + '\n')
PY
mkdir "$root/signed-helper"
unzip -q "$root/downloads/stable/ClaudeNotifier.app.zip" -d "$root/signed-helper"
"$python" -B - "$repo" "$root/signed-helper/ClaudeNotifier.app" "$evidence/helper-native-verification.jsonl" <<'PY'
import importlib.util, sys
from pathlib import Path
spec = importlib.util.spec_from_file_location('helper', Path(sys.argv[1]) / 'scripts/pi-macos-helper.py')
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)
helper.verify_native_helper(Path(sys.argv[2]), Path(sys.argv[3]))
PY

# Ordinary release build/packaging, with candidate checksums explicitly separated
# from the immutable public checksums. No product executable or helper is faked.
candidate_tag=$("$python" - "$repo/internal/config/runtime.go" <<'PYVERSION'
import re, sys
from pathlib import Path
version = re.search(r'^var ConsumerVersion = "([^"]+)"$', Path(sys.argv[1]).read_text(), re.MULTILINE)
if version is None:
    raise RuntimeError('candidate_release_version_missing')
print('v' + version[1])
PYVERSION
)
export PI_INSTALLER_CANDIDATE_TAG="$candidate_tag"
release="$root/releases/download/$candidate_tag"
mkdir -p "$release" "$root/raw/$source_sha/bin"
cp "$root/downloads/stable/ClaudeNotifier.app.zip" "$release/"
cp "$repo/bin/install.sh" "$root/raw/$source_sha/bin/install.sh"
for package in claude-notifications sound-preview list-devices list-sounds; do
    GOWORK=off CGO_ENABLED=1 "$go" build -trimpath \
        -ldflags="-s -w -X main.selectorSourceCommit=$source_sha" \
        -o "$release/$package-darwin-arm64" "./cmd/$package"
done
"$go" version -m "$release/claude-notifications-darwin-arm64" > "$evidence/candidate-build.txt"
GOWORK=off CGO_ENABLED=1 "$go" run ./cmd/build-portable-package -version "${candidate_tag#v}" -os darwin -arch arm64 \
    -executable "$release/claude-notifications-darwin-arm64" \
    -output "$release/agent-notify-portable-darwin-arm64.zip" -workdir "$root/portable-build"
"$python" - "$release" "$evidence" "$source_sha" "$candidate_tag" <<'PY'
import hashlib, json, sys
from pathlib import Path
stage, evidence = map(Path, sys.argv[1:3])
assets = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(stage.iterdir()) if p.is_file()}
manifest = ''.join(digest + '  ' + name + '\n' for name, digest in assets.items())
(stage / 'checksums.txt').write_text(manifest)
(evidence / 'candidate-checksums.txt').write_text(manifest)
(evidence / 'candidate-source.json').write_text(json.dumps({
    'source_sha': sys.argv[3], 'candidate_tag': sys.argv[4], 'kind': 'local_candidate_release_recipe',
    'published_release': False, 'desktop_qualified': False, 'assets': assets,
}, indent=2) + '\n')
PY

candidate_status=0
run_installer "$root/candidate" "$(dirname "$pi"):/usr/bin:/bin:/usr/sbin:/sbin" \
    "$repo/bin/bootstrap.sh" "$evidence/candidate-real-pi.log" \
    /usr/bin/env BOOTSTRAP_RELEASE_TAG="$candidate_tag" BOOTSTRAP_RELEASE_COMMIT="$source_sha" \
    BOOTSTRAP_SOURCE_COMMIT="$source_sha" BOOTSTRAP_RELEASES_BASE_URL="file://$root/releases" \
    BOOTSTRAP_RAW_BASE_URL="file://$root/raw" || candidate_status=$?
printf '%s\n' "$candidate_status" > "$evidence/candidate-real-pi.exit"
[ "$candidate_status" -eq 0 ]

# Repeat the same normal installer/profile with Pi absent from PATH and the
# explicit recovery override. No test bypass is passed to bootstrap.
recovery_status=0
mkdir -p "$root/candidate/tmp"
env -i HOME="$root/candidate/home" USERPROFILE="$root/candidate/home" TMPDIR="$root/candidate/tmp/" \
    PI_CODING_AGENT_DIR="$root/candidate/home/.pi/agent" \
    AGENT_NOTIFICATIONS_CONFIG="$root/candidate/global/config.json" \
    PATH=/usr/bin:/bin:/usr/sbin:/sbin TERM=dumb CI=true NO_COLOR=1 BOOTSTRAP_VERBOSE=1 \
    BOOTSTRAP_RELEASE_TAG="$candidate_tag" BOOTSTRAP_RELEASE_COMMIT="$source_sha" BOOTSTRAP_SOURCE_COMMIT="$source_sha" \
    BOOTSTRAP_RELEASES_BASE_URL="file://$root/releases" BOOTSTRAP_RAW_BASE_URL="file://$root/raw" \
    /bin/bash "$repo/bin/bootstrap.sh" --products pi --desktop --plain --pi-executable "$pi" \
    </dev/null > "$evidence/candidate-explicit-pi.log" 2>&1 || recovery_status=$?
printf '%s\n' "$recovery_status" > "$evidence/candidate-explicit-pi.exit"
[ "$recovery_status" -eq 0 ]

# Inspect installed ownership then let actual Pi discover/load the installed
# extension and complete one prompt against the existing local model fixture.
"$python" -B - "$repo" "$root" "$pi" "$release" <<'PY'
import hashlib, importlib.util, json, os, subprocess, sys
from pathlib import Path
repo, root, pi, release = map(Path, sys.argv[1:])
evidence = root / 'evidence'
def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module
P = load('installed', repo / 'tests/integration/pi_installed_fixture.py')
F = load('native', repo / 'tests/integration/pi_native_fixture.py')
profile = root / 'candidate/home/.pi/agent'
control = root / 'candidate/home/Library/Application Support/agent-notifications'
installed = P.installed(control, profile)
assert P.sha(installed['runtime']['executable']) == P.sha(release / 'claude-notifications-darwin-arm64')
runtime = Path(installed['binding']['runtimeRoot'])
assets = P.asset_receipts(control, runtime, repo)
# The generated active entry imports the installed native carrier. Both remain
# receipt-backed and unmodified; the TEST witness is a separate foreign extension.
assert (installed['active'] / 'index.ts').is_file()
provider, sink, process = F.Provider('success'), P.Webhooks(), None
try:
    # Configure a real loopback TEST destination, then obtain webhook consent
    # through the same normal installer. No receipt or authority is synthesized.
    global_config = root / 'candidate/global/config.json'
    P.configure(global_config, repo / 'config/config.json', sink, desktop=True)
    home = root / 'candidate/home'
    installer_env = {
        'HOME': str(home), 'USERPROFILE': str(home), 'TMPDIR': str(root / 'candidate/tmp') + '/',
        'XDG_CONFIG_HOME': str(home / '.config'), 'XDG_DATA_HOME': str(home / '.local/share'),
        'XDG_CACHE_HOME': str(home / '.cache'), 'PI_CODING_AGENT_DIR': str(profile),
        'AGENT_NOTIFICATIONS_CONFIG': str(global_config), 'PATH': '/usr/bin:/bin:/usr/sbin:/sbin',
        'TERM': 'dumb', 'CI': 'true', 'NO_COLOR': '1', 'BOOTSTRAP_VERBOSE': '1',
        'BOOTSTRAP_DIAGNOSTIC_ROOT': str(root / 'candidate/diagnostics'),
        'BOOTSTRAP_RELEASE_TAG': os.environ['PI_INSTALLER_CANDIDATE_TAG'], 'BOOTSTRAP_RELEASE_COMMIT': os.environ['PI_INSTALLER_SOURCE_SHA'],
        'BOOTSTRAP_SOURCE_COMMIT': os.environ['PI_INSTALLER_SOURCE_SHA'],
        'BOOTSTRAP_RELEASES_BASE_URL': 'file://' + str(root / 'releases'),
        'BOOTSTRAP_RAW_BASE_URL': 'file://' + str(root / 'raw'),
    }
    with (evidence / 'candidate-webhook-consent.log').open('wb') as output:
        consent = subprocess.run(['/bin/bash', str(repo / 'bin/bootstrap.sh'), '--products', 'pi',
                                  '--desktop', '--webhook', '--plain', '--pi-executable', str(pi)],
                                 cwd=root / 'candidate/project', env=installer_env, stdin=subprocess.DEVNULL,
                                 stdout=output, stderr=subprocess.STDOUT, timeout=180)
    (evidence / 'candidate-webhook-consent.exit').write_text(str(consent.returncode) + '\n')
    assert consent.returncode == 0, 'normal_installer_webhook_consent_failed'
    installed = P.installed(control, profile)
    assert P.sha(installed['runtime']['executable']) == P.sha(release / 'claude-notifications-darwin-arm64')
    assets = P.asset_receipts(control, Path(installed['binding']['runtimeRoot']), repo)
    case_root = root / 'candidate/process'
    env = F.prepare(case_root, provider, 'success', agent_dir=profile)
    env.update(HOME=str(home), USERPROFILE=str(home),
               XDG_CONFIG_HOME=str(home / '.config'), XDG_DATA_HOME=str(home / '.local/share'),
               XDG_CACHE_HOME=str(home / '.cache'), AGENT_NOTIFICATIONS_CONFIG=str(global_config))
    process = F.PiProcess(pi, case_root, env, 'json')
    assert process.wait() == 0 and process.stderr_bytes == 0, 'real_pi_loader_or_prompt_failed'
    counts = F.verify_case('success', F.witness(case_root), provider.requests)
    # This can only arrive from the receipt-backed product extension's real
    # pi-event child; the separate TEST witness never emits product callbacks.
    webhook = sink.wait('task_complete')
    sink.quiet()
    result = {'schema_version': 1, 'status': 'passed', 'source_sha': os.environ['PI_INSTALLER_SOURCE_SHA'],
              'candidate_tag': os.environ['PI_INSTALLER_CANDIDATE_TAG'],
              'public_missing_path': 'reproduced',
              'public_with_real_pi': (evidence / 'public-real-pi.outcome').read_text().strip(),
              'candidate_install': 'completed', 'explicit_executable_recovery': 'completed',
              'actual_pi_prompt': 'completed', 'native_event_counts': counts,
              'installed_active_leaf': installed['active'].name, 'asset_sha256': assets,
              'candidate_executable_sha256': installed['runtime']['primary_sha256'],
              'desktop_qualified': False, 'banner_verified': False, 'audio_verified': False,
              'click_verified': False, 'delivery_observed': True, 'delivery_channel': 'webhook',
              'webhook_verified': True, 'webhook_count': len(sink.rows),
              'webhook_status': webhook['status']}
    (evidence / 'installer-e2e.json').write_text(json.dumps(result, indent=2) + '\n')
finally:
    if process:
        process.close()
    provider.close()
    sink.close()
PY
