"""Stage SHA-bound external parent evidence AFTER exact checkout/build; no grants.
Importing this module is inert. Execution belongs to branch CI/root after review.
"""
import importlib.util
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tarfile
import urllib.parse
import urllib.request

repo = pathlib.Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location('native_inputs', repo / 'scripts/opencode-native-e2e.py')
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)
artifacts = repo / '.task-tools/artifacts'


def stage_parent_archive(archive, expected_sha256, target):
    """Unpack only regular evidence files into a fresh artifact directory outside tracked source.
    SDK tar/lock and embedded bytes remain in the exact source checkout; the parent
    archive must not carry replacement production source or another SDK copy.
    """
    r.require(re.fullmatch('[0-9a-f]{64}', str(expected_sha256)) is not None and
              r.digest(archive) == expected_sha256, 'parent_archive_hash_mismatch')
    target = pathlib.Path(target).absolute()
    r.require(target.resolve().is_relative_to(artifacts), 'parent_evidence_path_escape')
    r.require(not any(p.is_symlink() for p in (target, *target.parents)), 'parent_evidence_path_escape')
    with tarfile.open(archive, 'r:gz') as packed:
        members = packed.getmembers()
        names, total = set(), 0
        r.require(0 < len(members) <= 4096, 'parent_evidence_file_bound')
        for member in members:
            path = pathlib.PurePosixPath(member.name)
            r.require(not path.is_absolute() and all(re.fullmatch('[A-Za-z0-9._-]+', p)
                      and p not in ('.', '..') for p in path.parts)
                      and member.name not in names and (member.isfile() or member.isdir()), 'parent_evidence_member_invalid')
            # Exact SDK bytes come only from the existing npm-generated source lock.
            host_archives = {f'inputs/{o}-{a}-{v}/package.tgz' for o,a,v in r.CELLS}
            r.require(not member.name.endswith(('.tgz', '.tar.gz')) or member.name in host_archives, 'parent_sdk_copy_forbidden')
            names.add(member.name)
            total += member.size
        r.require(total <= 2 * 1024**3 and 'manifest.json' in names, 'parent_evidence_size_or_manifest')
        target.mkdir(mode=0o700)  # Never reuse/overwrite a prior evidence root.
        for member in members:
            path = target / member.name
            if member.isdir():
                path.mkdir(mode=0o700, parents=True, exist_ok=True)
            else:
                path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                with packed.extractfile(member) as source, path.open('xb') as out:
                    shutil.copyfileobj(source, out, 1024 * 1024)
                path.chmod(0o700 if member.mode & 0o111 else 0o600)
    manifest = target / 'manifest.json'
    return manifest, r.digest(manifest)


def main():
    report = artifacts / 'native-report.json'
    artifacts.mkdir(parents=True, exist_ok=True)
    try:
        if '--stage-evidence' in sys.argv:
            url = urllib.parse.urlsplit(os.environ.get('AN_EVIDENCE_URL', ''))
            sha = os.environ.get('AN_EVIDENCE_SHA256', '')
            r.require(url.scheme == 'https' and url.hostname and not url.username and not url.password
                      and not url.query and not url.fragment and re.fullmatch('[0-9a-f]{64}', sha), 'external_parent_evidence_required')
            archive = artifacts / 'parent-inputs.tar.gz'
            with urllib.request.urlopen(url.geturl(), timeout=30) as source, archive.open('xb') as out:
                total = 0
                while block := source.read(1024 * 1024):
                    total += len(block)
                    r.require(total <= 2 * 1024**3, 'parent_archive_size')
                    out.write(block)
            manifest, manifest_sha = stage_parent_archive(archive, sha, artifacts/'parent-inputs')
            # Outputs contain only fixed repository path/hex digest, never source URL.
            with pathlib.Path(os.environ['GITHUB_OUTPUT']).open('a') as outputs:
                outputs.write('manifest=' + str(manifest.relative_to(repo)) + '\nsha256=' + manifest_sha + '\n')
            return 0
        manifest = repo / os.environ['AN_MANIFEST']
        sha = os.environ['AN_MANIFEST_SHA256']
        m, cell, files, binary, archive = r.load_manifest(
            manifest, os.environ['AN_OS'], os.environ['AN_ARCH'], os.environ['AN_VERSION'], sha)
        tracked = subprocess.check_output(['git', 'ls-files', '--', str(manifest.resolve().relative_to(repo))], text=True)
        r.require(not tracked, 'external_parent_manifest_required')
        checkout = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
        dirty = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], text=True)
        dirty += subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '--', '.',
                                          ':(exclude).task-tools/artifacts'], text=True)
        build = subprocess.check_output(['go', 'version', '-m', str(binary)], text=True)
        r.verify_source_binding(m, checkout, build, dirty)
        receiver_record = artifacts / 'receiver-record.json'
        if '--execute' in sys.argv:
            argv = [sys.executable, '-B', str(repo/'scripts/opencode-native-e2e.py'),
                    '--manifest', str(manifest), '--manifest-sha256', sha, '--binary', str(binary),
                    '--archive', str(archive), '--os', cell['os'], '--arch', cell['arch'],
                    '--version', cell['version'], '--suite', os.environ['AN_SUITE'], '--report', str(report)]
            if os.environ['AN_SUITE'] == 'business':
                proof = manifest.parent / ('business-' + '-'.join(cell[key] for key in ('os','arch','version')) + '.json')
                r.require(proof.is_file() and not proof.is_symlink(), 'sealed_business_prerequisites_required')
                argv += ['--business-proof',str(proof),'--business-proof-sha256',r.digest(proof)]
            if cell['os'] == 'linux':
                argv += ['--receiver-record', str(receiver_record)]
            return subprocess.call(argv)
        if cell['os'] == 'linux':
            receiver = artifacts / 'TEST-dbus-receiver'
            subprocess.run(['go', 'build', '-o', str(receiver), str(pathlib.Path(__file__).with_name('receiver.go'))], check=True)
            r.write_json(receiver_record, {'path': str(receiver.relative_to(repo)), 'sha256': r.digest(receiver)})
        return 0
    except Exception as error:
        r.write_json(report, {'status': 'unqualified', 'firstFailedPrerequisite': str(error) if isinstance(error, r.Unqualified)
                     else type(error).__name__, 'businessPhasesStarted': False})
        return 1


if __name__ == '__main__':
    sys.exit(main())
