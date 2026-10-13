#!/usr/bin/env python3
"""Exercise an already-built release executable in disposable profiles.

No agent CLI, external webhook, desktop session, or user configuration is used.
This proves CLI/config/webhook delivery; actual agent and GUI tests are separate.
"""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading


# Diagnostic: prints Sddl/Owner/Access for path and every ancestor to the
# drive root. checkWindowsACL/openStoreParent (store_windows.go) walk every
# path component from the volume root down, so this identifies which
# ancestor (if any) carries a disqualifying ACE. Never modifies anything.
_WINDOWS_ACL_DIAGNOSTIC_SCRIPT = (
    '$p = $env:DIAG_PATH\n'
    'while ($true) {\n'
    '  try {\n'
    '    $acl = Get-Acl -LiteralPath $p\n'
    '    "--- $p ---"\n'
    '    "Owner: $($acl.Owner)"\n'
    '    "Sddl: $($acl.Sddl)"\n'
    '    $acl.Access | Format-Table -AutoSize | Out-String -Width 4096\n'
    '  } catch {\n'
    '    "--- $p (Get-Acl failed: $_) ---"\n'
    '  }\n'
    '  $parent = Split-Path -Path $p -Parent\n'
    '  if ([string]::IsNullOrEmpty($parent) -or $parent -eq $p) { break }\n'
    '  $p = $parent\n'
    '}\n'
)


def _windows_acl_diagnostic(path, label):
    if os.name != 'nt':
        return
    print(f'=== ACL diagnostic: {label} ===', flush=True)
    diag_env = dict(os.environ, DIAG_PATH=str(Path(path).resolve()))
    subprocess.run(
        ['pwsh', '-NoProfile', '-NonInteractive', '-Command', _WINDOWS_ACL_DIAGNOSTIC_SCRIPT],
        env=diag_env, text=True, timeout=30, check=False)


# Mirrors internal/config/store_windows_test.go's prepareTestRoot(): a single
# protected DACL (current user + SYSTEM, FullControl, inheritable) set once
# on the scratch root so everything created under it inherits it via normal
# NTFS inheritance. Python 3.12.4+ applies an OWNER RIGHTS ACL for mode 0700;
# children use default Windows inheritance instead. No ancestor, real profile,
# no product ACL check loosened.
_WINDOWS_PRIVATE_ROOT_SCRIPT = (
    '$ErrorActionPreference = "Stop"\n'
    '$p = $env:DIAG_PATH\n'
    '$user = [System.Security.Principal.WindowsIdentity]::GetCurrent().User\n'
    '$system = New-Object System.Security.Principal.SecurityIdentifier("S-1-5-18")\n'
    '$acl = New-Object System.Security.AccessControl.DirectorySecurity\n'
    '$acl.SetAccessRuleProtection($true, $false)\n'
    '$acl.SetOwner($user)\n'
    '$inherit = [System.Security.AccessControl.InheritanceFlags]"ContainerInherit, ObjectInherit"\n'
    '$none = [System.Security.AccessControl.PropagationFlags]::None\n'
    '$allow = [System.Security.AccessControl.AccessControlType]::Allow\n'
    '$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($user, "FullControl", $inherit, $none, $allow)))\n'
    '$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($system, "FullControl", $inherit, $none, $allow)))\n'
    'Set-Acl -LiteralPath $p -AclObject $acl\n'
)


def _windows_private_scratch_root(path):
    if os.name != 'nt':
        return
    root_env = dict(os.environ, DIAG_PATH=str(Path(path).resolve()))
    subprocess.run(
        ['pwsh', '-NoProfile', '-NonInteractive', '-Command', _WINDOWS_PRIVATE_ROOT_SCRIPT],
        env=root_env, text=True, timeout=30, check=True)


