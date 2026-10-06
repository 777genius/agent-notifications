#!/usr/bin/env python3
"""CI-only fresh finite clock observations of immutable v1.48.1 artifacts.

The exact candidate's stage_case/run_case and native predicates are unchanged.
Only external orchestration, original archive reuse and helper artifact reuse
replace preparation. This is no runtime/TimePolicy qualification or full gate.
--self-test is inert: synthetic metadata and namespace identities only.
"""
import argparse
import _thread
import hashlib
import os
from pathlib import Path
import platform
import re
import shutil
import socket
import tarfile
import threading
import time
import types

CANDIDATE = 'e86149a724f9b96cb5ee193d63dbc5a533c61e7e'
ORIGINAL_RUN_ID = '37519336110'
ORIGINAL_ARCHIVE_SHA256 = 'ea156e1978d5acad56ff6cfbebc8cb20ba646b55a271e137dc696e6646bc64d9'
HARNESS_SHA256 = '1c30c5e625ef4b59435a4231ef9bc45882ceacb6dcb16fa0419ccffada6c72e0'
HARNESS = 'scripts/opencode-platform-clock-prequalification.py'
RELEASE_CELLS = {(o, a, v) for o, a in (('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64'))
                 for v in ('1.18.33', '2.0.21')}
RELEASE_CELLS.add(('linux', 'amd64', '1.18.34'))


def need(ok, reason):
    if not ok:
        raise RuntimeError(reason)


def native_namespace(host, current, interfaces):
    need(re.fullmatch(r'net:\[[1-9][0-9]*\]', host or '') and
         re.fullmatch(r'net:\[[1-9][0-9]*\]', current or ''), 'native_namespace_identity')
    need(host != current and set(interfaces) == {'lo'}, 'native_private_loopback_namespace')
    return {'host': host, 'private': current, 'interfaces': ['lo']}


def build_settings(raw, os_name, arch):
    """Actual go version -m evidence, never a manifest's claimed build settings."""
    text = raw.decode('utf8')
    need(re.search(r'^.+: go1\.27\.1\r?$', text, re.M), 'original_pinned_go_version')
    pairs = re.findall(r'^\s*build\s+([^=\s]+)=(.*?)(?:\r)?$', text, re.M)
    settings = dict(pairs)
    need(len(settings) == len(pairs), 'duplicate_go_build_setting')
    expected = {'vcs.revision': CANDIDATE, 'vcs.modified': 'false', 'vcs': 'git',
                'GOOS': os_name, 'GOARCH': arch, 'CGO_ENABLED': '1', '-trimpath': 'true',
                '-buildmode': 'exe', '-compiler': 'gc'}
    need(all(settings.get(k) == v for k, v in expected.items()), 'exact_original_native_go_build')
    need(re.search(r'^\s*path\s+github\.com/777genius/agent-notifications/cmd/claude-notifications\r?$', text, re.M),
         'original_notification_command')
    need(not re.search(r'^\s*=>', text, re.M), 'original_go_replacement_forbidden')
    dependencies = re.findall(r'^\s*dep\s+(\S+)\s+(\S+)\s+(\S+)\s*$', text, re.M)
    need(len({x[0] for x in dependencies}) == len(dependencies), 'duplicate_original_go_dependency')
    return settings, {path: (version, checksum) for path, version, checksum in dependencies}


def exact_harness(repo):
    path = repo / HARNESS
    need(path.is_file() and not any(p.is_symlink() or
         (hasattr(p, 'is_junction') and p.is_junction()) for p in (path, *path.parents)), 'exact_harness_path')
    raw = path.read_bytes()
    # Git's Windows checkout conversion is the sole permitted byte transform.
    need(hashlib.sha256(raw.replace(b'\r\n', b'\n')).hexdigest() == HARNESS_SHA256,
         'immutable_candidate_harness_hash')
    # Execute only the bytes just verified: import loaders may read a stale
    # cached .pyc, and the immutable checkout must never receive new pycache.
    module = types.ModuleType('immutable_release_clock_harness')
    module.__file__ = str(path)
    exec(compile(raw, str(path), 'exec'), module.__dict__)
    need(module.REPO == repo and all(v is False for v in module.QUALIFICATIONS.values()), 'exact_harness_binding')
    return module, hashlib.sha256(raw).hexdigest()


