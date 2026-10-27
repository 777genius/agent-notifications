#!/usr/bin/env python3
"""Real published release -> candidate -> repeat, in fresh TEST profiles only.

Preparation is the only network discovery phase. Execution accepts the frozen
release/asset IDs and verified local bytes, plus actual Claude and Codex CLIs.
Native plugin listings prove registration/enablement, not a model turn, MCP
tool invocation, desktop notification, or client restart.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import stat
import subprocess
import tomllib
import urllib.parse
import urllib.request
import zipfile

REPO = '777genius/agent-notifications'
API = 'https://api.github.com/repos/' + REPO


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    path.write_text(json.dumps(value, indent=2) + '\n', encoding='utf-8')


class AssetRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None and urllib.parse.urlsplit(req.full_url)[:2] != urllib.parse.urlsplit(redirected.full_url)[:2]:
            redirected.remove_header('Authorization')
        return redirected


def fetch(url, binary=False):
    headers = {'Accept': 'application/octet-stream' if binary else 'application/vnd.github+json',
               'User-Agent': 'agent-notifications-TEST-upgrade'}
    if os.environ.get('GH_TOKEN'):
        headers['Authorization'] = 'Bearer ' + os.environ['GH_TOKEN']
    with urllib.request.build_opener(AssetRedirectHandler()).open(urllib.request.Request(url, headers=headers), timeout=90) as response:
        return response.read()


def prepare(args):
    root = args.output.resolve()
    root.mkdir(parents=True, exist_ok=False, mode=0o700)
    # Latest is resolved once, before executing any installation. Historical
    # published tags use the same immutable custody checks as the default.
    endpoint = '/releases/tags/' + args.stable_tag if args.stable_tag else '/releases/latest'
    release = json.loads(fetch(API + endpoint))
    assert not release['draft'] and not release['prerelease'] and release['published_at']
    assert re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', release['tag_name'])
    names = ('checksums.txt', 'claude-notifications-linux-amd64', 'agent-notify-portable-linux-amd64.zip')
    pin = {'schema_version': 1, 'repository': REPO, 'tag': release['tag_name'],
           'release_id': release['id'], 'published_at': release['published_at'], 'assets': {}}
    for name in names:
        asset, = [a for a in release['assets'] if a['name'] == name]
        expected = asset.get('digest', '')
        assert re.fullmatch(r'sha256:[0-9a-f]{64}', expected), ('missing published digest', name)
        # An asset-ID URL cannot silently follow a moved release tag.
        url = API + '/releases/assets/' + str(asset['id'])
        path = root / name
        path.write_bytes(fetch(url, binary=True))
        assert sha(path) == expected[7:], ('published asset digest mismatch', name)
        pin['assets'][name] = {'id': asset['id'], 'url': url, 'sha256': sha(path)}
    entries = {}
    for line in (root / 'checksums.txt').read_text().splitlines():
        if not line.strip() or line.startswith('#'):
            continue
        match = re.fullmatch(r'([0-9a-fA-F]{64})\s+\*?([^\r\n]+)', line)
        assert match and match[2] not in entries, 'invalid or duplicate checksum'
        entries[match[2]] = match[1].lower()
    for name in names[1:]:
        assert entries[name] == pin['assets'][name]['sha256'], ('checksum disagreement', name)
    (root / names[1]).chmod(0o700)
    write_json(root / 'stable-pin.json', pin)
    print(json.dumps(pin))


def verify_pin(path):
    pin = json.loads(path.read_text())
    assert pin['schema_version'] == 1 and pin['repository'] == REPO
    assert isinstance(pin['release_id'], int) and pin['release_id'] > 0
    assert re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', pin['tag'])
    for name, asset in pin['assets'].items():
        assert PurePosixPath(name).name == name and name not in ('.', '..')
        assert asset['url'] == API + '/releases/assets/' + str(asset['id'])
        assert sha(path.parent / name) == asset['sha256'], ('pinned input changed', name)
    return pin


def snapshot(root, excluded=()):
    """Exact bytes for preservation/no-op checks; paths also detect new files."""
    return {str(p.relative_to(root)): p.read_bytes() for p in sorted(root.rglob('*'))
            if p.is_file() and str(p.relative_to(root)) not in excluded}


def runtime_metadata(root):
    return {str(p.relative_to(root)): (stat.S_IMODE(p.lstat().st_mode),
                                     os.readlink(p) if p.is_symlink() else None)
            for p in root.rglob('*')}


def managed_control(control, runtime, installation_id):
    ledger = json.loads((control / 'ownership.json').read_text())
    policy = json.loads((control / 'policy-generation.json').read_text())
    assert ledger['Owner'] == 'existing-installer' and ledger['ID']
    assert ledger['RuntimeRoot'] == str(runtime) and not ledger.get('PendingMutation')
    assert policy['Generation'] == ledger['PolicyGeneration'] and policy['Enabled'] == ledger['Enabled']
    registrations = [json.loads(c['Registration']) for key, c in ledger['Consumers'].items()
                     if key.startswith('portable:')]
    assert sorted(r['integration'] for r in registrations) == ['claude', 'codex'], 'duplicate or missing managed consumer'
    for registration in registrations:
        assert registration['installationID'] == installation_id and registration['componentID'] == ledger['ID']
        assert registration['runtimeRoot'] == str(runtime) and registration['controlRoot'] == str(control)
    return ledger, policy


def execute(args):
    assert platform.system() == 'Linux' and platform.machine() in ('x86_64', 'amd64'), 'native Linux amd64 gate'
    pin_path = args.pin.resolve()
    pin = verify_pin(pin_path)  # No network calls or release discovery below.
    candidate, package = args.candidate_binary.resolve(strict=True), args.candidate_package.resolve(strict=True)
    with zipfile.ZipFile(package) as bundle:
        packed_binary = bundle.read('bin/claude-notifications')
        assert hashlib.sha256(packed_binary).hexdigest() == sha(candidate), 'candidate package embeds a different executable'
        package_version = json.loads(bundle.read('plugin.json'))['version']
    claude, codex = args.claude.resolve(strict=True), args.codex.resolve(strict=True)
    root = args.output.resolve()
    root.mkdir(parents=True, exist_ok=False, mode=0o700)
    report = {'status': 'FAIL', 'stable': pin, 'candidate_sha256': sha(candidate),
              'candidate_package_sha256': sha(package), 'native_clients': {}, 'cases': {},
              'candidate_version': package_version,
              'scope': 'real installers and native plugin registration/enablement; no provider, model turn, desktop delivery or restart'}
    try:
        for case in args.cases.split(','):
            assert case in ('clean', 'upgrade'), 'unknown install case'
            home = root / ('TEST-' + case)
            home.mkdir(mode=0o700)
            env = {'PATH': os.environ.get('PATH', os.defpath), 'HOME': str(home), 'USERPROFILE': str(home),
                   'GIT_CONFIG_NOSYSTEM': '1', 'DISABLE_AUTOUPDATER': '1',
                   'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC': '1'}
            for key in ('CLAUDE_CONFIG_DIR', 'CODEX_HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME',
                        'XDG_DATA_HOME', 'XDG_STATE_HOME', 'XDG_RUNTIME_DIR', 'APPDATA', 'LOCALAPPDATA', 'TMPDIR'):
                folder = home / key
                folder.mkdir(mode=0o700)
                env[key] = str(folder)
            env['CLAUDE_HOME'] = env['CLAUDE_CONFIG_DIR']
            control, runtime, global_config = home / 'control', home / 'runtime', home / 'config.json'
            scope = home / 'TEST-project'
            scope.mkdir()
            records = []

            def run(label, executable, *command):
                result = subprocess.run([str(executable), *map(str, command)], env=env, cwd=scope,
                                        input='', text=True, capture_output=True, timeout=120)
                (home / (label + '.stdout')).write_text(result.stdout)
                (home / (label + '.stderr')).write_text(result.stderr)
                records.append({'label': label, 'exit_code': result.returncode, 'argv': [str(executable), *map(str, command)]})
                write_json(home / 'commands.json', records)
                assert result.returncode == 0, (label, result.returncode, result.stdout, result.stderr)
                return result.stdout

            for client, executable in (('claude', claude), ('codex', codex)):
                report['native_clients'][client] = {'version': run(client + '-version', executable, '--version').strip(),
                                                     'sha256': sha(executable)}
            assert run('candidate-version', candidate, 'version').strip() == 'claude-notifications v' + package_version
            # A real foreign native plugin, separately installed before our
            # product. It carries no executable MCP server and never calls a model.
            foreign = home / 'TEST-foreign-plugin'
            for manifest in ('.claude-plugin/plugin.json', '.codex-plugin/plugin.json'):
                write_json(foreign / manifest, {'name': 'test-foreign', 'version': '1.0.0'})
            write_json(foreign / '.claude-plugin/marketplace.json', {
                'name': 'test-foreign-marketplace', 'owner': {'name': 'TEST'},
                'plugins': [{'name': 'test-foreign', 'source': './'}]})
            write_json(foreign / '.agents/plugins/marketplace.json', {
                'name': 'test-foreign-marketplace', 'plugins': [{'name': 'test-foreign',
                'category': 'Productivity', 'source': {'source': 'local', 'path': './'},
                'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'}}]})
            run('foreign-claude-marketplace', claude, 'plugin', 'marketplace', 'add', foreign)
            run('foreign-claude-install', claude, 'plugin', 'install', 'test-foreign@test-foreign-marketplace')
            run('foreign-codex-marketplace', codex, 'plugin', 'marketplace', 'add', foreign, '--json')
            run('foreign-codex-install', codex, 'plugin', 'add', 'test-foreign@test-foreign-marketplace', '--json')
            codex_config = Path(env['CODEX_HOME']) / 'config.toml'
            codex_config.write_bytes(b'model = "gpt-5.1"\n' + codex_config.read_bytes())
            # These user files must survive byte for byte, including formatting,
            # large JSON numbers and a user-owned file in the shared data root.
            settings = Path(env['CLAUDE_CONFIG_DIR']) / 'settings.local.json'
            settings.write_bytes(b'{ "TEST_user_setting": 9007199254740993 }\n')
            global_config.write_bytes(b'{"schemaVersion":2,"notifications":{"desktop":{"enabled":false,"volume":0.37}},"future":{"canary":9007199254740993}}\n')
            foreign_data = home / 'uap/plugin-data/TEST-foreign-user-data.json'
            foreign_data.parent.mkdir(parents=True)
            foreign_data.write_bytes(b'{ "TEST_user_data": [null, false, 9007199254740993] }\n')
            preserved = {p: p.read_bytes() for p in (settings, global_config, foreign_data)}
            foreign_before = snapshot(foreign)
            shared = ['--agents', 'claude,codex', '--hooks', 'false', '--agent-notify', 'true', '--yes', '--json',
                      '--control-root', control, '--runtime-root', runtime, '--global-config', global_config,
                      '--codex-home', env['CODEX_HOME'], '--claude-config', env['CLAUDE_CONFIG_DIR'],
                      '--claude-executable', claude, '--codex-executable', codex, '--scope-root', scope]
            entry = 'claude-notifications-linux-amd64'
            helper = runtime / 'bin' / entry

            def install(label, binary, archive, auto=False):
                # Exercise the same real runtime publication adapter used by
                # install.sh; only downloads were completed during preparation.
                stage = home / (label + '-stage')
                stage.mkdir(mode=0o700)
                shutil.copy2(binary, stage / entry)
                (stage / entry).chmod(0o700)
                run(label + '-runtime', binary, 'internal-install-runtime', '--stage', stage,
                    '--target', runtime / 'bin', '--entry', entry, '--control-root', control)
                command = ['setup-notifications', 'wizard', '--action', 'install', '--package', archive,
                           '--helper', helper, *shared]
                if auto:
                    command.append('--install-or-update')
                result = json.loads(run(label + '-wizard', binary, *command))
                assert result['outcome'] in ('completed', 'unchanged'), (label, result)
                assert all(t['outcome'] in ('completed', 'unchanged', 'installed')
                           for t in result['targets'] if t['unit'] == 'agent-notify'), result
                assert sha(helper) == sha(binary), 'runtime/version inconsistency'
                for path, before in preserved.items():
                    assert path.read_bytes() == before, ('user data changed', path)
                assert snapshot(foreign) == foreign_before, 'foreign plugin source changed'
                assert tomllib.loads(codex_config.read_text())['model'] == 'gpt-5.1', 'Codex user setting changed'
                return result

            state = home / 'uap/state/state-v2.json'
            # The public clients independently observe both the real product and
            # the foreign registration. Installer JSON alone cannot make this green.
            def native_listings(label, expected_version):
                c = json.loads(run(label + '-claude-list', claude, 'plugin', 'list', '--json'))
                x = json.loads(run(label + '-codex-list', codex, 'plugin', 'list', '--json'))['installed']
                for client, rows in (('claude', c), ('codex', x)):
                    for name in ('agent-notify', 'test-foreign'):
                        matches = [r for r in rows if r.get('name', r.get('id', '').split('@')[0]) == name]
                        assert len(matches) == 1 and matches[0]['enabled'] is True, (client, name, rows)
                        assert matches[0]['version'] == (expected_version if name == 'agent-notify' else '1.0.0'), (client, name, rows)
                return c, x
            state_before = None
            installation_id = None
            if case == 'upgrade':
                old = install('old', pin_path.parent / entry, pin_path.parent / 'agent-notify-portable-linux-amd64.zip')
                native_listings('old', pin['tag'][1:])
                installation_id = old['installationID']
                state_before = state.read_bytes()
                if pin['tag'] == 'v1.49.8':
                    assert json.loads(state_before)['schema_version'] == 4, 'historical artifact no longer demonstrates schema 4'
                (home / 'state-before-upgrade.json').write_bytes(state_before)
                # Include data owned by the existing product, not just unrelated files.
                for data_dir in (home / 'uap/plugin-data').iterdir():
                    if data_dir.is_dir():
                        sentinel = data_dir / 'TEST-user-data.txt'
                        sentinel.write_bytes(b'user-edited plugin data\n')
                        preserved[sentinel] = sentinel.read_bytes()
            upgraded = install('candidate', candidate, package, auto=True)
            if installation_id:
                assert upgraded['installationID'] == installation_id, 'installation identity changed'
            current = json.loads(state.read_text())
            backups = sorted((home / 'uap/state').glob('**/*backup*'))
            backup_files = {p: p.read_bytes() for p in backups if p.is_file()}
            old_schema = json.loads(state_before)['schema_version'] if state_before else None
            if old_schema is not None and old_schema != current['schema_version']:
                exact = [p for p, data in backup_files.items() if data == state_before]
                assert len(exact) == 1, ('expected one byte-exact legacy backup', list(backup_files))
            native_listings('candidate', package_version)
            # The mutation lock is a PID/acquisition-time lease, observed to
            # change on a real unchanged repeat. It is not a registry or config.
            excluded = {home / 'uap': ('state/mutation.lock',)}
            before_repeat = {p: snapshot(p, excluded.get(p, ()))
                             for p in (home / 'uap', runtime, Path(env['CLAUDE_CONFIG_DIR']), Path(env['CODEX_HOME']))}
            runtime_before = runtime_metadata(runtime)
            ledger_before, policy_before = managed_control(control, runtime, upgraded['installationID'])
            manifest = {str(root.relative_to(home)): {
                name: hashlib.sha256(data).hexdigest() for name, data in files.items()}
                for root, files in before_repeat.items()}
            manifest['managed-control'] = {'ownership': ledger_before, 'policy': policy_before}
            write_json(home / 'before-repeat-files.json', manifest)
            repeated = install('repeat', candidate, package, auto=True)
            assert repeated['installationID'] == upgraded['installationID']
            for directory, before in before_repeat.items():
                after = snapshot(directory, excluded.get(directory, ()))
                changed = [name for name in sorted(before.keys() | after.keys()) if before.get(name) != after.get(name)]
                assert not changed, ('repeat changed installation/config/registrations', directory, changed)
            assert runtime_metadata(runtime) == runtime_before, 'repeat changed runtime paths, permissions or symlink targets'
            ledger_after, policy_after = managed_control(control, runtime, repeated['installationID'])
            # Managed generations may advance for an otherwise unchanged apply.
            # Ownership, runtime identity, policy and consumer/file maps may not.
            counters = ('Generation', 'PolicyGeneration')
            assert {k: v for k, v in ledger_before.items() if k not in counters} == {
                k: v for k, v in ledger_after.items() if k not in counters}, 'repeat changed managed ownership or registrations'
            assert {k: v for k, v in policy_before.items() if k != 'Generation'} == {
                k: v for k, v in policy_after.items() if k != 'Generation'}, 'repeat changed managed policy'
            assert all(ledger_after[k] >= ledger_before[k] for k in counters), 'managed generation went backwards'
            assert {p: p.read_bytes() for p in backup_files} == backup_files, 'repeat changed migration backup'
            assert sorted((home / 'uap/state').glob('**/*backup*')) == backups, 'repeat added migration backup'
            native_listings('repeat', package_version)
            report['cases'][case] = {'status': 'PASS', 'old_schema': old_schema,
                                     'schema': current['schema_version'], 'installation_id': upgraded['installationID'],
                                     'migration_backups': [str(p.relative_to(home)) for p in backup_files],
                                     'native_registration': 'PASS', 'repeat_config_registrations_unchanged': True,
                                     'managed_runtime_unchanged': True, 'managed_control_consistent': True,
                                     'managed_generation_before': ledger_before['Generation'],
                                     'managed_generation_after': ledger_after['Generation']}
        report['status'] = 'PASS'
    except Exception as error:
        report['error'] = str(error)
        raise
    finally:
        write_json(root / 'report.json', report)
        print(json.dumps(report))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    prep = sub.add_parser('prepare')
    prep.add_argument('--output', type=Path, required=True)
    prep.add_argument('--stable-tag', help='Additional historical published fixture; default resolves Latest once')
    run = sub.add_parser('run')
    for flag in ('pin', 'candidate-binary', 'candidate-package', 'claude', 'codex', 'output'):
        run.add_argument('--' + flag, type=Path, required=True)
    run.add_argument('--cases', default='clean,upgrade', help='clean,upgrade (default), or one case for regression diagnosis')
    args = parser.parse_args()
    (prepare if args.command == 'prepare' else execute)(args)


if __name__ == '__main__':
    main()
