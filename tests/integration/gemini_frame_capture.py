"""Private Linux TEST capture custody; no consumer, lease or delivery effects."""
import contextlib
import os
from pathlib import Path
import re
import stat
import sys
import time

ROOT = ".private-native-frames"
LIMIT = 1024 * 1024
SLOTS = 64
EVENTS = ("AfterAgent", "Notification")


def require(ok):
    if not ok:
        raise ValueError("TEST_capture_unavailable")


def identity(s):
    return s.st_dev, s.st_ino, s.st_uid, s.st_mode


def fingerprint(s):
    return identity(s), s.st_nlink, s.st_size, s.st_mtime_ns, s.st_ctime_ns


def directory(s):
    require(stat.S_ISDIR(s.st_mode) and s.st_uid == os.getuid() and stat.S_IMODE(s.st_mode) == 0o700)


def regular(s):
    require(stat.S_ISREG(s.st_mode) and s.st_uid == os.getuid() and s.st_nlink == 1 and stat.S_IMODE(s.st_mode) == 0o600)


def prepare(lab):
    require(sys.platform == "linux" and (lab / ".an-gemini-TEST").is_file())
    directory(lab.stat())
    (lab / ROOT).mkdir(mode=0o700)


@contextlib.contextmanager
def custody(lab, exclusive=False):
    import fcntl
    path = lab / ROOT
    root = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    lock = None
    try:
        directory(os.fstat(root))
        lock = os.open(".lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=root)
        regular(os.fstat(lock))
        until = time.monotonic() + 1
        while True:
            try:
                fcntl.flock(lock, (fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH) | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                require(time.monotonic() < until)
                time.sleep(0.005)
        # A waiter on the old lock cannot write after the root has been removed
        # or replaced, even if it opened both descriptors before cleanup.
        require(identity(path.lstat()) == identity(os.fstat(root)))
        yield root
    finally:
        if lock is not None:
            os.close(lock)
        os.close(root)


def capture(lab, event, raw):
    if event not in EVENTS or not isinstance(raw, bytes) or len(raw) > LIMIT:
        return "unavailable"
    try:
        with custody(lab) as root:
            if ".sealed" in os.listdir(root):
                return "sealed"
            slot = None
            for n in range(SLOTS):
                name = ".slot-%02d" % n
                try:
                    os.mkdir(name, mode=0o700, dir_fd=root)
                    slot = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=root)
                    break
                except FileExistsError:
                    continue
            if slot is None:
                return "full"
            try:
                fd = os.open(".pending", os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=slot)
                try:
                    remaining = memoryview(raw)
                    while remaining:
                        n = os.write(fd, remaining)
                        require(n > 0)
                        remaining = remaining[n:]
                finally:
                    os.close(fd)
                os.rename(".pending", event + ".frame", src_dir_fd=slot, dst_dir_fd=slot)
            finally:
                os.close(slot)
            return "recorded"
    except Exception:
        return "unavailable"  # Never expose raw frame, path or filesystem error.


def finish(lab, classify, collection_known):
    """Drain writes and remove raw files, retaining monotonic sealed custody."""
    result = {"class": "unavailable", "cleanup": "unverified", "rows": []}
    try:
        with custody(lab, exclusive=True) as root:
            try:
                fd = os.open(".sealed", os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=root)
                os.close(fd)
            except FileExistsError:
                sealed = os.stat(".sealed", dir_fd=root, follow_symlinks=False)
                regular(sealed)
                require(sealed.st_size == 0)
            if not collection_known:
                result["class"] = "sealed_collection_unverified"
                return result
            names = os.listdir(root)
            require(len(names) <= SLOTS + 2 and all(n in (".lock", ".sealed") or re.fullmatch(r"\.slot-[0-9]{2}", n) and int(n[-2:]) < SLOTS for n in names))
            # Validate the whole custody set before removal; no broad rmtree.
            files, incomplete = [], 0
            for name in sorted(n for n in names if n.startswith(".slot-")):
                slot = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=root)
                try:
                    directory(os.fstat(slot))
                    entries = os.listdir(slot)
                    require(len(entries) <= 1 and (not entries or entries[0] in (".pending", *(e + ".frame" for e in EVENTS))))
                    if not entries:
                        incomplete += 1
                        files.append((name, None))
                        continue
                    file = entries[0]
                    fd = os.open(file, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=slot)
                    try:
                        before = os.fstat(fd)
                        regular(before)
                        require(before.st_size <= LIMIT)
                        data = bytearray()
                        while len(data) <= LIMIT:
                            part = os.read(fd, min(65536, LIMIT + 1 - len(data)))
                            if not part:
                                break
                            data.extend(part)
                        after = os.fstat(fd)
                        require(len(data) <= LIMIT and fingerprint(before) == fingerprint(after) and fingerprint(os.stat(file, dir_fd=slot, follow_symlinks=False)) == fingerprint(after))
                    finally:
                        os.close(fd)
                    # Classifier must return only its bounded public projection.
                    if file == ".pending":
                        incomplete += 1
                    else:
                        result["rows"].append(classify(file[:-6], bytes(data)))
                    files.append((name, file))
                finally:
                    os.close(slot)
            for name, file in files:
                slot = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=root)
                try:
                    if file is not None:
                        os.unlink(file, dir_fd=slot)
                finally:
                    os.close(slot)
                os.rmdir(name, dir_fd=root)
            # Keep both inodes: a late opener must share this lock and seal.
            # Terminal collection does not imply collection of all descendants.
            require(identity((lab / ROOT).lstat()) == identity(os.fstat(root)))
            result.update({"class": "partial_capture" if incomplete else "classified", "incomplete": incomplete, "cleanup": "raw_frames_removed_sealed_custody_retained"})
    except Exception:
        pass
    return result