def custody(h, args):
    """Bind downloaded manifest bytes to the immutable original parent archive."""
    archive = h.canonical_path(args.parent_archive)
    manifest_path = h.canonical_path(args.manifest)
    need(re.fullmatch('[a-f0-9]{64}', args.parent_archive_sha256 or '') and
         archive.is_file() and 0 < archive.stat().st_size <= 2 * 1024**3 and
         h.sha(archive) == args.parent_archive_sha256, 'original_parent_archive_hash')
    need(re.fullmatch('[a-f0-9]{64}', args.manifest_sha256 or '') and
         manifest_path.is_relative_to(h.REPO / '.task-tools/artifacts') and
         h.sha(manifest_path) == args.manifest_sha256, 'original_manifest_hash')
    manifest = h.load(manifest_path)
    need(manifest.get('schema') == 1 and manifest.get('purpose') == 'TEST installed AN dual native' and
         manifest.get('releaseScope') == 'linux-windows' and
         manifest.get('candidateCommit') == manifest.get('buildRevision') == CANDIDATE,
         'original_seven_cell_candidate_custody')
    need(isinstance(manifest.get('cells'), list) and len(manifest['cells']) == 7 and
         {(c['os'], c['arch'], c['version']) for c in manifest['cells']} == RELEASE_CELLS, 'seven_release_cells_only')
    wanted = {'manifest.json', 'release-inputs-receipt.json'}
    records = {}
    with tarfile.open(archive, 'r:gz') as packed:
        seen = set(); total = 0
        for member in packed:
            h.archive_name(member.name)
            need(member.isfile() and member.name not in seen and len(seen) < 128 and
                 0 <= member.size <= 536870912, 'original_parent_archive_members')
            seen.add(member.name); total += member.size
            need(total <= 2 * 1024**3, 'original_parent_archive_size')
            if member.name in wanted:
                need(member.size <= 1048576, 'original_parent_metadata_bound')
                with packed.extractfile(member) as source:
                    records[member.name] = source.read(member.size + 1)
    need(set(records) == wanted and records['manifest.json'] == manifest_path.read_bytes(),
         'manifest_from_original_parent_archive')
    receipt = h.parse(records['release-inputs-receipt.json'])
    need(str(receipt.get('runId')) == args.original_run_id and receipt.get('candidateCommit') == CANDIDATE and
         receipt.get('releaseScope') == 'linux-windows' and receipt.get('macOSOpenCodeDesktopQualified') is False,
         'original_release_run_receipt')
    return manifest


def checked_record(h, record):
    need(isinstance(record, dict) and set(record) == {'path', 'sha256'} and
         isinstance(record['path'], str) and re.fullmatch('[a-f0-9]{64}', record['sha256'] or ''), 'original_artifact_record')
    h.archive_name(record['path'])
    path = h.canonical_path(h.REPO / record['path'])
    need(path.is_relative_to(h.REPO) and path.is_file() and h.sha(path) == record['sha256'], 'original_artifact_hash')
    return path


