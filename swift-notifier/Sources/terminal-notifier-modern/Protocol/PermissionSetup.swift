import Foundation

struct PermissionSetupCapabilities: Encodable {
    let schemaVersion = 1
    let permissionRequestVersions = [1]
    let backend = "macos.usernotifications"

    static func encode(arguments: [String]) throws -> Data {
        guard arguments == ["--capabilities-json", "--setup"] else {
            throw NativeReason.malformed_request
        }
        var data = try JSONEncoder().encode(Self())
        data.append(10)
        return data
    }
}

struct PermissionSetupRequest {
    let correlationID: String
    let nonce: String

    init(arguments: [String]) throws {
        // LaunchServices may append its transport marker; it is never a value.
        var args = arguments
        if args.last == "-launchedViaLaunchServices" { args.removeLast() }
        guard args.count == 5, args[0] == "--request-permission-json",
              args[1] == "--correlation-id", args[3] == "--nonce",
              args[2].utf8.count == 36, UUID(uuidString: args[2]) != nil,
              args[4].utf8.count == 36, UUID(uuidString: args[4]) != nil else {
            throw NativeReason.malformed_request
        }
        correlationID = args[2]; nonce = args[4]
    }

    // Extend option-position scanning locally without changing legacy parsing.
    // In particular, UUID slots cannot select capabilities/send/setup modes.
    static func optionPositions(_ arguments: [String]) -> [String] {
        var filtered = [String]()
        var i = 0
        let valued: Set<String> = ["--correlation-id", "--nonce", "-title", "-message",
                                   "-subtitle", "-activate", "-execute", "-group", "-threadID",
                                   "--request-file", "--receipt-file"]
        while i < arguments.count {
            let option = arguments[i]
            filtered.append(option)
            i += valued.contains(option) ? 2 : 1
        }
        return filtered
    }
}

// No notification backend is accepted here. Providers only read settings or
// request authorization; they must dispatch promptly and never wait for callbacks.
// The recursive lock admits synchronous test/provider callbacks and linearizes
// the authorization dispatch with timeout, including concurrently arriving events.
final class PermissionSetup {
    private enum Phase { case idle, settings, authorization, finished }
    private let lock = NSRecursiveLock()
    private var phase = Phase.idle
    private let request: PermissionSetupRequest
    private let expired: () -> Bool
    private let emit: (Data) -> Void

    init(request: PermissionSetupRequest, expired: @escaping () -> Bool,
         emit: @escaping (Data) -> Void) {
        self.request = request; self.expired = expired; self.emit = emit
    }

    func start(settings: (@escaping (Int?) -> Void) -> Void,
               authorize: @escaping (@escaping (Bool, Error?) -> Void) -> Void,
               timer: (@escaping () -> Void) -> Void) {
        lock.lock(); defer { lock.unlock() }
        guard phase == .idle else { return }
        phase = .settings
        timer { self.timeout() }
        guard phase == .settings else { return }
        guard !expired() else { finish("unavailable"); return }
        settings { raw in
            self.lock.lock(); defer { self.lock.unlock() }
            guard self.phase == .settings else { return }
            guard !self.expired() else { self.finish("unavailable"); return }
            let permission = raw.map(PermissionProbe.permission(raw:)) ?? "unavailable"
            guard permission == "undetermined" else { self.finish(permission); return }
            self.phase = .authorization
            authorize { granted, error in
                self.lock.lock(); defer { self.lock.unlock() }
                guard self.phase == .authorization else { return }
                self.finish(self.expired() || error != nil ? "unavailable" : (granted ? "allowed" : "denied"))
            }
        }
    }

    func timeout() {
        lock.lock(); defer { lock.unlock() }
        finish("unavailable")
    }

    // Called with the lock held. Encoding this fixed string envelope cannot fail.
    private func finish(_ permission: String) {
        guard phase != .finished else { return }
        phase = .finished
        let envelope = PermissionEnvelope(correlationID: request.correlationID,
                                          nonce: request.nonce, permission: permission)
        guard var data = try? JSONEncoder().encode(envelope) else { return }
        data.append(10)
        emit(data)
    }
}
