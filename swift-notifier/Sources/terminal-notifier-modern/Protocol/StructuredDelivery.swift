import Foundation

enum NativePermission { case allowed, denied, undetermined }
protocol NativeBackend {
    func permission(_ completion: @escaping (NativePermission) -> Void)
    func add(_ request: NativeRequest, completion: @escaping (Bool) -> Void)
}

// All callbacks, including timeout(), must be serialized by the owner. No OS
// calls occur until validation; timeout after handoff is never a safe retry.
final class StructuredDelivery {
    private let backend: NativeBackend
    private let now: () -> Double
    private let bootID: String
    private let emit: (NativeReceipt) -> Void
    private var request: NativeRequest?
    private var handedOff = false
    private var finished = false

    init(backend: NativeBackend, bootID: String, now: @escaping () -> Double,
         emit: @escaping (NativeReceipt) -> Void) {
        self.backend = backend; self.bootID = bootID; self.now = now; self.emit = emit
    }

    func start(data: Data, correlationID: String, nonce: String) {
        guard request == nil, !finished else { return }
        do {
            let parsed = try NativeCodec.decodeRequest(data)
            guard parsed.correlationID == correlationID, parsed.nonce == nonce else {
                throw NativeReason.malformed_request
            }
            request = parsed
            guard validDeadline(parsed) else { finish(.rejected, .expired); return }
            backend.permission { [self] permission in
                guard !finished, !handedOff else { return }
                guard validDeadline(parsed) else { finish(.rejected, .expired); return }
                switch permission {
                case .denied: finish(.rejected, .permission_denied)
                case .undetermined: finish(.rejected, .activation_required)
                case .allowed:
                    handedOff = true
                    backend.add(parsed) { [self] accepted in
                        guard !finished else { return }
                        guard validDeadline(parsed) else { finish(.unknown, .timeout); return }
                        finish(accepted ? .submitted : .rejected, accepted ? .os_accepted : .os_rejected)
                    }
                }
            }
        } catch {
            finished = true
            emit(NativeReceipt(correlationID: correlationID, nonce: nonce, status: .rejected,
                               reason: (error as? NativeReason) ?? .malformed_request))
        }
    }

    func timeout() { finish(handedOff ? .unknown : .rejected, handedOff ? .timeout : .expired) }
    private func validDeadline(_ request: NativeRequest) -> Bool {
        let remaining = request.notAfter - now()
        return request.bootID == bootID && remaining > 0 && remaining <= 15
    }
    private func finish(_ status: NativeStatus, _ reason: NativeReason) {
        guard !finished, let request = request else { return }
        finished = true
        emit(NativeReceipt(correlationID: request.correlationID, nonce: request.nonce,
                           status: status, reason: reason))
    }
}
