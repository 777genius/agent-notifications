"""Owned TEST host controller; never run before complete assembly review."""
import base64, hashlib, importlib.util, json, os, pathlib, re, stat, subprocess, sys, time, uuid

if not __debug__: raise RuntimeError('optimized_interpreter_not_supported')
if len(sys.argv) != 2:
    raise RuntimeError('one_owned_TEST_root_argument_required')
if pathlib.Path(sys.argv[1]).is_symlink(): raise RuntimeError('symlink_TEST_root_argument_refused')
ROOT = pathlib.Path(sys.argv[1]).resolve()
context = json.loads((ROOT / 'operator-context.json').read_text())
IMAGE = 'sha256:3d6d8f7204a993dd58b7a2aa02312fbf4190cfecfe5c539d65d0a0eda3a0725c'
FROZEN = pathlib.Path('/tmp/navigation-guest-stage-TEST-b976acd4eef542a5ae2507425c8e96de')
BASE = FROZEN / 'base.img'
PROVISIONED = FROZEN / 'overlay.qcow2'
ARCHIVES = pathlib.Path('/tmp/navigation-handoff-runtime-export-TEST-20261006-a5e8f1b')
ARCHIVE_HASHES = {'portal.tar': '3a80227876fc6426bcf64658a18e0235f970daadc6c13923d4f7a6e78c00bc2c',
                  'gtk.tar': '68e165b67639cf8c77ed7edda63210c5ebfd0aa532f52e8e56d8fc21d1d5b35e'}
SOURCE_NAMES = {'probe.py', 'runtime-stage.py', 'guest-bootstrap.py', 'client-callback.py',
                'client-sender.py', 'guest-pointer-entry.py', 'guest-pointer.so',
                'kernel-observer.py', 'protocol-observer.py', 'client-controller.py',
                'server-observer.so', 'server-observer.py'}
EXPECTED_BASE = '6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2'
EXPECTED_PROVISIONED = '839d348d47bb6f370012da63479e6d9b7493e9ce5a5fa717d4f713b449327a4e'


def sha(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


assert set(context) == {'expectedMachineID', 'expectedSources', 'manifest'}
assert context['expectedMachineID'] == 'd856d40da5ad4e23b4f67773e5942842'
assert pathlib.Path('/etc/machine-id').read_text().strip() == context['expectedMachineID']
assert set(context['expectedSources']) == SOURCE_NAMES
assert os.getuid() == 1000 and ROOT.parent == pathlib.Path('/tmp') and re.fullmatch(r'navigation-handoff-stage-TEST-[0-9a-f]{32}', ROOT.name)
assert (ROOT / '.host-test-root').read_text() == 'Linux offline guest handoff TEST only\n'
assert ROOT.stat().st_uid == 1000 and ROOT.stat().st_mode & 0o777 == 0o700
# No automatic retry after this immutable native-intent gate has been created.
with (ROOT / 'started.json').open('x') as started:
    json.dump({'operatorSHA256': sha(pathlib.Path(__file__)), 'boot': time.clock_gettime(time.CLOCK_BOOTTIME)}, started)
nonce = uuid.uuid4().hex
name = 'navigation-guest-test-' + nonce
report = dict(scope='offline_selected_client_native_handoff_TEST', root=str(ROOT), owner=nonce,
    imageID=IMAGE, operatorSHA256=sha(pathlib.Path(__file__)), contextSHA256=sha(ROOT / 'operator-context.json'), passed=False,
    retryAllowed=False, containerRemoved=False, applicationLaunchOutcome='not_started',
    notificationAttempted=False, clickAttempted=False, handoffQualified=False, activationQualified=False,
    clientSandboxQualified=False, navigationQualified=False,
    commands=[])
docker = ['sudo', '-n', 'docker']
created_id = None
create_intent = False


def persist():
    temp = ROOT / 'operator-result.json.tmp'
    temp.write_text(json.dumps(report, indent=2) + '\n')
    temp.replace(ROOT / 'operator-result.json')


def command(label, argv, timeout=30):
    item = {'label': label, 'startedBoot': time.clock_gettime(time.CLOCK_BOOTTIME)}
    report['commands'].append(item); persist()
    try:
        with (ROOT / (label + '.stdout')).open('xb') as out, (ROOT / (label + '.stderr')).open('xb') as err:
            try:
                result = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=out, stderr=err, timeout=timeout)
                item.update(exitCode=result.returncode, collected=True)
            except subprocess.TimeoutExpired:
                item.update(timeout=True, collected=True); raise
    finally:
        item['collectedBoot'] = time.clock_gettime(time.CLOCK_BOOTTIME)
        for stream in ('stdout', 'stderr'):
            try: item[stream + 'SHA256'] = sha(ROOT / (label + '.' + stream))
            except Exception as error: item[stream + 'HashError'] = str(error)
        persist()
    return result.returncode, (ROOT / (label + '.stdout')).read_bytes()


