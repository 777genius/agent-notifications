import base64, hashlib, json, os, pathlib, re, stat, subprocess, sys, time, uuid

if not __debug__: raise RuntimeError('optimized_interpreter_not_supported')
if len(sys.argv) != 2:
    raise RuntimeError('one_owned_TEST_root_argument_required')
if pathlib.Path(sys.argv[1]).is_symlink(): raise RuntimeError('symlink_TEST_root_argument_refused')
ROOT = pathlib.Path(sys.argv[1]).resolve()
context = json.loads((ROOT / 'operator-context.json').read_text())
IMAGE = 'sha256:3d6d8f7204a993dd58b7a2aa02312fbf4190cfecfe5c539d65d0a0eda3a0725c'
BASE = pathlib.Path(context['authenticatedBase'])
PROVISIONED = pathlib.Path(context['frozenProvisioned'])
EXPECTED_BASE = '6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2'
EXPECTED_PROVISIONED = '839d348d47bb6f370012da63479e6d9b7493e9ce5a5fa717d4f713b449327a4e'


def sha(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


assert pathlib.Path('/etc/machine-id').read_text().strip() == context['expectedMachineID']
assert os.getuid() == 1000 and ROOT.parent == pathlib.Path('/tmp') and re.fullmatch(r'navigation-native-stage-TEST-[0-9a-f]{32}', ROOT.name)
assert (ROOT / '.host-test-root').read_text() == 'Linux offline guest client TEST only\n'
assert ROOT.stat().st_uid == 1000 and ROOT.stat().st_mode & 0o777 == 0o700
# No automatic retry after this immutable native-intent gate has been created.
with (ROOT / 'started.json').open('x') as started:
    json.dump({'operatorSHA256': sha(pathlib.Path(__file__)), 'boot': time.clock_gettime(time.CLOCK_BOOTTIME)}, started)
nonce = uuid.uuid4().hex
name = 'navigation-guest-test-' + nonce
report = dict(scope='offline_selected_client_renderer_preflight', root=str(ROOT), owner=nonce,
    imageID=IMAGE, operatorSHA256=sha(pathlib.Path(__file__)), contextSHA256=sha(ROOT / 'operator-context.json'), passed=False,
    retryAllowed=False, containerRemoved=False, applicationLaunchOutcome='not_started',
    notificationAttempted=False, clientSandboxQualified=False, navigationQualified=False,
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
        assert config['Memory'] == 6 * 1024**3 and config['PidsLimit'] == 64 and config['NanoCpus'] == 2 * 10**9
        assert len(obj['Mounts']) == 1 and obj['Mounts'][0]['Source'] == str(ROOT) and obj['Mounts'][0]['Destination'] == '/evidence'
        assert obj['Mounts'][0]['Type'] == 'bind' and obj['Mounts'][0]['RW'] is True
        assert obj['Config']['Entrypoint'] == ['/usr/bin/python3'] and obj['Config']['Cmd'] == ['-I', '/evidence/probe.py']
    return obj


try:
    assert stat.S_ISCHR(pathlib.Path('/dev/kvm').stat().st_mode) and pathlib.Path('/dev/kvm').stat().st_gid == 994
    available = int(next(row for row in pathlib.Path('/proc/meminfo').read_text().splitlines() if row.startswith('MemAvailable:')).split()[1]) * 1024
    filesystem = os.statvfs(ROOT)
    report['resources'] = dict(memoryAvailable=available, diskAvailable=filesystem.f_bavail * filesystem.f_frsize)
    if available < 7 * 1024**3 or report['resources']['diskAvailable'] < 5 * 1024**3:
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
    sources = json.loads((ROOT / 'sources.json').read_text())
    for filename in ('probe.py', 'guest-native.py'):
        data = base64.b64decode(sources[filename]['base64'], validate=True)
        assert hashlib.sha256(data).hexdigest() == sources[filename]['sha256']
        expected = context['expectedProbeSourceSHA256'] if filename == 'probe.py' else '19c8a05a08db74b16859dc44464e5b60aa589f3b41aff3589e60f50b99aa2f1c'
        assert hashlib.sha256(data).hexdigest() == expected
        (ROOT / filename).write_bytes(data); (ROOT / filename).chmod(0o444)
    report['sourceSHA256'] = sha(ROOT / 'probe.py')
    report['guestSourceSHA256'] = sha(ROOT / 'guest-native.py')
    (ROOT / 'container.marker').write_text('Linux offline guest client TEST only\n')
    create_intent = True; report['createIntent'] = True; persist()
    rc, raw = command('create', docker + ['create', '--init', '--name', name, '--user', '1000:1000',
        '--group-add', '994', '--device', '/dev/kvm:/dev/kvm:rw', '--label', 'navigation.test=true',
        '--label', 'navigation.owner=' + nonce, '--network=none', '--cap-drop=ALL',
        '--security-opt=no-new-privileges', '--pids-limit=64', '--memory=6g', '--cpus=2',
        '-e', 'NAVIGATION_GUEST_NATIVE_TEST=1', '-v', str(ROOT) + ':/evidence:rw',
        '--entrypoint', '/usr/bin/python3', IMAGE, '-I', '/evidence/probe.py'])
    assert rc == 0
    candidate = raw.decode().strip()
    assert len(candidate) == 64 and all(character in '0123456789abcdef' for character in candidate)
    created_id = candidate; report['containerID'] = candidate; persist()
    owned_container('before_boot_identity', True)
    report['applicationLaunchOutcome'] = 'unknown'; persist()
    rc, _ = command('run', docker + ['start', '--attach', created_id], 420)
    report['nativeExitCode'] = rc
    report['nativeResult'] = json.loads((ROOT / 'native-evidence.json').read_text())
    native = report['nativeResult']
    report['applicationLaunchOutcome'] = native.get('guestClientLaunchOutcome', 'unknown')
    report['passed'] = rc == 0 and native.get('passed') is True and native.get('sourceSHA256') == report['sourceSHA256'] and native.get('guestSourceSHA256') == report['guestSourceSHA256']
except Exception as error:
    report['failure'] = type(error).__name__ + ': ' + str(error)
finally:
    if create_intent:
        try:
            obj = owned_container('container_identity', False)
            rc, _ = command('remove', docker + ['rm', '-f', obj['Id']]); assert rc == 0
            by_id, id_raw = command('absent_by_id', docker + ['container', 'inspect', obj['Id']])
            by_name, name_raw = command('absent_by_name', docker + ['container', 'inspect', name])
            report['containerRemoved'] = by_id != 0 and by_name != 0 and id_raw.strip() == b'[]' and name_raw.strip() == b'[]' and all(b'No such container' in (ROOT / (label + '.stderr')).read_bytes() for label in ('absent_by_id', 'absent_by_name'))
        except Exception as error:
            report['cleanupFailure'] = type(error).__name__ + ': ' + str(error)
    checks = [('probe.py', report.get('sourceSHA256'), 'source'),
              ('guest-native.py', report.get('guestSourceSHA256'), 'guestSource'),
              ('base.img', EXPECTED_BASE, 'base'), ('provisioned.qcow2', EXPECTED_PROVISIONED, 'provisioned'),
              ('operator-context.json', report['contextSHA256'], 'context'),
              (str(pathlib.Path(__file__).resolve()), report['operatorSHA256'], 'operator')]
    integrity_errors = []
    for filename, expected, label in checks:
        try:
            report[label + 'Unchanged'] = expected is not None and sha(ROOT / filename) == expected
            if not report[label + 'Unchanged']: integrity_errors.append(label + '_changed')
        except Exception as error:
            report[label + 'Unchanged'] = False; integrity_errors.append(label + ': ' + str(error))
    report['integrityErrors'] = integrity_errors
    report['passed'] = report['passed'] and report['containerRemoved'] and not integrity_errors
    persist(); print(json.dumps(report), flush=True)
raise SystemExit(0 if report['passed'] else 1)
