import AppKit
import Darwin
import UserNotifications

private enum ContinuousClock {
    static func now() -> Double {
        var info = mach_timebase_info_data_t()
        mach_timebase_info(&info)
        return Double(mach_continuous_time()) * Double(info.numer) / Double(info.denom) / 1_000_000_000
    }
    static func bootID() -> String? {
        var size = 0
        guard sysctlbyname("kern.bootsessionuuid", nil, &size, nil, 0) == 0, size > 1, size <= 256 else { return nil }
        var bytes = [CChar](repeating: 0, count: size)
        guard sysctlbyname("kern.bootsessionuuid", &bytes, &size, nil, 0) == 0 else { return nil }
        return String(cString: bytes)
    }
}

private final class StructuredUNBackend: NativeBackend {
    private let center = UNUserNotificationCenter.current()
    func permission(_ completion: @escaping (NativePermission) -> Void) {
        center.getNotificationSettings { settings in
            DispatchQueue.main.async {
                switch settings.authorizationStatus {
                case .authorized, .provisional: completion(.allowed)
                case .notDetermined: completion(.undetermined)
                default: completion(.denied)
                }
            }
        }
    }
    func add(_ request: NativeRequest, completion: @escaping (Bool) -> Void) {
        let content: UNMutableNotificationContent
        do { content = try StructuredNotificationContent.make(request) }
        catch { completion(false); return }
        if case .desktopThread = request.action { NotificationCategory.register() }
        center.add(UNNotificationRequest(identifier: request.correlationID, content: content, trigger: nil)) { error in
            DispatchQueue.main.async { completion(error == nil) }
        }
    }
}

enum StructuredNotificationContent {
    static func make(_ request: NativeRequest) throws -> UNMutableNotificationContent {
        let content = UNMutableNotificationContent()
        content.title = request.title
        content.body = request.body
        content.subtitle = request.subtitle ?? ""
        content.sound = request.silent ? nil : .default
        if case .desktopThread(let action) = request.action {
            try action.validate()
            guard action.correlationID == request.correlationID else { throw NativeReason.malformed_request }
            guard let data = try? JSONEncoder().encode(action),
                  let json = String(data: data, encoding: .utf8) else { throw NativeReason.malformed_request }
            content.userInfo = ["desktop_thread_v1": json]
            content.categoryIdentifier = NotificationCategory.categoryIdentifier
        }
        return content
    }
}

