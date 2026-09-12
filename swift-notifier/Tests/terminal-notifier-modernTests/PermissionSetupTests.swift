import XCTest
@testable import terminal_notifier_modern

final class PermissionSetupTests: XCTestCase {
    private let id = "00000000-0000-4000-8000-000000000001"
    private func args() -> [String] { ["--request-permission-json", "--correlation-id", id, "--nonce", id] }

    // All mutable callback state is protected, including simultaneous emissions.
    private final class Harness {
        private let lock = NSRecursiveLock()
        private var output = [Data]()
        private var requests = 0
        private var reads = 0
        private var deadline = false
        private var settingsCallback: ((Int?) -> Void)?
        private var authorizationCallback: ((Bool, Error?) -> Void)?
        private var timerCallback: (() -> Void)?
        func locked<T>(_ body: () -> T) -> T { lock.lock(); defer { lock.unlock() }; return body() }
        var outputs: [Data] { locked { output } }
        var prompts: Int { locked { requests } }
        var settingsReads: Int { locked { reads } }
        var expired: Bool { locked { deadline } }
        func expire() { locked { deadline = true } }
        func emit(_ data: Data) { locked { output.append(data) } }
        func settings(_ callback: @escaping (Int?) -> Void) { locked { reads += 1; settingsCallback = callback } }
        func authorize(_ callback: @escaping (Bool, Error?) -> Void) { locked { requests += 1; authorizationCallback = callback } }
        func timer(_ callback: @escaping () -> Void) { locked { timerCallback = callback } }
        func status(_ raw: Int?) { let callback = locked { settingsCallback }; callback?(raw) }
        func result(_ granted: Bool, _ error: Error? = nil) { let callback = locked { authorizationCallback }; callback?(granted, error) }
        func timeout() { let callback = locked { timerCallback }; callback?() }
    }
    private func start(_ h: Harness) throws -> PermissionSetup {
        let setup = PermissionSetup(request: try PermissionSetupRequest(arguments: args()), expired: { h.expired }, emit: h.emit)
        setup.start(settings: h.settings, authorize: h.authorize, timer: h.timer)
        return setup
    }
    private func assertOutput(_ h: Harness, _ permission: String, file: StaticString = #filePath, line: UInt = #line) throws {
        XCTAssertEqual(h.outputs.count, 1, file: file, line: line)
        let data = try XCTUnwrap(h.outputs.first)
        XCTAssertEqual(data.last, 10, file: file, line: line)
        XCTAssertEqual(data.filter { $0 == 10 }.count, 1)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object.count, 5)
        XCTAssertEqual(object["schemaVersion"] as? Int, 1)
        XCTAssertEqual(object["correlationID"] as? String, id)
        XCTAssertEqual(object["nonce"] as? String, id)
        XCTAssertEqual(object["backend"] as? String, "macos.usernotifications")
        XCTAssertEqual(object["permission"] as? String, permission, file: file, line: line)
    }
    func testExactGrammarAndLiteralModes() throws {
        _ = try PermissionSetupRequest(arguments: args())
        _ = try PermissionSetupRequest(arguments: args() + ["-launchedViaLaunchServices"])
        var invalid: [[String]] = []
        invalid.append(Array(args().dropFirst()))
        invalid.append(args() + ["extra"])
        invalid.append(args() + ["--nonce", id])
        invalid.append(args() + ["--request-permission-json"])
        invalid.append(args() + ["-launchedViaLaunchServices", "-launchedViaLaunchServices"])
        invalid.append(["-launchedViaLaunchServices"] + args())
        invalid.append(["--request-permission-json"])
        invalid.append(["--request-permission-json", "--nonce", id, "--correlation-id", id])
        for literal in ["--capabilities-json", "--send-json", "--request-permission-json", "-launchedViaLaunchServices", "bad", id + "x"] {
            for slot in [2, 4] { var bad = args(); bad[slot] = literal; invalid.append(bad) }
        }
        let h = Harness()
        for bad in invalid {
            XCTAssertThrowsError(try PermissionSetupRequest(arguments: bad))
            // The same parse-before-start boundary used by the runtime.
            if let request = try? PermissionSetupRequest(arguments: bad) {
                PermissionSetup(request: request, expired: { false }, emit: h.emit)
                    .start(settings: h.settings, authorize: h.authorize, timer: h.timer)
            }
        }
        XCTAssertEqual(h.prompts, 0); XCTAssertEqual(h.settingsReads, 0)
        for literal in ["--request-permission-json", "--capabilities-json", "--send-json"] {
            XCTAssertFalse(PermissionSetupRequest.optionPositions(["-title", literal]).contains(literal))
            XCTAssertFalse(PermissionSetupRequest.optionPositions(["--nonce", literal]).contains(literal))
            XCTAssertFalse(PermissionSetupRequest.optionPositions(["--correlation-id", literal]).contains(literal))
        }
    }
    func testSeparateCapabilities() throws {
        let data = try PermissionSetupCapabilities.encode(arguments: ["--capabilities-json", "--setup"])
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object.count, 3)
        XCTAssertEqual(object["schemaVersion"] as? Int, 1)
        XCTAssertEqual(object["permissionRequestVersions"] as? [Int], [1])
        XCTAssertEqual(object["backend"] as? String, "macos.usernotifications")
        XCTAssertEqual(data.last, 10)
        for bad in [["--setup"], ["--setup", "--capabilities-json"], ["--capabilities-json"],
                    ["--capabilities-json", "--setup", "--setup"], ["--capabilities-json", "--setup", "2"],
                    ["--capabilities-json", "--setup", "-launchedViaLaunchServices"]] {
            XCTAssertThrowsError(try PermissionSetupCapabilities.encode(arguments: bad))
        }
    }
    func testSettingsMappingNeverPromptsExceptUndetermined() throws {
        let statuses: [(Int?, String)] = [(1, "denied"), (2, "allowed"), (3, "allowed"), (4, "unavailable"), (999, "unavailable"), (nil, "unavailable")]
        for (raw, expected) in statuses {
            let h = Harness(); let setup = try start(h)
            h.status(raw); h.status(0); h.timeout()
            XCTAssertEqual(h.prompts, 0)
            try assertOutput(h, expected)
            withExtendedLifetime(setup) {}
        }
        let results: [(Bool, Error?, String)] = [(true, nil, "allowed"), (false, nil, "denied"), (true, NSError(domain: "private", code: 1), "unavailable"), (false, NSError(domain: "private", code: 2), "unavailable")]
        for (granted, error, expected) in results {
            let h = Harness(); let setup = try start(h)
            h.status(0); h.status(0)
            XCTAssertEqual(h.prompts, 1)
            h.result(granted, error); h.result(!granted); h.timeout()
            try assertOutput(h, expected)
            setup.start(settings: h.settings, authorize: h.authorize, timer: h.timer)
            XCTAssertEqual(h.settingsReads, 1)
        }
    }
    func testTimeoutBeforeRequestAndAfterDispatch() throws {
        let early = Harness(); early.expire(); let first = try start(early)
        XCTAssertEqual(early.settingsReads, 0); XCTAssertEqual(early.prompts, 0)
        try assertOutput(early, "unavailable")
        let waiting = Harness(); let second = try start(waiting)
        waiting.timeout(); waiting.status(0)
        XCTAssertEqual(waiting.prompts, 0); try assertOutput(waiting, "unavailable")
        let late = Harness(); let third = try start(late)
        late.status(0); late.timeout(); late.result(true); late.result(false)
        XCTAssertEqual(late.prompts, 1); try assertOutput(late, "unavailable")
        let deadline = Harness(); let fourth = try start(deadline)
        deadline.expire(); deadline.status(0)
        XCTAssertEqual(deadline.prompts, 0); try assertOutput(deadline, "unavailable")
        let resultDeadline = Harness(); let fifth = try start(resultDeadline)
        resultDeadline.status(0); resultDeadline.expire(); resultDeadline.result(true)
        try assertOutput(resultDeadline, "unavailable")
        withExtendedLifetime([first, second, third, fourth, fifth]) {}
    }
    func testSynchronousTimerAndAuthorization() throws {
        let h = Harness()
        let setup = PermissionSetup(request: try PermissionSetupRequest(arguments: args()), expired: { false }, emit: h.emit)
        setup.start(settings: h.settings, authorize: h.authorize, timer: { $0() })
        XCTAssertEqual(h.settingsReads, 0); XCTAssertEqual(h.prompts, 0)
        try assertOutput(h, "unavailable")
        let sync = Harness()
        let immediate = PermissionSetup(request: try PermissionSetupRequest(arguments: args()), expired: { false }, emit: sync.emit)
        immediate.start(settings: { $0(0) }, authorize: { $0(true, nil) }, timer: sync.timer)
        sync.timeout(); try assertOutput(sync, "allowed")
    }
    func testSimultaneousCallbackAndTimeoutExactlyOnce() throws {
        for _ in 0..<100 {
            let h = Harness(); let setup = try start(h); h.status(0)
            DispatchQueue.concurrentPerform(iterations: 2) { index in
                if index == 0 { h.timeout() } else { h.result(true) }
            }
            XCTAssertEqual(h.prompts, 1); XCTAssertEqual(h.outputs.count, 1)
            let value = String(decoding: try XCTUnwrap(h.outputs.first), as: UTF8.self)
            XCTAssertTrue(value.contains("allowed") || value.contains("unavailable"))
            withExtendedLifetime(setup) {}
        }
    }
    func testSimultaneousSettingsAndTimeoutAtMostOnePrompt() throws {
        for _ in 0..<100 {
            let h = Harness(); let setup = try start(h)
            DispatchQueue.concurrentPerform(iterations: 2) { index in
                if index == 0 { h.timeout() } else { h.status(0) }
            }
            h.result(true); XCTAssertLessThanOrEqual(h.prompts, 1)
            try assertOutput(h, "unavailable")
            withExtendedLifetime(setup) {}
        }
    }
    func testReadOnlyProbeHasNoAuthorizationSeam() throws {
        let h = Harness()
        let request = try PermissionProbeRequest(arguments: ["--capabilities-json", "--permission-status", "--correlation-id", id, "--nonce", id])
        let probe = PermissionProbe(request: request, emit: h.emit)
        probe.start(settings: { $0(0) }, timer: h.timer)
        h.timeout(); XCTAssertEqual(h.prompts, 0)
        try assertOutput(h, "undetermined")
        // Neither PermissionProbe nor PermissionSetup accepts NativeBackend,
        // notification content, category registration, audio, or callbacks.
    }
}
