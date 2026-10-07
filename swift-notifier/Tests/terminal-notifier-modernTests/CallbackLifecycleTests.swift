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

    func testPendingOperationInvalidatesIdleAndExitsPromptlyWithoutCallbacks() {
        var timers: [() -> Void] = []
        var exits = 0
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: { exits += 1 })
        owner.start()
        let finish = owner.beginOperation()
        timers[0]() // A send/setup may outlive the callback-only idle budget.
        owner.start()
        XCTAssertEqual(exits, 0)
        XCTAssertFalse(owner.stopped)
        XCTAssertEqual(timers.count, 1)
        finish(ExitCode.success)
        XCTAssertEqual(exits, 1) // No new idle/grace timer for a finite operation.
        XCTAssertTrue(owner.stopped)
        XCTAssertEqual(timers.count, 1)
        finish(ExitCode.failed); timers[0]()
        XCTAssertEqual(exits, 1)
        XCTAssertEqual(owner.exitCode, ExitCode.success)
    }

    func testOperationResultWaitsForAcceptedCallbackCompletion() {
        var events: [String] = []
        var callbackDone: ((CallbackOutcome) -> Void)?
        let owner = CallbackLifecycle(schedule: { _, _ in }, exit: { events.append("exit") })
        let finish = owner.beginOperation()
        owner.accept(completion: { events.append("callback") }) { done, _ in callbackDone = done }
        events.append("result")
        finish(ExitCode.success)
        XCTAssertEqual(events, ["result"])
        XCTAssertEqual(owner.inFlight, 1)
        callbackDone?(.open_requested)
        XCTAssertEqual(events, ["result", "callback", "exit"])
        callbackDone?(.open_failed); finish(ExitCode.failed)
        XCTAssertEqual(events.count, 3)
        XCTAssertEqual(owner.exitCode, ExitCode.success)
    }

    func testBackgroundDelegateIngressPreventsExitBeforeMainQueueAcceptance() {
        XCTAssertTrue(Thread.isMainThread)
        var events: [String] = []
        var callbackDone: ((CallbackOutcome) -> Void)?
        let accepted = expectation(description: "callback accepted on main")
        let enqueued = DispatchSemaphore(value: 0)
        let owner = CallbackLifecycle(schedule: { _, _ in }, exit: { events.append("exit") })
        let finish = owner.beginOperation()
        DispatchQueue.global().async {
            XCTAssertFalse(Thread.isMainThread)
            owner.dispatchIngress(completion: { XCTFail("ingress rejected before operation finished") }) {
                XCTAssertTrue(Thread.isMainThread)
                owner.accept(completion: { events.append("callback") }) { done, _ in callbackDone = done }
                accepted.fulfill()
            }
            enqueued.signal()
        }
        // Keep main occupied until the delegate has queued its work, then finish
        // the send before allowing receive/accept to run. The former path exits here.
        guard enqueued.wait(timeout: .now() + 2) == .success else {
            XCTFail("delegate did not enqueue"); return
        }
        events.append("result")
        finish(ExitCode.success)
        XCTAssertEqual(events, ["result"])
        XCTAssertFalse(owner.stopped)
        XCTAssertEqual(owner.inFlight, 0)
        wait(for: [accepted], timeout: 2)
        XCTAssertEqual(owner.inFlight, 1)
        XCTAssertEqual(events, ["result"])
        callbackDone?(.open_requested)
        XCTAssertEqual(events, ["result", "callback", "exit"])
        callbackDone?(.open_failed)
        XCTAssertEqual(events.count, 3)
    }

    func testIngressReleaseExitsFinishedOperationOnceAndRejectsAfterStop() throws {
        var exits = 0
        var completions = 0
        let owner = CallbackLifecycle(schedule: { _, _ in }, exit: { exits += 1 })
        let finish = owner.beginOperation()
        let release = try XCTUnwrap(owner.reserveIngress())
        finish(ExitCode.failed)
        XCTAssertEqual(exits, 0)
        release(); release()
        XCTAssertEqual(exits, 1)
        XCTAssertEqual(owner.exitCode, ExitCode.failed)
        owner.dispatchIngress(completion: {
            completions += 1
            // Rejection must call the OS completion outside the ingress lock.
            XCTAssertNil(owner.reserveIngress())
        }) { XCTFail("started callback after stop") }
        XCTAssertEqual(completions, 1)
        XCTAssertEqual(exits, 1)
    }

    func testIngressBlocksCallbackOnlyIdleAndReleaseRestartsIdleOnce() throws {
        var timers: [() -> Void] = []
        var exits = 0
        let owner = CallbackLifecycle(schedule: { delay, work in
            XCTAssertEqual(delay, 10)
            timers.append(work)
        }, exit: { exits += 1 })
        owner.start()
        let release = try XCTUnwrap(owner.reserveIngress())
        timers[0]()
        XCTAssertEqual(exits, 0)
        owner.start()
        XCTAssertEqual(timers.count, 1)
        release(); release()
        XCTAssertEqual(timers.count, 2)
        timers[0]()
        XCTAssertEqual(exits, 0)
        timers[1](); timers[1]()
        XCTAssertEqual(exits, 1)
        XCTAssertNil(owner.reserveIngress())
    }

    func testCallbackAndChildDrainBeforeOperationDoNotExitEarly() {
        var exits = 0
        var callbackDone: ((CallbackOutcome) -> Void)?
        var release: (() -> Void)?
        let owner = CallbackLifecycle(schedule: { _, _ in }, exit: { exits += 1 })
        let finish = owner.beginOperation()
        owner.acceptOwned(completion: {}) { done, work in
            callbackDone = done
            release = work.own(cancel: {})
        }
        callbackDone?(.legacy_completed)
        XCTAssertEqual(owner.inFlight, 0)
        XCTAssertEqual(owner.ownedCount, 1)
        XCTAssertEqual(exits, 0)
        release?()
        XCTAssertEqual(owner.ownedCount, 0)
        XCTAssertEqual(exits, 0)
        finish(ExitCode.permissionDenied)
        XCTAssertEqual(exits, 1)
        XCTAssertEqual(owner.exitCode, ExitCode.permissionDenied)
    }

    func testOperationErrorWaitsForTimedOutCallbackAndChildReap() {
        var timers: [() -> Void] = []
        var events: [String] = []
        var release: (() -> Void)?
        var callbackDone: ((CallbackOutcome) -> Void)?
        let owner = CallbackLifecycle(schedule: { _, work in timers.append(work) }, exit: { events.append("exit") })
        let finish = owner.beginOperation()
        owner.acceptOwned(completion: { events.append("callback") }) { done, work in
            callbackDone = done
            release = work.own(cancel: { events.append("cancel") })
        }
        finish(ExitCode.failed)
        timers[0]() // Callback timeout cancels the child but does not prove reap.
        XCTAssertEqual(events, ["cancel", "callback"])
        XCTAssertEqual(owner.inFlight, 0)
        XCTAssertEqual(owner.ownedCount, 1)
        finish(ExitCode.success); callbackDone?(.legacy_completed)
        XCTAssertEqual(events.count, 2)
        release?(); release?()
        XCTAssertEqual(events, ["cancel", "callback", "exit"])
        XCTAssertEqual(owner.exitCode, ExitCode.failed)
    }

    func testSynchronousChildReapCannotExitBeforeCallbackCompletion() {
        var events: [String] = []
        var callbackDone: ((CallbackOutcome) -> Void)?
        var release: (() -> Void)?
        let owner = CallbackLifecycle(schedule: { _, _ in }, exit: { events.append("exit") })
        let finish = owner.beginOperation()
        owner.acceptOwned(completion: { events.append("callback") }) { done, work in
            callbackDone = done
            release = work.own(cancel: { events.append("reap"); release?() })
        }
        finish(ExitCode.failed)
        callbackDone?(.open_unknown)
        XCTAssertEqual(events, ["reap", "callback", "exit"])
        XCTAssertEqual(owner.ownedCount, 0)
    }

    private final class HeldBackend: NativeBackend {
        var added: ((Bool) -> Void)?
        func permission(_ completion: @escaping (NativePermission) -> Void) { completion(.allowed) }
        func add(_ request: NativeRequest, completion: @escaping (Bool) -> Void) { added = completion }
    }

    func testNativeReceiptPublishesOnceBeforeAcceptedCallbackDrain() throws {
        for timesOut in [false, true] {
            var events: [String] = []
            var callbackDone: ((CallbackOutcome) -> Void)?
            var receipts: [NativeReceipt] = []
            let owner = CallbackLifecycle(schedule: { _, _ in }, exit: { events.append("exit") })
            let finish = owner.beginOperation()
            let backend = HeldBackend()
            let delivery = StructuredDelivery(backend: backend, bootID: "boot", now: { 100 }) { receipt in
                receipts.append(receipt); events.append("receipt"); finish(ExitCode.success)
            }
            let correlation = "00000000-0000-4000-8000-000000000001"
            let nonce = "00000000-0000-4000-8000-000000000002"
            let request = NativeRequest(schemaVersion: 1, correlationID: correlation, nonce: nonce,
                bootID: "boot", notAfter: 110, title: "fixture", body: "body", subtitle: nil,
                category: .info, action: .none, silent: true)
            delivery.start(data: try JSONEncoder().encode(request), correlationID: correlation, nonce: nonce)
            owner.accept(completion: { events.append("callback") }) { done, _ in callbackDone = done }
            if timesOut { delivery.timeout() } else { backend.added?(true) }
            XCTAssertEqual(events, ["receipt"])
            XCTAssertEqual(receipts.first?.status, timesOut ? .unknown : .submitted)
            delivery.timeout(); backend.added?(false); backend.added?(true)
            XCTAssertEqual(receipts.count, 1)
            callbackDone?(.open_requested)
            XCTAssertEqual(events, ["receipt", "callback", "exit"])
        }
    }

}
