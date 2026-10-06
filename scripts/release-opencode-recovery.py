#!/usr/bin/env python3
"""Observe business scenarios of immutable release artifacts with fresh primitives.

The candidate checkout and its fixture stay unchanged. GitHub validation runs
before isolation; no GitHub token is needed or accepted by native execution.
"""
import argparse
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile

WINDOWS_CANDIDATE = 'e86149a724f9b96cb5ee193d63dbc5a533c61e7e'
RELEASE_TAG = 'v1.48.1'
# Parent must replace these from the actual new release build and custody.
# Predecessor v1.48.0 pins are intentionally not valid for this candidate.
# Preparation is not native-ready until all four observed values are supplied.
WINDOWS_MANIFEST_SHA = 'a7e4d3603b2958ee58b31a2db6fdb8c7dfb006183474828dbdad8197cb069c30'
WINDOWS_PARENT_SHA = 'ea156e1978d5acad56ff6cfbebc8cb20ba646b55a271e137dc696e6646bc64d9'
WINDOWS_BINARY_SHA = 'a026587157a9a0aa60c4c45ddae089622d3e7f4fd96321e11fa1a3545153c8e2'
WINDOWS_SOURCE_SHA = '671fea533825aa6fe0f55299871dafbbbcd61cb3f2b88ab783c4274d986e8774'
WINDOWS_EMBEDDED_SHA = WINDOWS_SOURCE_SHA
WINDOWS_EMBEDDED_OFFSET = 14257054
WINDOWS_ACL_HELPER_SHA = 'd0995c320cb3b03a287a43915d2319a1b71f93b1d112c1f60ecc61cb276c15bd'
WINDOWS_ACL_HELPER = 'scripts/opencode-private-root-windows.go'


def windows_release_bindings(candidate, manifest, parent, binary):
    require(all(isinstance(value, str) and re.fullmatch('[a-f0-9]{64}', value)
                for value in (WINDOWS_MANIFEST_SHA, WINDOWS_PARENT_SHA, WINDOWS_BINARY_SHA)) and
            type(WINDOWS_EMBEDDED_OFFSET) is int and WINDOWS_EMBEDDED_OFFSET >= 0,
            'new_windows_release_pins_pending_not_native_ready')
    require((candidate, manifest, parent, binary) ==
            (WINDOWS_CANDIDATE, WINDOWS_MANIFEST_SHA, WINDOWS_PARENT_SHA, WINDOWS_BINARY_SHA),
            'original_windows_release_custody_required')


def windows_embedded_asset(canonical, image, observed_offset):
    require(len(canonical) == 176304 and canonical.count(b'\r') == 0 and
            canonical.count(b'\n') == 4206 and hashlib.sha256(canonical).hexdigest() == WINDOWS_SOURCE_SHA,
            'pinned_canonical_lf_asset_required')
    require(type(observed_offset) is int and observed_offset >= 0,
            'observed_new_windows_embed_offset_required')
    require(image.count(canonical) == 1 and image.find(canonical) == observed_offset and
            canonical.replace(b'\n', b'\r\n') not in image,
            'unique_original_canonical_windows_embed_required')
    return canonical


def windows_registration_oracle(original, receipts):
    def registration(root, plugin, managed, canonical_asset):
        require(hashlib.sha256(canonical_asset.read_bytes()).hexdigest() == WINDOWS_SOURCE_SHA,
                'canonical_asset_changed_before_registration')
        require(re.fullmatch('TEST-installed-[a-f0-9]{24}', root.name), 'owned_TEST_registration_required')
        for path in (managed, root / 'control'):
            value = str(path)
            require(path.is_absolute() and 0 < len(value) <= 512 and
                    all(32 <= ord(c) <= 126 for c in value) and '"__AGENT_NOTIFICATIONS_' not in value,
                    'bounded_ascii_go_quote_paths_required')
        ledger = json.loads((root / 'control/ownership.json').read_text())
        actual = ledger['Consumers']['opencode-notifications']['OpenCode']
        require(actual['OriginBound'] is True and all(re.fullmatch('[a-f0-9]{64}', actual[k])
                for k in ('Origin', 'Salt', 'Namespace', 'BundleSHA256')), 'actual_origin_bound_registration_required')
        # Recording only: preserve the original fixture and its actual asset
        # argument. No EOL proxy, transformed asset or replacement oracle.
        result = original(root, plugin, managed, canonical_asset)
        require(result == actual, 'registration_changed_during_render_check')
        require(len(receipts) < 3, 'extra_registration_observation')
        receipts.append({'phase': ('install', 'update', 'reinstall')[len(receipts)],
                         'canonicalGitAssetSHA256': WINDOWS_SOURCE_SHA,
                         'platformEmbeddedSHA256': WINDOWS_EMBEDDED_SHA,
                         'binarySHA256': WINDOWS_BINARY_SHA, 'embeddedOffset': WINDOWS_EMBEDDED_OFFSET,
                         'embeddedBytes': 176304, 'transform': 'none', 'lineEndings': 'LF',
                         'renderedInstalledSHA256': result['BundleSHA256'],
                         'actualLedgerBundleSHA256': result['BundleSHA256'],
                         'origin': result['Origin'], 'originBound': True, 'rawInstalledEquality': True})
        return result

    return registration


