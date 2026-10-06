"""Seal same-run release custody from actual reviewed source bytes, never grants.

This tool downloads pinned inputs and reads build metadata, but never launches a
candidate, native host, provider or notifier. Execution belongs to release CI.
"""
import argparse
import base64
import gzip
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import urllib.parse
import urllib.request

REPO = Path(__file__).resolve().parents[1]
FIXTURES = REPO / 'scripts/testdata/opencode-native-e2e'
SPEC = importlib.util.spec_from_file_location('release_ci_inputs', FIXTURES / 'ci_inputs.py')
inputs = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(inputs)
r = inputs.r
PARENT = '.task-tools/artifacts/parent-inputs'
LIMIT = 2 * 1024**3
SDK_ARCHIVE_SHA256 = 'c3d5aaaf6ecc3116b48ab1ae3f0e00b47720f239df9938f0c499d03f2c21a752'
SDK_INDEX_SHA256 = '8e0c343aa9ea29bfce3e4d53c0d979ffc447af96c36b10c5253e3a766a6454a5'
# Exact source identity retained in the canonical sealed all-eleven parent.
# The immutable official archive/image identities remain checked independently.
LINUX_CURRENT_HOST_SOURCE = 'aec0b9a6d8898f68f923aaf08b7306d931fd9d76'


def download(url, destination, expected):
    parsed = urllib.parse.urlsplit(url)
    r.require(parsed.scheme == 'https' and parsed.hostname and not parsed.username
              and not parsed.password and not parsed.query and not parsed.fragment
              and re.fullmatch('[0-9a-f]{64}', str(expected)), 'input_url_or_digest_invalid')
    destination.parent.mkdir(parents=True, exist_ok=True)
    total = 0
    with urllib.request.urlopen(url, timeout=60) as source, destination.open('xb') as out:
        while block := source.read(1024 * 1024):
            total += len(block)
            r.require(total <= LIMIT, 'input_archive_size')
            out.write(block)
    r.require(r.digest(destination) == expected, 'input_archive_hash_mismatch')


def reviewed_sdk_index(archive, output):
    """Retain raw package/index.js, matching the original canonical custody."""
    with tarfile.open(archive, 'r:gz') as packed:
        matches = [member for member in packed.getmembers() if member.name == 'package/index.js']
        r.require(len(matches) == 1 and matches[0].isfile() and matches[0].size == 311,
                  'reviewed_sdk_index_member_invalid')
        with packed.extractfile(matches[0]) as source:
            body = source.read()
    r.require(hashlib.sha256(body).hexdigest() == SDK_INDEX_SHA256, 'reviewed_sdk_index_differs')
    (output / 'sdk-index.js').write_bytes(body)


def custody_sources(archive, output):
    reviewed_sdk_index(archive, output)
    return {
        'sdkSource': {'path': PARENT + '/sdk-index.js', 'sha256': SDK_INDEX_SHA256},
        # This is actual current production Go source, not a historical grant.
        'qualificationSource': record(REPO / 'internal/opencodecodec/qualification_rows.go'),
    }


def record(path):
    r.require(path.is_file() and not path.is_symlink(), 'release_input_not_regular')
    return {'path': str(path.relative_to(REPO)), 'sha256': r.digest(path)}


def seal_archive(root, archive):
    """Canonical tar metadata makes archive identity independent of runner time."""
    members = sorted(root.rglob('*'))
    r.require(not any(p.is_symlink() for p in members), 'parent_member_symlink')
    files = [p for p in members if p.is_file()]
    r.require(files and sum(p.stat().st_size for p in files) <= LIMIT, 'parent_archive_size')
    with archive.open('xb') as raw, gzip.GzipFile(fileobj=raw, mode='wb', filename='', mtime=0) as zipped:
        with tarfile.open(fileobj=zipped, mode='w', format=tarfile.USTAR_FORMAT) as packed:
            for path in files:
                r.require(not path.is_symlink(), 'parent_member_symlink')
                member = tarfile.TarInfo(str(path.relative_to(root)))
                member.size, member.mode = path.stat().st_size, 0o600
                with path.open('rb') as body:
                    packed.addfile(member, body)
    return r.digest(archive)


def release_manifest(scope):
    manifest = json.loads((FIXTURES / 'manifest.template.json').read_text())
    r.require(scope in ('all', 'linux-windows'), 'unsupported_release_scope')
    if scope == 'linux-windows':
        manifest['releaseScope'] = scope
        manifest['cells'] = [cell for cell in manifest['cells'] if cell['os'] != 'darwin']
    r.custody_cells(manifest, ('linux', 'amd64', '1.18.33'))
    # Check every requested binary before downloading hosts or sealing any bytes.
    for cell in manifest['cells']:
        name = f"claude-notifications-{cell['os']}-{cell['arch']}" + ('.exe' if cell['os'] == 'windows' else '')
        cell['candidate'] = record(REPO / 'dist' / name)
    return manifest


