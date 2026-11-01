import AppKit
import Darwin
import Foundation
import UserNotifications

// Only this opt-in mode changes Pi's context delivery. Legacy notification and
// explicit setup authorization retain their existing command contracts.
struct ContextNativeProcess: Codable, Equatable {
    let pid: Int32
    let startSeconds: UInt64
    let startMicroseconds: UInt64

    static func read(_ pid: Int32) -> Self? {
        var info = proc_bsdinfo()
        let size = Int32(MemoryLayout<proc_bsdinfo>.size)
        guard pid > 0, proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, size) == size,
              info.pbi_pid == UInt32(pid), info.pbi_start_tvsec > 0,
              info.pbi_start_tvusec < 1_000_000 else { return nil }
        return Self(pid: pid, startSeconds: info.pbi_start_tvsec, startMicroseconds: info.pbi_start_tvusec)
    }

    var valid: Bool { pid > 0 && startSeconds > 0 && startMicroseconds < 1_000_000 }
}

struct ContextNotificationRequest: Decodable {
    let schemaVersion: Int
    let correlationID: String
    let nonce: String
    let bootID: String
    let notAfter: Double
    let owner: ContextNativeProcess
    let arguments: [String]

    static func decode(_ data: Data) throws -> Self {
        let object = try StrictJSON.object(data)
        guard Set(object.keys) == Set(["schemaVersion", "correlationID", "nonce", "bootID", "notAfter", "owner", "arguments"]),
              let owner = object["owner"] as? [String: Any],
              Set(owner.keys) == Set(["pid", "startSeconds", "startMicroseconds"]) else { throw NativeReason.malformed_request }
        let request = try JSONDecoder().decode(Self.self, from: data)
        guard request.schemaVersion == 1, UUID(uuidString: request.correlationID) != nil,
              UUID(uuidString: request.nonce) != nil, !request.bootID.isEmpty,
              request.bootID.utf8.count <= 256, request.notAfter.isFinite, request.notAfter > 0,
              request.owner.valid, request.arguments.count <= 64 else { throw NativeReason.malformed_request }
        // Arguments are trusted host output, but cannot select another native
        // mode. Values remain literal, including text that looks like a flag.
        let options = ArgumentParser.optionPositions(request.arguments)
        let allowed: Set<String> = ["-title", "-message", "-subtitle", "-activate", "-execute", "-group", "-threadID", "-nosound", "-soundFile", "-soundVolume"]
        guard Set(options).isSubset(of: allowed), options.count == Set(options).count,
              options.contains("-title"), options.contains("-message") else { throw NativeReason.malformed_request }
        for argument in request.arguments { try NativeCodec.text(argument, limit: 4096, multiline: true) }
        _ = try ArgumentParser.parse(request.arguments)
        return request
    }
}

// Serialized on the main queue. Rechecking active immediately before the
// backend submission prevents a delayed permission callback from sending after
// owner exit, cancellation, sleep or the original continuous deadline.
final class ContextNotification {
    private let active: () -> Bool
    private let emit: (NativeStatus, NativeReason) -> Void
    private var started = false
    private var submitting = false
    private var finished = false

    init(active: @escaping () -> Bool, emit: @escaping (NativeStatus, NativeReason) -> Void) {
        self.active = active; self.emit = emit
    }

    func start(permission: (@escaping (Bool) -> Void) -> Void,
               send: @escaping (@escaping () -> Bool, @escaping (Bool) -> Void) -> Void) {
        guard !finished && !started else { return }
        started = true
        guard active() else { finish(.rejected, .expired); return }
        permission { allowed in
            guard !self.finished && !self.submitting else { return }
            guard self.active() else { self.finish(.rejected, .expired); return }
            guard allowed else { self.finish(.rejected, .permission_denied); return }
            self.submitting = true
            send({ !self.finished && self.active() }) { accepted in
                guard !self.finished else { return }
                guard self.active() else { self.finish(.unknown, .timeout); return }
                self.finish(accepted ? .submitted : .rejected, accepted ? .os_accepted : .os_rejected)
            }
        }
    }

    func poll() {
        if !finished && !active() {
            // Once submission starts the OS may already own the request.
            // Expiry cannot claim suppression or authorize a retry.
            finish(submitting ? .unknown : .rejected, submitting ? .timeout : .expired)
        }
    }

    private func finish(_ status: NativeStatus, _ reason: NativeReason) {
        guard !finished else { return }
        finished = true
        emit(status, reason)
    }
}

enum ContextNotificationRuntime {
    static func capabilities() -> Never {
        FileHandle.standardOutput.write(Data("{\"schemaVersion\":1,\"contextNotificationVersions\":[1]}\n".utf8))
        exit(0)
    }

    static func send(arguments: [String]) -> Never {
        guard arguments.count == 6, arguments[0] == "--send-context-json",
              arguments[1] == "--request-file", arguments[3] == "--receipt-file",
              arguments[5] == "-launchedViaLaunchServices",
              let files = try? OwnedRequest(requestPath: arguments[2], receiptPath: arguments[4]),
              let data = try? files.consume(), let request = try? ContextNotificationRequest.decode(data),
              request.correlationID == files.correlationID, request.nonce == files.nonce,
              Bundle.main.bundleIdentifier != nil, Bundle.main.bundleURL.pathExtension == "app",
              let process = ContextNativeProcess.read(getpid()),
              let config = try? ArgumentParser.parse(request.arguments) else { exit(1) }
        let active = {
            let now = ContinuousClock.now()
            return ContinuousClock.bootID() == request.bootID && now < request.notAfter &&
                request.notAfter - now <= 15 && files.contextIsLive() &&
                ContextNativeProcess.read(request.owner.pid) == request.owner
        }
        guard active() else { exit(0) }
        struct Started: Encodable {
            let schemaVersion = 1
            let correlationID: String
            let nonce: String
            let process: ContextNativeProcess
        }
        guard let started = try? JSONEncoder().encode(Started(correlationID: request.correlationID, nonce: request.nonce, process: process)),
              (try? files.writeContextStarted(started)) != nil, active() else { exit(0) }
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        let delegate = AppDelegate.install(on: app)
        let finish = delegate.lifecycle.beginOperation()
        let delivery = ContextNotification(active: active) { status, reason in
            try? files.write(NativeReceipt(correlationID: request.correlationID, nonce: request.nonce, status: status, reason: reason))
            // Reuse the existing callback owner so a click already accepted
            // during submission drains its owned work. With no click this
            // exits immediately; future clicks relaunch callback mode.
            finish(0)
        }
        let timer = Timer(timeInterval: 0.02, repeats: true) { _ in delivery.poll() }
        RunLoop.main.add(timer, forMode: .common)
        DispatchQueue.main.async {
            delivery.start(permission: { completed in
                // No event path requests authorization. Setup owns that choice.
                UNUserNotificationCenter.current().getNotificationSettings { settings in
                    DispatchQueue.main.async {
                        completed(settings.authorizationStatus == .authorized || settings.authorizationStatus == .provisional)
                    }
                }
            }, send: { admitted, completed in
                NotificationCategory.register()
                UNNotificationService().send(config: config, admitted: admitted) { result in
                    DispatchQueue.main.async { if case .success = result { completed(true) } else { completed(false) } }
                }
            })
        }
        withExtendedLifetime((delivery, timer, delegate)) { app.run() }
        exit(1)
    }
}