def owned_container(label, security):
    rc, raw = command(label, docker + ['container', 'inspect', created_id or name])
    assert rc == 0
    obj = json.loads(raw)[0]
    assert obj['Name'] == '/' + name and obj['Image'] == IMAGE
    assert obj['Config']['Labels']['navigation.owner'] == nonce and obj['Config']['Labels']['navigation.test'] == 'true'
    if created_id: assert obj['Id'] == created_id
    if security:
        config = obj['HostConfig']
        assert config['NetworkMode'] == 'none' and config['CapDrop'] == ['ALL'] and config['Privileged'] is False
        assert config['SecurityOpt'] == ['no-new-privileges'] and obj['Config']['User'] == '1000:1000'
        assert config['GroupAdd'] == ['994'] and config['Devices'] == [{'PathOnHost': '/dev/kvm', 'PathInContainer': '/dev/kvm', 'CgroupPermissions': 'rw'}]
        assert config['Memory'] == 6 * 1024**3 and config['PidsLimit'] == 128 and config['NanoCpus'] == 2 * 10**9
        assert len(obj['Mounts']) == 1 and obj['Mounts'][0]['Source'] == str(ROOT) and obj['Mounts'][0]['Destination'] == '/evidence'
        assert obj['Mounts'][0]['Type'] == 'bind' and obj['Mounts'][0]['RW'] is True
        assert obj['Config']['Entrypoint'] == ['/usr/bin/python3'] and obj['Config']['Cmd'] == ['-I', '/evidence/probe.py']
    return obj