def prepare(rebuilt, commit, scope='all'):
    manifest = release_manifest(scope)
    root = inputs.artifacts / 'release-parent'
    root.mkdir(mode=0o700)
    embedded = REPO / 'internal/opencodeplugin/dist/agent-notifications.js'
    r.require(rebuilt.read_bytes() == embedded.read_bytes(), 'rebuilt_bundle_differs')
    shutil.copyfile(rebuilt, root / 'rebuilt-agent-notifications.js')
    manifest['candidateCommit'] = manifest['buildRevision'] = commit
    manifest['assets'] = {
        'embedded': record(embedded),
        'rebuilt': {'path': PARENT + '/rebuilt-agent-notifications.js', 'sha256': r.digest(rebuilt)},
        'packageLock': record(REPO / 'opencode-plugin/package-lock.json'),
    }
    lock = json.loads((REPO / 'opencode-plugin/package-lock.json').read_text())
    dependency = lock['packages']['node_modules/universal-agent-plugins-opencode-events']
    resolved = dependency['resolved']
    r.require(resolved.startswith('file:vendor/'), 'release_sdk_must_be_reviewed_vendor')
    sdk = (REPO / 'opencode-plugin' / resolved[5:]).resolve(strict=True)
    r.require(sdk.is_relative_to(REPO / 'opencode-plugin/vendor'), 'sdk_path_escape')
    r.require(r.digest(sdk) == SDK_ARCHIVE_SHA256, 'reviewed_sdk_archive_differs')
    sri = 'sha512-' + base64.b64encode(hashlib.sha512(sdk.read_bytes()).digest()).decode()
    r.require(dependency['integrity'] == sri, 'sdk_lock_sri_mismatch')
    manifest['sdk'] = {'archive': record(sdk), 'sri': sri, 'lockResolved': resolved, 'lockIntegrity': dependency['integrity']}
    manifest['assets'].update(custody_sources(sdk, root))
    for cell in manifest['cells']:
        identity = (cell['os'], cell['arch'], cell['version'])
        if cell['hostSourceCommit'] is None:
            r.require(identity == ('linux', 'amd64', '1.18.34'), 'unknown_host_source_identity')
            cell['hostSourceCommit'] = LINUX_CURRENT_HOST_SOURCE
        r.require(re.fullmatch('[0-9a-f]{40}', str(cell['hostSourceCommit'])), 'host_source_missing')
        binary = r.checked_file(REPO, cell['candidate'])
        build = subprocess.check_output(['go', 'version', '-m', str(binary)], text=True)
        r.verify_source_binding(manifest, commit, build, '')
        directory = root / 'inputs' / '-'.join(identity)
        directory.mkdir(parents=True)
        archive = directory / 'package.tgz'
        download(cell['hostURL'], archive, cell['archive']['sha256'])
        r.require(r.sri(archive) == cell['archiveSRI'], 'host_archive_sri_mismatch')
        image = directory / 'image'
        r.extract_opencode(archive, image, cell['os'])
        r.require(r.digest(image) == cell['executableSHA256'], 'host_executable_hash_mismatch')
        image.unlink()
        # Custody supplies no desktop qualification, including skipped macOS.
        cell['nativeApp'] = None
    helper = REPO / '.task-tools/artifacts/release-helper/ClaudeNotifier.app.zip'
    receipt = {'purpose': 'same-run release input custody, no qualification grant',
               'candidateCommit': commit, 'runId': os.environ['GITHUB_RUN_ID'],
               'runAttempt': os.environ['GITHUB_RUN_ATTEMPT'],
               'releaseScope': scope,
               'includedPlatforms': sorted({cell['os'] + '/' + cell['arch'] for cell in manifest['cells']}),
               'skippedPlatforms': ['darwin/amd64', 'darwin/arm64'] if scope == 'linux-windows' else [],
               'signedHelperArchive': None if scope == 'linux-windows' else record(helper),
               'macOSOpenCodeDesktopQualified': False}
    (root / 'release-inputs-receipt.json').write_text(json.dumps(receipt, indent=2, sort_keys=True) + '\n')
    (root / 'manifest.json').write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
    return seal_archive(root, inputs.artifacts / 'release-opencode-inputs.tar.gz')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rebuilt', type=Path, required=True)
    parser.add_argument('--scope', choices=('all', 'linux-windows'), default='all',
                        help='Explicit partial custody; default retains all eleven cells')
    args = parser.parse_args()
    inputs.artifacts.mkdir(parents=True, exist_ok=True)
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=REPO, text=True).strip()
    r.require(commit == os.environ.get('GITHUB_SHA'), 'exact_release_checkout_required')
    dirty = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=REPO, text=True)
    dirty += subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '--', '.',
                                     ':(exclude).task-tools/artifacts'], cwd=REPO, text=True)
    r.require(not dirty, 'clean_release_checkout_required')
    sha = prepare(args.rebuilt, commit, args.scope)
    with Path(os.environ['GITHUB_OUTPUT']).open('a') as out:
        out.write('sha256=' + sha + '\n')


if __name__ == '__main__':
    main()
