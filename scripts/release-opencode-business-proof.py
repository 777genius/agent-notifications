"""External TEST evidence sealer. No native execution, network, builds or grants.

The immutable installed business runner checks only custody. This tool checks
the contents of fresh primitive reports before producing its six-key input.
"""
import argparse
import copy
import hashlib
import json
import math
from pathlib import Path, PurePosixPath
import re
import tarfile
import types
import unittest

CANDIDATE = 'e86149a724f9b96cb5ee193d63dbc5a533c61e7e'
CLOCK_VALIDATOR_SHA = '1c30c5e625ef4b59435a4231ef9bc45882ceacb6dcb16fa0419ccffada6c72e0'
ORIGINAL_RUN = '37519336110'
ORIGINAL_ARCHIVE = 'ea156e1978d5acad56ff6cfbebc8cb20ba646b55a271e137dc696e6646bc64d9'
CELLS = {(os_name, arch, version) for os_name, arch in
         [('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64')]
         for version in ['1.18.33', '2.0.21']} | {('linux', 'amd64', '1.18.34')}
READER_FLAGS = ('productionQualified', 'installedQualified', 'timePolicyQualified',
                'sourceEpochQualified', 'finalSpanQualified', 'platformLifetimeQualified',
                'loadedMappedBytesQualified', 'fullNativeQualified', 'clockQualified',
                'visibleDesktopQualified', 'sourceDescriptorGranted')
LIMIT = 32 * 1024 * 1024


def need(value, reason):
    if not value:
        raise ValueError(reason)


def digest(body):
    return hashlib.sha256(body).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()


def unique(pairs):
    result = {}
    for key, value in pairs:
        need(key not in result, 'duplicate_json_key')
        result[key] = value
    return result


def regular(path):
    path = Path(path).absolute()
    need(not any(p.is_symlink() for p in (path, *path.parents)), 'input_symlink')
    need(path.is_file(), 'input_regular_file')
    return path


def read(path, expected):
    path = regular(path)
    need(re.fullmatch('[a-f0-9]{64}', str(expected)), 'required_input_digest')
    need(path.stat().st_size <= LIMIT, 'input_size_bound')
    body = path.read_bytes()
    need(digest(body) == expected, 'input_digest_mismatch')
    value = json.loads(body, object_pairs_hook=unique,
                       parse_constant=lambda _: (_ for _ in ()).throw(ValueError('nonfinite_json')))
    need(isinstance(value, dict), 'report_object_required')
    return value, body


def leaf(root, name, expected):
    relative = PurePosixPath(name)
    need(not relative.is_absolute() and relative.parts and
         all(p not in ('.', '..') for p in relative.parts) and '\\' not in name,
         'source_leaf_path')
    path = regular(root.joinpath(*relative.parts))
    need(path.is_relative_to(root.absolute()), 'source_leaf_escape')
    need(digest(path.read_bytes()) == expected, 'source_leaf_changed')


def refuse_grants(value):
    """Reject nested affirmative grants, including future report grant fields."""
    if isinstance(value, dict):
        for key, item in value.items():
            if key.lower().endswith(('qualified', 'granted')):
                need(item is False, 'qualification_grant_refused')
            refuse_grants(item)
    elif isinstance(value, list):
        for item in value:
            refuse_grants(item)


def network_boundary(boundary, os_name):
    need(isinstance(boundary, dict), 'native_network_boundary_required')
    if os_name == 'linux':
        need(re.fullmatch(r'net:\[[1-9][0-9]*\]', str(boundary.get('host'))) and
             re.fullmatch(r'net:\[[1-9][0-9]*\]', str(boundary.get('private'))) and
             boundary['host'] != boundary['private'] and boundary.get('interfaces') == ['lo'],
             'native_private_loopback_namespace')
    else:
        need(boundary.get('platform') == 'windows' and boundary.get('networkIsolationClaimed') is False and
             boundary.get('isolation') == 'private_files_minimal_env_loopback_provider_no_network_isolation_claim',
             'honest_windows_network_limitation')


def identity(report, expected, cell):
    need(report.get('schema') == 1 and report.get('candidateCommit') == CANDIDATE,
         'exact_candidate_required')
    need(report.get('cell') == cell, 'exact_cell_required')
    for key in ('embeddedSHA256', 'candidateSHA256', 'manifestSHA256'):
        need(report.get(key) == expected[key], 'exact_' + key)
    need(not report.get('diagnosticOnly') and not report.get('failureReason'),
         'passing_nondiagnostic_report_required')
    refuse_grants(report)


