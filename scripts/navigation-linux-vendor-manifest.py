#!/usr/bin/env python3
"""Read authenticated Debian payload bytes only; never extract or execute them."""
import argparse
import hashlib
import io
import json
import lzma
import os
import pathlib
import posixpath
import re
import signal
import stat
import tarfile
import uuid

PACKAGE_SHA = '637c3c94bc50f8ee33a15e2e28ec7f92a787f0943e700efe111bc0bf0d4813b4'
PACKAGE_SIZE = 476322694
# Largest file (resources/app.asar) in the SHA256-pinned 26.930.51102 package.
PACKAGE_MAX_FILE_SIZE = 543408877
URL = 'https://persistent.oaistatic.com/codex-app-prod/linux/deb/pool/main/c/chatgpt/chatgpt_26.930.51102_amd64.deb'
ROOT = '/usr/lib/chatgpt'
PINS = {ROOT + '/codex-launcher': '8f983245c6c07070e2cdc480be50ec239e0f18ee36069126649d0692595c86ad',
        ROOT + '/ChatGPT': '207c4fbff7e2fcc1b0789448351ac6eed206206d94c5a0835e5f07c7cd73d6e3'}


def require(value, reason):
    if not value:
        raise ValueError(reason)


def digest(stream):
    h = hashlib.sha256()
    for block in iter(lambda: stream.read(1024 * 1024), b''):
        h.update(block)
    return h.hexdigest()


class Slice(io.RawIOBase):
    def __init__(self, stream, size):
        self.stream, self.remaining = stream, size

    def readable(self):
        return True

    def read(self, size=-1):
        size = self.remaining if size < 0 else min(size, self.remaining)
        result = self.stream.read(size)
        require(len(result) == size, 'truncated_ar_member')
        self.remaining -= len(result)
        return result


class BoundedTarXZ(io.RawIOBase):
    """Bound XZ memory/output and tar declarations before tarfile sees them."""
    def __init__(self, compressed, limit=4 * 1024**3 + 32 * 1024**2, memlimit=128 * 1024**2):
        self.compressed = compressed
        self.decoder = lzma.LZMADecompressor(format=lzma.FORMAT_XZ, memlimit=memlimit)
        self.limit, self.total = limit, 0
        self.buffer = bytearray()
        self.header = bytearray()
        self.body = self.count = 0
        self.ended = False
        self.root_seen = False
        self.metadata = None
        self.metadata_left = 0

    def readable(self):
        return True

    def guard(self, data):
        self.total += len(data)
        require(self.total <= self.limit, 'decoded_tar_bound')
        cursor = 0
        while cursor < len(data):
            if self.body:
                step = min(self.body, len(data) - cursor)
                if self.metadata is not None:
                    count = min(self.metadata_left, step)
                    self.metadata.extend(data[cursor:cursor + count])
                    self.metadata_left -= count
                cursor += step
                self.body -= step
                if self.body == 0 and self.metadata is not None:
                    self.validate_pax(bytes(self.metadata))
                    self.metadata = None
                continue
            step = min(512 - len(self.header), len(data) - cursor)
            self.header.extend(data[cursor:cursor + step])
            cursor += step
            if len(self.header) != 512:
                continue
            header = bytes(self.header)
            self.header.clear()
            if not any(header):
                self.ended = True
                continue
            require(not self.ended, 'tar_data_after_end')
            self.count += 1
            require(self.count <= 20000, 'tar_header_count_bound')
            if header[156:157] == b'5' and header[:100].split(b'\0', 1)[0] in (b'.', b'./'):
                require(not self.root_seen, 'duplicate_tar_root')
                self.root_seen = True
            field = header[124:136].strip(b'\0 ')
            require(field and all(c in b'01234567' for c in field), 'tar_size_encoding')
            size = int(field, 8)
            kind = header[156:157]
            require(kind in (b'0', b'\0', b'5', b'2', b'x', b'g', b'L', b'K'), 'unsupported_tar_type')
            metadata = kind in (b'x', b'g', b'L', b'K')
            require(size <= (65536 if metadata else PACKAGE_MAX_FILE_SIZE), 'tar_declared_size_bound')
            self.body = (size + 511) // 512 * 512
            if kind in (b'x', b'g') and size:
                self.metadata, self.metadata_left = bytearray(), size

    @staticmethod
    def validate_pax(payload):
        cursor = 0
        while cursor < len(payload):
            space = payload.find(b' ', cursor)
            require(space > cursor and payload[cursor:space].isdigit(), 'pax_record_length')
            length = int(payload[cursor:space])
            end = cursor + length
            require(end <= len(payload) and end > space + 1 and payload[end - 1:end] == b'\n', 'pax_record_bound')
            record = payload[space + 1:end - 1]
            require(b'=' in record, 'pax_record_key')
            key = record.split(b'=', 1)[0]
            require(key != b'size' and not key.startswith(b'GNU.sparse'), 'pax_layout_override')
            cursor = end

    def read(self, size=-1):
        require(0 <= size <= 1024 * 1024, 'decoder_read_bound')
        while len(self.buffer) < size and not self.decoder.eof:
            chunk = self.compressed.read(min(self.compressed.remaining, 65536)) if self.decoder.needs_input else b''
            require(chunk or not self.decoder.needs_input, 'truncated_xz')
            decoded = self.decoder.decompress(chunk, max_length=65536)
            self.guard(decoded)
            self.buffer.extend(decoded)
        if self.decoder.eof:
            require(not self.decoder.unused_data and self.compressed.remaining == 0, 'trailing_xz_data')
        result = bytes(self.buffer[:size])
        del self.buffer[:size]
        return result

    def finish(self):
        while self.read(65536):
            pass
        require(self.decoder.eof and not self.header and self.body == 0 and self.ended, 'tar_stream_incomplete')


