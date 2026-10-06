import XCTest
@testable import terminal_notifier_modern

// Read-only installed signature measurement, not notification/chat E2E.
// The production handler/executor/verifier run; OS opening is replaced by a spy.
final class NativePreflightMeasurementTests: XCTestCase {
    private final class NoOpen: DesktopURLOpening {
        var calls = 0
        func open(_ url: URL, application: URL, completion: @escaping (Bool) -> Void) {
            XCTAssertTrue(Thread.isMainThread)
            calls += 1
            completion(true) // Synthetic handoff, explicitly excluded from native evidence.
        }
    }
    func testInstalledPreflightMeasurementsWithoutApplicationActivation() throws {
        let env = ProcessInfo.processInfo.environment
        guard env["NOTIFIER_TEST_INSTALLED_PREFLIGHT"] == "1" else {
            throw XCTSkip("read-only native preflight measurement requires explicit opt-in")
        }
        let path = try XCTUnwrap(env["NOTIFIER_TEST_INSTALLED_APP"])
        let team = try XCTUnwrap(env["NOTIFIER_TEST_INSTALLED_TEAM"])
        let count = Int(env["NOTIFIER_TEST_PREFLIGHT_SAMPLES"] ?? "1") ?? 0
        XCTAssertTrue((1...50).contains(count))
        guard (1...50).contains(count) else { return }
        let output = FileManager.default.temporaryDirectory
            .appendingPathComponent("navigation-preflight-" + UUID().uuidString + ".json")
        var samples: [[CallbackDiagnostic]] = []
        for sample in 0..<count {
            var lines: [String] = []
            let done = expectation(description: "preflight sample \(sample)")
            let owner = CallbackLifecycle(schedule: { _, _ in }, exit: {}, diagnostic: {
                XCTAssertTrue(Thread.isMainThread); lines.append($0)
            })
            let opener = NoOpen()
            let executor = DesktopThreadExecutor(opener: opener, admission: PreflightAdmission(limit: 1))
            let correlation = UUID().uuidString
            let action = DesktopThreadAction(type: "desktop_thread_v1", schemaVersion: 1,
                threadID: "native-preflight-measurement", routeKind: "codex_thread",
                bundleID: "com.openai.codex", teamID: team, applicationPath: path, correlationID: correlation)
            CallbackHandler(lifecycle: owner, desktop: executor).receive(identifier: "OPEN",
                defaultIdentifier: "default", notificationID: correlation,
                userInfo: ["desktop_thread_v1": String(decoding: try JSONEncoder().encode(action), as: UTF8.self)],
                completion: { done.fulfill() })
            wait(for: [done], timeout: 35)
            let events = try lines.map { try JSONDecoder().decode(CallbackDiagnostic.self, from: Data($0.utf8)) }
            samples.append(events)
            // Retain every sample, including errors/unknowns, rather than bias latency.
            try JSONEncoder().encode(samples).write(to: output, options: .atomic)
            XCTAssertEqual(events.last?.outcome, "open_requested")
            XCTAssertEqual(opener.calls, 1)
        }
        print("READ_ONLY_PREFLIGHT_EVIDENCE=\(output.path)")
    }
}
