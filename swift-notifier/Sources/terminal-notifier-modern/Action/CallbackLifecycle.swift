import Foundation

enum CallbackOutcome: String {
    case ignored, malformed_action, legacy_completed, open_requested, open_failed
    case application_missing, application_moved, application_ambiguous, identity_mismatch, open_unknown
}

// Workers only inspect this token, never the main-queue lifecycle dictionary.
final class CallbackToken {
    private let lock = NSLock()
    private var cancelled = false
    private let deadline: Double
    private let now: () -> Double
    init(deadline: Double = .infinity,
         now: @escaping () -> Double = ContinuousClock.now) {
        self.deadline = deadline; self.now = now
    }
    var isActive: Bool {
        lock.lock(); defer { lock.unlock() }
        return !cancelled && now() < deadline
    }
    func cancel() { lock.lock(); cancelled = true; lock.unlock() }
}

enum CallbackPhase: String {
    case preflight_started, preflight_finished, open_submitted, open_completed
}

struct CallbackMeasurement {
    enum Stage: String { case queue, discovery, verification }
    let stage: Stage
    let durationSeconds: Double
}

struct CallbackDiagnostic: Codable {
    let event: String
    let correlationID: UUID
    let outcome: String?
    var elapsedSeconds: Double? = nil
    var durationSeconds: Double? = nil
    var json: String {
        // Only fixed event/outcome enums, a UUID and monotonic elapsed time enter this encoder.
        String(decoding: try! JSONEncoder().encode(self), as: UTF8.self)
    }
}

// Main-queue contract: acquire ownership before starting a child; release only
// after reaping. Callback completion and owned-work drain are separate barriers.
final class CallbackWork {
    let token: CallbackToken
    private let acquire: (@escaping () -> Void) -> (() -> Void)
    private let phase: (CallbackPhase) -> Void
    private let measurement: (CallbackMeasurement) -> Void
    init(token: CallbackToken, phase: @escaping (CallbackPhase) -> Void = { _ in },
         measurement: @escaping (CallbackMeasurement) -> Void = { _ in },
         acquire: @escaping (@escaping () -> Void) -> (() -> Void)) {
        self.token = token; self.phase = phase; self.measurement = measurement; self.acquire = acquire
    }
    func recordPhase(_ value: CallbackPhase) { phase(value) }
    func recordMeasurement(_ value: CallbackMeasurement) { measurement(value) }
    func own(cancel: @escaping () -> Void) -> () -> Void { acquire(cancel) }
}