def windows_acl_source_binding(head, git_body, actual_body, dirty):
    require(head == WINDOWS_CANDIDATE and not dirty.strip() and
            hashlib.sha256(git_body).hexdigest() == WINDOWS_ACL_HELPER_SHA and
            actual_body == git_body, 'immutable_windows_acl_helper_required')


def windows_acl_cache(value):
    require(isinstance(value, str) and value and pathlib.Path(value).is_absolute(),
            'absolute_prefetched_module_cache_required')
    path = pathlib.Path(value)
    require(not any(p.is_symlink() or (hasattr(p, 'is_junction') and p.is_junction())
                    for p in (path, *path.parents)), 'plain_prefetched_module_cache_required')
    path = path.resolve(strict=True)
    require(path.is_dir(), 'existing_prefetched_module_cache_required')
    return path


def windows_acl_environment(root, cache, inherited):
    # Keep the original disposable private directories and system whitelist.
    # Only the trusted prefetched module cache escapes this fresh TEST root.
    env = {key: inherited[key] for key in ('PATH', 'SystemRoot', 'WINDIR', 'COMSPEC', 'PATHEXT')
           if key in inherited}
    for key in ('HOME', 'USERPROFILE', 'GOCACHE', 'TEMP', 'TMP'):
        child = root / ('private-' + key.lower())
        child.mkdir(mode=0o700)
        env[key] = str(child)
    env.update(GOMODCACHE=str(cache), GOFLAGS='-mod=readonly', GOPROXY='off', GOSUMDB='off',
               GOTOOLCHAIN='local', GOENV='off', GOWORK='off')
    require(env.get('GOMODCACHE') == str(cache), 'explicit_prefetched_module_cache_required')
    return env


def bounded_acl_output(body):
    body = body or b''
    require(isinstance(body, bytes), 'binary_acl_diagnostics_required')
    return {'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest(),
            'truncated': len(body) > 16384,
            'prefix': body[:16384].decode('utf8', errors='replace')}