def _windows_owner_rights_profile(path):
    # OWNER RIGHTS represents the current owner. The unrelated read grant is
    # non-inheritable, like an already repaired config directory below a
    # profile still readable by CodexSandboxUsers. Only this disposable home
    # changes; the runner profile and APPDATA are never modified.
    script = (
        '$ErrorActionPreference = "Stop"\n'
        '$p = $env:DIAG_PATH\n'
        '$acl = Get-Acl -LiteralPath $p\n'
        '$ownerRights = New-Object System.Security.Principal.SecurityIdentifier("S-1-3-4")\n'
        '$everyone = New-Object System.Security.Principal.SecurityIdentifier("S-1-1-0")\n'
        '$inherit = [System.Security.AccessControl.InheritanceFlags]"ContainerInherit, ObjectInherit"\n'
        '$none = [System.Security.AccessControl.PropagationFlags]::None\n'
        '$allow = [System.Security.AccessControl.AccessControlType]::Allow\n'
        '$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($ownerRights, "FullControl", $inherit, $none, $allow)))\n'
        '$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($everyone, "ReadAndExecute", $allow)))\n'
        'Set-Acl -LiteralPath $p -AclObject $acl\n'
    )
    subprocess.run(['pwsh', '-NoProfile', '-NonInteractive', '-Command', script],
                   env=dict(os.environ, DIAG_PATH=str(path)), text=True,
                   timeout=30, check=True)


def _windows_writable_profile(path):
    # Reproduce #466's profile-root Modify/DeleteChild grants with an actual
    # unrelated trustee. Only this disposable profile is changed.
    script = (
        '$ErrorActionPreference = "Stop"\n'
        '$p = $env:DIAG_PATH\n'
        '$acl = Get-Acl -LiteralPath $p\n'
        '$everyone = New-Object System.Security.Principal.SecurityIdentifier("S-1-1-0")\n'
        '$inherit = [System.Security.AccessControl.InheritanceFlags]"ContainerInherit, ObjectInherit"\n'
        '$none = [System.Security.AccessControl.PropagationFlags]::None\n'
        '$allow = [System.Security.AccessControl.AccessControlType]::Allow\n'
        '$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($everyone, "Modify, DeleteSubdirectoriesAndFiles", $inherit, $none, $allow)))\n'
        'Set-Acl -LiteralPath $p -AclObject $acl\n'
    )
    subprocess.run(['pwsh', '-NoProfile', '-NonInteractive', '-Command', script],
                   env=dict(os.environ, DIAG_PATH=str(path)), text=True,
                   timeout=30, check=True)


def _windows_acl_snapshot(path):
    script = (
        '$ErrorActionPreference = "Stop"\n'
        '$acl = Get-Acl -LiteralPath $env:DIAG_PATH\n'
        '$sid = [System.Security.Principal.SecurityIdentifier]\n'
        '$rules = @($acl.GetAccessRules($true, $true, $sid) | ForEach-Object {\n'
        '  @{sid=$_.IdentityReference.Value; mask=[int64]$_.FileSystemRights; '
        'inherited=$_.IsInherited; type=$_.AccessControlType.ToString()}\n'
        '})\n'
        '@{sddl=$acl.Sddl; protected=$acl.AreAccessRulesProtected; '
        'owner=$acl.GetOwner($sid).Value; user=[System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value; '
        'rules=$rules} | ConvertTo-Json -Depth 5 -Compress\n'
    )
    return json.loads(subprocess.check_output(
        ['pwsh', '-NoProfile', '-NonInteractive', '-Command', script],
        env=dict(os.environ, DIAG_PATH=str(path)), text=True, timeout=30))


def _windows_assert_private(path):
    acl = _windows_acl_snapshot(path)
    assert acl['protected'] is True and acl['owner'] == acl['user'], (str(path), acl)
    rules = acl['rules']
    assert {rule['sid'] for rule in rules} == {acl['user'], 'S-1-5-18'}, (str(path), acl)
    assert all(rule['type'] == 'Allow' and rule['inherited'] is False
               and rule['mask'] == 0x1f01ff for rule in rules), (str(path), acl)
    return acl


