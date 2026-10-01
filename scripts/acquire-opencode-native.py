#!/usr/bin/env python3
"""Extract a pinned official native npm binary as data, without install hooks."""
import argparse
import base64
import hashlib
import json
import pathlib
import shutil
import tarfile
import tempfile
import urllib.request


def native_pin(version, os_name, arch):
    pins = json.loads(pathlib.Path(__file__).with_name('opencode-native-pins.json').read_text())['pins']
    return next(p for p in pins if (p['version'], p['os'], p['arch']) == (version, os_name, arch))


def acquire(pin, output):
    if output.exists():
        raise ValueError('native output already exists')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='TEST-native-acquisition-') as name:
        archive = pathlib.Path(name) / 'native.tgz'
        digest = hashlib.sha512()
        total = 0
        with urllib.request.urlopen(pin['url'], timeout=60) as source, archive.open('wb') as target:
            while chunk := source.read(1024 * 1024):
                total += len(chunk)
                if total > 256 * 1024 * 1024:
                    raise ValueError('native archive exceeds bound')
                digest.update(chunk)
                target.write(chunk)
        if 'sha512-' + base64.b64encode(digest.digest()).decode() != pin['integrity']:
            raise ValueError('native archive integrity mismatch')
        with tarfile.open(archive, 'r:gz') as bundle:
            members = [m for m in bundle.getmembers() if m.name == pin['member']]
            if len(members) != 1 or not members[0].isfile() or members[0].size > 256 * 1024 * 1024:
                raise ValueError('native archive has no unique regular binary')
            with bundle.extractfile(members[0]) as source, output.open('xb') as target:
                shutil.copyfileobj(source, target, 1024 * 1024)
        if hashlib.sha256(output.read_bytes()).hexdigest() != pin['binary_sha256']:
            output.unlink()
            raise ValueError('extracted native binary hash mismatch')
        output.chmod(0o755)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--os', required=True)
    parser.add_argument('--arch', required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    pin = native_pin(args.version, args.os, args.arch)
    acquire(pin, args.output)
    print(json.dumps(pin, sort_keys=True))
