import XCTest
import UserNotifications
@testable import terminal_notifier_modern

final class DesktopThreadActionTests: XCTestCase {
    func action(thread: String = "thread/other?#%👩‍💻") -> DesktopThreadAction {
        DesktopThreadAction(type: "desktop_thread_v1", schemaVersion: 1,
            threadID: thread, routeKind: "codex_thread", bundleID: "com.openai.codex",
            teamID: "TESTTEAM01", applicationPath: "/disposable/Codex.app",
            correlationID: "00000000-0000-4000-8000-000000000001")
    }
    func testRouteIsOneEncodedSegmentAndLiteralBytesSurvive() throws {
        let target = action()
        let decoded = try DesktopThreadAction.decode(JSONEncoder().encode(target))
        XCTAssertEqual(Array(decoded.threadID.utf8), Array(target.threadID.utf8))
        XCTAssertEqual(decoded.url.absoluteString,
            "codex://threads/thread%2Fother%3F%23%25%F0%9F%91%A9%E2%80%8D%F0%9F%92%BB")
        XCTAssertNil(URLComponents(url: decoded.url, resolvingAgainstBaseURL: false)?.query)
        XCTAssertNil(URLComponents(url: decoded.url, resolvingAgainstBaseURL: false)?.fragment)
        for thread in ["", ".", "..", "bad\0", String(repeating: "x", count: 257)] {
            XCTAssertThrowsError(try action(thread: thread).validate())
        }
    }
    func testStrictActionFieldsAndVersions() throws {
        let data = try JSONEncoder().encode(action())
        let original = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        for (key, value) in [("schemaVersion", 2), ("schemaVersion", true), ("url", "https://example.com"),
                             ("routeKind", "shell"), ("teamID", "\" or true"), ("applicationPath", "/a/../Codex.app")] as [(String, Any)] {
            var object = original; object[key] = value
            XCTAssertThrowsError(try DesktopThreadAction.decode(JSONSerialization.data(withJSONObject: object)))
        }
    }
    func testSharedActionFixtures() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().appendingPathComponent("Fixtures")
        let data = try Data(contentsOf: root.appendingPathComponent("desktop-thread-v1.actions.json"))
        let fixtures = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        for entry in try XCTUnwrap(fixtures["valid"] as? [[String: Any]]) {
            let action = try DesktopThreadAction.decode(JSONSerialization.data(withJSONObject: entry["action"]!))
            XCTAssertEqual(action.url.absoluteString, entry["uri"] as? String)
        }
        for entry in try XCTUnwrap(fixtures["invalid"] as? [[String: Any]]) {
            XCTAssertThrowsError(try DesktopThreadAction.decode(JSONSerialization.data(withJSONObject: entry)))
        }
    }
    func testCallbackSelectionAndTypedPayloadNeverFallsBack() throws {
        let target = action()
        let json = String(decoding: try JSONEncoder().encode(target), as: UTF8.self)
        for button in ["OPEN", "default"] {
            guard case .desktop(let decoded) = CallbackRoute.decode(identifier: button,
                defaultIdentifier: "default", notificationID: target.correlationID,
                userInfo: ["desktop_thread_v1": json]) else { return XCTFail("expected desktop") }
            XCTAssertEqual(decoded, target)
        }
        for button in ["DISMISS", "unknown"] {
            guard case .ignored = CallbackRoute.decode(identifier: button, defaultIdentifier: "default",
                notificationID: target.correlationID, userInfo: ["desktop_thread_v1": json]) else {
                return XCTFail("unexpected navigation")
            }
        }
        for value in [42, "{}", json] as [Any] {
            guard case .malformed = CallbackRoute.decode(identifier: "OPEN", defaultIdentifier: "default",
                notificationID: "wrong", userInfo: ["desktop_thread_v1": value,
                    "action": ClickAction.execute(command: "never run").toJSON()!]) else {
                return XCTFail("typed payload fell through")
            }
        }
    }
    func testStructuredContentContainsCompleteDurableTarget() throws {
        let target = action()
        let request = NativeRequest(schemaVersion: 1, correlationID: target.correlationID,
            nonce: "00000000-0000-4000-8000-000000000002", bootID: "boot", notAfter: 110,
            title: "--help", body: "[important]👩‍💻", subtitle: nil, category: .info,
            action: .desktopThread(target), silent: true)
        let decoded = try NativeCodec.decodeRequest(JSONEncoder().encode(request))
        let content = try StructuredNotificationContent.make(decoded)
        XCTAssertEqual(content.title, "--help")
        XCTAssertEqual(content.body, "[important]👩‍💻")
        XCTAssertEqual(content.categoryIdentifier, NotificationCategory.categoryIdentifier)
        let json = try XCTUnwrap(content.userInfo["desktop_thread_v1"] as? String)
        XCTAssertEqual(try DesktopThreadAction.decode(Data(json.utf8)), target)
        XCTAssertEqual(content.userInfo.count, 1)
        XCTAssertNil(content.sound)
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(request)) as? [String: Any])
        object["correlationID"] = "00000000-0000-4000-8000-000000000003"
        XCTAssertThrowsError(try NativeCodec.decodeRequest(JSONSerialization.data(withJSONObject: object)))
    }
    struct Discovery: ApplicationDiscovering {
        let url: URL?
        var registered: [URL] = []
        func registeredApplications(bundleID: String) -> [URL] { registered }
        func selectedApplication(path: String) -> URL? { url }
    }
    struct Verifier: ApplicationVerifying {
        let valid: Bool
        func verify(_ url: URL, bundleID: String, teamID: String) -> Bool { valid }
    }
    final class Opener: DesktopURLOpening {
        var calls = 0
        var completion: ((Bool) -> Void)?
        func open(_ url: URL, application: URL, completion: @escaping (Bool) -> Void) {
            calls += 1; self.completion = completion
        }
    }
    func testDeadlineDuringVerificationPreventsOpen() {
        let opener = Opener()
        let executor = DesktopThreadExecutor(discovery: Discovery(url: URL(fileURLWithPath: "/disposable/Codex.app")),
            verifier: Verifier(valid: true), opener: opener, verificationWork: { $0() }, deliverResult: { $0() })
        var checks = 0
        var result: DesktopOpenResult?
        executor.execute(action(), isActive: { checks += 1; return checks == 1 }) { result = $0 }
        XCTAssertEqual(result, .open_unknown)
        XCTAssertEqual(opener.calls, 0)
    }
    func testMissingSelectedLocationNeverPicksRegisteredCandidate() {
        for count in [1, 2, 17] {
            let opener = Opener()
            let urls = (0..<count).map { URL(fileURLWithPath: "/disposable/\($0)/Codex.app") }
            let executor = DesktopThreadExecutor(discovery: Discovery(url: nil, registered: urls),
                verifier: Verifier(valid: true), opener: opener, verificationWork: { $0() }, deliverResult: { $0() })
            var result: DesktopOpenResult?
            executor.execute(action()) { result = $0 }
            XCTAssertEqual(result, count == 1 ? .application_moved : .application_ambiguous)
            XCTAssertEqual(opener.calls, 0)
        }
    }
    func testDiscoveryVerificationAndDelayedOpen() {
        for (path, valid, expected) in [(nil, true, DesktopOpenResult.application_missing),
                                      ("/other/Codex.app", true, .application_moved),
                                      ("/disposable/Codex.app", false, .identity_mismatch),
                                      ("/disposable/Codex.app", true, .open_failed),
                                      ("/disposable/Codex.app", true, .open_requested)] as [(String?, Bool, DesktopOpenResult)] {
            let opener = Opener()
            let executor = DesktopThreadExecutor(discovery: Discovery(url: path.map { URL(fileURLWithPath: $0) }),
                verifier: Verifier(valid: valid), opener: opener, verificationWork: { $0() }, deliverResult: { $0() })
            var result: DesktopOpenResult?
            executor.execute(action()) { result = $0 }
            if expected == .open_failed || expected == .open_requested {
                XCTAssertNil(result)
                XCTAssertEqual(opener.calls, 1)
                opener.completion?(expected == .open_requested)
            } else { XCTAssertEqual(opener.calls, 0) }
            XCTAssertEqual(result, expected)
        }
    }
    func testPendingVerificationDoesNotBlockDeadlineOrOpenLate() {
        var jobs: [() -> Void] = []
        var timers: [() -> Void] = []
        var completions = 0
        var time = 0.0
        let lifecycle = CallbackLifecycle(schedule: { _, job in timers.append(job) },
            exit: {}, now: { time })
        let opener = Opener()
        let executor = DesktopThreadExecutor(
            discovery: Discovery(url: URL(fileURLWithPath: "/disposable/Codex.app")),
            verifier: Verifier(valid: true), opener: opener,
            verificationWork: { jobs.append($0) }, deliverResult: { $0() })
        lifecycle.accept(completion: { completions += 1 }) { done, active in
            executor.execute(action(), isActive: active, completion: done)
        }
        XCTAssertEqual(jobs.count, 1)
        XCTAssertEqual(completions, 0)
        time = 11
        timers[0]()
        XCTAssertEqual(completions, 1)
        XCTAssertEqual(lifecycle.inFlight, 0)
        jobs[0]()
        XCTAssertEqual(opener.calls, 0)
        XCTAssertEqual(completions, 1)
    }

    struct BackgroundVerifier: ApplicationVerifying {
        func verify(_ url: URL, bundleID: String, teamID: String) -> Bool {
            XCTAssertFalse(Thread.isMainThread)
            return false
        }
    }
    func testProductionSchedulerVerifiesOffMainAndReturnsToMain() {
        let done = expectation(description: "verification returns")
        let opener = Opener()
        let executor = DesktopThreadExecutor(
            discovery: Discovery(url: URL(fileURLWithPath: "/disposable/Codex.app")),
            verifier: BackgroundVerifier(), opener: opener)
        executor.execute(action()) { result in
            XCTAssertTrue(Thread.isMainThread)
            XCTAssertEqual(result, .identity_mismatch)
            XCTAssertEqual(opener.calls, 0)
            done.fulfill()
        }
        wait(for: [done], timeout: 2)
    }

}