def validator(source):
    path = regular(source / 'scripts/opencode-platform-clock-prequalification.py')
    body = path.read_bytes()
    need(digest(body) == CLOCK_VALIDATOR_SHA, 'immutable_clock_validator')
    # Execute verified source directly: never consume or create a pyc cache.
    module = types.ModuleType('immutable_clock_semantics')
    module.__file__ = str(path)
    exec(compile(body, str(path), 'exec'), module.__dict__)
    return module


def selected_manifest(manifest, manifest_sha, cell):
    need(manifest.get('candidateCommit') == manifest.get('buildRevision') == CANDIDATE,
         'manifest_exact_candidate')
    need(manifest.get('releaseScope') == 'linux-windows', 'explicit_partial_scope')
    cells = manifest.get('cells', [])
    actual = [(x['os'], x['arch'], x['version']) for x in cells]
    need(len(actual) == len(CELLS) and set(actual) == CELLS, 'all_seven_cells_required')
    matches = [x for x in cells if {k: x[k] for k in cell} == cell]
    need(len(matches) == 1, 'selected_cell_required')
    item = matches[0]
    expected = {'embeddedSHA256': manifest['assets']['embedded']['sha256'],
                'candidateSHA256': item['candidate']['sha256'], 'manifestSHA256': manifest_sha,
                'sdkArchiveSHA256': manifest['sdk']['archive']['sha256'],
                'hostArchiveSHA256': item['archive']['sha256'],
                'ownedImageSHA256': item['executableSHA256'], 'hostSourceCommit': item['hostSourceCommit']}
    need(all(re.fullmatch('[a-f0-9]{64}', str(v)) for k, v in expected.items() if k != 'hostSourceCommit') and
         re.fullmatch('[a-f0-9]{40}', str(expected['hostSourceCommit'])),
         'closed_manifest_digests')
    return expected


def source_leaves(leaves, source, required):
    need(isinstance(leaves, list) and leaves, 'source_closure_required')
    paths = []
    for record in leaves:
        need(isinstance(record, dict), 'source_leaf_record')
        if 'path' in record:
            paths.append(record['path'])
            leaf(source, record['path'], record['actualSHA256'])
    need(len(paths) == len(set(paths)) and set(required) <= set(paths), 'complete_source_closure')


