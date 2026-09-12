import XCTest
import Darwin
@testable import terminal_notifier_modern

final class LegacyFinalTests: XCTestCase {
    private struct Discovery: ApplicationDiscovering {
        func selectedApplication(path: String) -> URL? { URL(fileURLWithPath: path) }
        func registeredApplications(bundleID: String) -> [URL] { [] }
    }
    private struct Verifier: ApplicationVerifying {
        func verify(_ url: URL, bundleID: String, teamID: String) -> Bool { true }
    }
    private final class HeldOpener: DesktopURLOpening {
        var calls = 0
        func open(_ url: URL, application: URL, completion: @escaping (Bool) -> Void) {
            calls += 1 // keep typed callback pending until its own deadline
        }
    }

    func testLeaderExitRetainsOwnershipUntilHelperDrainsThenActivates() {
        var polls: [() -> Void] = []
        var timers: [() -> Void] = []
        var drained = false
        var reaps = 0
        var activations = 0
        var completed = 0
        var exits = 0
        let child = SpawnedCommand("unchanged", system: .init(spawn: { _ in 1234 },
            signalGroup: { _, _ in XCTFail("successful helper must not be killed") },
            reap: { _ in reaps += 1; return true }, leaderExited: { _ in true },
            groupDrained: { _ in drained }), schedule: { _, work in polls.append(work) })
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: { exits += 1 })
        owner.start()
        let executor = ActionExecutor(makeCommand: { _ in child }, activationSystem: .init(
            running: { _ in { activations += 1; return true } },
            lookup: { _ in XCTFail("running target succeeded"); return nil },
            open: { _, _ in XCTFail("unexpected open") }), discoveryWork: { $0() }, deliverResult: { $0() })
        owner.acceptOwned(completion: { completed += 1 }) { done, work in
            executor.execute(.executeAndActivate(command: "unchanged", bundleID: "test.bundle"), work: work) { done(.legacy_completed) }
        }
        polls.removeFirst()()
        timers[0]() // stale startup idle cannot exit with a leader-exits-first helper
        XCTAssertEqual(exits, 0)
        XCTAssertEqual(child.processID, 1234)
        XCTAssertEqual(owner.ownedCount, 1)
        XCTAssertEqual(reaps, 0)
        XCTAssertEqual(completed, 0)
        XCTAssertEqual(activations, 0)
        drained = true
        polls.removeFirst()()
        XCTAssertEqual(reaps, 1)
        XCTAssertEqual(child.processID, 0)
        XCTAssertEqual(owner.ownedCount, 0)
        XCTAssertEqual(completed, 1)
        XCTAssertEqual(activations, 1)
        child.cancel() // no signalling cached PID after reap
        timers.last?()
        XCTAssertEqual(exits, 1)
    }

    func testNoHelperReapsImmediatelyWithoutDeadlineDelay() {
        var completed = 0
        let child = SpawnedCommand("exit 0", system: .init(spawn: { _ in 1234 },
            signalGroup: { _, _ in XCTFail("unexpected signal") }, reap: { _ in true },
            leaderExited: { _ in true }, groupDrained: { _ in true }),
            schedule: { _, _ in XCTFail("no ten-second wait for an exited foreground command") })
        child.start { completed += 1 }
        XCTAssertEqual(completed, 1)
        XCTAssertEqual(child.processID, 0)
    }

    func testLeaderExitWithUndrainedOrUnknownGroupUsesOwnedDeadlineCleanup() {
        var polls: [(Double, () -> Void)] = []
        var timers: [() -> Void] = []
        var events: [String] = []
        var completed = 0
        let child = SpawnedCommand("helper & exit 0", system: .init(spawn: { _ in 1234 },
            signalGroup: { pid, signal in XCTAssertEqual(pid, 1234); events.append("signal:\(signal)") },
            reap: { _ in events.append("reap"); return true }, leaderExited: { _ in true },
            groupDrained: { _ in false }), schedule: { polls.append(($0, $1)) })
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: {})
        let executor = ActionExecutor(makeCommand: { _ in child }, activation: { _, _ in XCTFail("late activation") })
        owner.acceptOwned(completion: { completed += 1 }) { done, work in
            executor.execute(.executeAndActivate(command: "helper & exit 0", bundleID: "test.bundle"), work: work) { done(.legacy_completed) }
        }
        XCTAssertTrue(events.isEmpty)
        timers[0]()
        XCTAssertEqual(completed, 1)
        XCTAssertEqual(owner.ownedCount, 1)
        polls[0].1()
        XCTAssertEqual(events, ["signal:\(SIGTERM)"])
        XCTAssertEqual(polls[1].0, 0.25)
        polls[1].1()
        XCTAssertEqual(events, ["signal:\(SIGTERM)", "signal:\(SIGKILL)", "reap"])
        XCTAssertEqual(owner.ownedCount, 0)
        XCTAssertEqual(completed, 1)
        polls.forEach { $0.1() }; child.cancel()
        XCTAssertEqual(events.count, 3)
    }

    func testHeldLegacyLookupKeepsSharedSlotWhileTwoCallbacksTimeOutAndNeverOpensLate() {
        let entered = expectation(description: "legacy lookup held off main")
        let returned = expectation(description: "legacy lookup delivered")
        let deadlines = expectation(description: "both callbacks complete on main")
        deadlines.expectedFulfillmentCount = 2
        let gate = DispatchSemaphore(value: 0)
        defer { gate.signal() }
        let admission = PreflightAdmission(limit: 2)
        var opens = 0
        var completed = 0
        var deliveries = 0
        let executor = ActionExecutor(activationSystem: .init(running: { _ in
            XCTAssertFalse(Thread.isMainThread); return nil
        }, lookup: { _ in
            XCTAssertFalse(Thread.isMainThread)
            entered.fulfill()
            XCTAssertEqual(gate.wait(timeout: .now() + 5), .success)
            return URL(fileURLWithPath: "/disposable/Test.app")
        }, open: { _, done in opens += 1; done() }), deliverResult: { work in
            DispatchQueue.main.async {
                work(); deliveries += 1
                if deliveries == 2 { returned.fulfill() }
            }
        }, admission: admission)
        let owner = CallbackLifecycle(schedule: { _, work in
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5, execute: work)
        }, exit: {})
        let typedOpener = HeldOpener()
        let typed = DesktopThreadExecutor(discovery: Discovery(), verifier: Verifier(), opener: typedOpener,
            verificationWork: { $0() }, deliverResult: { $0() }, admission: admission)
        let handler = CallbackHandler(lifecycle: owner, desktop: typed, legacy: executor)
        handler.receive(identifier: "OPEN", defaultIdentifier: "default", notificationID: "legacy",
            userInfo: ["action": ClickAction.activate(bundleID: "test.bundle").toJSON()!]) {
                XCTAssertTrue(Thread.isMainThread); completed += 1; deadlines.fulfill()
            }
        wait(for: [entered], timeout: 2)
        // The typed path shares admission and keeps its opener pending. Its own
        // main-queue deadline must finish independently of the legacy lookup.
        let action = DesktopThreadAction(type: "desktop_thread_v1", schemaVersion: 1, threadID: "fixture",
            routeKind: "codex_thread", bundleID: "com.openai.codex", teamID: "TESTTEAM01",
            applicationPath: "/disposable/Codex.app", correlationID: "00000000-0000-4000-8000-000000000001")
        handler.receive(identifier: "OPEN", defaultIdentifier: "default", notificationID: action.correlationID,
            userInfo: ["desktop_thread_v1": String(decoding: try! JSONEncoder().encode(action), as: UTF8.self)]) {
                completed += 1; deadlines.fulfill()
            }
        XCTAssertEqual(typedOpener.calls, 1)
        wait(for: [deadlines], timeout: 2)
        XCTAssertEqual(completed, 2)
        XCTAssertTrue(admission.acquire()) // the typed preflight has returned
        XCTAssertFalse(admission.acquire()) // timeout did not free legacy slot
        admission.release()
        XCTAssertEqual(opens, 0)
        gate.signal()
        wait(for: [returned], timeout: 2)
        XCTAssertEqual(completed, 2)
        XCTAssertEqual(opens, 0)
        XCTAssertTrue(admission.acquire()); admission.release()
    }

    func testLegacyFallbackRechecksOriginalDeadlineAndPreservesRunningFirstStrategy() {
        for expires in [false, true] {
            var now = 0.0
            let token = CallbackToken(deadline: 10, now: { now })
            let work = CallbackWork(token: token, acquire: { _ in {} })
            var events: [String] = []
            let executor = ActionExecutor(activationSystem: .init(running: { _ in
                events.append("running")
                return { events.append("activate"); if expires { now = 10 }; return false }
            }, lookup: { _ in events.append("lookup"); return URL(fileURLWithPath: "/disposable/Test.app") },
            open: { _, done in events.append("open"); done() }), discoveryWork: { $0() }, deliverResult: { $0() })
            executor.execute(.activate(bundleID: "test.bundle"), work: work) { events.append("done") }
            XCTAssertEqual(events, expires ? ["running", "activate", "done"] : ["running", "activate", "lookup", "open", "done"])
        }
    }

    func testLegacyPendingDeliveryChecksDeadlineBeforeActivateAndOpen() {
        for hasRunning in [false, true] {
            var delivery: [() -> Void] = []
            var now = 0.0
            let token = CallbackToken(deadline: 10, now: { now })
            let work = CallbackWork(token: token, acquire: { _ in {} })
            var effects = 0
            var completed = 0
            let executor = ActionExecutor(activationSystem: .init(running: { _ in
                if hasRunning { return { effects += 1; return true } }; return nil
            }, lookup: { _ in URL(fileURLWithPath: "/disposable/Test.app") },
            open: { _, done in effects += 1; done() }), discoveryWork: { $0() },
            deliverResult: { delivery.append($0) })
            executor.execute(.activate(bundleID: "test.bundle"), work: work) { completed += 1 }
            if !hasRunning { delivery.removeFirst()() } // enqueue lookup result
            now = 10 // original deadline, without relying on the main timer firing
            delivery.removeFirst()()
            XCTAssertEqual(effects, 0)
            XCTAssertEqual(completed, 1)
        }
    }

    func testLegacyAdmissionCountsQueuedAndPendingDeliveryAndSkipsExpiredDiscovery() {
        let admission = PreflightAdmission(limit: 1)
        var jobs: [() -> Void] = []
        var deliveries: [() -> Void] = []
        var completed = 0
        let token = CallbackToken()
        let work = CallbackWork(token: token, acquire: { _ in {} })
        let executor = ActionExecutor(activationSystem: .init(
            running: { _ in XCTFail("expired job must not start discovery"); return nil },
            lookup: { _ in XCTFail("expired fallback"); return nil },
            open: { _, _ in XCTFail("expired open") }), discoveryWork: { jobs.append($0) },
            deliverResult: { deliveries.append($0) }, admission: admission)
        executor.execute(.activate(bundleID: "test.bundle"), work: work) { completed += 1 }
        token.cancel()
        for _ in 0..<20 {
            executor.execute(.activate(bundleID: "test.bundle"), isActive: { true }) { completed += 1 }
        }
        XCTAssertEqual(jobs.count, 1)
        XCTAssertEqual(completed, 20)
        jobs.removeFirst()()
        XCTAssertFalse(admission.acquire()) // returned work still awaits main delivery
        deliveries.removeFirst()()
        XCTAssertEqual(completed, 21)
        XCTAssertTrue(admission.acquire()); admission.release()
    }

    func testUnobservedLeaderExitNeverInspectsGroupOrConsumesStatus() {
        var scheduled = 0
        let child = SpawnedCommand("running", system: .init(spawn: { _ in 1234 },
            signalGroup: { _, _ in XCTFail("unexpected signal") },
            reap: { _ in XCTFail("must observe exit first"); return false },
            leaderExited: { _ in false },
            groupDrained: { _ in XCTFail("leader still running or observation failed"); return true }),
            schedule: { _, _ in scheduled += 1 })
        child.start { XCTFail("premature release") }
        XCTAssertEqual(child.processID, 1234)
        XCTAssertEqual(scheduled, 1)
    }

    private func groupSnapshot(_ pid: pid_t) -> [kinfo_proc]? {
        var mib: [Int32] = [CTL_KERN, KERN_PROC, KERN_PROC_PGRP, pid]
        var entries = [kinfo_proc](repeating: kinfo_proc(), count: 64)
        var bytes = entries.count * MemoryLayout<kinfo_proc>.stride
        let result = entries.withUnsafeMutableBytes {
            sysctl(&mib, 4, $0.baseAddress, &bytes, nil, 0)
        }
        guard result == 0, bytes < 64 * MemoryLayout<kinfo_proc>.stride,
              bytes % MemoryLayout<kinfo_proc>.stride == 0 else { return nil }
        return Array(entries.prefix(bytes / MemoryLayout<kinfo_proc>.stride))
    }

    // Coordinator-only: no UI, no apps, only disposable same-group /bin/sleep.
    func testMacLeaderExitsFirstNaturalDrainAndDeadlineCleanup() throws {
        guard ProcessInfo.processInfo.environment["NOTIFIER_TEST_LEADER_EXITS_FIRST"] == "1" else {
            throw XCTSkip("Coordinator macOS opt-in: NOTIFIER_TEST_LEADER_EXITS_FIRST=1")
        }
        for natural in [true, false] {
            let done = expectation(description: "leader and group drained")
            var sawLiveHelper = false
            var signals: [Int32] = []
            var child: SpawnedCommand!
            let live = SpawnedCommand.System.live
            let system = SpawnedCommand.System(spawn: live.spawn, signalGroup: { pid, signal in
                // Non-consuming observation still works immediately before every
                // signal: the leader's identity has never been released.
                XCTAssertTrue(live.leaderExited(pid))
                signals.append(signal); live.signalGroup(pid, signal)
            }, reap: live.reap, leaderExited: live.leaderExited, groupDrained: { pid in
                let drained = live.groupDrained(pid)
                if !drained && !sawLiveHelper {
                    let members = self.groupSnapshot(pid)
                    XCTAssertNotNil(members)
                    XCTAssertTrue(members?.contains(where: {
                        $0.kp_proc.p_pid != pid && Int32($0.kp_proc.p_stat) != SZOMB
                    }) == true)
                    sawLiveHelper = true
                    XCTAssertEqual(child.processID, pid)
                    if !natural { DispatchQueue.main.async { child.cancel() } }
                }
                return drained
            })
            child = SpawnedCommand(natural ? "/bin/sleep 1 & exit 0" : "/bin/sleep 30 & exit 0", system: system)
            child.start { done.fulfill() }
            let pid = child.processID
            defer { child.cancel() }
            wait(for: [done], timeout: 5)
            XCTAssertTrue(sawLiveHelper)
            XCTAssertEqual(child.processID, 0)
            XCTAssertEqual(signals, natural ? [] : [SIGTERM, SIGKILL])
            var status: Int32 = 0
            XCTAssertEqual(waitpid(pid, &status, WNOHANG), -1)
            XCTAssertEqual(errno, ECHILD)
            let groupGone = expectation(description: "no live same-group helper")
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                // Read-only inspection after reap; never signal this cached PGID.
                let members = self.groupSnapshot(pid)
                XCTAssertNotNil(members)
                XCTAssertTrue(members?.allSatisfy { Int32($0.kp_proc.p_stat) == SZOMB } == true)
                groupGone.fulfill()
            }
            wait(for: [groupGone], timeout: 2)
        }
    }
}