def windows_acl_preparer(repo, cache_value, receipts, receipt_path):
    cache = windows_acl_cache(cache_value)
    helper = repo / WINDOWS_ACL_HELPER

    def source_binding():
        head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()
        dirty = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'],
                                        cwd=repo, text=True)
        body = subprocess.check_output(['git', 'show', WINDOWS_CANDIDATE + ':' + WINDOWS_ACL_HELPER], cwd=repo)
        windows_acl_source_binding(head, body, helper.read_bytes(), dirty)

    def prepare(root, os_name):
        require(sys.platform == 'win32' and os_name == 'windows', 'actual_windows_acl_preparation_only')
        require(re.fullmatch('TEST-installed-[a-f0-9]{24}', root.name) and root.is_dir() and
                root.resolve(strict=True).is_relative_to(repo / '.task-tools/artifacts') and
                not any(p.is_symlink() or (hasattr(p, 'is_junction') and p.is_junction())
                        for p in (root, *root.parents)), 'fresh_owned_windows_acl_root_required')
        require(not receipts, 'one_windows_acl_preparation_only')
        source_binding()
        env = windows_acl_environment(root, cache, os.environ)
        record = {'helperPath': WINDOWS_ACL_HELPER, 'helperSHA256': WINDOWS_ACL_HELPER_SHA,
                  'candidateCommit': WINDOWS_CANDIDATE, 'prefetchedModuleCache': str(cache),
                  'cwd': str(repo), 'privateEnvironmentKeys': sorted(env), 'moduleMode': 'readonly',
                  'externalModuleFetchAllowed': False, 'timeoutSeconds': 90,
                  'aclReadbackClaimed': False, 'productBinaryRebuilt': False,
                  'helperReturncode': None, 'timedOut': False}
        try:
            result = subprocess.run(['go', 'run', str(helper), str(root)], cwd=repo, env=env,
                                    capture_output=True, timeout=90)
            record.update(helperReturncode=result.returncode,
                          stdout=bounded_acl_output(result.stdout), stderr=bounded_acl_output(result.stderr))
        except subprocess.TimeoutExpired as error:
            record.update(timedOut=True, stdout=bounded_acl_output(error.stdout),
                          stderr=bounded_acl_output(error.stderr))
        # Preserve the genuine command result before any acceptance assertion.
        receipts.append(record)
        with receipt_path.open('x', encoding='utf8', newline='\n') as output:
            json.dump({'schema': 1, 'purpose': 'TEST original Windows ACL helper preparation',
                       'receipts': receipts}, output, sort_keys=True, indent=2)
            output.write('\n')
        receipt_path.chmod(0o600)
        source_binding()
        require(not record['timedOut'] and record['helperReturncode'] == 0,
                'windows_private_dacl_failed')

    return prepare


def require(ok, code):
    if not ok:
        raise ValueError(code)


def bindings():
    run = os.environ.get('AN_RECOVERY_RUN_ID', '')
    candidate = os.environ.get('AN_RECOVERY_CANDIDATE_SHA', '')
    archive = os.environ.get('AN_EVIDENCE_SHA256', '')
    require(re.fullmatch('[1-9][0-9]{0,19}', run), 'numeric_original_run_required')
    require(re.fullmatch('[0-9a-f]{40}', candidate), 'exact_candidate_sha_required')
    require(candidate == WINDOWS_CANDIDATE, 'exact_v1481_candidate_required')
    require(re.fullmatch('[0-9a-f]{64}', archive), 'sealed_archive_sha_required')
    return run, candidate, archive


def recovery_matrix(scope=None):
    scope = os.environ.get('AN_RECOVERY_CELLS', '') if scope is None else scope
    require(scope in ('first-linux-amd64-v2', 'windows-two', 'all-seven'), 'explicit_recovery_cells_required')
    targets = [
        {'runner': 'ubuntu-22.04', 'platform': 'linux', 'arch': 'amd64',
         'binary': 'claude-notifications-linux-amd64'},
        {'runner': 'ubuntu-22.04-arm', 'platform': 'linux', 'arch': 'arm64',
         'binary': 'claude-notifications-linux-arm64'},
        {'runner': 'windows-latest', 'platform': 'windows', 'arch': 'amd64',
         'binary': 'claude-notifications-windows-amd64.exe'}]
    cells = [{'version': version, 'target': target}
             for target in targets for version in ('1.18.33', '2.0.21')]
    cells.append({'version': '1.18.34', 'target': targets[0]})
    if scope == 'first-linux-amd64-v2':
        cells = [cell for cell in cells if cell['target'] == targets[0] and cell['version'] == '2.0.21']
    elif scope == 'windows-two':
        cells = [cell for cell in cells if cell['target'] == targets[2]]
    return {'include': cells}


def namespace_proof(host, current, interfaces):
    require(re.fullmatch(r'net:\[[1-9][0-9]*\]', host or '') and
            re.fullmatch(r'net:\[[1-9][0-9]*\]', current or ''), 'network_namespace_identity_required')
    require(current != host, 'new_private_netns_required')
    require(set(interfaces) == {'lo'}, 'loopback_only_required')
    return {'host': host, 'private': current, 'interfaces': ['lo']}


