"""Installed business observation only. No clock/reader/source-epoch grant."""
import json
import pathlib
import os
import subprocess
import time
from portable_permission import native_join

PREFIX = '[agent-notifications] '
FLAGS = {'clockQualified': False, 'sourceEpochQualified': False,
         'timePolicyQualified': False, 'installedQualificationGranted': False, 'fullNativeQualified': False, 'visibleDesktopQualified': False,
         'platformLifetimeQualified': False}


def unique(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError('duplicate_diagnostic_key')
        value[key] = item
    return value


def diagnostic(line):
    # Native logger prefixes are harmless; the content-free record is closed.
    if PREFIX not in line:
        return None
    if line.count(PREFIX) != 1 or len(line) > 4096:
        raise ValueError('diagnostic_line_bound')
    value = json.loads(line.split(PREFIX, 1)[1], object_pairs_hook=unique)
    if not isinstance(value, dict):
        raise ValueError('diagnostic_schema')
    invalidated = value.get('ipc') == 'invalidated'
    fields = {'protocol', 'kind', 'ipc', 'childClosure', 'exitCode', 'forcedKill'}
    if set(value) != (fields if invalidated else fields | {'receipt'}):
        raise ValueError('diagnostic_schema')
    if (type(value['protocol']) is not int or value['protocol'] != 1 or
            value['kind'] != 'event' or value['childClosure'] != 'closed' or
            type(value['exitCode']) is not int or value['exitCode'] != 0 or
            value['forcedKill'] is not False):
        raise ValueError('diagnostic_actual_closed_dual_submission_required')
    if invalidated:
        return value  # Explicit non-success, never positive submission authority.
    receipt = value['receipt']
    if (value['ipc'] != 'ok' or not isinstance(receipt, dict) or
            set(receipt) != {'status', 'desktop', 'webhook'} or
            any(receipt[key] != 'submitted' for key in receipt)):
        raise ValueError('diagnostic_actual_closed_dual_submission_required')
    return value


class PortableOwned:
    """Public V1 finalization before fixture signals; no passive OS proof."""
    def __init__(self, owner, root, report, require):
        self.owner, self.root, self.report, self.require = owner, root, report, require
        self.host = None
        self.context = None
        self.attempted = False
        self.failure = None
        self.receipts = report.setdefault('portablePublicDisposal', [])

    def __getattr__(self, key):
        return getattr(self.owner, key)

    def bind(self, request, base, project, v2, headers):
        self.context = (request, base, project, v2, dict(headers))

    def launch(self, argv, root, env, name, **kwargs):
        proc = self.owner.launch(argv, root, env, name, **kwargs)
        if name in ('host', 'updated-host', 'reinstalled-host'):
            self.require(self.host is None or self.host.poll() is not None,
                         'previous_portable_host_not_reaped')
            self.host, self.attempted = proc, False
        return proc

    def release(self):
        for server in self.owner.servers:
            for name in ('interrupt_release', 'child_release', 'release'):
                gate = getattr(server, name, None)
                if gate is not None:
                    gate.set()

    def dispose_v1(self):
        if self.host is None or self.attempted or self.context is None or self.context[3]:
            return
        self.attempted = True  # Failed requests are never repeated during finally.
        self.release()
        request, base, project, _, headers = self.context
        self.require(self.host.poll() is None, 'live_portable_V1_disposal_host_required')
        provider = next(server for server in self.owner.servers if hasattr(server, 'records'))
        before = len(provider.records)
        paths = tuple((self.root / 'tmp').glob('agent-notifications-*'))
        self.require(all(path.is_dir() and not path.is_symlink() for path in paths),
                     'portable_private_registry_shape')
        until = time.monotonic() + 3
        answer = request(base, project, '/global/dispose', method='POST', v2=False,
                         timeout=max(0, until-time.monotonic()), auth_headers=headers)
        self.require(answer is True and time.monotonic() < until,
                     'actual_portable_V1_public_dispose_boolean_required')
        while any(path.exists() for path in paths):
            self.require(time.monotonic() < until, 'portable_registry_absence_unproved')
            time.sleep(min(.01, max(0, until-time.monotonic())))
        self.require(time.monotonic() < until and not tuple((self.root / 'tmp').glob('agent-notifications-*')),
                     'late_portable_registry_or_disposal_deadline')
        self.require(len(provider.records) == before and not provider.gaps,
                     'provider_effect_during_portable_disposal')
        self.receipts.append({'publicNativeDisposePOST': True, 'literalTrueResponse': True,
                              'privateRegistryPathsAbsentBeforeSignal': True,
                              'observedPrivateRegistryPaths': len(paths),
                              'directJSCallbackObserved': False, 'allHelperWaitEOFObserved': False})

    def stop(self, process):
        if process is self.host:
            try:
                self.dispose_v1()
            except Exception as error:
                self.failure = error
                raise
        self.owner.stop(process)

    def close(self):
        failure = self.failure
        try:
            self.dispose_v1()
        except Exception as error:
            if failure is None:
                failure = error
        try:
            self.owner.close()
        except Exception as error:
            if failure is None:
                failure = error
        if failure is not None:
            raise failure


class Observation:
    def __init__(self, args, manifest, files, require, digest):
        self.args, self.require, self.digest = args, require, digest
        self.root = None
        require(args.business_proof is not None and args.business_proof_sha256 is not None, 'sealed_business_prerequisites_required')
        self.cursors, self.partial, self.rows = {}, {}, []
        self.closedDenied = []
        self.proof = pathlib.Path(args.business_proof).absolute()
        require(self.proof.is_file() and not self.proof.is_symlink() and
                digest(self.proof) == args.business_proof_sha256, 'sealed_business_prerequisites_missing')
        proof = json.loads(self.proof.read_text(), object_pairs_hook=unique)
        require(set(proof) == {'schema', 'purpose', 'candidateCommit', 'embeddedSHA256', 'cell', 'primitives'},
                'business_prerequisites_schema')
        require(proof['schema'] == 1 and proof['purpose'] == 'TEST portable installed business prerequisites' and
                proof['candidateCommit'] == manifest['candidateCommit'] and
                proof['embeddedSHA256'] == digest(files['embedded']) and
                proof['cell'] == {'os': args.os, 'arch': args.arch, 'version': args.version},
                'business_exact_source_cell_required')
        require(set(proof['primitives']) == {'clock', 'reader'}, 'separate_clock_reader_evidence_required')
        for record in proof['primitives'].values():
            require(isinstance(record, dict) and set(record) == {'file', 'sha256'}, 'primitive_record_schema')
            relative = pathlib.PurePosixPath(record['file'])
            require(not relative.is_absolute() and relative.parts and all(part not in ('.','..') for part in relative.parts), 'primitive_relative_path_required')
            path = self.proof.parent.joinpath(*relative.parts)
            require(not any(parent.is_symlink() for parent in (path,*path.parents)) and path.resolve().is_relative_to(self.proof.parent.resolve()), 'primitive_path_escape')
            require(path.is_file() and not path.is_symlink() and digest(path) == record['sha256'],
                    'primitive_evidence_changed')
        # This is finite source/evidence custody. Actual production selectors still
        # decide every admission; these records are never passed to the plugin.
        self.primitives = proof['primitives']
        self.fixed = {str(path): digest(path) for path in (*files.values(), args.binary, args.archive, self.proof, pathlib.Path(__file__), pathlib.Path(__file__).with_name('portable_permission.py'), pathlib.Path(__file__).with_name('portable-capture.mjs'))}
        self.fixed.update({str(self.proof.parent / record['file']): record['sha256'] for record in self.primitives.values()})

    def windows_private_root(self, repo, root):
        # Public, stdlib-only DACL fixture; no ambient HOME/module download.
        env = {key: os.environ[key] for key in ('PATH','SystemRoot','WINDIR','COMSPEC','PATHEXT') if key in os.environ}
        for key in ('HOME','USERPROFILE','GOCACHE','TEMP','TMP'):
            child = root / ('private-' + key.lower())
            child.mkdir(mode=0o700)
            env[key] = str(child)
        env.update(GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOENV='off',GOWORK='off')
        result = subprocess.run(['go','run',str(repo/'scripts/opencode-private-root-windows.go'),str(root)],
                                cwd=root,env=env,capture_output=True,timeout=90)
        self.require(result.returncode == 0, 'windows_private_dacl_failed')

    def configure_loader(self, config, plugin, root, v2):
        if not v2:
            config['plugin'] = [[plugin.absolute().as_uri(), {'diagnostics': True}]]
            return
        # Stock V2 configured local plugins require directories. Import the exact
        # installer-owned file, not a copied bundle or modified factory.
        module = root / 'diagnostic-entry'
        module.mkdir(mode=0o700)
        (module / 'server.js').write_text('import installed from ' + json.dumps(plugin.absolute().as_uri()) + ';\nexport default {id: \"an-test-diagnostic-entry\", setup: installed.setup};\n')
        config['plugins'] = ['-agent-notifications',
                             {'package': str(module.absolute()), 'options': {'diagnostics': True}}]

    def update(self, root, closed=False):
        for name in ('host', 'updated-host', 'reinstalled-host'):
            path = root / (name + '-private.log')
            if not path.exists():
                continue
            self.require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 8 * 1024 * 1024,
                         'native_diagnostic_log_bound')
            position = self.cursors.get(name, 0)
            self.require(path.stat().st_size >= position, 'native_log_truncated_or_replaced')
            with path.open('rb') as stream:
                stream.seek(position)
                chunk = stream.read(8 * 1024 * 1024 + 1)
                self.cursors[name] = stream.tell()
            raw = self.partial.get(name, b'') + chunk
            lines = raw.split(b'\n')
            self.partial[name] = lines.pop()
            for rawline in lines:
                row = diagnostic(rawline.decode('utf-8', errors='strict'))
                if row is not None:
                    if row['ipc'] == 'invalidated':
                        self.require(self.args.version in ('1.18.33', '1.18.34', '2.0.21'),
                                     'closed_denied_native_generation_mismatch')
                        self.closedDenied.append(row)
                    else:
                        self.rows.append(row)
                    self.require(len(self.rows) + len(self.closedDenied) <= 32, 'actual_event_attempt_budget')
            if closed:
                self.require(PREFIX.encode() not in self.partial[name], 'closed_diagnostic_line_incomplete')
        return list(self.rows)

    def desktop_rows(self, root):
        diagnostics = self.update(root)
        if self.args.os != 'linux':
            return diagnostics  # Original receipt rows, never invented DBus rows.
        path = root / 'desktop-private.jsonl'
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def effects(self, root, webhook, start, name):
        desktop = self.desktop_rows(root)[start[0]:]
        posts = webhook.posts[start[1]:]
        self.require(len(desktop) <= 1 and len(posts) <= 1, 'duplicate_actual_submission')
        copy = {'completion': ('Task completed', 'task_complete'),
                'form': ('OpenCode asked a question', 'question'),
                'permission': ('OpenCode requested permission', 'permission_request'),
                'error': ('An error needs your attention', 'opencode_error')}
        body, status = copy['completion' if name == 'retry' else name]
        if self.args.os == 'linux':
            # Desktop question metadata is optional; webhook copy stays neutral.
            desktop_bodies = {body}
            if name == 'form':
                desktop_bodies.update({'P0 test choice?', 'TEST form\nP0 test choice?'})
            self.require(all(row.get('valid') is True and row.get('body') in desktop_bodies for row in desktop),
                         'desktop_independent_receiver_mismatch')
        for raw, headers in posts:
            payload = json.loads(raw, object_pairs_hook=unique)
            self.require(set(payload) == {'schema_version', 'status', 'notification_type', 'agent_source',
                                          'message', 'timestamp', 'session_id', 'source', 'title'} and
                         payload.get('notification_type') == status and payload.get('agent_source') == 'opencode' and
                         payload.get('message') == body and payload.get('title') == 'OpenCode' and
                         payload.get('session_id') == '' and b'PRIVATE_' not in raw and
                         not any(key.lower() == 'x-secret' for key in headers), 'independent_webhook_copy_privacy')
        return len(desktop) == len(posts) == 1

    def ready(self, root, server, provider, trace):
        until = time.monotonic() + 20
        while True:
            self.require(server.poll() is None and not provider.records and not provider.gaps,
                         'provider_or_host_effect_before_native_readiness')
            if any(row['kind'] == 'loader' for row in trace(root)):
                return
            self.require(time.monotonic() < until, 'native_capture_not_loaded')
            time.sleep(.1)

    def permission_join(self, g, base, project, enc, native, session, headers, key, pending):
        return native_join(g, base, project, enc, native, session,
                           g['private_redact']('call_p0_permission', key), g['private_redact']('bash', key),
                           headers, key, pending)

    def finish(self, root, provider, webhook, report):
        self.require(len(provider.records) == 21 and not provider.gaps, 'portable_planned_provider_budget_mismatch')
        report['actualProviderTransactions'] = len(provider.records)
        report['businessBodyComplete'] = True
        self.actual_provider = provider
        self.require(webhook.count() == 14, 'independent_webhook_total_mismatch')
        self.webhook = webhook

    def seal(self, root, report):
        rows = self.update(root, closed=True)
        self.require(len(self.actual_provider.records) == 21 and not self.actual_provider.gaps, 'late_or_unexpected_provider_transaction')
        self.require(len(rows) == self.webhook.count() == 14, 'closed_event_submission_membership_mismatch')
        if self.args.version == '2.0.21':
            tail = report.get('scenarios', [])[-4:]
            self.require(len(tail) == 4 and all(
                row.get('scenario') == 'completion' and row.get('status') == 'observed' and
                row.get('providerCalls') == 1 and row.get('desktopCount') == count and
                row.get('webhookCount') == count for row, count in zip(tail, (1, 0, 0, 1))),
                'independent_remove_stale_silence_required')
        self.require(all(self.digest(path) == pin for path,pin in self.fixed.items()), 'source_supplier_changed')
        if self.args.os == 'linux':
            self.require(len(self.desktop_rows(root)) == len(rows), 'dbus_closed_event_count_mismatch')
        report.update(status='installed_business_lifecycle_observed', productOwnedClose='14_actual_event_children_closed_diagnostics_only', **FLAGS,
                      actualClosedSubmittedChildren=len(rows), actualClosedDeniedChildren=len(self.closedDenied),
                      closedDeniedPositiveAuthority=False, diagnosticTransport='native_host_stderr',
                      desktopEvidence='independent_private_dbus' if self.args.os == 'linux' else 'actual_closed_child_backend_submitted',
                      primitiveEvidence=self.primitives, businessPrerequisitesSHA256=self.digest(self.proof),
                      WindowsV1OriginalAge='user_accepted_original_native_age_limitation' if self.args.os == 'windows' and self.args.version.startswith('1.') else 'not_applicable',
                      diagnosticAssociation='serial_native_fact_and_independent_webhook_interval_no_nativeID_in_diagnostic',
                      GUIProcessTreeClosure='unproved', OSCommandNonexecution='unproved_portable_native_rejection_join_only')
