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


def require(ok, code):
    if not ok:
        raise ValueError(code)


def bindings():
    run = os.environ.get('AN_RECOVERY_RUN_ID', '')
    candidate = os.environ.get('AN_RECOVERY_CANDIDATE_SHA', '')
    archive = os.environ.get('AN_EVIDENCE_SHA256', '')
    require(re.fullmatch('[1-9][0-9]{0,19}', run), 'numeric_original_run_required')
    require(re.fullmatch('[0-9a-f]{40}', candidate), 'exact_candidate_sha_required')
    require(re.fullmatch('[0-9a-f]{64}', archive), 'sealed_archive_sha_required')
    return run, candidate, archive


def recovery_matrix(scope=None):
    scope = os.environ.get('AN_RECOVERY_CELLS', '') if scope is None else scope
    require(scope in ('first-linux-amd64-v2', 'all-seven'), 'explicit_recovery_cells_required')
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
    try:
        recovery_matrix('all-eleven')
    except ValueError:
        pass
    else:
        raise AssertionError('unknown recovery scope accepted')
    print('PASS fresh namespace, valid identities and loopback-only boundary')


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
            origin['event'] == 'push' and origin['head_branch'] == 'v1.48.0' and
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