final class CallbackLifecycle {
    typealias Schedule = (Double, @escaping () -> Void) -> Void
    private let schedule: Schedule
    private let exit: () -> Void
    private let diagnostic: (String) -> Void
    private let now: () -> Double
    private struct Pending {
        let token: CallbackToken
        let correlation: UUID
        let started: Double
        let completion: () -> Void
    }
    private struct Owned { let callback: UUID; let cancel: () -> Void }
    private var owned: [UUID: Owned] = [:]
    private var generation = 0
    private var callbacks: [UUID: Pending] = [:]
    private var operationStarted = false
    private var operationPending = false
    private var finishingCallbacks = 0
    // Delegate ingress can arrive off-main before its main-queue work is accepted.
    // Only this reservation count and the stop claim cross that boundary.
    private let ingressLock = NSLock()
    private var pendingIngress = 0
    private var didStop = false
    private(set) var exitCode: Int32?
    var stopped: Bool {
        ingressLock.lock(); defer { ingressLock.unlock() }
        return didStop
    }
    var inFlight: Int { callbacks.count }
    var ownedCount: Int { owned.count }
    init(schedule: @escaping Schedule, exit: @escaping () -> Void,
         diagnostic: @escaping (String) -> Void = { _ in },
         now: @escaping () -> Double = ContinuousClock.now) {
        self.schedule = schedule; self.exit = exit; self.diagnostic = diagnostic; self.now = now
    }
    func start() { armIdle() }
    // Acquire on the delegate's incoming thread; release on main only after
    // receive has established callback ownership (or completed synchronously).
    func reserveIngress() -> (() -> Void)? {
        ingressLock.lock()
        guard !didStop else { ingressLock.unlock(); return nil }
        pendingIngress += 1
        ingressLock.unlock()
        var released = false
        return { [self] in
            ingressLock.lock()
            guard !released else { ingressLock.unlock(); return }
            released = true
            pendingIngress -= 1
            ingressLock.unlock()
            armIdle()
        }
    }
    func dispatchIngress(completion: @escaping () -> Void, operation: @escaping () -> Void) {
        guard let release = reserveIngress() else { completion(); return }
        let handle = {
            defer { release() }
            operation()
        }
        if Thread.isMainThread { handle() }
        else { DispatchQueue.main.async(execute: handle) }
    }
    // A finite send shares the callback owner. Its result can be published
    // immediately, but process exit must also wait for accepted callback work.
    // Starting invalidates any earlier callback-only idle timer; completion is
    // idempotent so late OS replies cannot replace a timeout/error exit status.
    func beginOperation() -> (Int32) -> Void {
        precondition(!operationStarted && !stopped)
        operationStarted = true
        operationPending = true
        generation += 1
        return { [weak self] code in
            guard let self = self, self.operationPending else { return }
            self.operationPending = false
            self.exitCode = code
            self.armIdle()
        }
    }
    func accept(completion: @escaping () -> Void,
                operation: (@escaping (CallbackOutcome) -> Void, @escaping () -> Bool) -> Void) {
        acceptOwned(completion: completion) { done, work in
            operation(done, { work.token.isActive })
        }
    }
    func acceptOwned(correlation: UUID = UUID(), budget: Double = 10, completion: @escaping () -> Void,
                     operation: (@escaping (CallbackOutcome) -> Void, CallbackWork) -> Void) {
        precondition(budget > 0 && budget.isFinite)
        let started = now()
        diagnostic(CallbackDiagnostic(event: "callback_received", correlationID: correlation,
                                      outcome: nil, elapsedSeconds: 0).json)
        guard !stopped else {
            diagnostic(CallbackDiagnostic(event: "callback_terminal", correlationID: correlation,
                                          outcome: CallbackOutcome.ignored.rawValue).json)
            completion(); return
        }
        generation += 1
        let id = UUID()
        let token = CallbackToken(deadline: started + budget, now: now)
        callbacks[id] = Pending(token: token, correlation: correlation, started: started, completion: completion)
        schedule(budget) { [weak self] in self?.finish(id, outcome: .open_unknown) }
        let work = CallbackWork(token: token, phase: { [weak self] phase in
            guard let self = self, token.isActive, self.callbacks[id] != nil else { return }
            self.diagnostic(CallbackDiagnostic(event: phase.rawValue, correlationID: correlation,
                outcome: nil, elapsedSeconds: max(0, self.now() - started)).json)
        }, measurement: { [weak self] value in
            guard let self = self, token.isActive, self.callbacks[id] != nil,
                  value.durationSeconds.isFinite, value.durationSeconds >= 0 else { return }
            self.diagnostic(CallbackDiagnostic(event: "preflight_" + value.stage.rawValue,
                correlationID: correlation, outcome: nil,
                durationSeconds: value.durationSeconds).json)
        }) { [self] cancel in
            let child = UUID()
            owned[child] = Owned(callback: id, cancel: cancel)
            generation += 1
            if !token.isActive { cancel() }
            return { [self] in
                guard owned.removeValue(forKey: child) != nil else { return }
                armIdle()
            }
        }
        operation({ [weak self] outcome in self?.finish(id, outcome: outcome) }, work)
    }
    private func finish(_ id: UUID, outcome: CallbackOutcome) {
        guard let pending = callbacks.removeValue(forKey: id) else { return }
        finishingCallbacks += 1
        let result = pending.token.isActive ? outcome : .open_unknown
        pending.token.cancel()
        // Snapshot permits a synchronous cancellation/reap seam to release ownership.
        let cancellations = owned.values.filter { $0.callback == id }.map { $0.cancel }
        cancellations.forEach { $0() }
        diagnostic(CallbackDiagnostic(event: "callback_terminal", correlationID: pending.correlation,
                                      outcome: result.rawValue,
                                      elapsedSeconds: max(0, now() - pending.started)).json)
        pending.completion()
        finishingCallbacks -= 1
        armIdle()
    }
    private func armIdle() {
        guard callbacks.isEmpty, owned.isEmpty, !operationPending,
              finishingCallbacks == 0, ingressAllowsIdle else { return }
        if exitCode != nil {
            claimExit()
            return
        }
        generation += 1
        let expected = generation
        schedule(10) { [weak self] in
            guard let self = self, self.generation == expected,
                  self.callbacks.isEmpty, self.owned.isEmpty,
                  !self.operationPending, self.finishingCallbacks == 0 else { return }
            self.claimExit()
        }
    }
    private var ingressAllowsIdle: Bool {
        ingressLock.lock(); defer { ingressLock.unlock() }
        return !didStop && pendingIngress == 0
    }
    private func claimExit() {
        ingressLock.lock()
        guard !didStop, pendingIngress == 0 else { ingressLock.unlock(); return }
        didStop = true
        ingressLock.unlock()
        exit()
    }
}
