#!/usr/bin/env python3
"""Independent pure refusal vectors. Run ONLY from a fresh owned TEST copy."""
import argparse
import ast
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import types
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent
if not ROOT.name.startswith('TEST-'):
    raise SystemExit('fresh TEST copy required; see README')
SPEC = importlib.util.spec_from_file_location('source_harness', ROOT / 'opencode-platform-clock-prequalification.py')
H = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(H)

# These literals are independent of parser-produced output and source samples.
GOOD = b'{"protocol":1,"boot":"11111111-2222-3333-4444-555555555555","clockDomain":"linux-time:4:19","clockKind":"linux-boottime","monoLoNs":"12000000000","monoHiNs":"12000010000","wallUnixNs":"1800000000000000000","uncertaintyNs":"3010000"}\n'


class PureVectors(unittest.TestCase):
    def setUp(self):
        # Owned-method vectors must never create an actual child. Node tooling
        # runs after this guard is restored, outside the Python pure tests.
        guard = patch.object(H.subprocess, 'Popen', side_effect=AssertionError('pure_test_spawn_refused'))
        guard.start(); self.addCleanup(guard.stop)

    def test_repeated_lifecycle_missing_first_close_uses_real_resource_count(self):
        # A retained measured file must fail even though warmup retention is allowed.
        os_name = {'linux': 'linux', 'darwin': 'darwin', 'win32': 'windows'}[sys.platform]
        subject = types.SimpleNamespace(pid=os.getpid(), _handle=-1, poll=lambda: None)
        count = lambda: H.native_resources(subject, os_name)
        with tempfile.TemporaryDirectory(prefix='TEST-repeat-missing-close-', dir=ROOT) as directory:
            count()  # Initialize only this observer before its fixed baseline.
            with open(Path(directory) / 'warmup', 'wb'):
                baseline = count()
                with open(Path(directory) / 'first-measured', 'wb'):
                    retained = count(); self.assertGreater(retained, baseline)
                    with self.assertRaisesRegex(RuntimeError, '^actual_module_resource_leak$'):
                        H.require_repeated_sampler_nonincrease(baseline, [retained], 1)
                H.require_repeated_sampler_nonincrease(baseline, [count()], 1)

    def test_repeated_lifecycle_consecutive_real_growth_never_rebases(self):
        # Independently observed +1/+2 retained files, never an API stub or mock.
        os_name = {'linux': 'linux', 'darwin': 'darwin', 'win32': 'windows'}[sys.platform]
        subject = types.SimpleNamespace(pid=os.getpid(), _handle=-1, poll=lambda: None)
        count = lambda: H.native_resources(subject, os_name)
        with tempfile.TemporaryDirectory(prefix='TEST-repeat-growth-', dir=ROOT) as directory:
            count()
            with open(Path(directory) / 'warmup', 'wb'):
                baseline = count()
                with open(Path(directory) / 'first-measured', 'wb'):
                    first = count()
                    with open(Path(directory) / 'second-measured', 'wb'):
                        second = count(); self.assertGreater(second, first)
                        with self.assertRaisesRegex(RuntimeError, '^actual_module_resource_leak$'):
                            H.require_repeated_sampler_nonincrease(baseline, [first, second], 2)
                H.require_repeated_sampler_nonincrease(baseline, [count(), count()], 2)

    def test_sampler_resource_boundary_keeps_persistent_growth_fail_closed(self):
        # Independent lifecycle counts; no native resource read or child spawn.
        H.require_sampler_nonincrease('windows', 80, 95, 95)
        H.require_sampler_nonincrease('windows', 172, 172, 172)
        for os_name, loader, after, imported in [('windows', 80, 96, 95),
                                                  ('windows', 81, 98, 96),
                                                  ('linux', 80, 95, 95),
                                                  ('darwin', 80, 95, 95)]:
            with self.subTest(os_name=os_name, after=after):
                with self.assertRaisesRegex(RuntimeError, '^actual_module_resource_leak$'):
                    H.require_sampler_nonincrease(os_name, loader, after, imported)
        for missing in [None, True, -1]:
            with self.subTest(imported=missing):
                with self.assertRaisesRegex(RuntimeError, '^actual_sampler_resource_baseline$'):
                    H.require_sampler_nonincrease('windows', 80, 80, missing)

    def test_resource_settling_keeps_real_open_file_growth_fail_closed(self):
        # Real kernel FD/handle counts, controlled deadline only. These policy
        # vectors do not establish Bun cleanup causality or native qualification.
        os_name = {'linux': 'linux', 'darwin': 'darwin', 'win32': 'windows'}[sys.platform]
        subject = types.SimpleNamespace(pid=os.getpid(), _handle=-1, poll=lambda: None)
        count = lambda: H.native_resources(subject, os_name)
        with tempfile.TemporaryDirectory(prefix='TEST-resource-settle-', dir=ROOT) as directory:
            count()  # Load this test's measurement library before its baseline.
            baseline = count()
            record = {}
            with patch.object(H.time, 'monotonic', return_value=100.0):
                result = H.settle_sampler_resources(baseline, baseline, 100.02,
                    lambda: self.fail('immediate pass must not reread'), record)
            self.assertEqual(result, baseline)
            self.assertTrue(record['immediateNonincrease'])
            self.assertEqual(record['readsAfterImmediate'], 0)
            for mode in ('release', 'persistent', 'late_read'):
                with self.subTest(mode=mode):
                    fd = os.open(Path(directory) / mode, os.O_CREAT | os.O_RDWR, 0o600)
                    opened = count()
                    self.assertGreater(opened, baseline)
                    clock = [100.0]; record = {}; closed = [False]
                    def release():
                        if not closed[0]: os.close(fd); closed[0] = True
                    def pause(seconds):
                        clock[0] += seconds
                        if mode == 'release': release()
                    def read_live():
                        if mode == 'late_read':
                            release(); clock[0] = 100.02
                        return count()
                    try:
                        with patch.object(H.time, 'monotonic', side_effect=lambda: clock[0]), \
                             patch.object(H.time, 'sleep', side_effect=pause):
                            if mode == 'release':
                                final = H.settle_sampler_resources(baseline, opened, 100.02, read_live, record)
                                self.assertLessEqual(final, baseline)
                                self.assertTrue(record['settled'])
                                self.assertEqual(record['readsAfterImmediate'], 1)
                            else:
                                with self.assertRaisesRegex(RuntimeError, '^absolute_deadline$'):
                                    H.settle_sampler_resources(baseline, opened, 100.02, read_live, record)
                                self.assertFalse(record['settled'])
                                if mode == 'persistent': self.assertGreater(record['final'], baseline)
                                else: self.assertLessEqual(record['final'], baseline)
                        self.assertEqual(record['baseline'], baseline)
                        self.assertEqual(record['immediate'], opened)
                        self.assertFalse(record['immediateNonincrease'])
                    finally: release()

    def test_held_helper_admission_refuses_change_without_rehashing_inside_span(self):
        with tempfile.TemporaryDirectory(prefix='TEST-held-helper-', dir=ROOT) as directory:
            helper = Path(directory) / 'helper'; helper.write_bytes(b'abc')
            pinned = 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'
            owned = H.Owned(H.time.monotonic() + 60)
            try:
                owned.admit_helper(helper, pinned)
                with patch.object(H, 'sha', side_effect=AssertionError('no_sha_inside_native_span')), \
                     patch.object(owned, 'start', return_value={'startedAt': 1.0}):
                    owned.start_helper(helper, pinned, Path(directory), {}, H.time.monotonic() + 2)
                with self.assertRaisesRegex(RuntimeError, '^held_helper_image_changed$'):
                    owned.verify_helper(helper, '0' * 64)
                helper.write_bytes(b'abcd')
                with self.assertRaisesRegex(RuntimeError, '^held_helper_image_changed$'):
                    owned.verify_helper(helper, pinned)
                self.assertFalse(owned.summary()['allOwnedHandlesClosed'])
            finally: owned.cleanup()
            self.assertTrue(owned.summary()['allOwnedHandlesClosed'])

    def test_failed_span_diagnostic_keeps_exact_numeric_widths_and_rejects_forgery(self):
        good = {'round': 1, 'outerWidthNs': 225000001, 'goWidthNs': 7000}
        for widths in [good, dict(good, round=True), dict(good, outerWidthNs=224000000),
                       dict(good, goWidthNs=100000001)]:
            owned = H.Owned(H.time.monotonic() + 2)
            state = {'overflow': False, 'pipeError': False, 'lines': H.queue.Queue()}
            state['lines'].put(H.canonical({'kind': 'failure', 'reason': 'actual_translation_counter_span',
                                          'nativeComparisonWidths': widths, **H.QUALIFICATIONS}))
            expected = 'actual_translation_counter_span' if widths is good else 'closed_comparison_width_diagnostic'
            with self.assertRaisesRegex(RuntimeError, '^' + expected + '$') as caught:
                owned.message(state, 'helper_request', H.time.monotonic() + 2)
            if widths is good: self.assertEqual(caught.exception.nativeComparisonWidths, good)

    def test_helper_hash_preflight_launch_budget(self):
        with tempfile.TemporaryDirectory(prefix='TEST-helper-preflight-', dir=ROOT) as directory:
            helper = Path(directory) / 'private-helper-bytes'
            helper.write_bytes(b'abc'); helper.chmod(0o600)
            expected = 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'
            original_sha = H.sha
            # A 300ms SHA still leaves 1.7s in the original operation. It must
            # retain only the original operation/Go window; translation224ms
            # is independently checked on actual native endpoints, not this timer.
            for operation_end, job_end, wanted_end in [(102.0, 900.0, 102.0),
                                                       (100.4, 900.0, 100.4),
                                                       (102.0, 100.4, 100.4)]:
                with self.subTest(operation_end=operation_end, job_end=job_end):
                    now = [100.0]; hashes = []; launches = []
                    owned = H.Owned(job_end)
                    def slow_sha(path):
                        hashes.append(path)
                        value = original_sha(path); now[0] += .3
                        return value
                    def launch(*args):
                        launches.append((now[0], args)); return {'startedAt': now[0]}
                    with patch.object(H.time, 'monotonic', side_effect=lambda: now[0]), \
                         patch.object(H, 'sha', side_effect=slow_sha), \
                         patch.object(owned, 'start', side_effect=launch):
                        state, end = owned.start_helper(helper, expected, Path(directory), {}, operation_end)
                    self.assertEqual(hashes, [helper])
                    self.assertEqual(len(launches), 1)
                    self.assertEqual(launches[0][1], ([str(helper), 'opencode-clock', '--protocol', '1'],
                                                   Path(directory), {}, 'helper', 1024))
                    self.assertAlmostEqual(state['startedAt'], 100.3)
                    self.assertAlmostEqual(end, wanted_end)

    def test_helper_hash_deadlines_and_wrong_hash_refuse_launch(self):
        with tempfile.TemporaryDirectory(prefix='TEST-helper-refusal-', dir=ROOT) as directory:
            helper = Path(directory) / 'private-helper-bytes'
            helper.write_bytes(b'abc'); helper.chmod(0o600)
            expected = 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'
            original_sha = H.sha
            vectors = [(102.0, 900.0, 2.0, expected, 'absolute_deadline', 1),
                       (102.0, 900.0, 2.01, expected, 'absolute_deadline', 1),
                       (102.0, 100.3, .3, expected, 'absolute_deadline', 1),
                       (100.0, 900.0, 0.0, expected, 'absolute_deadline', 0),
                       (102.0, 100.0, 0.0, expected, 'absolute_deadline', 0),
                       (102.0, 900.0, .3, '0' * 64, 'actual_helper_hash', 1)]
            for operation_end, job_end, hash_seconds, pinned, diagnostic, hash_count in vectors:
                with self.subTest(operation_end=operation_end, job_end=job_end, pinned=pinned, hash_seconds=hash_seconds):
                    now = [100.0]; hashes = []
                    owned = H.Owned(job_end)
                    def elapsed_sha(path):
                        hashes.append(path)
                        value = original_sha(path); now[0] += hash_seconds
                        return value
                    with patch.object(H.time, 'monotonic', side_effect=lambda: now[0]), \
                         patch.object(H, 'sha', side_effect=elapsed_sha), \
                         patch.object(owned, 'start') as launch:
                        with self.assertRaisesRegex(RuntimeError, '^' + diagnostic + '$'):
                            owned.start_helper(helper, pinned, Path(directory), {}, operation_end)
                    launch.assert_not_called()
                    self.assertEqual(len(hashes), hash_count)

    def test_owned_cleanup_kills_first_and_shares_one_deadline(self):
        # Scripted process/EOF delays exercise the actual Owned cleanup/close
        # methods. No native child, signal, FFI, clock or sleeping is used.
        now = [100.0]; events = []
        class Process:
            def __init__(self, pid, delay):
                self.pid, self.delay, self.returncode = pid, delay, None
                self.stdin = self.stdout = self.stderr = None
            def poll(self): return self.returncode
            def kill(self): events.append(('kill', self.pid))
            def wait(self, timeout):
                events.append(('wait', self.pid, timeout))
                now[0] += min(self.delay, timeout)
                if self.delay >= timeout: raise subprocess.TimeoutExpired('pure-owned', timeout)
                self.returncode = -9
        class PipeEOF:
            def __init__(self, pid, index, delay):
                self.pid, self.index, self.delay, self.alive = pid, index, delay, True
            def join(self, timeout):
                events.append(('join', self.pid, self.index, timeout))
                now[0] += min(self.delay, timeout)
                self.alive = self.delay >= timeout
            def is_alive(self): return self.alive
        for wait_delay, pipe_delays, expected_closes, expected_live in [
                (1.0, (.25, .25), 3, 1), (0.0, (3.0, 3.0), 0, 4), (0.0, (0.0, 0.0), 4, 0)]:
            with self.subTest(wait_delay=wait_delay, pipe_delays=pipe_delays):
                now[0] = 100.0; events.clear()
                owned = H.Owned(99.0)  # cleanup is separate from expired job/operation
                for pid in range(1, 5):
                    owned.live[pid] = {'p': Process(pid, wait_delay), 'label': 'helper' if pid == 1 else 'module',
                        'threads': [PipeEOF(pid, index, delay) for index, delay in enumerate(pipe_delays)],
                        'overflow': False, 'pipeError': False}
                owned.starts = owned.highwater = 4; owned.helpers = 1
                with patch.object(H.time, 'monotonic', side_effect=lambda: now[0]), \
                     patch.object(H.os, 'getpgid', side_effect=lambda pid: pid, create=True), \
                     patch.object(H.os, 'killpg', side_effect=lambda pid, sig: events.append(('kill', pid)), create=True):
                    owned.cleanup()
                    self.assertEqual(events[:4], [('kill', pid) for pid in range(1, 5)])
                    self.assertLessEqual(now[0], 105.0)
                    self.assertEqual(owned.closes, expected_closes)
                    self.assertEqual(len(owned.live), expected_live)
                    for event in events:
                        if event[0] in ('wait', 'join'): self.assertGreater(event[-1], 0)
                    # A repeated finally/cleanup path cannot grant another 5s.
                    before = now[0]
                    owned.cleanup()
                    self.assertLessEqual(now[0], 105.0)
                    self.assertEqual(now[0], before)
                self.assertEqual(owned.summary()['allOwnedHandlesClosed'], expected_live == 0)
                self.assertTrue(all(value is False for value in H.QUALIFICATIONS.values()))

    def test_rejected_symlink_has_only_safe_path_role(self):
        with tempfile.TemporaryDirectory(prefix='TEST-path-role-', dir=ROOT) as directory:
            root = Path(directory); target = root / 'PRIVATE-target'; target.mkdir()
            (target / 'leaf').write_bytes(b'private')
            self.assertEqual(H.canonical_path(target / 'leaf'), target / 'leaf')
            link = root / 'PRIVATE-link'
            try: link.symlink_to(target, target_is_directory=True)
            except OSError: self.skipTest('Actual symlink creation unavailable; no junction proof')
            with self.assertRaisesRegex(RuntimeError, '^symlink_ancestry$') as caught:
                H.canonical_path(link / 'leaf', 'go_dependency_directory')
            observed = caught.exception.path_failure
            self.assertEqual(observed, {'role': 'go_dependency_directory', 'ancestorDepth': 1, 'kind': 'symlink'})
            self.assertNotIn('PRIVATE', json.dumps(observed))

    def test_exact_head_refusal(self):
        self.assertEqual(H.commit_match('ab' * 20, 'ab' * 20), 'ab' * 20)
        for observed, expected in [('ab' * 20, 'cd' * 20), ('AB' * 20, 'AB' * 20), ('a' * 39, 'a' * 39), ('a' * 41, 'a' * 41), ('', '')]:
            with self.subTest(observed=observed):
                with self.assertRaisesRegex(RuntimeError, 'exact_source_commit'): H.commit_match(observed, expected)

    def test_literal_frame_and_original_allowance(self):
        result = H.frame(GOOD, 'linux')
        self.assertEqual(result['monoLoNs'], '12000000000')
        self.assertEqual(result['uncertaintyNs'], '3010000')
        for raw in [GOOD.replace(b'3010000', b'3000000'), GOOD.replace(b'12000010000', b'11999999999'),
                    GOOD.replace(b'12000010000', b'12100000001'), GOOD.replace(b'"12000000000"', b'"012000000000"'),
                    GOOD.replace(b'"12000000000"', b'"9223372036854775808"'), GOOD.replace(b'"protocol":1', b'"protocol":true'),
                    GOOD.replace(b'"protocol":1', b'"protocol":1,"protocol":1'), GOOD.rstrip(b'\n'), GOOD + GOOD,
                    GOOD.replace(b'linux-boottime', b'process-hrtime'), GOOD.replace(b'linux-time:4:19', b'linux-time:4:019'),
                    GOOD.replace(b'11111111-2222-3333-4444-555555555555', b'00000000-0000-0000-0000-000000000000')]:
            with self.subTest(raw=raw):
                with self.assertRaises((RuntimeError, ValueError)): H.frame(raw, 'linux')
        with self.assertRaises(RuntimeError): H.frame(GOOD, 'windows')
        with self.assertRaises(RuntimeError): H.frame(b'x' * 1025, 'linux')

    def test_independent_architecture_headers(self):
        elf = bytearray(64); elf[:6] = b'\x7fELF\x02\x01'; elf[18:20] = b'\x3e\x00'
        H.native_header(bytes(elf), 'linux', 'amd64')
        with self.assertRaises(RuntimeError): H.native_header(bytes(elf), 'linux', 'arm64')
        elf[18:20] = b'\xb7\x00'; H.native_header(bytes(elf), 'linux', 'arm64')
        macho = b'\xcf\xfa\xed\xfe\x0c\x00\x00\x01' + b'\0' * 56
        H.native_header(macho, 'darwin', 'arm64')
        with self.assertRaises(RuntimeError): H.native_header(macho, 'darwin', 'amd64')
        pe = bytearray(128); pe[:2] = b'MZ'; pe[60:64] = b'\x40\x00\x00\x00'
        pe[64:70] = b'PE\0\0\x64\x86'; pe[88:90] = b'\x0b\x02'
        H.native_header(bytes(pe), 'windows', 'amd64')
        pe[60:64] = b'\xff\xff\xff\xff'
        with self.assertRaises(RuntimeError): H.native_header(bytes(pe), 'windows', 'amd64')
        with self.assertRaises(RuntimeError): H.native_header(bytes(elf), 'windows', 'arm64')

    def test_archive_traversal_and_alias_refusals(self):
        self.assertEqual(str(H.archive_name('package/bin/opencode')), 'package/bin/opencode')
        for name in ['/package/bin/opencode', '../opencode', 'package/../bin/opencode', 'package//bin/opencode',
                     'package/./bin/opencode', 'C:/opencode', 'package\\bin\\opencode', 'package/bin/opencode\0evil']:
            with self.subTest(name=name):
                with self.assertRaises(RuntimeError): H.archive_name(name)

    def test_exact_hash_and_input_refusal(self):
        self.assertEqual(H.digest(b'abc'), 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')
        with self.assertRaises(RuntimeError): H.closed_inputs({'schema': 2, 'sources': [], 'artifacts': []})
        with self.assertRaises(RuntimeError): H.parse(b'{"x":1,"x":1}')
        with self.assertRaises(RuntimeError): H.parse(b'{"x":NaN}')
        with self.assertRaises(RuntimeError): H.parse(b'a' * 1025, 1024)

    def test_checkout_bytes_preserve_provenance(self):
        self.assertEqual(H.bound_source(b'a\nb\n', b'a\nb\n', 'linux'), 'git_blob_bytes')
        self.assertEqual(H.bound_source(b'a\nb\n', b'a\r\nb\r\n', 'windows'), 'windows_git_crlf_checkout_bytes')
        for actual, os_name in [(b'a\r\nb\r\n', 'linux'), (b'a\r\nc\r\n', 'windows'), (b'a\nb\r\n', 'windows')]:
            with self.assertRaises(RuntimeError): H.bound_source(b'a\nb\n', actual, os_name)

    def test_current_primary_no_new_binary_or_wrong_cell(self):
        primary = {'version': '1.18.34', 'commit': 'aec0b9a6d8898f68f923aaf08b7306d931fd9d76',
                   'bun': '1.3.14', 'os': 'linux', 'arch': 'x64',
                   'archive': 'https://registry.npmjs.org/opencode-linux-x64/-/opencode-linux-x64-1.18.34.tgz',
                   'archiveSHA256': 'a' * 64, 'binarySHA256': 'b' * 64, 'sri': 'sha512-' + 'A' * 86 + '=='}
        H.current_primary(primary, 'b' * 64)
        for key, bad in [('arch', 'arm64'), ('commit', 'f' * 40), ('binarySHA256', 'c' * 64),
                         ('bun', '1.4.2'), ('archiveSHA256', 'z' * 64), ('sri', 'sha256-abc')]:
            value = {**primary, key: bad}
            with self.subTest(key=key):
                with self.assertRaises(RuntimeError): H.current_primary(value, 'b' * 64)

    def test_result_budget_grant_and_frame_refusal(self):
        result = {'kind': 'disposed', 'status': 'module_prequalification_observed', 'actualModuleBound': True,
                  'rounds': 3, 'samples': 102, 'comparisons': [{'outerWidthNs': '100000000', 'goWidthNs': '10000'}] * 3,
                  'datePredicates': [], 'disposeCalls': 2, 'sampleAfterDisposeRefused': True,
                  'operationElapsedMs': 1900, 'jsElapsedMs': 23000,
                  'checks': dict.fromkeys(['actualTupleParity', 'causalCounterContainment', 'causalWallContainment',
                    'actualWallCounterTypes', 'currentImageHeldFile', 'sampleNonregression', 'samplerBounds', 'stickyDisposal'], True),
                  **dict.fromkeys(['TimePolicyQualified', 'runtimeEligibilityGranted', 'candidateRQualified', 'candidateTQualified',
                    'sourceWallBoundQualified', 'suspendQualified', 'finalSpanQualified', 'registryQualified'], False)}
        H.validate_result(result, 'linux')
        for key, bad in [('operationElapsedMs', 2000), ('jsElapsedMs', 25000), ('samples', 513), ('samples', 101),
                         ('TimePolicyQualified', True), ('runtimeEligibilityGranted', 0), ('disposeCalls', 1), ('rounds', 2)]:
            value = copy.deepcopy(result); value[key] = bad
            with self.subTest(key=key):
                with self.assertRaises(RuntimeError): H.validate_result(value, 'linux')
        value = copy.deepcopy(result); value['comparisons'][0]['outerWidthNs'] = '224000000'
        H.validate_result(value, 'linux')
        value['comparisons'][0]['outerWidthNs'] = '224000001'
        with self.assertRaises(RuntimeError): H.validate_result(value, 'linux')
        value = copy.deepcopy(result); value['comparisons'][0]['outerWidthNs'] = '2000000001'
        with self.assertRaises(RuntimeError): H.validate_result(value, 'linux')
        value = copy.deepcopy(result); value['rawBoot'] = 'not-public'
        with self.assertRaises(RuntimeError): H.validate_result(value, 'linux')
        with self.assertRaises(RuntimeError): H.validate_result(result, 'windows')
        windows = copy.deepcopy(result); windows['samples'] = 110
        windows.update(warmupSamples=1, measuredSamples=109, measuredInstances=2, disposeCalls=4,
            ownedClockLifetime={'descriptorOpens': 4, 'descriptorCloses': 4, 'firstInitCancelRejected': True,
                                'overlapDisposalPreservedB': True, 'abortSticky': True})
        windows['datePredicates'] = [{'dateInsideNativeInterval': False, 'datePreciseDistanceNs': '1'}] * 3
        H.validate_result(windows, 'windows')
        for key, value in [('warmupSamples', 0), ('measuredSamples', 107), ('measuredInstances', 1), ('disposeCalls', 2)]:
            malformed = copy.deepcopy(windows); malformed[key] = value
            with self.assertRaises(RuntimeError): H.validate_result(malformed, 'windows')
        for key in ('warmupSamples', 'measuredSamples', 'measuredInstances', 'disposeCalls'):
            for value in (True, False, float(windows[key])):
                with self.subTest(counter=key, value=value):
                    malformed = copy.deepcopy(windows); malformed[key] = value
                    with self.assertRaises(RuntimeError): H.validate_result(malformed, 'windows')
        for key, value in [('descriptorCloses', 3), ('descriptorCloses', 4.0), ('overlapDisposalPreservedB', False), ('abortSticky', 1)]:
            malformed = copy.deepcopy(windows); malformed['ownedClockLifetime'][key] = value
            with self.assertRaises(RuntimeError): H.validate_result(malformed, 'windows')
        windows['datePredicates'][0] = {'dateInsideNativeInterval': True, 'datePreciseDistanceNs': '1'}
        with self.assertRaises(RuntimeError): H.validate_result(windows, 'windows')

    def test_ast_and_fixed_plan(self):
        for path in [ROOT / 'opencode-platform-clock-prequalification.py', Path(__file__)]:
            ast.parse(path.read_bytes(), filename=path.name)
        self.assertEqual(len(H.CELLS), 5)
        self.assertEqual(H.BUDGETS, {'preparationMs': 2000, 'jsMs': 25000, 'goMs': 20000, 'helperMs': 224,
                                   'sampleCap': 512, 'chunkSize': 32, 'rounds': 3, 'frameBytes': 16384, 'jobSeconds': 900})
        self.assertTrue(all(x is False for x in H.QUALIFICATIONS.values()))
        self.assertEqual(H.remaining.__name__, 'remaining')
        with self.assertRaisesRegex(RuntimeError, 'absolute_deadline'): H.remaining(0)


NODE_VECTORS = r'''
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
const h = await import(pathToFileURL(process.argv[1]).href);
const raw = Buffer.from('{"protocol":1,"boot":"11111111-2222-3333-4444-555555555555","clockDomain":"windows-kernel","clockKind":"windows-interrupt-precise","monoLoNs":"12000000000","monoHiNs":"12000010000","wallUnixNs":"1800000000000000000","uncertaintyNs":"3010000"}\n');
const good = h.helperFrame(raw, 'win32');
assert.equal(good.lo, 12000000000n);
for (const bad of [raw.toString().replace('3010000', '3000000'), raw.toString().replace('windows-interrupt-precise', 'process-hrtime'),
  raw.toString().replace('"protocol":1', '"protocol":1,"protocol":1'), raw.toString().trim(), raw.toString() + raw.toString()])
  assert.throws(() => h.helperFrame(Buffer.from(bad), 'win32'));
assert.throws(() => h.helperFrame(raw, 'linux'));
assert.throws(() => h.decimal('9223372036854775808'));
assert.throws(() => h.decimal('01'));
const before = { boot: good.boot, domain: 'windows-kernel', kind: 'windows-interrupt-precise', lo: 11999900000n, hi: 11999950000n, wall: 1799999999999999000n };
const after = { ...before, lo: 12000015000n, hi: 12000020000n, wall: 1800000000000001000n };
assert.deepEqual(h.compare(before, good, after, 'win32'), { outerWidthNs: '120000', goWidthNs: '10000' });
// Independent endpoint vectors, with no claim about physical clock rate.
const boundary = { ...after, lo: before.lo + 223900000n, hi: before.lo + 224000000n };
assert.equal(h.compare(before, good, boundary, 'win32').outerWidthNs, '224000000');
assert.throws(() => h.compare(before, good, { ...boundary, hi: boundary.hi + 1n }, 'win32'), /actual_translation_counter_span/);
// A point-only comparison would pass; the full high endpoint must refuse.
assert.throws(() => h.compare(before, good, { ...boundary, lo: before.lo + 220000000n, hi: before.lo + 225000000n }, 'win32'), /actual_translation_counter_span/);
assert.throws(() => h.compare(before, {...good, boot: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'}, after, 'win32'));
assert.throws(() => h.compare(before, {...good, lo: before.lo - 1n}, after, 'win32'));
assert.throws(() => h.compare(before, {...good, wall: before.wall - 1n}, after, 'win32'));
assert.throws(() => h.compare(before, good, {...after, hi: before.lo + 2000000001n}, 'win32'));
const sample = Object.freeze({ boot: good.boot, domain: good.domain, rawKind: good.kind, loNS: good.lo, hiNS: good.hi, wallNS: good.wall });
assert.equal(h.productSample(sample, 'win32').wall, 1800000000000000000n);
assert.throws(() => h.productSample({...sample}, 'win32'));
assert.throws(() => h.productSample(Object.freeze({...sample, wallNS: 1}), 'win32'));
assert.throws(() => h.productSample(Object.freeze({...sample, hiNS: sample.loNS + 100000001n}), 'win32'));
const linux = Object.freeze({ ...sample, domain: 'linux-time:4:19', rawKind: 'linux-boottime',
  offsetLoNS: 1799999987997990000n, offsetHiNS: 1799999988002000000n });
assert.equal(h.productSample(linux, 'linux').kind, 'linux-boottime');
for (const bad of [{...linux, rawKind: 'process-hrtime'}, {...linux, extra: true}])
  assert.throws(() => h.productSample(Object.freeze(bad), 'linux'));
const {rawKind: removedKind, ...missingKind} = linux;
assert.throws(() => h.productSample(Object.freeze(missingKind), 'linux'));
assert.deepEqual(h.preciseDate(1000000000n, 1000, 1000000100n), {dateInsideNativeInterval: true, datePreciseDistanceNs: '0'});
assert.deepEqual(h.preciseDate(1000000100n, 1000, 1000000200n), {dateInsideNativeInterval: false, datePreciseDistanceNs: '100'});
assert.deepEqual(h.preciseDate(999999000n, 1000, 999999999n), {dateInsideNativeInterval: false, datePreciseDistanceNs: '1'});
assert.throws(() => h.preciseDate(2n, 1000, 1n));
assert.throws(() => h.preciseDate(1n, '1000', 2n));
h.budgetCheck(1999, 0, 512, 32);
for (const vector of [[2000, 0, 102, 32], [1999, 0, 513, 32], [1999, 0, 102, 33], [0, 1, 102, 32], [NaN, 0, 1, 1]])
  assert.throws(() => h.budgetCheck(...vector));
const id = { platform: 'linux', arch: 'arm64', bun: '1.4.2', imageSha256: 'a'.repeat(64), fixtureSha256: 'b'.repeat(64), sourceCommit: 'ab'.repeat(20) };
h.identity(id, id);
for (const bad of [{...id, arch: 'x64'}, {...id, platform: 'win32'}, {...id, imageSha256: 'c'.repeat(64)}, {...id, sourceCommit: 'AB'.repeat(20)}])
  assert.throws(() => h.identity(bad, id));
assert.equal(Object.values(h.qualifications).every(v => v === false), true);
process.stdout.write(JSON.stringify({ pureNodeVectors: 'passed', nativeExecution: false, qualificationGranted: false }) + '\n');
'''


def main():
    parser = argparse.ArgumentParser(); parser.add_argument('--node', required=True)
    args = parser.parse_args()
    results = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(PureVectors))
    if not results.wasSuccessful(): return 1
    node = Path(args.node).resolve()
    need_env = {'PATH': '/usr/bin:/bin', 'HOME': str(ROOT), 'TMPDIR': str(ROOT), 'LANG': 'C.UTF-8'}
    for argv in [[str(node), '--check', str(ROOT / 'opencode-platform-clock-prequalification.mjs')],
                 [str(node), '--input-type=module', '-e', NODE_VECTORS, str(ROOT / 'opencode-platform-clock-prequalification.mjs')]]:
        result = subprocess.run(argv, cwd=ROOT, env=need_env, stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
        print(result.stdout.decode(), end=''); print(result.stderr.decode(), end='')
        if result.returncode: return result.returncode
    print(json.dumps({'pythonPureTests': results.testsRun, 'nodePureVectors': 'passed', 'nodeSyntax': 'passed',
                      'nativeExecution': False, 'qualificationGranted': False, 'nodeSHA256': H.sha(node)}))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
