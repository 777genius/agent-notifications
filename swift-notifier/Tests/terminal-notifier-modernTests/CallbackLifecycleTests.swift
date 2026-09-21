import XCTest
@testable import terminal_notifier_modern

final class CallbackLifecycleTests: XCTestCase {
    func testNearIdleTwoClicksAndLateDoubleCompletion() {
        var timers: [() -> Void] = []
        var exits = 0
        var completions = 0
        var diagnostics: [String] = []
        var ends: [(CallbackOutcome) -> Void] = []
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: { exits += 1 },
            diagnostic: { diagnostics.append($0) })
        owner.start()
        for _ in 0..<2 {
            owner.accept(completion: { completions += 1 }, operation: { done, _ in ends.append(done) })
        }
        timers[0]() // original idle watchdog must not kill either callback
        XCTAssertEqual(exits, 0)
        XCTAssertEqual(owner.inFlight, 2)
        ends[0](.open_requested); ends[0](.open_requested)
        XCTAssertEqual(completions, 1)
        XCTAssertEqual(owner.inFlight, 1)
        timers[2]() // second callback deadline
        XCTAssertEqual(completions, 2)
        XCTAssertEqual(owner.inFlight, 0)
        ends[1](.open_failed); timers[1]() // late completion and first deadline
        XCTAssertEqual(completions, 2)
        timers.last?()
        XCTAssertEqual(exits, 1)
        XCTAssertEqual(diagnostics.compactMap { try? JSONDecoder().decode(CallbackDiagnostic.self, from: Data($0.utf8)) }.compactMap { $0.outcome }, ["open_requested", "open_unknown"])
        XCTAssertEqual(diagnostics.count, 4)
    }
    func testNewClickInvalidatesDrainIdleTimer() {
        var timers: [() -> Void] = []
        var exits = 0
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: { exits += 1 })
        owner.start()
        owner.accept(completion: {}, operation: { done, _ in done(.ignored) })
        let stale = timers.last!
        var finish: ((CallbackOutcome) -> Void)?
        owner.accept(completion: {}, operation: { done, _ in finish = done })
        stale()
        XCTAssertEqual(exits, 0)
        finish?(.open_requested)
        timers.last?()
        XCTAssertEqual(exits, 1)
    }
    func testDeadlineInvalidatesPendingContinuationAndStoppedOwnerDoesNotStartWork() {
        var timers: [() -> Void] = []
        var active: (() -> Bool)?
        var completions = 0
        let owner = CallbackLifecycle(schedule: { delay, work in
            XCTAssertEqual(delay, 10)
            timers.append(work)
        }, exit: {})
        owner.accept(completion: { completions += 1 }) { _, isActive in active = isActive }
        XCTAssertEqual(active?(), true)
        timers[0]()
        XCTAssertEqual(active?(), false)
        XCTAssertEqual(completions, 1)
        timers.last?()
        owner.accept(completion: { completions += 1 }) { _, _ in XCTFail("started after exit") }
        XCTAssertEqual(completions, 2)
    }

    func testElapsedDeadlineWinsEvenBeforeTimerDelivery() {
        var time = 100.0
        var finish: ((CallbackOutcome) -> Void)?
        var active: (() -> Bool)?
        var diagnostics: [String] = []
        let owner = CallbackLifecycle(schedule: { _, _ in }, exit: {},
            diagnostic: { diagnostics.append($0) }, now: { time })
        owner.accept(completion: {}) { done, isActive in finish = done; active = isActive }
        time = 110
        XCTAssertEqual(active?(), false)
        finish?(.open_requested)
        XCTAssertEqual(diagnostics.compactMap { try? JSONDecoder().decode(CallbackDiagnostic.self, from: Data($0.utf8)) }.compactMap { $0.outcome }, ["open_unknown"])
        XCTAssertEqual(diagnostics.count, 2)
        XCTAssertEqual(owner.inFlight, 0)
    }

}
