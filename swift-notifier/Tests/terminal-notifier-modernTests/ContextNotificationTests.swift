import Darwin
import XCTest
@testable import terminal_notifier_modern

final class ContextNotificationTests: XCTestCase {
    private func request(arguments: [String] = ["-title", "Pi", "-message", "fixed"]) throws -> Data {
        try JSONSerialization.data(withJSONObject: ["schemaVersion": 1,
            "correlationID": "00000000-0000-4000-8000-000000000001",
            "nonce": "00000000-0000-4000-8000-000000000002", "bootID": "TEST-boot", "notAfter": 115,
            "owner": ["pid": 123, "startSeconds": 10, "startMicroseconds": 20], "arguments": arguments])
    }

    func testContextGrammarPreservesSoundAndFocusButCannotSelectAnotherMode() throws {
        let args = ["-title", "--send-json", "-message", "Pi completed a task", "-execute", "'/TEST/runtime' focus-window 'com.apple.Terminal' '/TEST/project'", "-soundFile", "/TEST/cue.mp3", "-soundVolume", "0.25", "-nosound"]
        let decoded = try ContextNotificationRequest.decode(request(arguments: args))
        let config = try ArgumentParser.parse(decoded.arguments)
        XCTAssertEqual(config.title, "--send-json")
        XCTAssertEqual(config.soundFile, "/TEST/cue.mp3")
        XCTAssertEqual(config.soundVolume, 0.25)
        if case .execute(let command) = config.action { XCTAssertEqual(command, args[5]) }
        else { XCTFail("focus action lost") }
        for invalid in [["-title", "Pi", "-message", "fixed", "--request-permission-json"],
                        ["-title", "Pi", "-title", "other", "-message", "fixed"],
                        ["--send-json"], ["-title", "Pi", "-message"]] {
            XCTAssertThrowsError(try ContextNotificationRequest.decode(request(arguments: invalid)))
        }
        var value = try XCTUnwrap(JSONSerialization.jsonObject(with: request()) as? [String: Any])
        value["extra"] = true
        XCTAssertThrowsError(try ContextNotificationRequest.decode(JSONSerialization.data(withJSONObject: value)))
    }

    func testNoAuthorizationAndNoSendFromDeniedOrDelayedPermission() {
        for cancelBeforeReply in [false, true] {
            var active = true
            var replies = [(NativeStatus, NativeReason)]()
            var permissionReply: ((Bool) -> Void)?
            var sends = 0
            let delivery = ContextNotification(active: { active }, emit: { replies.append(($0, $1)) })
            delivery.start(permission: { permissionReply = $0 }, send: { _, _ in sends += 1 })
            if cancelBeforeReply { active = false; delivery.poll() }
            permissionReply?(cancelBeforeReply)
            permissionReply?(true)
            XCTAssertEqual(sends, 0)
            XCTAssertEqual(replies.count, 1)
            XCTAssertEqual(replies.first?.1, cancelBeforeReply ? .expired : .permission_denied)
        }
    }

    func testAdmissionIsCheckedAgainAtActualBackendSubmission() {
        var active = true
        var backendAdmission: (() -> Bool)?
        var backendReply: ((Bool) -> Void)?
        var replies = [(NativeStatus, NativeReason)]()
        let delivery = ContextNotification(active: { active }, emit: { replies.append(($0, $1)) })
        delivery.start(permission: { $0(true) }, send: { backendAdmission = $0; backendReply = $1 })
        XCTAssertTrue(backendAdmission?() == true)
        active = false
        XCTAssertFalse(backendAdmission?() == true)
        delivery.poll()
        backendReply?(true)
        XCTAssertEqual(replies.count, 1)
        XCTAssertEqual(replies.first?.0, .unknown)
        XCTAssertEqual(replies.first?.1, .timeout)
    }

    func testExactClaimDeletionAndReplacementCannotReviveContext() throws {
        try OwnedRequestTests().fixture { directory, nonce, files in
            _ = try OwnedRequestTests().body(directory, nonce, data: Data("{}".utf8))
            _ = try files.consume()
            XCTAssertTrue(files.contextIsLive())
            let started = directory.appendingPathComponent(nonce + ".started")
            try files.writeContextStarted(Data("{}".utf8))
            XCTAssertEqual(try Data(contentsOf: started), Data("{}".utf8))
            let claim = directory.appendingPathComponent(nonce + ".claim")
            // Keep the original inode alive so this checks replacement identity
            // rather than depending on the filesystem's inode reuse policy.
            let fd = open(claim.path, O_RDONLY)
            XCTAssertGreaterThanOrEqual(fd, 0)
            defer { close(fd) }
            try FileManager.default.removeItem(at: claim)
            XCTAssertFalse(files.contextIsLive())
            XCTAssertTrue(FileManager.default.createFile(atPath: claim.path, contents: Data(), attributes: [.posixPermissions: 0o600]))
            XCTAssertFalse(files.contextIsLive())
            XCTAssertThrowsError(try files.writeContextStarted(Data("{}".utf8)))
        }
    }

    func testKernelOwnerBirthIdentityMatchesOnlyTheLiveProcess() throws {
        let owner = try XCTUnwrap(ContextNativeProcess.read(getpid()))
        XCTAssertTrue(owner.valid)
        XCTAssertEqual(ContextNativeProcess.read(getpid()), owner)
        XCTAssertNotEqual(owner, ContextNativeProcess(pid: owner.pid, startSeconds: owner.startSeconds + 1, startMicroseconds: owner.startMicroseconds))
        XCTAssertNil(ContextNativeProcess.read(-1))
    }
}