def validate_clock(report, expected, cell, source, module):
    need(report.get('schema') == 1 and
         report.get('purpose') == 'fresh_release_artifact_platform_clock_observation' and
         report.get('status') == 'module_prequalification_observed' and
         report.get('qualificationLevel') == 'module_prequalification_only', 'actual_clock_report_required')
    need(report.get('expectedSourceCommit') == report.get('actualGitHEAD') == CANDIDATE,
         'exact_clock_candidate')
    need(report.get('platform') == cell['os'] and report.get('arch') == cell['arch'] and
         report.get('plannedVersions') == [cell['version']], 'exact_clock_cell')
    need(report.get('originalManifestSHA256') == expected['manifestSHA256'], 'clock_manifest_binding')
    if cell['os'] == 'linux':
        network_boundary(report.get('nativeNamespaceProof'), cell['os'])
    need(report.get('selectedCell') == cell and report.get('candidateSHA256') == expected['candidateSHA256'] and
         report.get('embeddedSHA256') == expected['embeddedSHA256'] and
         report.get('manifestSHA256') == expected['manifestSHA256'], 'clock_selected_identity')
    need(report.get('immutableHarnessGitBlobSHA256') == report.get('immutableHarnessActualSHA256') ==
         CLOCK_VALIDATOR_SHA, 'actual_immutable_clock_harness')
    need(report.get('budgets') == module.BUDGETS and report.get('artifactReused') is True and
         report.get('buildPerformed') is False and report.get('cleanTrackedCheckout') is True and
         not report.get('failureReason'), 'honest_clock_reuse')
    refuse_grants(report)
    need(all(report.get(k) is False for k in module.QUALIFICATIONS), 'clock_false_grants')
    need(all(report.get(k) == 0 for k in ('modelCalls', 'providerCalls', 'sessionCreates',
         'installerCalls', 'suspendExperiments', 'qualificationRowsFilled')), 'clock_zero_business_effects')
    leaves = report.get('checkoutLeaves')
    need(report.get('checkoutClosureSHA256') == digest(module.canonical(leaves)), 'clock_checkout_closure_hash')
    source_leaves(leaves, source, ['opencode-plugin/platform-clock.mjs', 'opencode-plugin/protocol.mjs'])
    build = report.get('goBuildClosure')
    need(isinstance(build, dict) and report.get('goBuildClosureSHA256') == digest(module.canonical(build)),
         'clock_go_closure_hash')
    need(build.get('sourceCommit') == CANDIDATE and build.get('binarySHA256') == expected['candidateSHA256'] and
         build.get('artifactReused') is True and build.get('buildPerformed') is False and
         build.get('vcsModified') is False and build.get('CGO_ENABLED') == '1', 'clock_original_binary')
    settings = build.get('originalBuildSettings', {})
    need(settings.get('vcs.revision') == CANDIDATE and settings.get('vcs.modified') == 'false' and
         settings.get('GOOS') == cell['os'] and settings.get('GOARCH') == cell['arch'] and
         settings.get('CGO_ENABLED') == '1', 'clock_original_build_identity')
    need(build.get('originalRunID') == report.get('originalRunID') == ORIGINAL_RUN and
         build.get('originalParentArchiveSHA256') == report.get('originalParentArchiveSHA256') == ORIGINAL_ARCHIVE,
         'clock_original_run_binding')
    need(build.get('closureMethod') == 'fresh_go_list_deps_and_go_mod_verify_bound_to_original_binary_modules' and
         isinstance(build.get('modules'), dict) and build['modules'], 'clock_dependency_closure')
    source_leaves(build.get('leaves'), source, ['cmd/claude-notifications/main.go',
                 'cmd/claude-notifications/opencode_clock.go', 'internal/opencodeevent/clockprotocol.go'])
    cases = report.get('cases')
    need(isinstance(cases, list) and len(cases) == 1, 'single_actual_clock_case')
    metadata = report.get('caseMetadata', {})
    need(metadata.get('version') == cell['version'] and metadata.get('sourceCommit') == CANDIDATE and
         metadata.get('originCommit') == expected['hostSourceCommit'] and
         metadata.get('os') == cell['os'] and metadata.get('goarch') == cell['arch'] and
         metadata.get('platform') == ('win32' if cell['os'] == 'windows' else cell['os']) and
         metadata.get('arch') == ('x64' if cell['arch'] == 'amd64' else 'arm64') and
         metadata.get('imageSha256') == expected['ownedImageSHA256'] and
         metadata.get('archiveSha256') == expected['hostArchiveSHA256'] and
         metadata.get('helperSha256') == expected['candidateSHA256'], 'clock_actual_stock_case_binding')
    need(metadata.get('moduleLeaves') == [x for x in leaves if x['path'] in module.MODULES] and
         {x['path'] for x in metadata['moduleLeaves']} == set(module.MODULES), 'clock_actual_module_leaf_binding')
    case = cases[0]
    keys = {'status', 'actualModuleBound', 'rounds', 'samples', 'comparisons', 'datePredicates',
            'disposeCalls', 'sampleAfterDisposeRefused', 'operationElapsedMs', 'jsElapsedMs', 'checks',
            *module.QUALIFICATIONS}
    if cell['os'] == 'windows':
        keys.update(('warmupSamples', 'measuredSamples', 'measuredInstances', 'ownedClockLifetime'))
    actual = case.get('actualModuleResult')
    need(isinstance(actual, dict), 'genuine_clock_disposed_result_required')
    module.validate_result(actual, cell['os'])
    need(all(case[k] == actual[k] for k in keys), 'clock_summary_matches_actual_result')
    need(re.fullmatch('[a-f0-9]{64}', str(case.get('originalModuleStreamSHA256'))),
         'genuine_clock_stream_custody')
    need(case.get('kernelExecutingImageVerified') is True and case.get('allOwnedHandlesClosed') is True and
         case.get('helperStarts') == case.get('helperActualCloses') == 3 and
         case.get('ownedStarts') == case.get('ownedActualCloses') == 4 and
         case.get('maxOwnedWeight') <= 2 and not case.get('failureReason'), 'clock_case_actual_closure')
    need(len(case.get('actualHelperOutputReceiptSHA256', [])) == 3 and
         all(re.fullmatch('[a-f0-9]{64}', str(x)) for x in case['actualHelperOutputReceiptSHA256']) and
         re.fullmatch('[a-f0-9]{64}', str(case.get('rawPrivateReceiptSHA256'))), 'clock_raw_receipt_custody')
    parent = report.get('parentOwnership', {})
    need(parent.get('allOwnedHandlesClosed') is True and
         parent.get('ownedStarts') == parent.get('ownedActualCloses') and
         parent.get('helperStarts') == parent.get('helperActualCloses') and
         report.get('helperStarts') == report.get('helperActualCloses') == 3 and
         report.get('allOwnedHandlesClosed') is True and report.get('maxOwnedWeightIncludingSupervisor') <= 4,
         'clock_parent_actual_closure')