def members(stream):
    require(stream.read(8) == b'!<arch>\n', 'ar_magic')
    result = {}
    order = (('debian-binary',), ('control.tar.xz', 'control.tar.gz'), ('data.tar.xz',), ('_gpgorigin',))
    while stream.tell() < PACKAGE_SIZE:
        header = stream.read(60)
        require(len(header) == 60 and header[58:] == b'`\n', 'ar_header')
        name = header[:16].decode('ascii').strip().removesuffix('/')
        require(name in ('debian-binary', 'control.tar.xz', 'control.tar.gz', 'data.tar.xz', '_gpgorigin')
                and name not in result, 'unknown_or_duplicate_ar_member')
        require(len(result) < len(order) and name in order[len(result)], 'ar_member_order')
        require(re.fullmatch(rb' *[0-9]+ *', header[48:58]) is not None, 'ar_size')
        size = int(header[48:58])
        start = stream.tell()
        require(size > 0 and start + size <= PACKAGE_SIZE, 'ar_member_bound')
        # Opaque publisher metadata; authentication remains the full-package SHA256 pin.
        require(name != '_gpgorigin' or size <= 65536, 'ar_signature_bound')
        result[name] = (start, size)
        stream.seek(size, 1)
        if size % 2:
            require(stream.read(1) == b'\n', 'ar_padding')
    require(stream.tell() == PACKAGE_SIZE and len(result) in (3, 4)
            and 'debian-binary' in result and 'data.tar.xz' in result, 'ar_layout')
    start, size = result['debian-binary']
    stream.seek(start)
    require(size == 4 and stream.read(size) == b'2.0\n', 'debian_version')
    return result


def archive_path(name):
    if name.startswith('./'):
        name = name[2:]
    name = name.rstrip('/')
    require(name and not name.startswith('/') and len(name.encode()) <= 4096
            and all(ord(c) >= 32 and ord(c) != 127 for c in name)
            and all(p not in ('', '.', '..') for p in name.split('/')), 'unsafe_archive_path')
    return '/' + name