enum StructuredRuntime {
    static func requestPermission(arguments: [String]) -> Never {
        // Parse completely before AppKit or UN access, even for mixed modes.
        guard let request = try? PermissionSetupRequest(arguments: arguments) else { exit(1) }
        let deadline = ContinuousClock.now() + 120
        let setup = PermissionSetup(request: request, expired: { ContinuousClock.now() >= deadline }) { data in
            FileHandle.standardOutput.write(data)
            exit(0)
        }
        guard let identifier = Bundle.main.bundleIdentifier, !identifier.isEmpty,
              Bundle.main.bundleURL.pathExtension == "app",
              Bundle.main.object(forInfoDictionaryKey: "CFBundlePackageType") as? String == "APPL" else {
            setup.timeout()
            exit(1)
        }
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        // Continuous time also bounds suspension. The deadline does not cancel
        // an OS authorization UI already dispatched, or prove its grant outcome.
        let timer = Timer(timeInterval: 0.1, repeats: true) { _ in
            if ContinuousClock.now() >= deadline { setup.timeout() }
        }
        RunLoop.main.add(timer, forMode: .common)
        DispatchQueue.main.async {
            setup.start(settings: { completion in
                UNUserNotificationCenter.current().getNotificationSettings { settings in
                    DispatchQueue.main.async { completion(settings.authorizationStatus.rawValue) }
                }
            }, authorize: { completion in
                UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, error in
                    DispatchQueue.main.async { completion(granted, error) }
                }
            }, timer: { _ in /* Main run loop timer installed before work dispatch. */ })
        }
        withExtendedLifetime(setup) { app.run() }
        exit(1)
    }

    static func capabilities(arguments: [String]) -> Never {
        if arguments == ["--capabilities-json", "--setup"] {
            guard let data = try? PermissionSetupCapabilities.encode(arguments: arguments) else { exit(1) }
            FileHandle.standardOutput.write(data)
            exit(0)
        }
        if arguments != ["--capabilities-json"] {
            guard let request = try? PermissionProbeRequest(arguments: arguments),
                  Bundle.main.bundleIdentifier != nil,
                  Bundle.main.bundleURL.pathExtension == "app" else { exit(1) }
            let probe = PermissionProbe(request: request) { data in
                FileHandle.standardOutput.write(data)
                exit(0)
            }
            probe.start(settings: { completion in
                UNUserNotificationCenter.current().getNotificationSettings { settings in
                    completion(settings.authorizationStatus.rawValue)
                }
            }, timer: { timeout in
                DispatchQueue.global().asyncAfter(deadline: .now() + 1, execute: timeout)
            })
            dispatchMain()
        }
        guard arguments == ["--capabilities-json"],
              let data = try? JSONEncoder().encode(NativeCapabilities()) else { exit(1) }
        FileHandle.standardOutput.write(data)
        FileHandle.standardOutput.write(Data([10]))
        exit(0)
    }

    static func send(arguments: [String]) -> Never {
        // Fixed grammar: no data-bearing argument can select another mode.
        var paths: [String: String] = [:]
        var i = 0
        var sendCount = 0
        var launchCount = 0
        while i < arguments.count {
            switch arguments[i] {
            case "--send-json": sendCount += 1
            case "-launchedViaLaunchServices": launchCount += 1
            case "--request-file", "--receipt-file":
                let key = arguments[i]
                guard i + 1 < arguments.count, paths[key] == nil else { exit(1) }
                i += 1; paths[key] = arguments[i]
            default: exit(1)
            }
            i += 1
        }
        guard sendCount == 1, launchCount <= 1,
              let requestPath = paths["--request-file"], let receiptPath = paths["--receipt-file"],
              let files = try? OwnedRequest(requestPath: requestPath, receiptPath: receiptPath) else { exit(1) }
        func emit(_ receipt: NativeReceipt) -> Never {
            do { try files.write(receipt); exit(0) }
            catch { exit(1) } // no receipt is an unknown handoff, never a retry signal
        }
        let data: Data
        do { data = try files.consume() }
        catch { emit(NativeReceipt(correlationID: files.correlationID, nonce: files.nonce,
                                  status: .rejected, reason: .invalid_file)) }
        // Validate before initializing AppKit / UN (including bundle guard).
        let request: NativeRequest
        do {
            request = try NativeCodec.decodeRequest(data)
            guard request.correlationID == files.correlationID, request.nonce == files.nonce else {
                throw NativeReason.malformed_request
            }
        } catch {
            emit(NativeReceipt(correlationID: files.correlationID, nonce: files.nonce,
                               status: .rejected, reason: (error as? NativeReason) ?? .malformed_request))
        }
        guard launchCount == 1, Bundle.main.bundleIdentifier != nil,
              Bundle.main.bundleURL.pathExtension == "app", let boot = ContinuousClock.bootID() else {
            emit(NativeReceipt(correlationID: files.correlationID, nonce: files.nonce,
                               status: .rejected, reason: .unsupported_notifier))
        }
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        let delivery = StructuredDelivery(backend: StructuredUNBackend(), bootID: boot,
                                          now: ContinuousClock.now, emit: { emit($0) })
        // Short polling also catches suspend: continuous clock, not a fresh launch budget.
        let timer = Timer(timeInterval: 0.05, repeats: true) { _ in
            if ContinuousClock.now() >= request.notAfter {
                delivery.timeout()
            }
        }
        RunLoop.main.add(timer, forMode: .common)
        DispatchQueue.main.async { delivery.start(data: data, correlationID: files.correlationID, nonce: files.nonce) }
        withExtendedLifetime(delivery) { app.run() }
        exit(1)
    }
}