def validate_reader_graph(report, source, expected):
    graph = report.get('sourceGraph', {})
    need(graph.get('schema') == 1 and graph.get('status') == 'fresh_release_reader_bundle_built' and
         graph.get('candidateCommit') == CANDIDATE and graph.get('sdkVersion') == '0.3.0' and
         graph.get('sdkArchiveSHA256') == expected['sdkArchiveSHA256'] and
         graph.get('bundleSHA256') == report.get('bundleSHA256'), 'fresh_reader_source_graph')
    compiler = graph.get('compiler', {})
    need(compiler.get('strict') is True and compiler.get('erasableSyntaxOnly') is True,
         'reader_strict_compilation')
    lock = json.loads(regular(source / 'opencode-plugin/package-lock.json').read_bytes(), object_pairs_hook=unique)
    need(compiler.get('esbuildVersion') == lock['packages']['node_modules/esbuild']['version'] and
         re.fullmatch('[a-f0-9]{64}', str(graph.get('metafileSHA256'))) and
         re.fullmatch('[a-f0-9]{64}', str(report.get('bundleSHA256'))), 'reader_locked_compiler_and_bundle')
    records = graph.get('leaves')
    need(isinstance(records, list) and records, 'reader_source_leaves_required')
    known = {x['path']: x for x in records}
    need(len(known) == len(records), 'reader_unique_leaves')
    compiled = graph.get('compiledInputs')
    required = {'candidate/opencode-plugin/native-v1.mjs', 'candidate/opencode-plugin/native-v2.mjs',
                'candidate/opencode-plugin/protocol.mjs',
                'node_modules/universal-agent-plugins-opencode-events/v1.js',
                'node_modules/universal-agent-plugins-opencode-events/v2.js'}
    need(isinstance(compiled, list) and len(compiled) == len(set(compiled)) and
         required <= set(compiled) <= set(known), 'reader_complete_import_graph')
    sdk = regular(source / 'opencode-plugin/vendor/universal-agent-plugins-opencode-events-0.3.0.tgz')
    need(digest(sdk.read_bytes()) == expected['sdkArchiveSHA256'], 'reader_exact_sdk_archive')
    with tarfile.open(sdk, 'r:gz') as packed:
        sdk_members = {x.name: packed.extractfile(x).read() for x in packed.getmembers() if x.isfile()}
    sdk_prefix = 'node_modules/universal-agent-plugins-opencode-events/'
    need({sdk_prefix + member[len('package/'):] for member in sdk_members} <= set(known),
         'reader_complete_sdk_source_archive')
    for name, record in known.items():
        if name.startswith('candidate/'):
            need(record['origin'] == 'candidate:' + CANDIDATE, 'reader_original_leaf_origin')
            leaf(source, name[len('candidate/'):], record['sha256'])
            body = regular(source / name[len('candidate/'):]).read_bytes()
        elif name == 'sdk.tgz':
            need(record['origin'] == 'candidate:' + CANDIDATE, 'reader_sdk_archive_origin')
            body = sdk.read_bytes()
        elif name.startswith('node_modules/universal-agent-plugins-opencode-events/'):
            member = 'package/' + name.split('universal-agent-plugins-opencode-events/', 1)[1]
            need(record['origin'] == 'sdk:' + expected['sdkArchiveSHA256'] + '#' + member,
                 'reader_original_sdk_leaf_origin')
            need(member in sdk_members, 'reader_original_sdk_member')
            body = sdk_members[member]
        else:
            mapping = {'release-opencode-reader.mts': ('fresh-reader-consumer', 'release-opencode-reader.mts'),
                       'driver/release-opencode-reader.py': ('fresh-reader-driver', 'release-opencode-reader.py')}
            need(name in mapping and record['origin'] == mapping[name][0], 'reader_reviewed_tool_leaf')
            body = regular(Path(__file__).parent / mapping[name][1]).read_bytes()
        need(record['sha256'] == digest(body) and record['bytes'] == len(body), 'reader_source_leaf_identity')