def self_test():
    # A reused/malformed namespace or any external interface must fail closed.
    namespace_proof('net:[10]', 'net:[11]', ['lo'])
    cases = [('', 'net:[11]', ['lo']), ('net:[10]', 'bad', ['lo']),
             ('net:[10]', 'net:[10]', ['lo']), ('net:[10]', 'net:[11]', []),
             ('net:[10]', 'net:[11]', ['lo', 'eth0'])]
    for host, current, interfaces in cases:
        try:
            namespace_proof(host, current, interfaces)
        except ValueError:
            continue
        raise AssertionError('unsafe namespace accepted')
    # A vertical canary must not silently run extra cells; all-seven must not
    # lose the separately pinned 1.18.34 cell or admit macOS.
    first = recovery_matrix('first-linux-amd64-v2')['include']
    require(len(first) == 1 and first[0]['version'] == '2.0.21' and
            (first[0]['target']['platform'], first[0]['target']['arch']) == ('linux', 'amd64'),
            'first_recovery_cell_scope')
    all_cells = recovery_matrix('all-seven')['include']
    actual = {(c['target']['platform'], c['target']['arch'], c['version']) for c in all_cells}
    expected = {(o, a, v) for o, a in (('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64'))
                for v in ('1.18.33', '2.0.21')} | {('linux', 'amd64', '1.18.34')}
    require(len(all_cells) == 7 and actual == expected, 'all_seven_recovery_cell_scope')
    windows = recovery_matrix('windows-two')['include']
    expected_windows = [{'version': version, 'target': {
        'runner': 'windows-latest', 'platform': 'windows', 'arch': 'amd64',
        'binary': 'claude-notifications-windows-amd64.exe'}} for version in ('1.18.33', '2.0.21')]
    require(windows == expected_windows, 'exact_two_windows_recovery_cell_scope')
    try:
        recovery_matrix('all-eleven')
    except ValueError:
        pass
    else:
        raise AssertionError('unknown recovery scope accepted')
    windows_adapter_self_test()
    windows_acl_self_test()
    print('PASS fresh namespace, valid identities and loopback-only boundary')


def windows_adapter_self_test():
    # Inert byte/ledger fixtures only: no Go binary, installer or native host.
    source = pathlib.Path(__file__).resolve().parents[1] / 'internal/opencodeplugin/dist/agent-notifications.js'
    canonical = source.read_bytes()
    observed_offset = 64  # Synthetic fixture position, never a release pin.
    prefix = b'\x00' * observed_offset
    image = prefix + canonical + b'\x00'
    require(windows_embedded_asset(canonical, image, observed_offset) == canonical,
            'actual_canonical_lf_fixture')

    def rejected(operation):
        try:
            operation()
        except (ValueError, fixture.Unqualified):
            return
        raise AssertionError('unsafe_windows_render_custody_accepted')

    spec = importlib.util.spec_from_file_location('inert_registration_oracle', source.parents[3] / 'scripts/opencode-native-e2e.py')
    fixture = importlib.util.module_from_spec(spec); spec.loader.exec_module(fixture)
    crlf = canonical.replace(b'\n', b'\r\n')
    for bad_source, bad_image, bad_offset in ((crlf, image, observed_offset),
            (canonical + b'x', image, observed_offset), (canonical, prefix + crlf, observed_offset),
            (canonical, image + canonical, observed_offset),
            (canonical, b'\x00' + image, observed_offset),
            (canonical, prefix + b'x' + canonical[1:], observed_offset),
            (canonical, image + crlf, observed_offset), (canonical, image, None)):
        rejected(lambda: windows_embedded_asset(bad_source, bad_image, bad_offset))
    # Fresh artifact pins are not inferred or copied from the predecessor.
    pins = [WINDOWS_CANDIDATE, WINDOWS_MANIFEST_SHA, WINDOWS_PARENT_SHA, WINDOWS_BINARY_SHA]
    if any(pin is None for pin in pins) or WINDOWS_EMBEDDED_OFFSET is None:
        rejected(lambda: windows_release_bindings(WINDOWS_CANDIDATE, '0' * 64, '0' * 64, '0' * 64))
    else:
        windows_release_bindings(*pins)
        for index in range(4):
            altered_pins = list(pins); altered_pins[index] = '0' * len(pins[index])
            rejected(lambda: windows_release_bindings(*altered_pins))
    with tempfile.TemporaryDirectory(prefix='TEST-recovery-render-') as temp:
        root = pathlib.Path(temp) / ('TEST-installed-' + 'a' * 24)
        (root / 'control').mkdir(parents=True)
        plugin, managed = root / 'installed.js', root / 'runtime/claude-notifications-windows-amd64.exe'
        record = dict(Origin='1' * 64, Salt='2' * 64, Namespace='3' * 64, OriginBound=True)
        rendered = canonical.decode()
        for key, value in {'EXECUTABLE': str(managed), 'CONTROL_ROOT': str(root / 'control'), 'ORIGIN': record['Origin']}.items():
            rendered = rendered.replace('"__AGENT_NOTIFICATIONS_' + key + '__"', json.dumps(value, ensure_ascii=False))
        actual = rendered.encode(); record['BundleSHA256'] = hashlib.sha256(actual).hexdigest()
        ledger = root / 'control/ownership.json'

        def stage(registration, installed):
            ledger.write_text(json.dumps({'Consumers': {'opencode-notifications': {'OpenCode': registration}}}))
            plugin.write_bytes(installed)

        def original_with_actual_asset(root, plugin, managed, asset):
            require(asset is source, 'original_fixture_actual_asset_argument_required')
            return fixture.registration(root, plugin, managed, asset)

        receipts = []; oracle = windows_registration_oracle(original_with_actual_asset, receipts)
        stage(record, actual)
        require(oracle(root, plugin, managed, source) == record and len(receipts) == 1,
                'unchanged_canonical_lf_registration_oracle')
        wrong_newlines = actual.replace(b'\n', b'\r\n')
        for altered, installed in ((dict(record, Origin='f' * 64), actual),
                                   (dict(record, Origin='bad'), actual),
                                   (dict(record, BundleSHA256='0' * 64), actual),
                                   (dict(record, BundleSHA256=hashlib.sha256(wrong_newlines).hexdigest()), wrong_newlines),
                                   (dict(record, BundleSHA256=hashlib.sha256(actual + b'x').hexdigest()), actual + b'x')):
            stage(altered, installed)
            rejected(lambda: oracle(root, plugin, managed, source))
        require(len(receipts) == 1, 'original_registration_failure_must_not_emit_receipt')
        stage(record, actual)
        oracle(root, plugin, managed, source); oracle(root, plugin, managed, source)
        require([r['phase'] for r in receipts] == ['install', 'update', 'reinstall'] and
                all(r['transform'] == 'none' and r['lineEndings'] == 'LF' for r in receipts),
                'three_untransformed_original_registration_receipts')
        rejected(lambda: oracle(root, plugin, managed, source))
    print('PASS canonical LF unique embedding, no stale pins and unchanged original registration failures')