def reuse_helper(h, owned, env, root, args, manifest):
    cells = [c for c in manifest['cells'] if (c['os'], c['arch']) == (args.os, args.arch)]
    records = [c['candidate'] for c in cells]
    need(records and all(record == records[0] for record in records), 'one_original_platform_binary')
    binary = checked_record(h, records[0])
    name = f'claude-notifications-{args.os}-{args.arch}' + ('.exe' if args.os == 'windows' else '')
    need(binary == h.canonical_path(h.REPO / 'dist' / name), 'normal_original_release_binary_path')
    with binary.open('rb') as stream:
        h.native_header(stream.read(4096), args.os, args.arch)
    if args.os == 'windows':
        go_root = h.canonical_path(Path(owned.command(['go', 'env', 'GOROOT'], root, env).decode().strip()).resolve(strict=True))
        go_exe = h.canonical_path(go_root / 'bin/go.exe')
        selected = shutil.which('go', path=env['PATH'])
        need(selected and go_exe.samefile(selected), 'same_native_go_toolchain')
        env = dict(env, GOROOT=str(go_root), PATH=str(go_root / 'bin') + os.pathsep + env['PATH'])
    version = owned.command(['go', 'version'], root, env)
    need(re.fullmatch(rb'go version go1\.27\.1 ' + args.os.encode() + b'/' + args.arch.encode() + rb'\r?\n', version),
         'pinned_native_validation_go')
    info = owned.command(['go', 'version', '-m', str(binary)], root, env)
    settings, binary_modules = build_settings(info, args.os, args.arch)
    dependencies = h.json_stream(owned.command(['go', 'list', '-deps', '-json', './cmd/claude-notifications'],
                                             h.REPO, env, 120, 16777216))
    owned.command(['go', 'mod', 'verify'], h.REPO, env, 60)
    leaves, modules = [], {}
    for package in dependencies:
        need(not package.get('Error') and not package.get('Incomplete'), 'complete_go_dependency_closure')
        directory = h.canonical_path(package['Dir'], 'go_dependency_directory')
        module = package.get('Module'); identity = 'native_go_toolchain_stdlib'
        if module:
            need(not module.get('Replace'), 'unreviewed_go_module_replace')
            identity = module['Path'] + '@' + module.get('Version', 'checkout')
            if identity not in modules:
                modules[identity] = {'path': module['Path'], 'version': module.get('Version'),
                                     'sum': module.get('Sum'), 'goModSHA256': h.sha(h.canonical_path(module['GoMod']))}
        for category in ('GoFiles', 'CgoFiles', 'CFiles', 'CXXFiles', 'MFiles', 'HFiles', 'FFiles', 'SFiles', 'SysoFiles', 'EmbedFiles'):
            for name in package.get(category, []):
                path = h.canonical_path(directory / name, 'go_dependency_leaf')
                need(path.is_relative_to(directory) and path.is_file(), 'original_go_leaf_containment')
                leaf = {'package': package['ImportPath'], 'category': category, 'leaf': name,
                        'actualSHA256': h.sha(path), 'actualBytes': path.stat().st_size, 'origin': identity}
                if path.is_relative_to(h.REPO):
                    leaf.update(h.git_leaf(owned, env, path.relative_to(h.REPO).as_posix(), CANDIDATE, args.os))
                leaves.append(leaf)
    observed_modules = {v['path']: (v['version'], v['sum']) for v in modules.values() if v['version'] is not None}
    need(observed_modules == binary_modules, 'original_binary_dependency_closure')
    need(all(any(x.get('path') == p for x in leaves) for p in ('cmd/claude-notifications/main.go',
         'cmd/claude-notifications/opencode_clock.go', 'internal/opencodeevent/clockprotocol.go')), 'actual_go_main_e0_closure')
    helper = root / ('helper.exe' if args.os == 'windows' else 'helper')
    shutil.copyfile(binary, helper); helper.chmod(0o700)
    need(h.sha(helper) == records[0]['sha256'], 'original_helper_copy_identity')
    closure = {'leaves': leaves, 'modules': modules, 'buildInfoSHA256': h.digest(info),
               'binarySHA256': records[0]['sha256'], 'goVersion': version.decode().strip(), 'sourceCommit': CANDIDATE,
               'CGO_ENABLED': '1', 'vcsModified': False, 'artifactReused': True, 'buildPerformed': False,
               'originalBinaryPath': records[0]['path'], 'originalRunID': args.original_run_id,
               'originalParentArchiveSHA256': args.parent_archive_sha256, 'originalBuildSettings': settings,
               'closureMethod': 'fresh_go_list_deps_and_go_mod_verify_bound_to_original_binary_modules',
               'validationCompilerSHA256': h.sha(h.canonical_path(Path(shutil.which('go', path=env['PATH'])).resolve(strict=True)))}
    h.write_json(root / 'go-artifact-reuse-closure.json', closure)
    return helper, closure


