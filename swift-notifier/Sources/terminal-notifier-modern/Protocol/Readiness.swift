import Foundation

struct PermissionProbeRequest {
    let correlationID: String
    let nonce: String
    init(arguments: [String]) throws {
        guard arguments.count == 6, arguments[0] == "--capabilities-json",
              arguments[1] == "--permission-status", arguments[2] == "--correlation-id",
              arguments[4] == "--nonce",
              arguments[3].utf8.count == 36, UUID(uuidString: arguments[3]) != nil,
              arguments[5].utf8.count == 36, UUID(uuidString: arguments[5]) != nil else {
            throw NativeReason.malformed_request
        }
        correlationID = arguments[3]; nonce = arguments[5]
    }
}

struct PermissionEnvelope: Encodable {
    let schemaVersion = 1
    let correlationID: String
    let nonce: String
    let backend = "macos.usernotifications"
    let permission: String
}

// Independent of UN/AppKit. Timer and settings providers are injected; either
// may complete first or concurrently. Only one bounded response can be emitted.
final class PermissionProbe {
    private let lock = NSLock()
    private var finished = false
    private let request: PermissionProbeRequest
    private let emit: (Data) -> Void
    init(request: PermissionProbeRequest, emit: @escaping (Data) -> Void) {
        self.request = request; self.emit = emit
    }
    static func permission(raw: Int) -> String {
        switch raw {
        case 2, 3: return "allowed" // authorized, provisional
        case 0: return "undetermined"
        case 1: return "denied"
        default: return "unavailable"
        }
    }
    func start(settings: (@escaping (Int) -> Void) -> Void,
               timer: (@escaping () -> Void) -> Void) {
        timer { self.finish("unavailable") }
        lock.lock(); let done = finished; lock.unlock()
        if !done { settings { self.finish(Self.permission(raw: $0)) } }
    }
    private func finish(_ permission: String) {
        lock.lock()
        guard !finished else { lock.unlock(); return }
        finished = true
        lock.unlock()
        let envelope = PermissionEnvelope(correlationID: request.correlationID, nonce: request.nonce, permission: permission)
        guard var data = try? JSONEncoder().encode(envelope) else { return }
        data.append(10)
        emit(data)
    }
}