def windows_acl_self_test():
    # No Windows helper or Go compiler execution. These assertions turn red if
    # the external cache is omitted, credentials leak or source pins are relaxed.
    source = pathlib.Path(__file__).resolve().parents[1] / WINDOWS_ACL_HELPER
    body = source.read_bytes()
    windows_acl_source_binding(WINDOWS_CANDIDATE, body, body, '')
    for values in [('0' * 40, body, body, ''), (WINDOWS_CANDIDATE, body + b'x', body, ''),
                   (WINDOWS_CANDIDATE, body, body + b'x', ''),
                   (WINDOWS_CANDIDATE, body, body, ' M scripts/opencode-private-root-windows.go')]:
        try:
            windows_acl_source_binding(*values)
        except ValueError:
            continue
        raise AssertionError('changed_acl_source_accepted')
    with tempfile.TemporaryDirectory(prefix='TEST-recovery-acl-env-') as temporary:
        base = pathlib.Path(temporary).resolve()
        cache = base / 'prefetched-modules'; cache.mkdir()
        root = base / 'TEST-private-environment'; root.mkdir()
        env = windows_acl_environment(root, windows_acl_cache(str(cache)),
                                      {'PATH': 'system-tools', 'SystemRoot': 'system-root',
                                       'HOME': 'ambient-home', 'GH_TOKEN': 'TEST-auth-sentinel',
                                       'OPENAI_API_KEY': 'TEST-auth-sentinel'})
        require(env['GOMODCACHE'] == str(cache) and pathlib.Path(env['GOCACHE']).is_relative_to(root) and
                env['HOME'] != 'ambient-home' and 'TEST-auth-sentinel' not in env.values(),
                'private_acl_env_explicit_external_cache_no_auth')
        require(all(env[k] == v for k, v in {'GOFLAGS': '-mod=readonly', 'GOPROXY': 'off',
                'GOSUMDB': 'off', 'GOTOOLCHAIN': 'local', 'GOENV': 'off', 'GOWORK': 'off'}.items()),
                'offline_readonly_acl_go_contract')
        file = base / 'not-a-cache'; file.write_bytes(b'TEST')
        for value in ['', 'relative/cache', str(file)]:
            try:
                windows_acl_cache(value)
            except ValueError:
                continue
            raise AssertionError('invalid_module_cache_accepted')
    output = bounded_acl_output(b'x' * 16385)
    require(len(output['prefix']) == 16384 and output['bytes'] == 16385 and output['truncated'] and
            output['sha256'] == hashlib.sha256(b'x' * 16385).hexdigest(), 'bounded_acl_diagnostic_receipt')
    print('PASS immutable ACL source, explicit offline module cache, private env and bounded diagnostics')