try:
    assert stat.S_ISCHR(pathlib.Path('/dev/kvm').stat().st_mode) and pathlib.Path('/dev/kvm').stat().st_gid == 994
    available = int(next(row for row in pathlib.Path('/proc/meminfo').read_text().splitlines() if row.startswith('MemAvailable:')).split()[1]) * 1024
    filesystem = os.statvfs(ROOT)
    report['resources'] = dict(memoryAvailable=available, diskAvailable=filesystem.f_bavail * filesystem.f_frsize)
    if available < 7 * 1024**3 or report['resources']['diskAvailable'] < 2 * 1024**3:
        raise RuntimeError('insufficient_actual_resources')
    rc, raw = command('image_identity', docker + ['image', 'inspect', IMAGE])
    assert rc == 0
    image = json.loads(raw)[0]
    assert image['Id'] == IMAGE and image['Config']['Labels']['navigation.owner'] == 'e1ebe48b80e84a8fa5fc4973051dbb46'
    assert image['Config']['Labels']['navigation.test'] == 'true' and image['Config']['Labels']['navigation.source'] == '4be5d1481f684a891df96e6c6e69a51037f20350afc682c689cb70b3096f1be3'
    assert sha(BASE) == EXPECTED_BASE and sha(PROVISIONED) == EXPECTED_PROVISIONED
    assert BASE.stat().st_mode & 0o222 == 0 and PROVISIONED.stat().st_mode & 0o222 == 0
    # Read-only hardlinks within the owned TEST storage preserve the frozen inputs.
    os.link(BASE, ROOT / 'base.img'); os.link(PROVISIONED, ROOT / 'provisioned.qcow2')
    assert sha(ROOT / 'base.img') == EXPECTED_BASE and sha(ROOT / 'provisioned.qcow2') == EXPECTED_PROVISIONED
    if (ROOT / 'sources.json').stat().st_size > 4 * 1024**2:
        raise RuntimeError('bounded_source_capsule_required')
    sources = json.loads((ROOT / 'sources.json').read_text())
    assert set(sources) == SOURCE_NAMES
    decoded = {}
    for filename in SOURCE_NAMES:
        assert set(sources[filename]) == {'base64', 'sha256'}
        data = base64.b64decode(sources[filename]['base64'], validate=True)
        digest = hashlib.sha256(data).hexdigest()
        assert digest == sources[filename]['sha256'] == context['expectedSources'][filename]
        decoded[filename] = data
    manifest = context['manifest']
    assert set(manifest) == {'files', 'frontendSHA256', 'backendSHA256'} | ({'shipping'} if 'shipping' in manifest else set())
    assert set(manifest['files']) == SOURCE_NAMES - {'probe.py', 'runtime-stage.py', 'guest-bootstrap.py'}
    assert all(manifest['files'][name] == context['expectedSources'][name] for name in manifest['files'])
    assert manifest['frontendSHA256'] == '7fe62c1a938985b8ca4ec335a768ad36f1d17abe027098624fa4f5989c0b4594'
    assert manifest['backendSHA256'] == 'b95c473ae8fe4e3b51e7ca4bf27d4f4719786d552524443e246d1b40468d40af'
    for filename, digest in ARCHIVE_HASHES.items():
        source = ARCHIVES / filename
        assert source.is_file() and not source.is_symlink() and source.stat().st_size < 64 * 1024**2
        assert sha(source) == digest
        with source.open('rb') as inp, (ROOT / filename).open('xb') as out:
            for block in iter(lambda: inp.read(1048576), b''): out.write(block)
        (ROOT / filename).chmod(0o444)
        assert sha(ROOT / filename) == digest
    for filename, data in decoded.items():
        with (ROOT / filename).open('xb') as out: out.write(data)
        (ROOT / filename).chmod(0o444)
    if 'shipping' in manifest:
        spec = importlib.util.spec_from_file_location('TEST_shipping_assets', ROOT / 'guest-bootstrap.py')
        assets = importlib.util.module_from_spec(spec); spec.loader.exec_module(assets)
        assets.shipping_inputs(ROOT, manifest)
        for filename in manifest['shipping']['files']:
            info = (ROOT / filename).lstat()
            assert info.st_uid == 1000 and stat.S_IMODE(info.st_mode) == 0o444
        report['shipping'] = manifest['shipping']
        report['scope'] = 'offline_shipping_go_native_handoff_TEST'
    with (ROOT / 'manifest.json').open('x') as out: json.dump(manifest, out, sort_keys=True)
    (ROOT / 'manifest.json').chmod(0o444)
    report['manifestSHA256'] = sha(ROOT / 'manifest.json')
    report['sourceSHA256'] = context['expectedSources']['probe.py']
    report['guestSourceSHA256'] = manifest['files']
    (ROOT / 'container.marker').write_text('Linux offline guest handoff TEST only\n')
    create_intent = True; report['createIntent'] = True; persist()
    rc, raw = command('create', docker + ['create', '--init', '--name', name, '--user', '1000:1000',
        '--group-add', '994', '--device', '/dev/kvm:/dev/kvm:rw', '--label', 'navigation.test=true',
        '--label', 'navigation.owner=' + nonce, '--network=none', '--cap-drop=ALL',
        '--security-opt=no-new-privileges', '--pids-limit=128', '--memory=6g', '--cpus=2',
        '-e', 'NAVIGATION_GUEST_HANDOFF_TEST=1', '-v', str(ROOT) + ':/evidence:rw',
        '--entrypoint', '/usr/bin/python3', IMAGE, '-I', '/evidence/probe.py'])
    assert rc == 0
    candidate = raw.decode().strip()
    assert len(candidate) == 64 and all(character in '0123456789abcdef' for character in candidate)
    created_id = candidate; report['containerID'] = candidate; persist()
    owned_container('before_boot_identity', True)
    report['applicationLaunchOutcome'] = 'unknown'
    report['notificationAttempted'] = None; report['clickAttempted'] = None; persist()
    rc, _ = command('run', docker + ['start', '--attach', created_id], 420)
    report['nativeExitCode'] = rc
    terminal = owned_container('terminal_container_identity', True)['State']
    report['containerState'] = terminal
    report['containerExitObserved'] = terminal['Status'] == 'exited' and terminal['Running'] is False
    terminal_clean = report['containerExitObserved'] and terminal['ExitCode'] == 0 and terminal['OOMKilled'] is False and terminal['Error'] == ''
    assert (ROOT / 'native-evidence.json').stat().st_size <= 4 * 1024**2
    report['nativeResult'] = json.loads((ROOT / 'native-evidence.json').read_text())
    native = report['nativeResult']
    report['applicationLaunchOutcome'] = native.get('guestClientLaunchOutcome', 'unknown')
    for key in ('notificationAttempted', 'clickAttempted'):
        value = native.get(key); report[key] = value if isinstance(value, bool) else None
    report['passed'] = rc == 0 and terminal_clean and native.get('passed') is True and native.get('sourceSHA256') == report['sourceSHA256'] and native.get('guestSourceSHA256') == report['guestSourceSHA256']
    if 'shipping' in manifest:
        report['passed'] = report['passed'] and native.get('shipping') == manifest['shipping']
