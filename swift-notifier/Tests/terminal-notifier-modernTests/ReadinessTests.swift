import XCTest
@testable import terminal_notifier_modern

final class ReadinessTests: XCTestCase {
    let id = "00000000-0000-4000-8000-000000000001"
    func args() -> [String] { ["--capabilities-json", "--permission-status", "--correlation-id", id, "--nonce", id] }
    func testStrictGrammar() throws {
        _ = try PermissionProbeRequest(arguments: args())
        for bad in [Array(args().dropFirst()), args() + ["extra"], ["--capabilities-json"], ["--permission-status"], ["--capabilities-json", "--permission-status", "--correlation-id", "bad", "--nonce", id]] {
            XCTAssertThrowsError(try PermissionProbeRequest(arguments: bad))
        }
    }
    func testSettingsAndLateTimer() throws {
        for (raw, expected) in [(0,"undetermined"),(1,"denied"),(2,"allowed"),(3,"allowed"),(4,"unavailable"),(999,"unavailable")] {
            var outputs = [Data](); var timeout: (() -> Void)?
            let probe = PermissionProbe(request: try PermissionProbeRequest(arguments: args())) { outputs.append($0) }
            probe.start(settings: { $0(raw) }, timer: { timeout = $0 })
            timeout?(); timeout?()
            XCTAssertEqual(outputs.count, 1)
            let object = try XCTUnwrap(JSONSerialization.jsonObject(with: outputs[0]) as? [String: Any])
            XCTAssertEqual(object["permission"] as? String, expected)
            XCTAssertEqual(object["nonce"] as? String, id)
            XCTAssertEqual(object.count, 5)
        }
    }
    func testTimeoutAndLateSettings() throws {
        var outputs = [Data](); var callback: ((Int) -> Void)?; var timeout: (() -> Void)?
        let probe = PermissionProbe(request: try PermissionProbeRequest(arguments: args())) { outputs.append($0) }
        probe.start(settings: { callback = $0 }, timer: { timeout = $0 })
        timeout?(); callback?(2)
        XCTAssertEqual(outputs.count, 1)
        XCTAssertTrue(String(decoding: outputs[0], as: UTF8.self).contains("unavailable"))
    }
}
