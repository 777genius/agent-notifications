import Foundation
import Darwin

// Caller creates a fresh 0700 UUID directory and 0600 nonce.request file.
// Descriptor-relative operations pin the owned directory, never follow symlinks,
// never recreate it, and never overwrite an existing terminal receipt.
final class OwnedRequest {
    let correlationID: String
    let nonce: String
    private let directory: Int32
    private let requestName: String
    private let receiptName: String
    private var attempted = false
    private var ownsReceipt = false

    init(requestPath: String, receiptPath: String) throws {
        let request = URL(fileURLWithPath: requestPath)
        let receipt = URL(fileURLWithPath: receiptPath)
        let parent = request.deletingLastPathComponent()
        correlationID = parent.lastPathComponent
        nonce = request.deletingPathExtension().lastPathComponent
        requestName = request.lastPathComponent
        receiptName = receipt.lastPathComponent
        guard requestPath.hasPrefix("/"), receiptPath.hasPrefix("/"),
              parent.path == receipt.deletingLastPathComponent().path,
              UUID(uuidString: correlationID) != nil, UUID(uuidString: nonce) != nil,
              requestName == nonce + ".request", receiptName == nonce + ".receipt" else {
            throw NativeReason.invalid_file
        }
        // Walk all components with O_NOFOLLOW, including ancestors.
        var fd = open("/", O_RDONLY | O_DIRECTORY | O_CLOEXEC)
        guard fd >= 0 else { throw NativeReason.invalid_file }
        for component in parent.pathComponents.dropFirst() {
            let next = openat(fd, component, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
            close(fd)
            guard next >= 0 else { throw NativeReason.invalid_file }
            fd = next
        }
        var metadata = stat()
        guard fstat(fd, &metadata) == 0, metadata.st_uid == geteuid(),
              metadata.st_mode & 0o7777 == 0o700 else {
            close(fd); throw NativeReason.invalid_file
        }
        directory = fd
    }
    deinit { close(directory) }

    func consume() throws -> Data {
        guard !attempted else { throw NativeReason.invalid_file }
        attempted = true
        var existing = stat()
        guard fstatat(directory, receiptName, &existing, AT_SYMLINK_NOFOLLOW) != 0,
              errno == ENOENT else { throw NativeReason.invalid_file }
        // A second LaunchServices instance must not publish a rejected receipt
        // while the first instance is submitting. This exclusive attempt marker
        // survives body consumption and stays until the caller cleans the attempt.
        // It is not a replay journal: an abandoned claim remains unknown.
        let claim = openat(directory, nonce + ".claim",
                           O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard claim >= 0 else { throw NativeReason.invalid_file }
        close(claim)
        ownsReceipt = true
        let fd = openat(directory, requestName, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC)
        guard fd >= 0 else { throw NativeReason.invalid_file }
        defer { close(fd) }
        var metadata = stat()
        guard flock(fd, LOCK_EX | LOCK_NB) == 0, fstat(fd, &metadata) == 0,
              metadata.st_mode & S_IFMT == S_IFREG, metadata.st_mode & 0o7777 == 0o600,
              metadata.st_uid == geteuid(), metadata.st_nlink == 1,
              metadata.st_size > 0, metadata.st_size <= NativeCodec.maxBytes else {
            throw NativeReason.invalid_file
        }
        var bytes = [UInt8](repeating: 0, count: NativeCodec.maxBytes + 1)
        var count = 0
        while count < bytes.count {
            let n = bytes.withUnsafeMutableBytes { buffer in
                read(fd, buffer.baseAddress!.advanced(by: count), buffer.count - count)
            }
            if n == 0 { break }
            if n < 0 { if errno == EINTR { continue }; throw NativeReason.invalid_file }
            count += n
        }
        guard count == metadata.st_size, count <= NativeCodec.maxBytes else { throw NativeReason.invalid_file }
        // Delete only after the full bounded copy, while holding the claim.
        guard unlinkat(directory, requestName, 0) == 0 else { throw NativeReason.invalid_file }
        return Data(bytes.prefix(count))
    }

    func write(_ receipt: NativeReceipt) throws {
        guard ownsReceipt, receipt.correlationID == correlationID, receipt.nonce == nonce else {
            throw NativeReason.invalid_file
        }
        let data = try JSONEncoder().encode(receipt)
        let temporary = nonce + "." + UUID().uuidString + ".tmp"
        let fd = openat(directory, temporary, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else { throw NativeReason.invalid_file }
        defer { close(fd); unlinkat(directory, temporary, 0) }
        try data.withUnsafeBytes { buffer in
            var offset = 0
            while offset < buffer.count {
                let n = Darwin.write(fd, buffer.baseAddress!.advanced(by: offset), buffer.count - offset)
                if n < 0 && errno == EINTR { continue }
                guard n > 0 else { throw NativeReason.invalid_file }
                offset += n
            }
        }
        guard fsync(fd) == 0,
              linkat(directory, temporary, directory, receiptName, 0) == 0 else {
            throw NativeReason.invalid_file
        }
    }
}