except Exception as error:
    report['failure'] = type(error).__name__ + ': ' + str(error)
finally:
    if create_intent:
        try:
            obj = owned_container('container_identity', False)
            rc, _ = command('remove', docker + ['rm', '-f', obj['Id']]); assert rc == 0
            by_id, id_raw = command('absent_by_id', docker + ['container', 'inspect', obj['Id']])
            by_name, name_raw = command('absent_by_name', docker + ['container', 'inspect', name])
            def exact_absence(rc, raw, label, target):
                error = (ROOT / (label + '.stderr')).read_bytes().strip()
                return rc == 1 and raw.strip() == b'[]' and error == b'Error response from daemon: No such container: ' + target.encode()
            report['containerRemoved'] = exact_absence(by_id, id_raw, 'absent_by_id', obj['Id']) and exact_absence(by_name, name_raw, 'absent_by_name', name)
        except Exception as error:
            report['cleanupFailure'] = type(error).__name__ + ': ' + str(error)
    checks = [('probe.py', report.get('sourceSHA256'), 'source'),
              ('manifest.json', report.get('manifestSHA256'), 'manifest'),
              ('base.img', EXPECTED_BASE, 'base'), ('provisioned.qcow2', EXPECTED_PROVISIONED, 'provisioned'),
              ('operator-context.json', report['contextSHA256'], 'context'),
              (str(pathlib.Path(__file__).resolve()), report['operatorSHA256'], 'operator')]
    checks += [(name, digest, name) for name, digest in context['expectedSources'].items()]
    checks += [(name, digest, name) for name, digest in ARCHIVE_HASHES.items()]
    checks += [(name, digest, name) for name, digest in context['manifest'].get('shipping', {}).get('files', {}).items()]
    integrity_errors = []
    for filename, expected, label in checks:
        try:
            report[label + 'Unchanged'] = expected is not None and sha(ROOT / filename) == expected
            if not report[label + 'Unchanged']: integrity_errors.append(label + '_changed')
        except Exception as error:
            report[label + 'Unchanged'] = False; integrity_errors.append(label + ': ' + str(error))
    report['integrityErrors'] = integrity_errors
    report['cleanupUnknown'] = create_intent and not report['containerRemoved']
    report['passed'] = report['passed'] and report['containerRemoved'] and not integrity_errors
    report['handoffQualified'] = report['passed'] and report.get('nativeResult', {}).get('handoffQualified') is True
    report['activationQualified'] = report['passed'] and report.get('nativeResult', {}).get('activationQualified') is True
    persist(); print(json.dumps(report), flush=True)
raise SystemExit(0 if report['passed'] else 1)