def github_json(endpoint, paginate=False):
    argv = ['gh', 'api', endpoint]
    if paginate:
        argv += ['--paginate', '--slurp']
    return json.loads(subprocess.check_output(argv, text=True))


def verify_origin():
    run, candidate, archive = bindings()
    matrix = recovery_matrix()
    repository = os.environ.get('GITHUB_REPOSITORY', '')
    require(repository == '777genius/agent-notifications', 'release_repository_required')
    base = f'repos/{repository}/actions/runs/{run}'
    origin = github_json(base)
    require(str(origin['id']) == run and origin['head_sha'] == candidate and
            origin['path'] == '.github/workflows/release.yml' and
            origin['event'] == 'push' and origin['head_branch'] == RELEASE_TAG and
            origin['status'] == 'completed' and origin['conclusion'] in ('success', 'failure'),
            'immutable_completed_release_run_required')
    jobs = [job for page in github_json(base + '/jobs?filter=latest&per_page=100', True)
            for job in page['jobs']]
    expected = ['Seal same-run OpenCode release inputs']
    for platform, arch in (('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64')):
        expected += [f'Build {platform} {arch}', f'Test {platform} {arch} binary']
    for name in expected:
        matches = [job for job in jobs if job['name'] == name]
        require(len(matches) == 1 and matches[0]['status'] == 'completed' and
                matches[0]['conclusion'] == 'success', 'original_build_canary_and_seal_required')
    artifacts = [item for page in github_json(base + '/artifacts?per_page=100', True)
                 for item in page['artifacts']]
    for name in ('binaries-linux-amd64', 'binaries-linux-arm64', 'binaries-windows-amd64',
                 'release-opencode-inputs-' + candidate):
        matches = [item for item in artifacts if item['name'] == name]
        require(len(matches) == 1 and not matches[0]['expired'], 'original_artifact_required')
    with pathlib.Path(os.environ['GITHUB_OUTPUT']).open('a') as output:
        output.write(f'run_id={run}\ncandidate_sha={candidate}\nevidence_sha256={archive}\n')
        output.write('matrix=' + json.dumps(matrix, separators=(',', ':')) + '\n')
    print('Verified original release SHA, three builds/canaries and sealed artifact custody')


def external_helper(name):
    return pathlib.Path(__file__).resolve().with_name(name)


def prepare_primitives(repo):
    """Only offline witness compilation; no native host or production rebuild."""
    primitives = repo / '.task-tools/artifacts/primitives'
    primitives.mkdir(mode=0o700)
    node = shutil.which('node')
    require(node is not None, 'pinned_node_required')
    modules = repo / 'opencode-plugin/node_modules'
    compiler = modules / '.bin' / ('tsc.cmd' if sys.platform == 'win32' else 'tsc')
    subprocess.run([sys.executable, '-B', str(external_helper('release-opencode-reader.py')),
                    '--build', '--candidate-repo', str(repo), '--build-root', str(primitives / 'TEST-reader-build'),
                    '--node', node, '--tsc', str(compiler), '--node-types', str(modules / '@types'),
                    '--esbuild', str(modules / 'esbuild/bin/esbuild')], check=True, timeout=180)
    cache = pathlib.Path(subprocess.check_output(['go', 'env', 'GOMODCACHE'], text=True).strip()).resolve(strict=True)
    if sys.platform == 'win32':
        clock_base = pathlib.Path('C:/TEST-clock-' + secrets.token_hex(6))
        clock_base.mkdir(mode=0o700)
    else:
        clock_base = pathlib.Path(tempfile.mkdtemp(prefix='TEST-release-clock-', dir='/tmp'))
    require(not clock_base.resolve().is_relative_to(repo), 'external_clock_test_root_required')
    with pathlib.Path(os.environ['GITHUB_OUTPUT']).open('a') as output:
        output.write(f'clock_temp_base={clock_base}\ngo_module_cache={cache}\n')