def generate(stream):
    start, size = members(stream)['data.tar.xz']
    stream.seek(start)
    entries, seen, aggregate = [], {}, 0
    decoded = BoundedTarXZ(Slice(stream, size))
    with tarfile.open(fileobj=decoded, mode='r|') as archive:
        for item in archive:
            if item.name in ('.', './') and item.isdir():
                require('/' not in seen and len(seen) < 20000, 'duplicate_or_entry_bound')
                seen['/'] = True
                continue
            path = archive_path(item.name)
            require(path not in seen and len(seen) < 20000, 'duplicate_or_entry_bound')
            seen[path] = item.isdir()
            require(item.isfile() or item.isdir() or item.issym(), 'unsafe_archive_type')
            require(0 <= item.size <= PACKAGE_MAX_FILE_SIZE, 'entry_size_bound')
            aggregate += item.size
            require(aggregate <= 4 * 1024**3, 'aggregate_bound')
            selected = path == ROOT or path.startswith(ROOT + '/') or path == '/usr/bin/chatgpt'
            entry = {'path': path, 'mode': item.mode, 'type': 'directory'}
            if item.issym():
                target = item.linkname
                require(target and len(target.encode()) <= 4096 and all(ord(c) >= 32 and ord(c) != 127 for c in target), 'link_bound')
                resolved = posixpath.normpath(posixpath.join(posixpath.dirname(path), target))
                require(resolved.startswith('/') and not resolved.startswith('/../'), 'link_escape')
                if selected:
                    require(resolved == ROOT or resolved.startswith(ROOT + '/'), 'selected_link_escape')
                entry.update(type='symlink', target=target)
            elif item.isfile():
                payload = archive.extractfile(item)
                require(payload is not None, 'file_stream_missing')
                with payload:
                    sha = digest(payload)
                entry.update(type='file', size=item.size, sha256=sha)
            if selected:
                entries.append(entry)
    decoded.finish()
    for path in seen:
        parent = posixpath.dirname(path)
        while parent != '/':
            require(seen.get(parent) is True, 'missing_or_non_directory_ancestor')
            parent = posixpath.dirname(parent)
    by_path = {e['path']: e for e in entries}
    require(by_path.get(ROOT, {}).get('type') == 'directory', 'vendor_root_missing')
    for path, sha in PINS.items():
        require(by_path.get(path, {}).get('sha256') == sha, 'vendor_binary_pin')
    require(by_path.get('/usr/bin/chatgpt', {}).get('target') == '../lib/chatgpt/codex-launcher', 'vendor_link_pin')
    return {'version': 1, 'packageSHA256': PACKAGE_SHA, 'root': ROOT,
            'launcher': ROOT + '/codex-launcher', 'executable': ROOT + '/ChatGPT',
            'entries': sorted(entries, key=lambda e: e['path'])}


def write(root, name, value):
    data = (json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=True) + '\n').encode()
    require(len(data) <= 8 * 1024 * 1024, 'catalog_bound')
    with (root / name).open('xb') as output:
        output.write(data)
    return hashlib.sha256(data).hexdigest()


