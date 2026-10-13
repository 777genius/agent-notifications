#!/usr/bin/python3
"""Provision only the disposable TEST guest; never start a client or notifier."""
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess

SEED = Path('/mnt/navigation-test-seed')
ROOT = Path('/var/lib/navigation-client-TEST')
PACKAGE_SHA = '637c3c94bc50f8ee33a15e2e28ec7f92a787f0943e700efe111bc0bf0d4813b4'
PROFILE_SHA = '05be1a8336a80236f4b56798ea7a75accb55f51438afa1e7cb5e94bfa326b1b7'


def sha(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def main():
    if os.getuid() != 0 or Path('/.dockerenv').exists():
        raise RuntimeError('guest_root_required_not_host_container')
    # A seed is presented as a read-only virtual CD, never a host shared folder.
    mounts = Path('/proc/mounts').read_text().splitlines()
    if not any(row.split()[1:3] == [str(SEED), 'iso9660'] and
               'ro' in row.split()[3].split(',') for row in mounts):
        raise RuntimeError('read_only_TEST_seed_required')
    if (SEED / 'navigation.marker').read_text() != 'Linux client provisioning TEST only\n':
        raise RuntimeError('owned_TEST_seed_required')
    ROOT.mkdir(mode=0o700, exist_ok=False)
    report = dict(scope='disposable_guest_provisioning_only', passed=False,
                  clientLaunchAttempted=False, notificationAttempted=False,
                  clientSandboxQualified=False, navigationQualified=False,
                  sourceSHA256=sha(Path(__file__)), commands=[])

    def command(label, argv, timeout=600):
        out_path, err_path = ROOT / (label + '.stdout'), ROOT / (label + '.stderr')
        with out_path.open('xb') as out, err_path.open('xb') as err:
            try:
                result = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=out,
                    stderr=err, timeout=timeout, env={'PATH': '/usr/sbin:/usr/bin:/sbin:/bin',
                    'HOME': '/root', 'LANG': 'C.UTF-8', 'DEBIAN_FRONTEND': 'noninteractive'})
                item = dict(label=label, argv=argv, exitCode=result.returncode)
            except subprocess.TimeoutExpired:
                item = dict(label=label, argv=argv, timeout=True)
        item['stdoutSHA256'], item['stderrSHA256'] = sha(out_path), sha(err_path)
        report['commands'].append(item)
        if item.get('exitCode') != 0:
            raise RuntimeError(label + '_failed')
        return out_path.read_bytes()

    try:
        report['packageSHA256'] = sha(SEED / 'chatgpt.deb')
        if report['packageSHA256'] != PACKAGE_SHA:
            raise RuntimeError('selected_package_mismatch')
        command('apt_update', ['apt-get', '-o', 'APT::Update::Error-Mode=any', 'update'])
        packages = ['sway', 'mako-notifier', 'xdg-desktop-portal', 'xdg-desktop-portal-gtk',
                    'python3-gi', 'gir1.2-gtk-3.0', 'dbus-x11', 'build-essential',
                    'libwayland-dev', 'wayland-protocols', 'apparmor', 'apparmor-utils']
        command('candidate_policy', ['apt-cache', 'policy', *packages])
        command('guest_install', ['apt-get', '-y', '--no-install-recommends', 'install',
                                 *packages, str(SEED / 'chatgpt.deb')], 1200)
        report['installedPackage'] = command('selected_version',
            ['dpkg-query', '-W', '-f=${Package} ${Version} ${Architecture}\n', 'chatgpt']).decode().strip()
        if report['installedPackage'] != 'chatgpt 26.930.51102 amd64':
            raise RuntimeError('installed_version_mismatch')
        profile = Path('/etc/apparmor.d/chatgpt')
        report['profileSHA256'] = sha(profile)
        if report['profileSHA256'] != PROFILE_SHA or Path('/etc/apparmor.d/local/chatgpt').exists():
            raise RuntimeError('selected_AppArmor_policy_mismatch')
        command('load_vendor_profile', ['apparmor_parser', '-r', str(profile)])
        profiles = Path('/sys/kernel/security/apparmor/profiles').read_text()
        (ROOT / 'loaded-profiles.txt').write_text(profiles)
        if not any(line.split(' (', 1)[0] == 'chatgpt' for line in profiles.splitlines()):
            raise RuntimeError('selected_AppArmor_profile_not_loaded')
        report['selectedVendorProfileLoaded'] = True
        report['executableSHA256'] = sha(Path('/usr/lib/chatgpt/ChatGPT'))
        report['launcherSHA256'] = sha(Path('/usr/lib/chatgpt/codex-launcher'))
        command('packages', ['dpkg-query', '-W'])
        command('sync_before_completion', ['sync'], 20)
        report['guestSyncSucceeded'] = True
        report['passed'] = True
    except Exception as error:
        report['failure'] = type(error).__name__ + ': ' + str(error)
    finally:
        report['artifacts'] = {p.name: {'bytes': p.stat().st_size, 'sha256': sha(p)}
                               for p in ROOT.iterdir() if p.is_file()}
        data = json.dumps(report, sort_keys=True).encode()
        (ROOT / 'provision-result.json').write_bytes(data)
        # The controller validates framing/hash and collected QEMU exit separately.
        frame = 'NAVIGATION_TEST_PROVISION_V1 ' + hashlib.sha256(data).hexdigest() + ' ' + base64.b64encode(data).decode() + '\n'
        with Path('/dev/ttyS0').open('w') as serial:
            serial.write(frame); serial.flush()
        # Controller requires the independent QMP guest-shutdown event, not this
        # record alone, as evidence that the guest actually powered off.
        subprocess.run(['systemctl', 'poweroff'], check=True, timeout=20)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
