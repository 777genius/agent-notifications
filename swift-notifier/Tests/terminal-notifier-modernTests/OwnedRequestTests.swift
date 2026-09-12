import XCTest
import Darwin
@testable import terminal_notifier_modern

final class OwnedRequestTests: XCTestCase {
    // Only disposable test files; no native backend, notifications or settings.
    func fixture(_ test: (URL, String, OwnedRequest) throws -> Void) throws {
        // Foundation preserves the /var alias on macOS. Use the physical
        // directory required by the protocol, without relaxing O_NOFOLLOW.
        let resolved = try XCTUnwrap(realpath(FileManager.default.temporaryDirectory.path, nil))
        let physicalRoot = String(cString: resolved)
        free(resolved)
        let directory = URL(fileURLWithPath: physicalRoot)
            .appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false,
                                                attributes: [.posixPermissions: 0o700])
        defer { try? FileManager.default.removeItem(at: directory) }
        let nonce = UUID().uuidString
        let files = try OwnedRequest(requestPath: directory.appendingPathComponent(nonce + ".request").path,
                                     receiptPath: directory.appendingPathComponent(nonce + ".receipt").path)
        try test(directory, nonce, files)
    }
    func body(_ directory: URL, _ nonce: String, data: Data, mode: Int = 0o600) throws -> URL {
        let file = directory.appendingPathComponent(nonce + ".request")
        XCTAssertTrue(FileManager.default.createFile(atPath: file.path, contents: data,
                                                    attributes: [.posixPermissions: mode]))
        return file
    }
    func testConsumeOnceAndAtomicReceiptNoOverwrite() throws {
        try fixture { directory, nonce, files in
            let file = try body(directory, nonce, data: Data("{}".utf8))
            XCTAssertEqual(try files.consume(), Data("{}".utf8))
            XCTAssertFalse(FileManager.default.fileExists(atPath: file.path))
            XCTAssertThrowsError(try files.consume())
            let receipt = NativeReceipt(correlationID: directory.lastPathComponent, nonce: nonce,
                                        status: .rejected, reason: .expired)
            try files.write(receipt)
            XCTAssertThrowsError(try files.write(receipt))
            let data = try Data(contentsOf: directory.appendingPathComponent(nonce + ".receipt"))
            XCTAssertEqual(try NativeCodec.decodeReceipt(data, correlationID: directory.lastPathComponent, nonce: nonce).reason, .expired)
        }
    }
    func testDuplicateReaderCannotPublishFalseRejection() throws {
        try fixture { directory, nonce, first in
            _ = try body(directory, nonce, data: Data("{}".utf8))
            let second = try OwnedRequest(
                requestPath: directory.appendingPathComponent(nonce + ".request").path,
                receiptPath: directory.appendingPathComponent(nonce + ".receipt").path)
            _ = try first.consume()
            XCTAssertThrowsError(try second.consume())
            XCTAssertThrowsError(try second.write(NativeReceipt(
                correlationID: directory.lastPathComponent, nonce: nonce,
                status: .rejected, reason: .invalid_file)))
            XCTAssertFalse(FileManager.default.fileExists(
                atPath: directory.appendingPathComponent(nonce + ".receipt").path))
            try first.write(NativeReceipt(correlationID: directory.lastPathComponent,
                nonce: nonce, status: .submitted, reason: .os_accepted))
        }
    }

    func testAbandonedClaimCannotResubmitOrWriteReceipt() throws {
        try fixture { directory, nonce, files in
            _ = try body(directory, nonce, data: Data("{}".utf8))
            XCTAssertTrue(FileManager.default.createFile(
                atPath: directory.appendingPathComponent(nonce + ".claim").path,
                contents: Data(), attributes: [.posixPermissions: 0o600]))
            XCTAssertThrowsError(try files.consume())
            XCTAssertThrowsError(try files.write(NativeReceipt(
                correlationID: directory.lastPathComponent, nonce: nonce,
                status: .rejected, reason: .invalid_file)))
            XCTAssertTrue(FileManager.default.fileExists(
                atPath: directory.appendingPathComponent(nonce + ".request").path))
        }
    }

    func testUnsafeModeOversizeAndSymlink() throws {
        for mode in [0o644, 0o660] {
            try fixture { directory, nonce, files in
                _ = try body(directory, nonce, data: Data("{}".utf8), mode: mode)
                XCTAssertThrowsError(try files.consume())
            }
        }
        try fixture { directory, nonce, files in
            _ = try body(directory, nonce, data: Data(repeating: 65, count: 16385))
            XCTAssertThrowsError(try files.consume())
        }
        try fixture { directory, nonce, files in
            let target = directory.appendingPathComponent("target")
            try Data("{}".utf8).write(to: target)
            try FileManager.default.createSymbolicLink(at: directory.appendingPathComponent(nonce + ".request"), withDestinationURL: target)
            XCTAssertThrowsError(try files.consume())
            XCTAssertEqual(try Data(contentsOf: target), Data("{}".utf8))
        }
    }
    func testParentSymlinkIsRejectedWithoutConsumingBody() throws {
        try fixture { directory, nonce, _ in
            let bodyURL = try body(directory, nonce, data: Data("{}".utf8))
            let alias = directory.appendingPathComponent(UUID().uuidString)
            try FileManager.default.createSymbolicLink(at: alias, withDestinationURL: directory)
            XCTAssertThrowsError(try OwnedRequest(
                requestPath: alias.appendingPathComponent(nonce + ".request").path,
                receiptPath: alias.appendingPathComponent(nonce + ".receipt").path))
            XCTAssertEqual(try Data(contentsOf: bodyURL), Data("{}".utf8))
        }
    }

    func testHardlinkFIFOAndDeletedDirectory() throws {
        try fixture { directory, nonce, files in
            let file = try body(directory, nonce, data: Data("{}".utf8))
            XCTAssertEqual(link(file.path, directory.appendingPathComponent("link").path), 0)
            XCTAssertThrowsError(try files.consume())
        }
        try fixture { directory, nonce, files in
            XCTAssertEqual(mkfifo(directory.appendingPathComponent(nonce + ".request").path, 0o600), 0)
            XCTAssertThrowsError(try files.consume()) // O_NONBLOCK: never wait on a writer
        }
        try fixture { directory, nonce, files in
            try FileManager.default.removeItem(at: directory)
            XCTAssertThrowsError(try files.consume())
            XCTAssertThrowsError(try files.write(NativeReceipt(correlationID: directory.lastPathComponent,
                nonce: nonce, status: .unknown, reason: .timeout)))
            XCTAssertFalse(FileManager.default.fileExists(atPath: directory.path))
        }
    }
}
