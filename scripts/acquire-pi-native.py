#!/usr/bin/env python3
"""Acquire an exact official Pi standalone archive; never resolve latest/npm."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import shutil
import stat
import tarfile
import urllib.request
import zipfile

PINS = Path(__file__).with_name("testdata") / "pi-native/pins.json"


def require(value, message):
    if not value:
        raise ValueError(message)


def sha(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        while block := stream.read(65536):
            digest.update(block)
    return digest.hexdigest()


def verified_archive(path, pin):
    require(path.is_file() and not path.is_symlink(), "archive must be a real regular file")
    require(path.stat().st_size == pin["bytes"], "Pi archive size differs from immutable pin")
    require(sha(path) == pin["sha256"], "Pi archive SHA-256 differs from immutable pin")


def contained_member(name):
    path = PurePosixPath(name)
    require("\\" not in name and not path.is_absolute() and path.parts and path.parts[0] == "pi", "noncontained Pi archive member")
    require(all(part not in ("..", ".", "") and ":" not in part for part in path.parts), "unsafe Pi archive member")
    return path


def extract(archive, output):
    require(not output.exists(), "Pi output must be fresh")
    output.mkdir(parents=True, mode=0o700)
    total, names = 0, set()

    def copy(name, size, mode, directory, source):
        nonlocal total
        relative = contained_member(name)
        require(str(relative) not in names and len(names) < 4096, "duplicate or excessive Pi members")
        names.add(str(relative))
        total += size
        require(0 <= size <= 300 * 1024 * 1024 and total <= 500 * 1024 * 1024, "Pi extraction bound exceeded")
        destination = output.joinpath(*relative.parts)
        if directory:
            destination.mkdir(parents=True, exist_ok=True, mode=0o700)
            return
        destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        with destination.open("xb") as target:
            shutil.copyfileobj(source, target, length=65536)
        require(destination.stat().st_size == size, "Pi extracted member size changed")
        destination.chmod(0o700 if mode & 0o111 or destination.name in ("pi", "pi.exe") else 0o600)

    try:
        if archive.name.endswith(".zip"):
            with zipfile.ZipFile(archive) as bundle:
                for member in bundle.infolist():
                    mode = member.external_attr >> 16
                    require(not stat.S_ISLNK(mode), "Pi archive symlink refused")
                    with bundle.open(member) as source:
                        copy(member.filename.rstrip("/"), member.file_size, mode, member.is_dir(), source)
        else:
            with tarfile.open(archive, "r:gz") as bundle:
                for member in bundle:
                    require(member.isfile() or member.isdir(), "nonregular Pi archive member refused")
                    source = bundle.extractfile(member) if member.isfile() else None
                    try:
                        copy(member.name.rstrip("/"), member.size, member.mode, member.isdir(), source)
                    finally:
                        if source:
                            source.close()
    except Exception:
        shutil.rmtree(output)
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("linux", "darwin", "windows"), required=True)
    parser.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--archive", type=Path)
    source.add_argument("--download", action="store_true")
    args = parser.parse_args()
    pins = json.loads(PINS.read_text())
    pin = pins["targets"][args.platform + "/" + args.arch]
    output = args.output.absolute()
    archive = args.archive.resolve(strict=True) if args.archive else output.parent / pin["archive"]
    if args.download:
        require(not archive.exists(), "Pi download destination already exists")
        archive.parent.mkdir(parents=True, exist_ok=True)
        try:
            with urllib.request.urlopen(pins["download_base"] + pin["archive"], timeout=90) as response, archive.open("xb") as stream:
                remaining = pin["bytes"]
                while block := response.read(min(65536, remaining + 1)):
                    remaining -= len(block)
                    require(remaining >= 0, "Pi download exceeds immutable size")
                    stream.write(block)
        except Exception:
            archive.unlink(missing_ok=True)
            raise
    verified_archive(archive, pin)
    extract(archive, output)
    executable = output / "pi" / ("pi.exe" if args.platform == "windows" else "pi")
    require(executable.is_file() and not executable.is_symlink(), "Pi physical executable missing")
    report = {"schema_version": 1, "version": pins["version"], "source_commit": pins["source_commit"],
              "platform": args.platform, "architecture": args.arch, "archive_name": pin["archive"],
              "archive_sha256": pin["sha256"], "binary_sha256": sha(executable), "executable": str(executable.resolve())}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report))


if __name__ == "__main__":
    main()