def execute(args):
    need(os.environ.get('GITHUB_ACTIONS') == 'true', 'explicit_github_actions_execution_only')
    need(not any(value for key, value in os.environ.items() if key in ('GH_TOKEN', 'GITHUB_TOKEN') or
                 key.endswith('_API_KEY')), 'credentials_forbidden_in_native_adapter')
    need(os.environ.get('SOURCE_COMMIT') == CANDIDATE, 'immutable_release_source_commit')
    need(args.original_run_id == ORIGINAL_RUN_ID and args.parent_archive_sha256 == ORIGINAL_ARCHIVE_SHA256,
         'immutable_original_release_run_and_archive')
    need((args.os, args.arch, args.version) in RELEASE_CELLS, 'seven_release_cells_only')
    actual_os = {'Linux': 'linux', 'Windows': 'windows'}.get(platform.system())
    actual_arch = {'x86_64': 'amd64', 'AMD64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine())
    need((actual_os, actual_arch) == (args.os, args.arch), 'actual_native_host')
    h, actual_harness_sha = exact_harness(args.candidate_repo.resolve(strict=True))
    started = time.monotonic(); job_end = started + h.BUDGETS['jobSeconds']
    watchdog = threading.Timer(h.BUDGETS['jobSeconds'], _thread.interrupt_main)
    watchdog.daemon = True; watchdog.start()
    owned = h.Owned(job_end); source_leaves = []; build = None; stage = 'original_custody'
    report = {'schema': 1, 'purpose': 'fresh_release_artifact_platform_clock_observation', 'status': 'unqualified',
              'qualificationLevel': 'module_prequalification_only', 'executionOwner': 'parent_ci',
              'nativeWorkerExecution': False, 'platform': args.os, 'arch': args.arch, 'cases': [],
              'selectedCell': {'os': args.os, 'arch': args.arch, 'version': args.version},
              'plannedVersions': [args.version], 'budgets': h.BUDGETS, 'reviewedBase': h.BASE,
              'expectedSourceCommit': CANDIDATE,
              'immutableHarnessGitBlobSHA256': HARNESS_SHA256, 'immutableHarnessActualSHA256': actual_harness_sha,
              'adapterSHA256': h.sha(Path(__file__).resolve()), 'originalRunID': args.original_run_id,
              'originalParentArchiveSHA256': args.parent_archive_sha256, 'originalManifestSHA256': args.manifest_sha256,
              'manifestSHA256': args.manifest_sha256,
              'preparationDeviations': ['single_requested_release_cell', 'original_release_helper_and_archive_reuse',
                'fresh_dependency_closure_without_build'] + (['externally_isolated_unprivileged_linux_namespace'] if args.os == 'linux' else []),
              'operationalAssumptions': {'trustedGlobalsAndBuiltins': True, 'immutableProtectedMappings': True,
                'windowsProtectedDefaultSystem32Win64APISet': args.os == 'windows',
                'linuxTrustedProcMountAndCallingThreadTimeNamespace': args.os == 'linux',
                'sampleObservationsAreNotFutureWallRateSuspendOrComparisonBounds': True},
              'modelCalls': 0, 'providerCalls': 0, 'sessionCreates': 0, 'installerCalls': 0,
              'suspendExperiments': 0, 'qualificationRowsFilled': 0, 'buildPerformed': False, **h.QUALIFICATIONS}
    try:
        root = h.private_root(args.temp_base)
        env = h.environment(root, tooling=True)
        # Dependencies are pre-fetched in the trusted CI preparation phase.
        # Fresh closure reading cannot fetch or change any module/source bytes.
        cache = h.canonical_path(args.go_module_cache.resolve(strict=True), 'prefetched_go_module_cache')
        env.update(GOMODCACHE=str(cache), GOPROXY='off', GOSUMDB='off', GOFLAGS='-mod=readonly')
        h.checkout(owned, env, CANDIDATE)
        dirty = h.git(owned, env, 'ls-files', '--others', '--exclude-standard', '--', '.', ':(exclude).task-tools/artifacts')
        need(not dirty.strip(), 'clean_untracked_candidate_checkout')
        manifest = custody(h, args)
        embedded = checked_record(h, manifest['assets']['embedded'])
        need(embedded == h.canonical_path(h.REPO / 'internal/opencodeplugin/dist/agent-notifications.js'),
             'original_embedded_asset_path')
        report['embeddedSHA256'] = manifest['assets']['embedded']['sha256']
        source_leaves = h.source_closure(owned, env, CANDIDATE, args.os, root)
        report.update(actualGitHEAD=CANDIDATE, cleanTrackedCheckout=True, checkoutLeaves=source_leaves,
                      checkoutClosureSHA256=h.digest(h.canonical(source_leaves)))
        stage = 'original_helper_dependency_closure'
        helper, build = reuse_helper(h, owned, env, root, args, manifest)
        report.update(goBuildClosure=build, goBuildClosureSHA256=h.digest(h.canonical(build)), artifactReused=True,
                      candidateSHA256=build['binarySHA256'])
        cell = next(c for c in manifest['cells'] if (c['os'], c['arch'], c['version']) == (args.os, args.arch, args.version))
        archive = checked_record(h, cell['archive'])
        item = {'archive': cell['hostURL'], 'archiveSHA256': cell['archive']['sha256'],
                'binarySHA256': cell['executableSHA256'], 'sri': cell['archiveSRI']}
        need(cell['hostSourceCommit'] == h.VERSIONS[args.version][0], 'original_stock_origin_commit')
        if args.version == '1.18.34':
            pin = re.search(r"\['1\.18\.34', '([a-f0-9]{64})'\]", (h.REPO / 'opencode-plugin/clock-cells.mjs').read_text())
            need(pin and pin[1] == cell['executableSHA256'], 'existing_current_linux_image_pin')
            item = h.current_primary(dict(item, version=args.version, commit=cell['hostSourceCommit'], bun='1.3.14',
                                         os='linux', arch='x64'), pin[1])
        else:
            reviewed = h.closed_inputs(h.load(h.REPO / 'scripts/testdata/opencode-clock-qualification/closed-inputs.json'))
            matches = [v for v in reviewed['artifacts'] if (v['version'], v['os'], v['arch']) ==
                       (args.version, args.os, 'x64' if args.arch == 'amd64' else 'arm64')]
            need(len(matches) == 1 and all(matches[0][k] == item[k] for k in item), 'immutable_reviewed_stock_archive')

        def original_archive(_owned, _env, case_root, requested):
            need(all(requested[k] == item[k] for k in item), 'requested_original_archive_identity')
            copied = case_root / 'official.tgz'; shutil.copyfile(archive, copied); copied.chmod(0o600)
            need(h.sha(copied) == item['archiveSHA256'], 'original_stock_archive_copy')
            import base64
            with copied.open('rb') as stream:
                sri = 'sha512-' + base64.b64encode(hashlib.file_digest(stream, 'sha512').digest()).decode()
            need(sri == item['sri'], 'original_stock_archive_sri')
            return copied

        h.fetch_archive = original_archive
        stage = 'original_case_staging'
        case_root, metadata = h.stage_case(owned, env, root, args.version, item, source_leaves, helper,
                                           build['binarySHA256'], CANDIDATE, args.os, args.arch, cell['hostSourceCommit'])
        report['caseMetadata'] = metadata
        if args.os == 'linux':
            need(os.geteuid() != 0, 'unprivileged_native_fixture')
            report['nativeNamespaceProof'] = native_namespace(os.environ.get('AN_HOST_NETNS', ''),
                os.readlink('/proc/self/ns/net'), [name for _, name in socket.if_nameindex()])
        stage = 'actual_native_observation'
        result = h.run_case(case_root, metadata, args.os, args.arch, job_end)
        report['cases'].append(result)
        need(result['status'] == 'module_prequalification_observed' and
             all(result[k] is False for k in h.QUALIFICATIONS), 'original_native_case_required')
        # Preserve the genuine semantic result, including its original kind.
        # The immutable harness normally exposes only its validated projection.
        stream_path = case_root / 'module-stream-0.private'
        transcript = stream_path.read_bytes()
        need(0 < len(transcript) <= 65536, 'original_module_stream_bound')
        frames = [h.parse(line, h.BUDGETS['frameBytes']) for line in transcript.splitlines()]
        disposed = [frame for frame in frames if frame.get('kind') == 'disposed']
        need(len(disposed) == 1, 'one_genuine_disposed_result')
        h.validate_result(disposed[0], args.os)
        need(all(result[k] == v for k, v in disposed[0].items() if k != 'kind'), 'original_result_projection_identity')
        result.update(actualModuleResult=disposed[0], originalModuleStreamSHA256=h.digest(transcript))
        h.checkout(owned, env, CANDIDATE)
        need(all(h.sha(h.REPO / v['path']) == v['actualSHA256'] for v in source_leaves), 'final_source_closure')
        need(h.sha(checked_record(h, cell['candidate'])) == build['binarySHA256'], 'final_original_helper_identity')
        need(h.sha(embedded) == report['embeddedSHA256'], 'final_original_embedded_identity')
        h.remaining(job_end)
        report['status'] = 'module_prequalification_observed'
    except Exception as error:
        report.update(status='unqualified', failureReason=h.failure_reason(error),
                      failureObservation={'stage': stage, 'parentElapsedMs': round((time.monotonic() - started) * 1000, 3)})
    except KeyboardInterrupt:
        report.update(status='unqualified', failureReason='absolute_deadline' if time.monotonic() >= job_end else 'parent_interrupted')
    finally:
        watchdog.cancel(); owned.cleanup(); report['parentOwnership'] = owned.summary()
        report['helperStarts'] = sum(c.get('helperStarts', 0) for c in report['cases'])
        report['helperActualCloses'] = sum(c.get('helperActualCloses', 0) for c in report['cases'])
        report['maxOwnedWeightIncludingSupervisor'] = max([owned.highwater] + [c.get('maxOwnedWeight', 0) for c in report['cases']])
        report['allOwnedHandlesClosed'] = not owned.live and all(c.get('allOwnedHandlesClosed') is True for c in report['cases'])
        if report['status'] == 'module_prequalification_observed' and not (report['helperStarts'] == report['helperActualCloses'] == 3 and
                report['allOwnedHandlesClosed'] and report['maxOwnedWeightIncludingSupervisor'] <= 4):
            report.update(status='unqualified', failureReason='cell_lifecycle_counts')
        if time.monotonic() >= job_end:
            report.update(status='unqualified', failureReason='absolute_deadline')
        h.write_json(args.report, report)
        if time.monotonic() >= job_end and report['status'] != 'unqualified':
            report.update(status='unqualified', failureReason='absolute_deadline')
            late = args.report.with_name(args.report.name + '.owned-late')
            h.write_json(late, report); os.replace(late, args.report)
    return 0 if report['status'] == 'module_prequalification_observed' else 1


def self_test():
    # These fail if reused/externally connected namespaces or wrong build
    # revision/CGO/platform metadata become admissible. No runtime is launched.
    native_namespace('net:[10]', 'net:[11]', ['lo'])
    for values in [('net:[10]', 'net:[10]', ['lo']), ('bad', 'net:[11]', ['lo']),
                   ('net:[10]', 'net:[11]', ['lo', 'eth0'])]:
        try:
            native_namespace(*values)
        except RuntimeError:
            continue
        raise AssertionError('unsafe_namespace_accepted')
    info = ('synthetic: go1.27.1\n\tpath\tgithub.com/777genius/agent-notifications/cmd/claude-notifications\n' +
            ''.join('\tbuild\t' + k + '=' + v + '\n' for k, v in {'vcs.revision': CANDIDATE,
              'vcs.modified': 'false', 'vcs': 'git', 'GOOS': 'linux', 'GOARCH': 'amd64', 'CGO_ENABLED': '1',
              '-trimpath': 'true', '-buildmode': 'exe', '-compiler': 'gc'}.items())).encode()
    build_settings(info, 'linux', 'amd64')
    for old, new in [(CANDIDATE.encode(), b'0' * 40), (b'vcs.modified=false', b'vcs.modified=true'),
                     (b'CGO_ENABLED=1', b'CGO_ENABLED=0'), (b'GOARCH=amd64', b'GOARCH=arm64')]:
        try:
            build_settings(info.replace(old, new), 'linux', 'amd64')
        except RuntimeError:
            continue
        raise AssertionError('wrong_original_build_accepted')
    print('PASS inert namespace and original Go metadata admission')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument('--self-test', action='store_true'); modes.add_argument('--execute-ci', action='store_true')
    for name in ('candidate-repo', 'temp-base', 'report', 'manifest', 'parent-archive', 'go-module-cache'):
        parser.add_argument('--' + name, type=Path)
    parser.add_argument('--os', choices=('linux', 'windows'))
    parser.add_argument('--arch', choices=('amd64', 'arm64'))
    parser.add_argument('--version', choices=('1.18.33', '2.0.21', '1.18.34'))
    for name in ('manifest-sha256', 'parent-archive-sha256', 'original-run-id'):
        parser.add_argument('--' + name)
    args = parser.parse_args(); os.umask(0o077)
    if args.self_test:
        self_test(); return 0
    need(all(v is not None for k, v in vars(args).items() if k not in ('self_test', 'execute_ci')), 'explicit_ci_inputs_required')
    return execute(args)


if __name__ == '__main__':
    raise SystemExit(main())