def fresh_business_proof(repo, manifest, manifest_sha, run, parent_sha, cell, r):
    primitives = repo / '.task-tools/artifacts/primitives'
    clock_report, reader_report = primitives / 'clock-report.json', primitives / 'reader-report.json'
    platform_args = ['--os', cell[0], '--arch', cell[1], '--version', cell[2]]
    subprocess.run([sys.executable, '-B', str(external_helper('release-opencode-clock.py')),
                    '--execute-ci', '--candidate-repo', str(repo),
                    '--temp-base', os.environ['AN_CLOCK_TEMP_BASE'], '--report', str(clock_report),
                    '--manifest', str(manifest), '--manifest-sha256', manifest_sha,
                    '--parent-archive', str(repo / '.task-tools/artifacts/custody/release-opencode-inputs.tar.gz'),
                    '--parent-archive-sha256', parent_sha, '--original-run-id', run,
                    '--go-module-cache', os.environ['AN_GO_MODULE_CACHE'], *platform_args],
                   check=True, timeout=600)
    build = primitives / 'TEST-reader-build'
    bundle, receipt = build / 'reader.js', build / 'build-receipt.json'
    subprocess.run([sys.executable, '-B', str(external_helper('release-opencode-reader.py')),
                    '--execute', '--candidate-repo', str(repo), '--root', str(primitives / 'TEST-reader-native'),
                    '--manifest', str(manifest), '--manifest-sha256', manifest_sha,
                    '--bundle', str(bundle), '--bundle-sha256', r.digest(bundle),
                    '--build-receipt', str(receipt), '--build-receipt-sha256', r.digest(receipt),
                    '--report', str(reader_report), *platform_args], check=True, timeout=180)
    sealed = subprocess.check_output([sys.executable, '-B', str(external_helper('release-opencode-business-proof.py')),
                    '--manifest', str(manifest), '--manifest-sha256', manifest_sha,
                    '--source-root', str(repo), '--clock-report', str(clock_report),
                    '--clock-report-sha256', r.digest(clock_report), '--reader-report', str(reader_report),
                    '--reader-report-sha256', r.digest(reader_report),
                    '--output', str(primitives / 'sealed'), *platform_args], text=True, timeout=60)
    record = json.loads(sealed)
    proof = pathlib.Path(record['proof']).resolve(strict=True)
    require(proof == (primitives / 'sealed/business-proof.json').resolve(strict=True) and
            r.digest(proof) == record['sha256'], 'fresh_semantically_sealed_business_proof_required')
    return proof, record['sha256']


