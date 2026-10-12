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


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    binary = args.binary.resolve()
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
                cases.extend(('windows-owner-rights', 'windows-sandbox-profile'))
            for case in cases:
                home = root / case
                home.mkdir()
                if case == 'windows-owner-rights':
                    _windows_owner_rights_profile(home)
                env = {key: os.environ[key] for key in
                       ('PATH', 'SystemRoot', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'PATHEXT')
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

                def run(*command, data=None, success=True):
                    result = subprocess.run([str(binary), *command], cwd=home, env=env,
                                            input=data, text=True, capture_output=True, timeout=30)
                    assert (result.returncode == 0) == success, (case, command, result.returncode, result.stderr)
                    return result

                assert run('version').stdout.strip() == 'claude-notifications ' + args.version
                legacy = home / '.claude' / 'claude-notifications-go' / 'config.json'
                if case == 'windows-sandbox-profile':
                    _windows_writable_profile(home)
                    # Repairing APPDATA alone cannot fix the writable home.
                    _windows_private_scratch_root(Path(env['APPDATA']))
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
                    env['AGENT_NOTIFICATIONS_CONFIG'] = str(Path(env['APPDATA']) / 'agent-notifications' / 'config.json')
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
                elif case == 'explicit':
                    env['AGENT_NOTIFICATIONS_CONFIG'] = str(home / 'custom' / 'config.json')
                if case == 'legacy':
                    legacy.parent.mkdir(parents=True, mode=0o777 if os.name == 'nt' else 0o700)
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
                selected.write_text('{invalid-config')
                count = len(deliveries)
                run('config', 'inspect', '--json', success=False)
                result = run('handle-hook', 'Stop', '--product', 'codex', data=json.dumps(payload))
                assert result.stdout == result.stderr == ''
                assert len(deliveries) == count
                assert selected.read_text() == '{invalid-config'
                results.append(case)
        print(json.dumps({'result': 'PASS', 'version': args.version,
                          'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                          'profiles': results, 'webhook_deliveries': len(deliveries)}))
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


if __name__ == '__main__':
    main()