def self_test():
    import unittest

    class DecoderBounds(unittest.TestCase):
        def test_ar_optional_signature_layout(self):
            def member(name, body):
                header = f'{name + "/":<16}{0:<12}{0:<6}{0:<6}{644:<8}{len(body):<10}`\n'.encode()
                return header + body + (b'\n' if len(body) % 2 else b'')

            required = [('debian-binary', b'2.0\n'), ('control.tar.xz', b'x'), ('data.tar.xz', b'x')]
            signature = [('_gpgorigin', b'opaque')]
            valid = b'!<arch>\n' + b''.join(member(*entry) for entry in required + signature)
            cases = [(valid, None),
                     (b'!<arch>\n' + b''.join(member(*entry) for entry in required), None),
                     (valid + member(*signature[0]), 'unknown_or_duplicate_ar_member'),
                     (valid + member(*required[2]), 'unknown_or_duplicate_ar_member'),
                     (b'!<arch>\n' + b''.join(member(*entry) for entry in required[:1] + signature + required[1:]), 'ar_member_order'),
                     (b'!<arch>\n' + b''.join(member(*entry) for entry in required[1:2] + required[:1] + required[2:]), 'ar_member_order'),
                     (valid + member('unknown', b'x'), 'unknown_or_duplicate_ar_member'),
                     (valid[:-1], 'ar_member_bound'),
                     (valid + member('control.tar.gz', b'x'), 'ar_member_order'),
                     (b'!<arch>\n' + b''.join(member(*entry) for entry in required + [('_gpgorigin', bytes(65537))]), 'ar_signature_bound'),
                     (valid[:-8] + b'x' + valid[-7:], 'ar_header'),
                     (valid.replace(b'x\n', b'x!', 1), 'ar_padding')]
            global PACKAGE_SIZE
            original_size = PACKAGE_SIZE
            try:
                for raw, error in cases:
                    with self.subTest(error=error, size=len(raw)):
                        PACKAGE_SIZE = len(raw)
                        if error:
                            with self.assertRaisesRegex(ValueError, error):
                                members(io.BytesIO(raw))
                        else:
                            parsed = members(io.BytesIO(raw))
                            self.assertEqual(list(parsed), [entry[0] for entry in required]
                                             + (['_gpgorigin'] if raw == valid else []))
            finally:
                PACKAGE_SIZE = original_size

        def decode(self, raw, **limits):
            packed = lzma.compress(raw, preset=0)
            decoder = BoundedTarXZ(Slice(io.BytesIO(packed), len(packed)), **limits)
            decoder.finish()

        def test_metadata_declaration_rejected_without_payload(self):
            info = tarfile.TarInfo('metadata')
            info.type, info.size = tarfile.XHDTYPE, 65537
            with self.assertRaisesRegex(ValueError, 'tar_declared_size_bound'):
                self.decode(info.tobuf())

        def test_pinned_largest_file_declaration_supported_without_payload(self):
            info = tarfile.TarInfo('usr/lib/chatgpt/resources/app.asar')
            info.size = 543408877
            packed = lzma.compress(info.tobuf(), preset=0)
            decoder = BoundedTarXZ(Slice(io.BytesIO(packed), len(packed)))
            with tarfile.open(fileobj=decoder, mode='r|') as archive:
                self.assertEqual(next(iter(archive)).size, 543408877)

        def test_file_declaration_above_pinned_maximum_rejected_without_payload(self):
            info = tarfile.TarInfo('oversized')
            info.size = 543408878
            with self.assertRaisesRegex(ValueError, 'tar_declared_size_bound'):
                self.decode(info.tobuf())

        def test_actual_decoded_bytes_bounded(self):
            with self.assertRaisesRegex(ValueError, 'decoded_tar_bound'):
                self.decode(bytes(2048), limit=1024)

        def test_decoder_memory_bounded(self):
            with self.assertRaises(lzma.LZMAError):
                self.decode(bytes(1024), memlimit=1)

        def test_duplicate_root_rejected(self):
            info = tarfile.TarInfo('./')
            info.type = tarfile.DIRTYPE
            with self.assertRaisesRegex(ValueError, 'duplicate_tar_root'):
                self.decode(info.tobuf() * 2 + bytes(1024))

        def test_pax_layout_desynchronization_rejected(self):
            # Independent reviewer counterexample: old guard accepted a hidden >64KiB PAX record.
            def header(name, kind, size):
                info = tarfile.TarInfo(name)
                info.type, info.size = kind, size
                return info.tobuf()

            def pad(body):
                return body + bytes((-len(body)) % 512)

            pax = b'9 size=0\n'
            prefix = b'65537 comment='
            large = prefix + b'a' * (512 - len(prefix)) + b''.join(
                header('fake' + str(i), tarfile.REGTYPE, 1024 if i == 126 else 0)
                for i in range(127)) + b'\n'
            raw = (header('pax', tarfile.XHDTYPE, len(pax)) + pad(pax)
                   + header('zero', tarfile.REGTYPE, 1024)
                   + header('hidden-pax', tarfile.XHDTYPE, len(large)) + pad(large)
                   + header('real-end', tarfile.REGTYPE, 0) + bytes(1024))
            self.assertEqual(len(raw), 69632)
            packed = lzma.compress(raw, preset=0)
            decoder = BoundedTarXZ(Slice(io.BytesIO(packed), len(packed)))
            with self.assertRaisesRegex(ValueError, 'pax_layout_override'):
                with tarfile.open(fileobj=decoder, mode='r|') as archive:
                    list(archive)

        def test_normal_filename_metadata_supported(self):
            raw = io.BytesIO()
            with tarfile.open(fileobj=raw, mode='w', format=tarfile.PAX_FORMAT) as archive:
                info = tarfile.TarInfo('a' * 256)
                archive.addfile(info)
            self.decode(raw.getvalue())

        def test_sparse_type_rejected(self):
            info = tarfile.TarInfo('sparse')
            info.type = tarfile.GNUTYPE_SPARSE
            with self.assertRaisesRegex(ValueError, 'unsupported_tar_type'):
                self.decode(info.tobuf(format=tarfile.GNU_FORMAT) + bytes(1024))

        def test_valid_empty_tar(self):
            self.decode(bytes(1024))

    result = unittest.TextTestRunner().run(unittest.defaultTestLoader.loadTestsFromTestCase(DecoderBounds))
    require(result.wasSuccessful(), 'decoder_tests_failed')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--preflight', action='store_true')
    parser.add_argument('--self-test', action='store_true')
    parser.add_argument('--package')
    parser.add_argument('--output-root')
    args = parser.parse_args()
    if args.self_test:
        require(not args.preflight and args.package is None and args.output_root is None, 'self_test_has_inputs')
        self_test()
        return
    if args.preflight:
        require(args.package is None and args.output_root is None, 'preflight_has_inputs')
        print('schema_version=1; stdlib_streaming_only; no_package_effects')
        return
    require(os.environ.get('NAVIGATION_LINUX_VENDOR_MANIFEST_TEST') == '1', 'explicit_opt_in_required')
    require(args.package and args.output_root, 'inputs_required')
    root = pathlib.Path(args.output_root)
    require(not root.is_symlink() and root.is_absolute() and root.resolve() == root, 'physical_root_required')
    rs = root.stat()
    require(stat.S_ISDIR(rs.st_mode) and rs.st_uid == os.getuid() and stat.S_IMODE(rs.st_mode) == 0o700
            and root.name.startswith('navigation-linux-vendor-manifest-TEST-') and not list(root.iterdir()), 'owned_empty_root_required')
    source = os.environ.get('NAVIGATION_SOURCE_SHA', '')
    require(re.fullmatch('[0-9a-f]{40}', source) is not None, 'exact_source_required')
    evidence = {'version': 1, 'passed': False, 'nonce': str(uuid.uuid4()), 'sourceCommit': source,
                'sourceSHA256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
                'run': os.environ.get('GITHUB_RUN_ID', ''), 'attempt': os.environ.get('GITHUB_RUN_ATTEMPT', ''),
                'packageURL': URL, 'packageSHA256': PACKAGE_SHA, 'packageSize': PACKAGE_SIZE,
                'installationPerformed': False, 'payloadExecuted': False}
    write(root, 'ownership.json', {'nonce': evidence['nonce'], 'purpose': 'vendor-manifest-test'})
    signal.signal(signal.SIGALRM, lambda *_: (_ for _ in ()).throw(TimeoutError('decode_deadline')))
    signal.alarm(600)
    try:
        package = pathlib.Path(args.package)
        require(not package.is_symlink(), 'package_symlink')
        with package.open('rb') as stream:
            before = os.fstat(stream.fileno())
            require(stat.S_ISREG(before.st_mode) and before.st_size == PACKAGE_SIZE, 'package_size')
            require(digest(stream) == PACKAGE_SHA, 'package_hash_before')
            stream.seek(0)
            manifest = generate(stream)
            stream.seek(0)
            require(digest(stream) == PACKAGE_SHA, 'package_hash_after')
            after = os.fstat(stream.fileno())
            require((before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) ==
                    (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns), 'package_changed')
        evidence.update(manifestSHA256=write(root, 'client-tree-manifest.json', manifest),
                        entryCount=len(manifest['entries']), passed=True)
    except Exception as error:
        evidence['error'] = type(error).__name__ + ':' + str(error)[:256]
        raise
    finally:
        signal.alarm(0)
        write(root, 'evidence.json', evidence)


if __name__ == '__main__':
    main()