def qualify(execute_native, prepare=False):
    run, candidate_sha, parent_sha = bindings()
    require(not any(os.environ.get(key) for key in ('GH_TOKEN', 'GITHUB_TOKEN')),
            'github_credentials_forbidden_in_native_fixture')
    repo = pathlib.Path.cwd().resolve()
    fixture = repo / 'scripts/testdata/opencode-native-e2e/ci_inputs.py'
    spec = importlib.util.spec_from_file_location('exact_release_inputs', fixture)
    inputs = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(inputs)
    r = inputs.r
    manifest = repo / os.environ['AN_MANIFEST']
    manifest_sha = os.environ['AN_MANIFEST_SHA256']
    os_name, arch, version = (os.environ[key] for key in ('AN_OS', 'AN_ARCH', 'AN_VERSION'))
    require((os_name, arch, version) in r.RELEASE_CELLS, 'seven_release_cells_only')
    m, cell, files, binary, archive = r.load_manifest(manifest, os_name, arch, version, manifest_sha)
    require(m.get('releaseScope') == 'linux-windows' and
            m['candidateCommit'] == m['buildRevision'] == candidate_sha,
            'immutable_partial_release_custody_required')
    binary_name = f'claude-notifications-{os_name}-{arch}' + ('.exe' if os_name == 'windows' else '')
    require(binary == (repo / 'dist' / binary_name).resolve(strict=True), 'normal_release_binary_required')
    require(r.digest(repo / '.task-tools/artifacts/custody/release-opencode-inputs.tar.gz') == parent_sha,
            'same_original_parent_archive_required')
    checkout = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    dirty = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], text=True)
    dirty += subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '--', '.',
                                     ':(exclude).task-tools/artifacts'], text=True)
    build = subprocess.check_output(['go', 'version', '-m', str(binary)], text=True)
    r.verify_source_binding(m, checkout, build, dirty)
    if prepare:
        prepare_primitives(repo)
        return 0
    if not execute_native:
        print('Verified original binary path, source assets and clean Go revision')
        return 0
    metadata = {'recoveryHarnessSHA256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
                'recoveryOriginalRunID': run, 'recoveryCandidateCommit': candidate_sha,
                'recoveryParentArchiveSHA256': parent_sha}
    if os_name == 'linux':
        require(sys.platform == 'linux' and os.geteuid() != 0, 'unprivileged_linux_fixture_required')
        baseline = os.environ.get('AN_HOST_NETNS', '')

        def guard():
            metadata['recoveryNetworkNamespaceProof'] = namespace_proof(
                baseline, os.readlink('/proc/self/ns/net'), [name for _, name in socket.if_nameindex()])

        r.network_guard = guard  # The sole override; candidate/fixture source remains immutable.
        guard()
        metadata['recoveryNetworkIsolation'] = True
    else:
        require(sys.platform == 'win32', 'actual_windows_fixture_required')
        metadata['recoveryNetworkIsolation'] = False
        image = binary.read_bytes()
        windows_release_bindings(candidate_sha, manifest_sha, parent_sha, hashlib.sha256(image).hexdigest())
        embedded = windows_embedded_asset(files['embedded'].read_bytes(), image, WINDOWS_EMBEDDED_OFFSET)
        del image
        receipts = metadata['recoveryWindowsRenderCustody'] = []
        metadata.update(canonicalGitAssetSHA256=WINDOWS_SOURCE_SHA, platformEmbeddedSHA256=WINDOWS_EMBEDDED_SHA)
        r.registration = windows_registration_oracle(r.registration, receipts)
        acl_receipts = metadata['recoveryWindowsACLPreparation'] = []
        acl_report = inputs.artifacts / 'windows-acl-preparation.json'
        r.prepare_sandbox_root = windows_acl_preparer(repo, os.environ.get('AN_GO_MODULE_CACHE', ''),
                                                    acl_receipts, acl_report)
    proof, proof_sha = fresh_business_proof(repo, manifest, manifest_sha, run,
                                          parent_sha, (os_name, arch, version), r)
    metadata.update(recoveryObservationScope='installed_business_only',
                    recoveryFreshBusinessProofSHA256=proof_sha,
                    recoveryPrimitiveHarnessesSHA256={name: r.digest(external_helper(name)) for name in
                    ('release-opencode-clock.py', 'release-opencode-reader.py',
                     'release-opencode-reader.mts', 'release-opencode-business-proof.py')})
    report = inputs.artifacts / 'native-report.json'
    sys.argv = [str(repo / 'scripts/opencode-native-e2e.py'), '--manifest', str(manifest),
                '--manifest-sha256', manifest_sha, '--binary', str(binary), '--archive', str(archive),
                '--os', os_name, '--arch', arch, '--version', version,
                '--suite', 'business', '--business-proof', str(proof), '--business-proof-sha256', proof_sha,
                '--report', str(report)]
    if os_name == 'linux':
        sys.argv += ['--receiver-record', str(inputs.artifacts / 'receiver-record.json')]
    code = r.main()
    result = json.loads(report.read_text())
    if os_name == 'windows' and code == 0:
        require(len(metadata['recoveryWindowsRenderCustody']) == 3, 'three_actual_registration_receipts_required')
    result.update(metadata)
    r.write_json(report, result)
    return code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group(required=True)
    for name in ('verify-origin', 'verify-inputs', 'prepare-primitives', 'execute', 'self-test'):
        modes.add_argument('--' + name, action='store_true')
    args = parser.parse_args()
    if args.self_test:
        self_test()
    elif args.verify_origin:
        verify_origin()
    else:
        return qualify(args.execute, args.prepare_primitives)
    return 0


if __name__ == '__main__':
    sys.exit(main())
