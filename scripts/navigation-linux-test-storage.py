"""Narrow TEST-only scratch custody and exclusive frozen-input staging; no subprocesses."""
import hashlib, os, pathlib, re, stat, time


def identity(info):
    return dict(device=info.st_dev, inode=info.st_ino, size=info.st_size, uid=info.st_uid,
                mode=stat.S_IMODE(info.st_mode), links=info.st_nlink,
                modifiedNS=info.st_mtime_ns, changedNS=info.st_ctime_ns)


def digest_fd(fd, size, deadline):
    digest = hashlib.sha256(); offset = 0
    while offset < size:
        if time.monotonic() >= deadline: raise RuntimeError('TEST_storage_deadline')
        block = os.pread(fd, min(1048576, size - offset), offset)
        if not block: raise RuntimeError('TEST_source_truncated')
        digest.update(block); offset += len(block)
    if os.pread(fd, 1, size): raise RuntimeError('TEST_source_grew')
    return digest.hexdigest()


def require_scratch_device(info):
    if info.st_dev != 66305 or (os.major(info.st_dev), os.minor(info.st_dev)) != (259, 1):
        raise RuntimeError('TEST_scratch_device_required')


class Scratch:
    def __init__(self, path):
        self.path = pathlib.Path(path)
        if self.path.parent != pathlib.Path('/srv/workers') or not re.fullmatch(r'navigation-handoff-stage-TEST-[0-9a-f]{32}', self.path.name):
            raise RuntimeError('exact_job_owned_TEST_scratch_required')
        self.parent_fd = os.open('/srv/workers', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            self.fd = os.open(self.path.name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=self.parent_fd)
            self.initial = identity(os.fstat(self.fd)); self.prove()
        except BaseException:
            if hasattr(self, 'fd'): os.close(self.fd)
            os.close(self.parent_fd); raise

    def close(self): os.close(self.fd); os.close(self.parent_fd)

    def prove(self, needed=0):
        parent = pathlib.Path('/srv/workers'); held_parent = os.fstat(self.parent_fd); named_parent = parent.lstat()
        require_scratch_device(held_parent)
        if parent.resolve(strict=True) != parent or not stat.S_ISDIR(named_parent.st_mode) or held_parent.st_uid != 0 or stat.S_IMODE(held_parent.st_mode) != 0o755 or (held_parent.st_dev, held_parent.st_ino) != (named_parent.st_dev, named_parent.st_ino):
            raise RuntimeError('TEST_scratch_parent_identity_owner_required')
        info = os.fstat(self.fd); require_scratch_device(info)
        if os.getuid() != 1000 or self.path.resolve(strict=True) != self.path or self.path.lstat().st_ino != info.st_ino or self.path.lstat().st_dev != info.st_dev or info.st_uid != 1000 or stat.S_IMODE(info.st_mode) != 0o700 or (os.major(info.st_dev), os.minor(info.st_dev)) != (259, 1) or info.st_ino != self.initial['inode']:
            raise RuntimeError('TEST_scratch_identity_device_owner_changed')
        rows = [line.split() for line in pathlib.Path('/proc/self/mountinfo').read_text().splitlines()]
        matches = [row for row in rows if row[4] == '/srv/workers']
        if len(matches) != 1: raise RuntimeError('TEST_scratch_mount_required')
        row = matches[0]; separator = row.index('-')
        if row[2] != '259:1' or row[3] != '/' or row[separator + 1:separator + 3] != ['ext4', '/dev/nvme2n1p1'] or 'rw' not in row[5].split(','):
            raise RuntimeError('TEST_scratch_mount_device_required')
        space = os.fstatvfs(self.fd)
        if space.f_bavail * space.f_frsize < needed: raise RuntimeError('TEST_scratch_actual_space_required')
        return dict(**identity(info), mount='/srv/workers', filesystem='ext4', blockDevice='/dev/nvme2n1p1', available=space.f_bavail * space.f_frsize)

    def copy(self, source, name, expected, size, reserve=2 * 1024**3, readonly_required=True):
        if pathlib.Path(name).name != name or name in ('', '.', '..'): raise RuntimeError('TEST_destination_leaf_required')
        source = pathlib.Path(source)
        if source.resolve(strict=True) != source: raise RuntimeError('TEST_source_redirected')
        self.prove(size + reserve); deadline = time.monotonic() + 180
        inp = os.open(source, os.O_RDONLY | os.O_NOFOLLOW); out = None
        try:
            before = os.fstat(inp)
            if not stat.S_ISREG(before.st_mode) or before.st_uid != 1000 or (stat.S_IMODE(before.st_mode) != 0o444 if readonly_required else stat.S_IMODE(before.st_mode) not in (0o444, 0o664) or size >= 64 * 1024**2) or before.st_size != size or not 0 < size <= 4 * 1024**3 or digest_fd(inp, size, deadline) != expected:
                raise RuntimeError('bounded_readonly_frozen_TEST_source_required')
            self.prove(size + reserve)
            out = os.open(name, os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=self.fd)
            offset = 0
            while offset < size:
                if time.monotonic() >= deadline: raise RuntimeError('TEST_copy_deadline')
                block = os.pread(inp, min(1048576, size - offset), offset)
                if not block: raise RuntimeError('TEST_copy_source_truncated')
                if not block.strip(b'\0'): os.lseek(out, len(block), os.SEEK_CUR)  # Preserve holes without hardlink/reflink sharing.
                else:
                    pending = memoryview(block)
                    while pending:
                        written = os.write(out, pending)
                        if written <= 0: raise RuntimeError('TEST_copy_short_write')
                        pending = pending[written:]
                offset += len(block)
            os.ftruncate(out, size); os.fchmod(out, 0o444); os.fsync(out)
            source_after_hash = digest_fd(inp, size, deadline); destination_hash = digest_fd(out, size, deadline)
            after = os.fstat(inp); destination = os.fstat(out)
            if identity(before) != identity(after) or identity(source.lstat()) != identity(before) or source_after_hash != expected or destination_hash != expected or destination.st_uid != 1000 or stat.S_IMODE(destination.st_mode) != 0o444 or destination.st_nlink != 1 or destination.st_dev != os.fstat(self.fd).st_dev or identity(os.stat(name, dir_fd=self.fd, follow_symlinks=False)) != identity(destination):
                raise RuntimeError('TEST_copy_source_or_destination_changed')
            os.fsync(self.fd); self.prove()
            return dict(source=str(source), sourceBefore=identity(before), sourceAfter=identity(after), sourceSHA256=expected,
                        destination=name, destinationIdentity=identity(destination), destinationSHA256=expected, sparse=True, collected=True)
        finally:
            if out is not None: os.close(out)
            os.close(inp)
