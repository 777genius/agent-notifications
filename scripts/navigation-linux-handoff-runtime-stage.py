#!/usr/bin/python3
"""Bounded, authenticated runtime staging for the offline TEST seed factory."""
import hashlib
import io
from pathlib import Path
import tarfile

LIMIT = 64 * 1024 * 1024
QUALIFIED = {
    'portal': ('3a80227876fc6426bcf64658a18e0235f970daadc6c13923d4f7a6e78c00bc2c',
               'libexec/xdg-desktop-portal',
               '7fe62c1a938985b8ca4ec335a768ad36f1d17abe027098624fa4f5989c0b4594'),
    'gtk': ('68e165b67639cf8c77ed7edda63210c5ebfd0aa532f52e8e56d8fc21d1d5b35e',
            'libexec/xdg-desktop-portal-gtk',
            'b95c473ae8fe4e3b51e7ca4bf27d4f4719786d552524443e246d1b40468d40af'),
}


def validated_tree(data, archive_sha, executable, executable_sha):
    """Validate the complete archive before emitting any filesystem entries."""
    if len(data) > LIMIT or hashlib.sha256(data).hexdigest() != archive_sha:
        raise RuntimeError('qualified_archive_bytes_required')
    entries = {}
    total = 0
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:') as archive:
        for member in archive:
            if len(entries) >= 4096:
                raise RuntimeError('archive_entry_bound')
            if not member.isdir() and not member.isreg():
                raise RuntimeError('regular_files_and_directories_only')
            name = member.name
            if name.startswith('./'): name = name[2:]
            if name == '.': name = ''
            if name and ('\\' in name or any(part in ('', '.', '..') for part in name.split('/'))):
                raise RuntimeError('canonical_relative_archive_path_required')
            if name in entries or (not name and not member.isdir()):
                raise RuntimeError('duplicate_or_invalid_archive_root')
            if member.size < 0 or member.size > 16 * 1024 * 1024 or (member.isdir() and member.size):
                raise RuntimeError('archive_member_size_bound')
            total += member.size
            if total > LIMIT:
                raise RuntimeError('archive_unpacked_size_bound')
            content = None
            if member.isreg():
                with archive.extractfile(member) as stream:
                    content = stream.read(member.size + 1)
                if len(content) != member.size:
                    raise RuntimeError('complete_archive_member_required')
            entries[name] = (content, bool(member.mode & 0o111))
    for name in entries:
        parts = name.split('/')
        for depth in range(1, len(parts)):
            ancestor = '/'.join(parts[:depth])
            if ancestor in entries and entries[ancestor][0] is not None:
                raise RuntimeError('file_cannot_be_path_ancestor')
    if executable not in entries or entries[executable][0] is None or not entries[executable][1]:
        raise RuntimeError('qualified_executable_required')
    if hashlib.sha256(entries[executable][0]).hexdigest() != executable_sha:
        raise RuntimeError('qualified_executable_bytes_required')
    return entries


def stage_runtime_archives(archive_root, destination):
    """Create a fresh tree; a failure leaves partial staging for the owning factory.

    No extraction API, subprocess, runtime load, guest boot or native effect. The
    caller owns a private TEST parent and must never publish a failed staging tree.
    """
    archive_root, destination = Path(archive_root), Path(destination)
    trees = {}
    for name, (digest, executable, executable_sha) in QUALIFIED.items():
        with (archive_root / (name + '.tar')).open('rb') as stream:
            data = stream.read(LIMIT + 1)
        trees[name] = validated_tree(data, digest, executable, executable_sha)
    destination.mkdir(mode=0o700)  # Exclusive admission; never reuse an old tree.
    hashes = {}
    for name, entries in trees.items():
        root = destination / name
        root.mkdir(mode=0o700)
        for relative, (content, executable) in sorted(entries.items(), key=lambda item: (item[0].count('/'), item[0])):
            if not relative: continue
            path = root / relative
            path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            if content is None:
                path.mkdir(mode=0o700, exist_ok=True)
            else:
                with path.open('xb') as output:
                    output.write(content)
                path.chmod(0o555 if executable else 0o444)
                hashes[name + '/' + relative] = hashlib.sha256(content).hexdigest()
        # Lock directories only after their children have been written.
        for path in sorted(root.rglob('*'), key=lambda p: len(p.parts), reverse=True):
            if path.is_dir(): path.chmod(0o555)
        root.chmod(0o555)
    return hashes