def validate_reader(report, expected, cell, source):
    identity(report, expected, cell)
    network_boundary(report.get('nativeNetworkBoundary'), cell['os'])
    need(report.get('purpose') == 'TEST fresh stock OpenCode packaged reader primitive' and
         report.get('status') == 'release_packaged_reader_observed' and
         all(report.get(k) is False for k in READER_FLAGS), 'actual_bounded_reader_report')
    for key in ('sdkArchiveSHA256', 'hostArchiveSHA256', 'ownedImageSHA256'):
        need(report.get(key) == expected[key], 'reader_exact_' + key)
    need(report.get('hostSourceCommit') == expected['hostSourceCommit'], 'reader_stock_origin_binding')
    need(report.get('installedRuntimeVersion') == cell['version'] and
         report.get('freshTestRootCreated') is True and report.get('packagedReaderTransportObserved') is True and
         report.get('sourcesArchiveBundleUnchanged') is True and report.get('providerThreadJoined') is True and
         report.get('ownedLeaderWaitObserved') is True and not report.get('hostForcedKillUsed'),
         'reader_actual_scope_and_close')
    validate_reader_graph(report, source, expected)
    v2 = cell['version'] == '2.0.21'
    validate_provider(report, v2)
    ready, closed = report.get('readerReady', {}), report.get('readerClosed', {})
    for receipt in (ready, closed):
        need(receipt.get('schemaVersion') == 1 and receipt.get('scope') == 'TEST packaged SDK reader' and
             receipt.get('generation') == ('v2' if v2 else 'v1') and receipt.get('version') == cell['version'] and
             receipt.get('os') == cell['os'] and receipt.get('arch') == cell['arch'] and
             receipt.get('ownedImageSHA256') == expected['ownedImageSHA256'] and
             receipt.get('testAuthorityDerived') is True, 'reader_native_receipt_identity')
        need(all(receipt.get(k) is False for k in READER_FLAGS if k != 'sourceDescriptorGranted'),
             'reader_receipt_false_grants')
    need(ready.get('nativePID') == closed.get('nativePID') and type(ready.get('nativePID')) is int and
         ready['nativePID'] > 1 and ready.get('parentPID') == closed.get('parentPID') and
         ready.get('actualExecPath') == closed.get('actualExecPath') and closed.get('actualSDKDispose') is True,
         'reader_actual_dispose_identity')
    rows = report.get('observations')
    need(isinstance(rows, list) and 0 < len(rows) <= 512 and
         all(isinstance(r, dict) and isinstance(r.get('value'), dict) and
             r.get('kind') != 'uncertainty' for r in rows), 'reader_certain_actual_trace')
    need(any(r['kind'] == 'reader-closed' and r['value'].get('actualDrain') is True for r in rows),
         'reader_trace_actual_drain')
    if v2:
        validate_v2(rows, ready, closed, report)
    else:
        validate_v1(rows, closed, report)


def validate_provider(report, v2):
    calls = 0 if v2 else 1
    need(all(type(report.get(k)) is int for k in ('plannedProviderTransactions',
         'actualProviderHTTPRequestAttempts', 'actualProviderTransactions', 'actualToolCalls')),
         'reader_actual_numeric_counts')
    need(report.get('mode') == ('api-only' if v2 else 'one-completion') and
         report.get('plannedProviderTransactions') == report.get('actualProviderHTTPRequestAttempts') ==
         report.get('actualProviderTransactions') == calls and report.get('actualToolCalls') == 0 and
         len(report.get('providerReceipts', [])) == calls, 'reader_exact_provider_budget')