def _windows_configure_first(run, binary, home, bundle, env, automatic_root):
    # Refs #466: configure must be the FIRST canonical writer. Initializing the
    # config first hides the inherited Users ACL left by the old wizard writer.
    assert not automatic_root.exists(), ('preexisting automatic config root', automatic_root)
    stage = home / 'stage'
    stage.mkdir()
    entry = 'claude-notifications-windows-amd64.exe'
    shutil.copyfile(binary, stage / entry)
    (bundle / 'bin').mkdir()
    (bundle / 'config').mkdir()
    source = {'notifications': {'desktop': {'enabled': True, 'sound': False, 'clickToFocus': False}},
              'future': {'configure_canary': 9007199254740993}}
    defaults = bundle / 'config' / 'config.json'
    defaults.write_text(json.dumps(source))
    source_bytes = defaults.read_bytes()
    control = Path(env['APPDATA']) / 'agent-notifications'
    run('internal-install-runtime', '--stage', str(stage), '--target', str(bundle / 'bin'),
        '--entry', entry, '--control-root', str(control))
    installed = bundle / 'bin' / entry
    configure = ('setup-notifications', 'configure', '--provider', 'claude',
                 '--navigation', 'none', '--policy-only', '--json')
    _windows_writable_profile(home)
    _windows_private_scratch_root(Path(env['APPDATA']))
    selected = automatic_root / 'config.json'
    assert 'AGENT_NOTIFICATIONS_CONFIG' not in env
    assert not automatic_root.exists(), 'runtime installation created the canonical directory'

    def snapshot():
        return {str(path.relative_to(automatic_root)): (
            _windows_acl_snapshot(path)['sddl'], None if path.is_dir() else path.read_bytes())
            for path in [automatic_root, *automatic_root.rglob('*')]}

    try:
        # Windows creates this TEST directory with native ProgramData inheritance.
        # Verify that the test actually exercises the reported Users RX + WD/AD.
        automatic_root.mkdir(mode=0o777)
        inherited = _windows_acl_snapshot(automatic_root)
        users = [rule for rule in inherited['rules'] if rule['sid'] == 'S-1-5-32-545'
                 and rule['type'] == 'Allow' and rule['inherited']]
        assert inherited['protected'] is False, inherited
        assert any(rule['mask'] & 1 for rule in users), inherited
        assert any(rule['mask'] & 6 == 6 for rule in users), inherited
        selected.write_text(json.dumps(source))
        before = snapshot()
        run(*configure, executable=installed, success=False)
        denied = json.loads(run('config', 'inspect', '--json', success=False).stdout)
        assert denied['errorCode'] == 'ConfigPermissionDenied', denied
        assert any(item['code'] == 'ConfigPublicReadable'
                   for item in denied['selection']['diagnostics']), denied
        assert snapshot() == before, 'configure repaired or wrote into an unsafe existing directory'
        selected.unlink()
        automatic_root.rmdir()

        # A private parent must not cause an unsafe existing config to be adopted.
        automatic_root.mkdir(mode=0o777)
        _windows_private_scratch_root(automatic_root)
        selected.write_text(json.dumps(source))
        script = (
            '$ErrorActionPreference = "Stop"; $p = $env:DIAG_PATH; $acl = Get-Acl -LiteralPath $p; '
            '$users = New-Object System.Security.Principal.SecurityIdentifier("S-1-5-32-545"); '
            '$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule('
            '$users, "ReadAndExecute", "Allow"))); Set-Acl -LiteralPath $p -AclObject $acl'
        )
        subprocess.run(['pwsh', '-NoProfile', '-NonInteractive', '-Command', script],
                       env=dict(os.environ, DIAG_PATH=str(selected)), text=True, timeout=30, check=True)
        before = snapshot()
        run(*configure, executable=installed, success=False)
        denied = json.loads(run('config', 'inspect', '--json', success=False).stdout)
        assert denied['errorCode'] == 'ConfigPermissionDenied', denied
        assert any(item['code'] == 'ConfigPublicReadable'
                   for item in denied['selection']['diagnostics']), denied
        assert snapshot() == before, 'configure repaired or replaced an unsafe existing file'
        selected.unlink()
        automatic_root.rmdir()

        configured = json.loads(run(*configure, executable=installed).stdout)
        assert configured['ready'] is True and configured['changed'] is True, configured
        assert configured['reason'] == 'configured', configured
        assert any(stage == {'name': 'prepare', 'outcome': 'saved'}
                   for stage in configured['stages']), configured
        directory_acl = _windows_assert_private(automatic_root)
        _windows_assert_private(selected)
        assert json.loads(selected.read_text())['future'] == source['future']
        initial_bytes = selected.read_bytes()
        chosen = json.loads(run('config', 'path', '--json').stdout)
        assert Path(chosen['path']) == selected and chosen['source'] == 'windows-private', chosen
        run('config', 'init', '--json')
        inspection = json.loads(run('config', 'inspect', '--json').stdout)
        assert inspection['valid'] is True and Path(inspection['selection']['path']) == selected, inspection
        status = json.loads(run('setup-notifications', 'status', '--json', executable=installed).stdout)
        assert status['globalConfiguration'] == 'configured', status
        retried = json.loads(run(*configure, executable=installed).stdout)
        assert retried['ready'] is True and retried['changed'] is False, retried
        assert selected.read_bytes() == initial_bytes and defaults.read_bytes() == source_bytes
        edited = json.loads(run('config', 'edit', '--stdin', '--expect-revision', inspection['revision'],
                                data=json.dumps({'set': {'/notifications/desktop/volume': 0.5}})).stdout)
        assert edited['changed'] is True, edited
        updated = json.loads(selected.read_text())
        assert updated['future'] == source['future'] and updated['notifications']['desktop']['volume'] == 0.5
        assert _windows_assert_private(automatic_root) == directory_acl
        _windows_assert_private(selected)
        # Selection persists when the disposable profile later becomes private.
        _windows_private_scratch_root(home)
        assert Path(run('config', 'path').stdout.strip()) == selected
        assert not (home / '.agent-notifications-config.lock').exists()
    finally:
        # The initial absent-state check establishes ownership. Never clean an
        # existing SID directory or modify native ProgramData's DACL.
        if automatic_root.exists():
            shutil.rmtree(automatic_root)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--windows-standard-user-runner', type=Path)
    args = parser.parse_args()
    binary = args.binary.resolve()
    standard_runner = args.windows_standard_user_runner.resolve() if args.windows_standard_user_runner else None
    if standard_runner:
        assert os.name == 'nt'
        token = json.loads(subprocess.check_output([str(standard_runner), '--check'], text=True))
        assert token['administrator'] is False and token['disabledPrivileges'] is True, token
        print(json.dumps({'standard_user_token': token}), flush=True)
    deliveries = []

    class Sink(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            deliveries.append((self.path, self.rfile.read(int(self.headers['Content-Length'])).decode()))
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'ok')

    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Sink)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    results = []
    try:
        with tempfile.TemporaryDirectory(prefix='notification-release-e2e-') as scratch:
            root = Path(scratch).resolve()
            _windows_acl_diagnostic(root, 'before private-root normalization')
            _windows_private_scratch_root(root)
            _windows_acl_diagnostic(root, 'after private-root normalization')
            cases = ['fresh', 'legacy', 'explicit']
            if os.name == 'nt':
                expected = subprocess.check_output(
                    ['pwsh', '-NoProfile', '-NonInteractive', '-Command',
                     "$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value; "
                     "Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) ('agent-notifications-' + $sid)"],
                    text=True).strip()
                automatic_root = Path(expected)
                assert not automatic_root.exists(), ('preexisting automatic config root', automatic_root)
                cases.extend(('windows-owner-rights', 'windows-sandbox-profile', 'windows-sandbox-automatic',
                              'windows-sandbox-configure'))
            for case in cases:
                home = root / case
                # A Linux umask of 0002 would otherwise make this ancestry
                # group-writable, which the config store correctly rejects.
                # Windows must retain the protected DACL inherited from root.
                home.mkdir(mode=0o777 if os.name == 'nt' else 0o700)
                if case == 'windows-owner-rights':
                    _windows_owner_rights_profile(home)
                env = {key: os.environ[key] for key in
                       # KnownFolder expands these machine-level variables.
                       # Preserve them while keeping all user paths isolated.
                       ('PATH', 'SystemRoot', 'SYSTEMROOT', 'SystemDrive', 'SYSTEMDRIVE',
                        'ALLUSERSPROFILE', 'WINDIR', 'COMSPEC', 'PATHEXT')
                       if key in os.environ}
                env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM='1')
                for key in ('CODEX_HOME', 'CLAUDE_HOME', 'CLAUDE_CONFIG_DIR',
                            'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME',
                            'XDG_DATA_HOME', 'XDG_STATE_HOME', 'XDG_RUNTIME_DIR', 'TMPDIR', 'TMP', 'TEMP'):
                    folder = home / key
                    folder.mkdir(mode=0o777 if os.name == 'nt' else 0o700)
                    env[key] = str(folder)
                bundle = home / 'bundle'
                bundle.mkdir()
                env['PLUGIN_ROOT'] = str(bundle)
                env['CLAUDE_PLUGIN_ROOT'] = str(bundle)

                def run(*command, data=None, success=True, executable=binary):
                    prefix = [str(standard_runner)] if standard_runner else []
                    result = subprocess.run([*prefix, str(executable), *command], cwd=home, env=env,
                                            input=data, text=True, capture_output=True, timeout=30)
                    assert (result.returncode == 0) == success, (case, command, result.returncode, result.stdout, result.stderr)
                    return result

                assert run('version').stdout.strip() == 'claude-notifications ' + args.version
                legacy = home / '.claude' / 'claude-notifications-go' / 'config.json'
                if case == 'windows-sandbox-configure':
                    _windows_configure_first(run, binary, home, bundle, env, automatic_root)
                    results.append(case)
                    continue
                if case == 'windows-sandbox-profile':
                    _windows_writable_profile(home)
                    # Repairing APPDATA alone cannot fix the writable home.
                    _windows_private_scratch_root(Path(env['APPDATA']))
                    env['AGENT_NOTIFICATIONS_CONFIG'] = str(Path(env['APPDATA']) / 'agent-notifications' / 'config.json')
                    denied = run('config', 'init', '--json', success=False)
                    failure = json.loads(denied.stdout)
                    detail = failure['permissionFailure']
                    assert failure['errorCode'] == 'ConfigPermissionDenied', failure
                    assert Path(detail['objectPath']) == home, detail
                    assert detail['trusteeSid'] == 'S-1-1-0', detail
                    assert detail['reason'] == 'unrelated_write_grant', detail
                    assert detail['accessMask'] & 0x40, detail
                    assert 'objectPath=' + json.dumps(str(home)) in denied.stderr, denied.stderr
                    assert 'S-1-1-0' in denied.stderr, denied.stderr
                    assert not (home / '.agent-notifications-config.lock').exists()
                    denied = run('config', 'init', '--json', success=False)
                    assert json.loads(denied.stdout)['permissionFailure'] == detail, denied.stdout
                    # An existing managed config encounters the same ancestor
                    # on reads. Its safe inspection must not disclose contents.
                    blocked = Path(env['AGENT_NOTIFICATIONS_CONFIG'])
                    blocked.parent.mkdir()
                    blocked.write_text('{"future":"ACL_DIAGNOSTIC_SECRET_CANARY"}')
                    Path(str(blocked) + '.lock').write_text('')
                    inspected = run('config', 'inspect', '--json', success=False)
                    assert json.loads(inspected.stdout)['permissionFailure'] == detail, inspected.stdout
                    assert 'ACL_DIAGNOSTIC_SECRET_CANARY' not in inspected.stdout + inspected.stderr
                    assert blocked.read_text() == '{"future":"ACL_DIAGNOSTIC_SECRET_CANARY"}'
                    # Explicit selection outside that ancestry can skip the
                    # unavailable profile guard and retain its private target lock.
                    safe = root / 'sandbox-private-config'
                    safe.mkdir()
                    env['AGENT_NOTIFICATIONS_CONFIG'] = str(safe / 'config.json')
                elif case == 'windows-sandbox-automatic':
                    # Exercise the real native ProgramData directory, not a
                    # private scratch substitute. Refuse any preexisting state.
                    assert not automatic_root.exists(), ('preexisting automatic config root', automatic_root)
                    _windows_writable_profile(home)
                    _windows_private_scratch_root(Path(env['APPDATA']))
                    chosen = json.loads(run('config', 'path', '--json').stdout)
                    assert Path(chosen['path']) == automatic_root / 'config.json', chosen
                    assert 'AGENT_NOTIFICATIONS_CONFIG' not in env
                    # An unrelated broad-read grant on a precreated SID root
                    # must not be adopted, repaired or hidden by a new default.
                    automatic_root.mkdir(mode=0o777)
                    _windows_writable_profile(automatic_root)
                    acl_command = ['pwsh', '-NoProfile', '-NonInteractive', '-Command',
                                   '(Get-Acl -LiteralPath $env:DIAG_PATH).Sddl']
                    acl_env = dict(os.environ, DIAG_PATH=str(automatic_root))
                    original_acl = subprocess.check_output(acl_command, env=acl_env, text=True)
                    denied = run('config', 'init', '--json', success=False)
                    assert json.loads(denied.stdout)['errorCode'] == 'ConfigPermissionDenied', denied.stdout
                    assert list(automatic_root.iterdir()) == [], 'denial created entries'
                    assert subprocess.check_output(acl_command, env=acl_env, text=True) == original_acl
                    automatic_root.rmdir()
                elif case == 'explicit':
                    env['AGENT_NOTIFICATIONS_CONFIG'] = str(home / 'custom' / 'config.json')
                if case == 'legacy':
                    private_mode = 0o777 if os.name == 'nt' else 0o700
                    legacy.parent.parent.mkdir(mode=private_mode)
                    legacy.parent.mkdir(mode=private_mode)
                    legacy.write_text('{}')
                    legacy.chmod(0o600)
                selected = Path(run('config', 'path').stdout.strip())
                if case == 'legacy':
                    assert selected == legacy
                elif case in ('explicit', 'windows-sandbox-profile'):
                    assert selected == Path(env['AGENT_NOTIFICATIONS_CONFIG'])
                else:
                    assert selected != legacy
                try:
                    run('config', 'init')
                except AssertionError:
                    _windows_acl_diagnostic(selected.parent, 'on config init failure')
                    raise
                assert selected.exists()
                if case == 'windows-sandbox-profile':
                    assert (selected.parent / (selected.name + '.lock')).exists()
                    assert not (home / '.agent-notifications-config.lock').exists()
                if case == 'windows-sandbox-automatic':
                    assert selected == automatic_root / 'config.json'
                    assert not (home / '.agent-notifications-config.lock').exists()
                    # Tightening the profile later must not silently select a
                    # different absent config and discard established settings.
                    _windows_private_scratch_root(home)
                    assert Path(run('config', 'path').stdout.strip()) == selected
                    _windows_writable_profile(home)
                if case != 'legacy':
                    assert not legacy.exists()
                document = {
                    'notifications': {
                        'desktop': {'enabled': False, 'sound': False, 'terminalBell': False},
                        'webhook': {'enabled': True, 'preset': 'slack',
                                    'url': f'http://127.0.0.1:{server.server_port}/shared'},
                    },
                    'future': {'preserved': 9007199254740993},
                }
                if case != 'legacy':
                    document.update(schemaVersion=2, agents={
                        product: {'notifications': {'webhook': {
                            'url': f'http://127.0.0.1:{server.server_port}/{product}'}}}
                        for product in ('claude', 'codex')
                    })
                selected.write_text(json.dumps(document))
                before = selected.read_bytes()
                run('config', 'init')
                run('config', 'inspect', '--json')
                assert selected.read_bytes() == before
                for product in ('claude', 'codex'):
                    marker = f'release-{case}-{product}'
                    if product == 'codex':
                        event = 'Stop'
                        payload = {'hook_event_name': event, 'session_id': marker, 'turn_id': marker,
                                   'cwd': str(home), 'last_assistant_message': marker, 'stop_hook_active': False}
                    else:
                        event = 'Notification'
                        payload = {'hook_event_name': event, 'session_id': marker,
                                   'notification_type': 'permission_prompt', 'message': marker}
                    count = len(deliveries)
                    run('handle-hook', event, '--product', product, data=json.dumps(payload))
                    assert len(deliveries) == count + 1, (case, product, deliveries)
                    expected = '/shared' if case == 'legacy' else '/' + product
                    assert deliveries[-1][0] == expected and marker in deliveries[-1][1], deliveries[-1]
                    if product == 'codex':
                        assert marker in json.loads(deliveries[-1][1])['attachments'][0]['text']
                payload.update(session_id=case + '-literal-help', turn_id='literal-help',
                               last_assistant_message='--help')
                count = len(deliveries)
                run('handle-hook', 'Stop', '--product', 'codex', data=json.dumps(payload))
                assert len(deliveries) == count + 1
                # The shared message formatter strips a leading Markdown bullet.
                # The important regression is delivery, not CLI help interception.
                assert json.loads(deliveries[-1][1])['attachments'][0]['text'].endswith('-help'), deliveries[-1]
                assert selected.read_bytes() == before
                if case == 'windows-sandbox-automatic':
                    # Mutation uses the same automatic identity as both hooks.
                    inspected = json.loads(run('config', 'inspect', '--json').stdout)
                    assert inspected['selection']['path'] == str(selected), inspected
                    edited = run('config', 'edit', '--stdin', '--expect-revision', inspected['revision'],
                                 data=json.dumps({'set': {'/notifications/desktop/volume': 0.5}}))
                    assert json.loads(edited.stdout)['changed'] is True, edited.stdout
                    updated = json.loads(selected.read_text())
                    assert updated['future'] == document['future']
                    assert updated['agents'] == document['agents']
                    assert updated['notifications']['webhook'] == document['notifications']['webhook']
                    assert updated['notifications']['desktop']['volume'] == 0.5
                    private_bytes = selected.read_bytes()
                    legacy.parent.mkdir(parents=True, mode=0o777)
                    planted = '{"future":"PLANTED_PROFILE_CONFIG_CANARY"}'
                    legacy.write_text(planted)
                    conflict = run('config', 'inspect', '--json', success=False)
                    assert 'ConfigConflict' in conflict.stderr
                    assert 'PLANTED_PROFILE_CONFIG_CANARY' not in conflict.stdout + conflict.stderr
                    count = len(deliveries)
                    for product in ('claude', 'codex'):
                        prefix = [str(standard_runner)] if standard_runner else []
                        rejected = subprocess.run([*prefix, str(binary), 'handle-hook', 'Stop', '--product', product],
                                                  cwd=home, env=env, input=json.dumps(payload), text=True,
                                                  capture_output=True, timeout=30)
                        assert 'PLANTED_PROFILE_CONFIG_CANARY' not in rejected.stdout + rejected.stderr
                    assert len(deliveries) == count
                    assert selected.read_bytes() == private_bytes and legacy.read_text() == planted
                    legacy.unlink()
                selected.write_text('{invalid-config')
                count = len(deliveries)
                run('config', 'inspect', '--json', success=False)
                result = run('handle-hook', 'Stop', '--product', 'codex', data=json.dumps(payload))
                assert result.stdout == result.stderr == ''
                assert len(deliveries) == count
                assert selected.read_text() == '{invalid-config'
                results.append(case)
                if case == 'windows-sandbox-automatic':
                    # Created by this invocation after the absent-state check;
                    # never remove an existing application/user directory.
                    shutil.rmtree(automatic_root)
        print(json.dumps({'result': 'PASS', 'version': args.version,
                          'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                          'profiles': results, 'webhook_deliveries': len(deliveries)}))
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


if __name__ == '__main__':
    main()
