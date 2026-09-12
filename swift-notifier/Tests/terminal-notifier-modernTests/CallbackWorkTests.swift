import XCTest
import Darwin
@testable import terminal_notifier_modern

final class CallbackWorkTests: XCTestCase {
    private func action(_ n: Int = 1) -> DesktopThreadAction {
        DesktopThreadAction(type: "desktop_thread_v1", schemaVersion: 1, threadID: "private-thread",
            routeKind: "codex_thread", bundleID: "com.openai.codex", teamID: "TESTTEAM01",
            applicationPath: "/disposable/Codex.app",
            correlationID: String(format: "00000000-0000-4000-8000-%012d", n))
    }
    private func receive(_ handler: CallbackHandler, _ action: DesktopThreadAction,
                         completion: @escaping () -> Void = {}) throws {
        handler.receive(identifier: "OPEN", defaultIdentifier: "default", notificationID: action.correlationID,
            userInfo: ["desktop_thread_v1": String(decoding: try JSONEncoder().encode(action), as: UTF8.self)],
            completion: completion)
    }
    private struct Discovery: ApplicationDiscovering {
        var selected: URL? = URL(fileURLWithPath: "/disposable/Codex.app")
        var candidates: [URL] = []
        var check: () -> Void = {}
        func selectedApplication(path: String) -> URL? { check(); return selected }
        func registeredApplications(bundleID: String) -> [URL] { check(); return candidates }
    }
    private struct Verifier: ApplicationVerifying {
        var check: () -> Bool = { true }
        func verify(_ url: URL, bundleID: String, teamID: String) -> Bool { check() }
    }
    private final class Opener: DesktopURLOpening {
        var completions: [(Bool) -> Void] = []
        func open(_ url: URL, application: URL, completion: @escaping (Bool) -> Void) {
            XCTAssertTrue(Thread.isMainThread)
            completions.append(completion)
        }
    }
    func testHeldSecurityKeepsSlotsAfterTimeoutAndOtherCallbacksProgress() throws {
        let entered = expectation(description: "two blocked checks")
        entered.expectedFulfillmentCount = 2
        let returned = expectation(description: "late results")
        returned.expectedFulfillmentCount = 2
        let gate = DispatchSemaphore(value: 0)
        defer { gate.signal(); gate.signal() }
        var timers: [() -> Void] = []
        var completions = 0
        var events: [String] = []
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: {},
                                      diagnostic: { events.append($0) })
        let opener = Opener()
        let executor = DesktopThreadExecutor(
            discovery: Discovery(selected: nil, candidates: (0..<16).map { URL(fileURLWithPath: "/disposable/\($0).app") },
                                 check: { XCTAssertFalse(Thread.isMainThread) }),
            verifier: Verifier(check: {
                XCTAssertFalse(Thread.isMainThread)
                entered.fulfill()
                // The safety timeout only prevents a broken fixture stranding a worker.
                XCTAssertEqual(gate.wait(timeout: .now() + 5), .success)
                return true
            }), opener: opener,
            deliverResult: { work in DispatchQueue.main.async { work(); returned.fulfill() } },
            admission: PreflightAdmission(limit: 2))
        let handler = CallbackHandler(lifecycle: owner, desktop: executor)
        try receive(handler, action(1)) { completions += 1 }
        try receive(handler, action(2)) { completions += 1 }
        wait(for: [entered], timeout: 2)
        // Acceptance and timeout run on the real main queue while Security is held.
        timers[0](); timers[1]()
        XCTAssertEqual(completions, 2)
        for n in 3...24 { try receive(handler, action(n)) { completions += 1 } }
        XCTAssertEqual(completions, 24) // no queued work behind occupied slots
        XCTAssertEqual(opener.completions.count, 0)
        gate.signal(); gate.signal()
        wait(for: [returned], timeout: 2)
        XCTAssertEqual(completions, 24)
        XCTAssertEqual(opener.completions.count, 0)
        XCTAssertEqual(events.count, 48)
        // No further candidate checks after cancellation; overfulfilment of entered fails.
    }
    func testMainQueueDeadlineFiresWhileVerifierRemainsHeld() throws {
        let entered = expectation(description: "held verifier")
        let timedOut = expectation(description: "main queue deadline callback")
        let returned = expectation(description: "late verification result")
        let gate = DispatchSemaphore(value: 0)
        defer { gate.signal() }
        var completions = 0
        let owner = CallbackLifecycle(schedule: { delay, work in
            XCTAssertEqual(delay, 10)
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.05, execute: work)
        }, exit: {})
        let opener = Opener()
        let executor = DesktopThreadExecutor(discovery: Discovery(), verifier: Verifier(check: {
            entered.fulfill()
            XCTAssertEqual(gate.wait(timeout: .now() + 5), .success)
            return true
        }), opener: opener,
        deliverResult: { work in DispatchQueue.main.async { work(); returned.fulfill() } },
        admission: PreflightAdmission(limit: 1))
        let handler = CallbackHandler(lifecycle: owner, desktop: executor)
        try receive(handler, action()) { completions += 1; timedOut.fulfill() }
        wait(for: [entered], timeout: 2)
        handler.receive(identifier: "DISMISS", defaultIdentifier: "default", notificationID: "untrusted",
                        userInfo: [:]) { completions += 1 }
        wait(for: [timedOut], timeout: 2)
        XCTAssertEqual(completions, 2)
        gate.signal()
        wait(for: [returned], timeout: 2)
        XCTAssertEqual(completions, 2)
        XCTAssertTrue(opener.completions.isEmpty)
    }

    func testBlockedDiscoveryAlsoLeavesCallbackQueueResponsive() throws {
        let entered = expectation(description: "discovery held")
        let returned = expectation(description: "discovery returned")
        let gate = DispatchSemaphore(value: 0)
        defer { gate.signal() }
        var timers: [() -> Void] = []
        var completions = 0
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: {})
        let opener = Opener()
        let executor = DesktopThreadExecutor(discovery: Discovery(check: {
            XCTAssertFalse(Thread.isMainThread); entered.fulfill()
            XCTAssertEqual(gate.wait(timeout: .now() + 5), .success)
        }), verifier: Verifier(check: { XCTFail("verified after timeout"); return true }), opener: opener,
        deliverResult: { work in DispatchQueue.main.async { work(); returned.fulfill() } },
        admission: PreflightAdmission(limit: 1))
        let handler = CallbackHandler(lifecycle: owner, desktop: executor)
        try receive(handler, action()) { completions += 1 }
        wait(for: [entered], timeout: 2)
        timers[0]()
        handler.receive(identifier: "DISMISS", defaultIdentifier: "default", notificationID: "untrusted",
                        userInfo: [:]) { completions += 1 }
        XCTAssertEqual(completions, 2)
        gate.signal()
        wait(for: [returned], timeout: 2)
        XCTAssertTrue(opener.completions.isEmpty)
    }
    func testAdmissionIncludesScheduledJobsAndReleasesOnlyWhenWorkReturns() throws {
        var jobs: [() -> Void] = []
        var completions = 0
        let token = CallbackToken()
        let executor = DesktopThreadExecutor(discovery: Discovery(), verifier: Verifier(check: { false }),
            opener: Opener(), verificationWork: { jobs.append($0) }, deliverResult: { $0() },
            admission: PreflightAdmission(limit: 1))
        executor.execute(action(), token: token) { _ in completions += 1 }
        token.cancel()
        for _ in 0..<20 { executor.execute(action()) { result in
            XCTAssertEqual(result, .open_unknown); completions += 1
        } }
        XCTAssertEqual(jobs.count, 1)
        jobs[0]()
        executor.execute(action()) { _ in completions += 1 }
        XCTAssertEqual(jobs.count, 2)
        jobs[1]()
        XCTAssertEqual(completions, 22)
    }
    func testReversedCompletionRetainsCorrelationAndEmitsOneTerminal() throws {
        var lines: [String] = []
        let owner = CallbackLifecycle(schedule: { _, _ in }, exit: {}, diagnostic: { lines.append($0) })
        let opener = Opener()
        let executor = DesktopThreadExecutor(discovery: Discovery(), verifier: Verifier(), opener: opener,
            verificationWork: { $0() }, deliverResult: { $0() }, admission: PreflightAdmission(limit: 2))
        let handler = CallbackHandler(lifecycle: owner, desktop: executor)
        try receive(handler, action(1)); try receive(handler, action(2))
        opener.completions[1](false); opener.completions[0](true)
        opener.completions[1](true); opener.completions[0](false)
        let events = try lines.map { try JSONDecoder().decode(CallbackDiagnostic.self, from: Data($0.utf8)) }
        XCTAssertEqual(events.map { $0.event }, ["callback_received", "callback_received", "callback_terminal", "callback_terminal"])
        XCTAssertEqual(events.map { $0.correlationID }, [1, 2, 2, 1].map { UUID(uuidString: action($0).correlationID)! })
        XCTAssertEqual(events.compactMap { $0.outcome }, ["open_failed", "open_requested"])
        XCTAssertTrue(lines.allSatisfy { $0.utf8.count < 200 && !$0.contains("private-thread") && !$0.contains("disposable") })
    }
    func testMalformedCorrelationIsGeneratedAndNeverLogsInput() throws {
        var lines: [String] = []
        let owner = CallbackLifecycle(schedule: { _, _ in }, exit: {}, diagnostic: { lines.append($0) })
        let handler = CallbackHandler(lifecycle: owner)
        let untrusted = "private-body-shell-path"
        handler.receive(identifier: "OPEN", defaultIdentifier: "default", notificationID: untrusted,
            userInfo: ["desktop_thread_v1": untrusted, "action": untrusted], completion: {})
        let events = try lines.map { try JSONDecoder().decode(CallbackDiagnostic.self, from: Data($0.utf8)) }
        XCTAssertEqual(events.count, 2)
        XCTAssertEqual(events[0].correlationID, events[1].correlationID)
        XCTAssertEqual(events[1].outcome, "malformed_action")
        XCTAssertFalse(lines.joined().contains(untrusted))
    }

    private final class Child: OwnedCommand {
        var started = 0
        var cancelled = 0
        var end: (() -> Void)?
        func start(completion: @escaping () -> Void) { started += 1; end = completion }
        func cancel() { cancelled += 1 }
    }
    func testLegacyTimeoutOwnsDrainWhileSecondCallbackCompletes() throws {
        var timers: [() -> Void] = []
        var exits = 0
        var completions = 0
        var activations: [String] = []
        var lines: [String] = []
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: { exits += 1 },
                                      diagnostic: { lines.append($0) })
        let a = Child(), b = Child()
        var children = [a, b]
        let executor = ActionExecutor(makeCommand: { command in
            XCTAssertEqual(command, "literal shell command")
            return children.removeFirst()
        }, activation: { bundle, done in activations.append(bundle); done() })
        let handler = CallbackHandler(lifecycle: owner, legacy: executor)
        owner.start()
        let command = ClickAction.executeAndActivate(command: "literal shell command", bundleID: "test.bundle")
        for _ in 0..<2 {
            handler.receive(identifier: "OPEN", defaultIdentifier: "default", notificationID: "legacy",
                            userInfo: ["action": command.toJSON()!]) { completions += 1 }
        }
        timers[0]() // stale startup idle
        timers[1]() // timeout A
        XCTAssertEqual(a.cancelled, 1)
        XCTAssertEqual(b.cancelled, 0)
        XCTAssertEqual(completions, 1)
        XCTAssertEqual(owner.ownedCount, 2)
        b.end?(); b.end?()
        XCTAssertEqual(activations, ["test.bundle"])
        XCTAssertEqual(completions, 2)
        XCTAssertEqual(owner.inFlight, 0)
        XCTAssertEqual(owner.ownedCount, 1)
        timers.forEach { $0() }
        XCTAssertEqual(exits, 0) // expired callback alone cannot permit parent exit
        a.end?(); a.end?()
        XCTAssertEqual(owner.ownedCount, 0)
        XCTAssertEqual(completions, 2)
        XCTAssertEqual(activations.count, 1)
        timers.last?()
        XCTAssertEqual(exits, 1)
        XCTAssertEqual(lines.count, 4)
    }

    func testProductionChildOwnerEscalatesBeforeReapAndSignalsOnlyOwnedGroup() {
        var scheduled: [(Double, () -> Void)] = []
        var signals: [(pid_t, Int32)] = []
        var reapCalls = 0
        var readyToReap = false
        var completions = 0
        let system = SpawnedCommand.System(spawn: { command in
            XCTAssertEqual(command, "unchanged shell payload")
            return 1234
        }, signalGroup: { pid, signal in signals.append((pid, signal)) }, reap: { pid in
            XCTAssertEqual(pid, 1234)
            reapCalls += 1
            return readyToReap
        }, leaderExited: { _ in true }, groupDrained: { _ in true })
        let child = SpawnedCommand("unchanged shell payload", system: system,
                                   schedule: { scheduled.append(($0, $1)) })
        child.start { completions += 1 }
        XCTAssertEqual(reapCalls, 1)
        XCTAssertEqual(scheduled.map { $0.0 }, [0.05])
        child.cancel(); child.cancel()
        XCTAssertEqual(signals.map { $0.0 }, [1234])
        XCTAssertEqual(signals.map { $0.1 }, [SIGTERM])
        XCTAssertEqual(scheduled.map { $0.0 }, [0.05, 0.25])
        scheduled[0].1() // old poll must not reap/release the PID during escalation
        XCTAssertEqual(reapCalls, 1)
        XCTAssertEqual(completions, 0)
        XCTAssertEqual(child.processID, 1234)
        scheduled[1].1()
        XCTAssertEqual(signals.map { $0.0 }, [1234, 1234])
        XCTAssertEqual(signals.map { $0.1 }, [SIGTERM, SIGKILL])
        XCTAssertEqual(reapCalls, 2)
        XCTAssertEqual(completions, 0) // KILL alone does not mean reaped
        readyToReap = true
        scheduled[2].1()
        XCTAssertEqual(child.processID, 0)
        XCTAssertEqual(completions, 1)
        scheduled[0].1(); scheduled[1].1(); scheduled[2].1(); child.cancel()
        XCTAssertEqual(completions, 1)
        XCTAssertEqual(signals.count, 2) // no stale signal after PID release
    }

    func testProductionChildOwnerHandlesSpawnFailureAndCancelBeforeStart() {
        for cancelled in [false, true] {
            var spawns = 0
            var completions = 0
            let system = SpawnedCommand.System(spawn: { _ in spawns += 1; return nil },
                signalGroup: { _, _ in XCTFail("no owned group to signal") },
                reap: { _ in XCTFail("no owned child to reap"); return false },
                leaderExited: { _ in XCTFail("no child to observe"); return false },
                groupDrained: { _ in XCTFail("no group to inspect"); return false })
            let child = SpawnedCommand("literal", system: system,
                                       schedule: { _, _ in XCTFail("unexpected timer") })
            if cancelled { child.cancel() }
            child.start { completions += 1 }
            child.cancel()
            XCTAssertEqual(spawns, cancelled ? 0 : 1)
            XCTAssertEqual(completions, 1)
            XCTAssertEqual(child.processID, 0)
        }
    }

    // Explicit macOS qualification only; ordinary unit runs have no child effects.
    func testMacOwnedHarmlessChildTimeoutReapsBeforeIdleExit() throws {
        guard ProcessInfo.processInfo.environment["NOTIFIER_TEST_OWNED_CHILD"] == "1" else {
            throw XCTSkip("Coordinator macOS opt-in: NOTIFIER_TEST_OWNED_CHILD=1")
        }
        var timers: [() -> Void] = []
        let exited = expectation(description: "idle after reap")
        var completed = 0
        let child = SpawnedCommand("trap '' TERM; while :; do /bin/sleep 1; done")
        defer { child.cancel() }
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: { exited.fulfill() })
        let handler = CallbackHandler(lifecycle: owner, legacy: ActionExecutor(makeCommand: { _ in child }))
        handler.receive(identifier: "OPEN", defaultIdentifier: "default", notificationID: "legacy",
                        userInfo: ["action": ClickAction.execute(command: "fixture").toJSON()!]) { completed += 1 }
        let pid = child.processID
        XCTAssertGreaterThan(pid, 0)
        timers[0]()
        XCTAssertEqual(completed, 1)
        XCTAssertEqual(owner.ownedCount, 1)
        let drained = expectation(description: "kernel child reaped")
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
            XCTAssertEqual(owner.ownedCount, 0)
            var status: Int32 = 0
            XCTAssertEqual(waitpid(pid, &status, WNOHANG), -1)
            XCTAssertEqual(errno, ECHILD)
            XCTAssertEqual(completed, 1)
            timers.last?()
            drained.fulfill()
        }
        wait(for: [drained, exited], timeout: 3)
    }
}