def validate_v2(rows, ready, closed, report):
    need(ready.get('firstReaderClosureObserved') is True and ready.get('actualAbortRequested') is True and
         closed.get('actualSDKDone') is True and closed.get('actualAbortRequested') is True and
         closed.get('sdkFacts') == 0 and all(closed.get(k) == 2 for k in
         ('subscriptions', 'actualNativeReaderClosures', 'registrations', 'actualRegistrationDisposals', 'markerReads')),
         'reader_two_actual_rpc_closures')
    need(report.get('factoryFinalizationObserved') is False, 'v2_no_business_completion_claim')
    closes = [(i, r['value']) for i, r in enumerate(rows) if r['kind'] == 'native-reader-close']
    aborts = [(i, r['value']) for i, r in enumerate(rows) if r['kind'] == 'requested-native-abort']
    need(len(closes) == 2 and [r['number'] for _, r in closes] == [1, 2] and
         closes[0][1].get('actualSignalAborted') is True and len(aborts) == 1 and
         aborts[0][1].get('number') == 1 and aborts[0][0] < closes[0][0], 'reader_abort_before_close')
    registers = [r['value'] for r in rows if r['kind'] == 'rpc-register']
    markers = [r['value'] for r in rows if r['kind'] == 'rpc-marker-consumed']
    native = [r['value'] for r in rows if r['kind'] == 'native-v2']
    need(len(registers) == len(markers) == 2 and [r.get('number') for r in registers] == [1, 2] and
         [r.get('number') for r in markers] == [1, 2] and
         not any(r['kind'] == 'sdk-fact' for r in rows), 'reader_two_actual_registrations')
    for index, (registration, marker) in enumerate(zip(registers, markers), 1):
        envelope = marker.get('envelope', {})
        need(marker.get('markerReads') == index and envelope.get('type') == registration.get('type') and
             isinstance(envelope.get('id'), str) and re.fullmatch('h:[a-f0-9]{24}', envelope['id']) and
             type(envelope.get('created')) in (int, float) and math.isfinite(envelope['created']) and
             0 < envelope['created'] < 1e16 and
             isinstance(envelope.get('data'), dict) and set(envelope['data']) == {'nonce'} and
             isinstance(envelope['data']['nonce'], str) and
             re.fullmatch('h:[a-f0-9]{24}', envelope['data']['nonce']) and envelope in native,
             'reader_actual_rpc_identity_time')
        callback_at = next(i for i, row in enumerate(rows) if row['kind'] == 'native-v2' and row['value'] == envelope)
        marker_at = next(i for i, row in enumerate(rows) if row['kind'] == 'rpc-marker-consumed' and row['value'] == marker)
        need(callback_at < marker_at, 'reader_native_callback_before_marker')
    need(markers[0]['envelope']['id'] != markers[1]['envelope']['id'] and
         markers[0]['envelope']['data']['nonce'] != markers[1]['envelope']['data']['nonce'],
         'reader_distinct_rpc_markers')
    disposals = [r['value'].get('number') for r in rows if r['kind'] == 'rpc-disposed']
    need(disposals == [1, 2], 'reader_actual_registration_disposals')


def validate_v1(rows, closed, report):
    need(closed.get('actualTrackedJobsSettled') is True and type(closed.get('nativeCalls')) is int and
         closed['nativeCalls'] > 0 and sum(r['kind'] == 'sdk-observe-invoked' for r in rows) == closed['nativeCalls'] and
         closed.get('sdkFacts') == 1 and report.get('factoryFinalizationObserved') is True,
         'reader_actual_callback_jobs_settled')
    identity = report.get('nativeV1Identity', {})
    need(identity.get('finish') == 'stop' and all(isinstance(identity.get(k), str) and
         re.fullmatch('h:[a-f0-9]{24}', identity[k]) for k in ('sessionID', 'userID', 'assistantID')) and
         len({identity[k] for k in ('sessionID', 'userID', 'assistantID')}) == 3 and
         all(type(identity.get(k)) is int and identity[k] > 0 for k in
         ('userCreated', 'assistantCreated', 'assistantCompleted')) and
         identity['userCreated'] <= identity['assistantCreated'] <= identity['assistantCompleted'],
         'reader_actual_native_turn_identity_time')
    facts = [r['value'] for r in rows if r['kind'] == 'sdk-fact']
    need(len(facts) == 1, 'reader_one_actual_sdk_fact')
    value, fact = facts[0], facts[0].get('event', {})
    provenance = fact.get('provenance', {})
    need(value.get('currentAtCallback') is True and value.get('clockID') == 'local-performance' and
         fact.get('rootSession') is True and fact.get('kind') == 'turn_idle_verified' and
         fact.get('sessionID') == identity['sessionID'] and fact.get('turnID') == identity['userID'] and
         fact.get('messageID') == identity['assistantID'] and provenance.get('generation') == 'v1' and
         provenance.get('timeBasis') == 'assistant_completed' and
         provenance.get('nativeTime') == identity['assistantCompleted'], 'reader_original_callback_finalization')
    native = [r['value'].get('properties', {}).get('info', {}) for r in rows if
              r['kind'] == 'native-v1' and r['value'].get('type') == 'message.updated']
    need(any(info.get('id') == identity['assistantID'] and
         info.get('time', {}).get('completed') == identity['assistantCompleted'] for info in native),
         'reader_independent_native_final_callback')
    need(all(r.get('requestKind') == 'ordinary' for r in report['providerReceipts']), 'reader_ordinary_provider_request')


class InertContracts(unittest.TestCase):
    """Counterfeit reports must fail without launching a host or provider."""
    def test_identity_and_grants(self):
        expected = {k: 'a' * 64 for k in ('embeddedSHA256', 'candidateSHA256', 'manifestSHA256')}
        cell = {'os': 'linux', 'arch': 'amd64', 'version': '2.0.21'}
        report = dict(schema=1, candidateCommit=CANDIDATE, cell=cell, **expected)
        identity(report, expected, cell)
        for mutation in ({'candidateCommit': '0' * 40}, {'cell': dict(cell, arch='arm64')},
                         {'clockQualified': True}, {'nested': {'productionQualified': True}},
                         {'candidateSHA256': 'b' * 64}):
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                identity(dict(report, **mutation), expected, cell)

    def test_seven_cell_manifest(self):
        cell = {'os': 'linux', 'arch': 'amd64', 'version': '2.0.21'}
        manifest = {'candidateCommit': CANDIDATE, 'buildRevision': CANDIDATE,
                    'releaseScope': 'linux-windows', 'cells': []}
        # Scope markers alone cannot excuse any missing mandatory cell.
        with self.assertRaisesRegex(ValueError, 'all_seven_cells_required'):
            selected_manifest(manifest, 'a' * 64, cell)

    def test_provider_counts_are_actual_not_planned(self):
        report = dict(mode='api-only', plannedProviderTransactions=0, actualProviderHTTPRequestAttempts=0,
                      actualProviderTransactions=0, actualToolCalls=0, providerReceipts=[])
        validate_provider(report, True)
        for key in ('actualProviderHTTPRequestAttempts', 'actualProviderTransactions', 'actualToolCalls'):
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate_provider(dict(report, **{key: 1}), True)

    def test_v1_original_time_and_independent_callback(self):
        native = dict(sessionID='h:' + '1' * 24, userID='h:' + '2' * 24, assistantID='h:' + '3' * 24,
                      userCreated=100, assistantCreated=101, assistantCompleted=102, finish='stop')
        fact = dict(rootSession=True, kind='turn_idle_verified', sessionID=native['sessionID'],
                    turnID=native['userID'], messageID=native['assistantID'],
                    provenance={'generation': 'v1', 'timeBasis': 'assistant_completed', 'nativeTime': 102})
        rows = [{'kind': 'sdk-observe-invoked', 'value': {}},
                {'kind': 'sdk-fact', 'value': {'event': fact, 'currentAtCallback': True, 'clockID': 'local-performance'}},
                {'kind': 'native-v1', 'value': {'type': 'message.updated', 'properties':
                 {'info': {'id': native['assistantID'], 'time': {'completed': 102}}}}}]
        closed = dict(actualTrackedJobsSettled=True, nativeCalls=1, sdkFacts=1)
        report = dict(nativeV1Identity=native, factoryFinalizationObserved=True,
                      providerReceipts=[{'requestKind': 'ordinary'}])
        validate_v1(rows, closed, report)
        damaged = copy.deepcopy(rows)
        damaged[1]['value']['event']['provenance']['nativeTime'] = 103
        with self.assertRaisesRegex(ValueError, 'reader_original_callback_finalization'):
            validate_v1(damaged, closed, report)
        with self.assertRaisesRegex(ValueError, 'reader_independent_native_final_callback'):
            validate_v1(rows[:2], closed, report)

    def test_v2_actual_marker_and_closure_contract(self):
        ready = {'firstReaderClosureObserved': True, 'actualAbortRequested': True}
        closed = dict(actualSDKDone=True, actualAbortRequested=True, sdkFacts=0,
                      **{k: 2 for k in ('subscriptions', 'actualNativeReaderClosures',
                                       'registrations', 'actualRegistrationDisposals', 'markerReads')})
        rows = []
        for number in (1, 2):
            envelope = {'id': 'h:' + str(number) * 24, 'created': 100.5 + number,
                        'type': 'rpc.test.' + str(number), 'data': {'nonce': 'h:' + str(number + 2) * 24}}
            rows.extend([{'kind': 'rpc-register', 'value': {'number': number, 'type': envelope['type']}},
                         {'kind': 'native-v2', 'value': envelope},
                         {'kind': 'rpc-marker-consumed', 'value': {'number': number,
                          'markerReads': number, 'envelope': envelope}}])
            if number == 1:
                rows.append({'kind': 'requested-native-abort', 'value': {'number': 1}})
            rows.extend([{'kind': 'native-reader-close', 'value': {'number': number, 'actualSignalAborted': number == 1}},
                         {'kind': 'rpc-disposed', 'value': {'number': number}}])
        report = {'factoryFinalizationObserved': False}
        validate_v2(rows, ready, closed, report)
        for kind in ('native-v2', 'requested-native-abort', 'native-reader-close', 'rpc-disposed'):
            damaged = copy.deepcopy(rows)
            damaged.pop(next(i for i, r in enumerate(damaged) if r['kind'] == kind))
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                validate_v2(damaged, ready, closed, report)
        damaged = copy.deepcopy(rows)
        marker = next(r['value'] for r in damaged if r['kind'] == 'rpc-marker-consumed')
        marker['envelope'] = dict(marker['envelope'], created=500)
        with self.assertRaises(ValueError):
            validate_v2(damaged, ready, closed, report)
        with self.assertRaises(ValueError):
            validate_v2(rows, ready, dict(closed, actualSDKDone=False), report)
        damaged = copy.deepcopy(rows)
        callback = damaged.pop(next(i for i, r in enumerate(damaged) if r['kind'] == 'native-v2'))
        damaged.append(callback)
        with self.assertRaisesRegex(ValueError, 'reader_native_callback_before_marker'):
            validate_v2(damaged, ready, closed, report)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--self-test', action='store_true')
    for name in ('manifest', 'clock-report', 'reader-report', 'source-root', 'output'):
        parser.add_argument('--' + name, type=Path)
    for name in ('manifest-sha256', 'clock-report-sha256', 'reader-report-sha256', 'os', 'arch', 'version'):
        parser.add_argument('--' + name)
    args = parser.parse_args()
    if args.self_test:
        result = unittest.main(argv=['business-proof'], exit=False)
        need(result.result.wasSuccessful(), 'inert_contract_tests_failed')
        return
    need(all(v is not None for k, v in vars(args).items() if k != 'self_test'), 'all_inputs_required')
    cell = {'os': args.os, 'arch': args.arch, 'version': args.version}
    need(tuple(cell.values()) in CELLS, 'supported_partial_cell')
    manifest, _ = read(args.manifest, args.manifest_sha256)
    expected = selected_manifest(manifest, args.manifest_sha256, cell)
    source = args.source_root.absolute()
    leaf(source, manifest['assets']['embedded']['path'], expected['embeddedSHA256'])
    clock, clock_body = read(args.clock_report, args.clock_report_sha256)
    reader, reader_body = read(args.reader_report, args.reader_report_sha256)
    module = validator(source)
    validate_clock(clock, expected, cell, source, module)
    validate_reader(reader, expected, cell, source)
    # Every semantic/custody check finishes before the exclusive output exists.
    output = args.output.absolute()
    need(not any(p.is_symlink() for p in (output, *output.parents)), 'output_symlink')
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    (output / 'primitives').mkdir(mode=0o700)
    records = {}
    for name, body in [('clock', clock_body), ('reader', reader_body)]:
        relative = 'primitives/' + name + '.json'
        with (output / relative).open('xb') as stream:
            stream.write(body)
        (output / relative).chmod(0o600)
        records[name] = {'file': relative, 'sha256': digest(body)}
    proof = {'schema': 1, 'purpose': 'TEST portable installed business prerequisites',
             'candidateCommit': CANDIDATE, 'embeddedSHA256': expected['embeddedSHA256'],
             'cell': cell, 'primitives': records}
    body = canonical(proof) + b'\n'
    with (output / 'business-proof.json').open('xb') as stream:
        stream.write(body)
    (output / 'business-proof.json').chmod(0o600)
    print(json.dumps({'proof': str(output / 'business-proof.json'), 'sha256': digest(body)}))


if __name__ == '__main__':
    main()
