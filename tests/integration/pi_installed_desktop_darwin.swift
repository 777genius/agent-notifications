// TEST observer for the installed Pi product. Never sends a notification,
// plays a cue or performs the product's focus callback. Ordinary OS consent
// uses only a physical click on the uniquely observed candidate Allow dialog.
// ScreenCaptureKit/CGEvent primitives match pi_native_preflight_darwin.swift.
import AppKit
import ApplicationServices
import AVFoundation
import CoreAudio
import CoreMedia
import ScreenCaptureKit

enum ProbeError: Error { case unavailable(String) }

func publish(_ value: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([10]))
}

func attribute(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
    var value: CFTypeRef?
    return AXUIElementCopyAttributeValue(element, name as CFString, &value) == .success ? value : nil
}

func rectangle(_ element: AXUIElement) -> CGRect? {
    guard let pos = attribute(element, kAXPositionAttribute), let size = attribute(element, kAXSizeAttribute),
          CFGetTypeID(pos) == AXValueGetTypeID(), CFGetTypeID(size) == AXValueGetTypeID() else { return nil }
    var point = CGPoint.zero
    var dimensions = CGSize.zero
    guard AXValueGetValue(pos as! AXValue, .cgPoint, &point),
          AXValueGetValue(size as! AXValue, .cgSize, &dimensions),
          point.x.isFinite, point.y.isFinite, dimensions.width > 0, dimensions.height > 0 else { return nil }
    return CGRect(origin: point, size: dimensions)
}

func onScreenWindows(_ pid: pid_t) -> [CGRect] {
    let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    return windows.compactMap { row in
        guard (row[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == pid,
              (row[kCGWindowAlpha as String] as? NSNumber)?.doubleValue ?? 0 > 0,
              let bounds = row[kCGWindowBounds as String] as? [String: Any],
              let x = bounds["X"] as? NSNumber, let y = bounds["Y"] as? NSNumber,
              let width = bounds["Width"] as? NSNumber, let height = bounds["Height"] as? NSNumber,
              width.doubleValue > 1, height.doubleValue > 1 else { return nil }
        return CGRect(x: CGFloat(x.doubleValue), y: CGFloat(y.doubleValue),
                      width: CGFloat(width.doubleValue), height: CGFloat(height.doubleValue))
    }
}

@MainActor func foreground() -> [String: Any] {
    guard let app = NSWorkspace.shared.frontmostApplication else { return [:] }
    var result: [String: Any] = ["pid": Int(app.processIdentifier), "bundle_id": app.bundleIdentifier ?? ""]
    if let window = attribute(AXUIElementCreateApplication(app.processIdentifier), kAXFocusedWindowAttribute) {
        let element = window as! AXUIElement
        if let number = attribute(element, "AXWindowNumber") as? NSNumber { result["window_number"] = number.intValue }
        if let box = rectangle(element) { result["window_bounds"] = [Double(box.minX), Double(box.minY), Double(box.width), Double(box.height)] }
    }
    return result
}

// Only the fixed candidate product copy may enter the receipt. Unrelated
// Notification Center text is never serialized, including in diagnostics.
@MainActor func banners(_ expected: String) -> [[String: Any]] {
    let applications = NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.notificationcenterui")
    var result: [[String: Any]] = []
    for app in applications {
        let root = AXUIElementCreateApplication(app.processIdentifier)
        let visible = onScreenWindows(app.processIdentifier)
        var remaining = 1600
        func visit(_ element: AXUIElement, _ depth: Int) {
            guard remaining > 0, depth < 20 else { return }
            remaining -= 1
            let text = [kAXValueAttribute, kAXTitleAttribute, kAXDescriptionAttribute].compactMap {
                attribute(element, $0) as? String
            }
            if text.contains(where: { $0.contains(expected) }), let box = rectangle(element),
               visible.contains(where: { $0.contains(CGPoint(x: box.midX, y: box.midY)) }) {
                result.append(["notification_center_pid": Int(app.processIdentifier), "fixed_body": expected,
                               "role": attribute(element, kAXRoleAttribute) as? String ?? "", "on_screen_owner_verified": true,
                               "bounds": [Double(box.minX), Double(box.minY), Double(box.width), Double(box.height)]])
            }
            for child in attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? [] { visit(child, depth + 1) }
        }
        visit(root, 0)
    }
    // Multiple AX attributes/ancestors can expose the same banner. The smallest
    // rectangle carrying its literal body is the physical click target.
    return result.sorted {
        let a = $0["bounds"] as! [Double], b = $1["bounds"] as! [Double]
        return a[2] * a[3] < b[2] * b[3]
    }
}

@MainActor func notificationConsent() -> [[String: Any]] {
    let allowed = Set(["com.apple.notificationcenterui", "com.apple.UserNotificationCenter",
                       "com.apple.usernotificationsd", "com.777genius.agent-notifications"])
    var result: [[String: Any]] = []
    for app in NSWorkspace.shared.runningApplications where allowed.contains(app.bundleIdentifier ?? "") {
        let root = AXUIElementCreateApplication(app.processIdentifier)
        let visible = onScreenWindows(app.processIdentifier)
        let windows = attribute(root, kAXWindowsAttribute) as? [AXUIElement] ?? [root]
        for window in windows {
            var labels = [String]()
            var allowButtons = [CGRect]()
            var remaining = 1200
            func visit(_ element: AXUIElement, _ depth: Int) {
                guard remaining > 0, depth < 20 else { return }
                remaining -= 1
                let text = [kAXValueAttribute, kAXTitleAttribute, kAXDescriptionAttribute].compactMap {
                    attribute(element, $0) as? String
                }
                labels.append(contentsOf: text)
                if attribute(element, kAXRoleAttribute) as? String == kAXButtonRole,
                   text.contains("Allow"), let box = rectangle(element),
                   visible.contains(where: { $0.contains(CGPoint(x: box.midX, y: box.midY)) }) { allowButtons.append(box) }
                for child in attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? [] { visit(child, depth + 1) }
            }
            visit(window, 0)
            let mentionsCandidate = labels.contains { $0.contains("Agent Notifications") }
            let mentionsNotification = labels.contains { $0.lowercased().contains("notification") }
            let hasDenial = labels.contains { $0 == "Don't Allow" || $0 == "Don’t Allow" }
            if mentionsCandidate && mentionsNotification && hasDenial && allowButtons.count == 1 {
                let box = allowButtons[0]
                result.append(["observer": "native_accessibility", "os_dialog_bundle": app.bundleIdentifier ?? "",
                               "os_dialog_pid": Int(app.processIdentifier), "candidate_name": "Agent Notifications",
                               "allow_button": "Allow", "denial_button_observed": true,
                               "on_screen_owner_verified": true,
                               "bounds": [Double(box.minX), Double(box.minY), Double(box.width), Double(box.height)]])
            }
        }
    }
    return result
}

final class ProductCapture: NSObject, SCStreamOutput, SCStreamDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private let output: FileHandle
    private var samples = 0
    private var frames = 0
    private var peak: Float = 0
    private var nonzero = 0
    private var sumSquares: Double = 0
    private var closed = false
    init(path: String) throws {
        FileManager.default.createFile(atPath: path, contents: nil)
        output = try FileHandle(forWritingTo: URL(fileURLWithPath: path))
    }
    func stream(_ stream: SCStream, didOutputSampleBuffer buffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard buffer.isValid else { return }
        lock.lock()
        defer { lock.unlock() }
        guard !closed else { return }
        if type == .screen { frames += 1; return }
        guard type == .audio, let description = buffer.formatDescription else { return }
        let format = AVAudioFormat(cmAudioFormatDescription: description)
        let count = AVAudioFrameCount(buffer.numSamples)
        guard let pcm = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: count) else { return }
        pcm.frameLength = count
        guard CMSampleBufferCopyPCMDataIntoAudioBufferList(buffer, at: 0, frameCount: Int32(count), into: pcm.mutableAudioBufferList) == noErr,
              let channels = pcm.floatChannelData else { return }
        var values = [Int16]()
        values.reserveCapacity(Int(count))
        for index in 0..<Int(count) {
            var mono: Float = 0
            for channel in 0..<Int(format.channelCount) {
                mono += format.isInterleaved ? channels[0][index * Int(format.channelCount) + channel] : channels[channel][index]
            }
            mono /= Float(format.channelCount)
            if !mono.isFinite { mono = 0 }
            peak = max(peak, abs(mono))
            if abs(mono) > 0.004 { nonzero += 1 }
            sumSquares += Double(mono * mono)
            values.append(Int16(max(-32767, min(32767, mono * 32767))).littleEndian)
        }
        samples += values.count
        values.withUnsafeBytes { output.write(Data($0)) }
    }
    func measurements() -> [String: Any] {
        lock.lock()
        defer { lock.unlock() }
        return ["audio_samples": samples, "audio_peak": Double(peak), "screen_frames": frames,
                "audio_nonzero_samples": nonzero, "audio_rms": samples > 0 ? sqrt(sumSquares / Double(samples)) : 0,
                "sample_rate": 48000, "channels": 1, "format": "s16le"]
    }
    func close() throws {
        lock.lock()
        defer { lock.unlock() }
        closed = true
        try output.close()
    }
}

@main struct PiInstalledDesktop {
    @MainActor static func execute() async {
        do {
            let args = Array(CommandLine.arguments.dropFirst())
            guard let operation = args.first else { throw ProbeError.unavailable("operation_missing") }
            switch operation {
            case "preflight":
                let session = CGSessionCopyCurrentDictionary() as? [String: Any] ?? [:]
                var device = AudioDeviceID(0)
                var size = UInt32(MemoryLayout<AudioDeviceID>.size)
                var address = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDefaultOutputDevice,
                    mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
                let status = AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &device)
                let checks = ["interactive_gui": session["kCGSSessionOnConsoleKey"] as? Bool == true
                              && session["CGSSessionScreenIsLocked"] as? Bool != true && NSScreen.main != nil,
                              "screen_permission": CGPreflightScreenCaptureAccess(),
                              "input_permission": AXIsProcessTrusted() && CGPreflightPostEventAccess(),
                              "audio_endpoint": status == noErr && device != kAudioObjectUnknown]
                publish(["checks": checks, "foreground": foreground(), "desktop_qualified": false])
                guard checks.values.allSatisfy({ $0 }) else { throw ProbeError.unavailable("native_observer_prerequisite_unavailable") }
            case "foreground": publish(foreground())
            case "helper-processes":
                guard args.count == 2, args[1].hasPrefix("/"), args[1].hasSuffix(".app") else {
                    throw ProbeError.unavailable("physical_installed_app_required")
                }
                let expected = URL(fileURLWithPath: args[1]).resolvingSymlinksInPath().path
                let pids = NSWorkspace.shared.runningApplications.filter {
                    $0.bundleIdentifier == "com.777genius.agent-notifications"
                    && $0.bundleURL?.resolvingSymlinksInPath().path == expected
                }.map { Int($0.processIdentifier) }
                publish(["installed_helper_pids": pids])
            case "notification-consent", "allow-notification-consent":
                guard args.count == 1 else { throw ProbeError.unavailable("fixed_notification_consent_only") }
                let dialogs = notificationConsent()
                if operation == "allow-notification-consent" {
                    guard dialogs.count == 1 else { throw ProbeError.unavailable("unique_candidate_notification_consent_dialog_required") }
                    let box = dialogs[0]["bounds"] as! [Double]
                    let point = CGPoint(x: box[0] + box[2] / 2, y: box[1] + box[3] / 2)
                    CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)
                    CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)
                }
                publish(["dialogs": dialogs, "physical_allow_click": operation == "allow-notification-consent"])
            case "banner", "click":
                guard args.count == 2, ["Pi completed a task", "Pi is waiting for input"].contains(args[1]) else {
                    throw ProbeError.unavailable("fixed_product_body_required")
                }
                let observed = banners(args[1])
                if operation == "click" {
                    guard let first = observed.first else { throw ProbeError.unavailable("actual_product_banner_missing_at_click") }
                    let box = first["bounds"] as! [Double]
                    let point = CGPoint(x: box[0] + box[2] / 2, y: box[1] + box[3] / 2)
                    CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)
                    CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)
                }
                publish(["matches": observed, "physical_mouse_click": operation == "click", "foreground": foreground()])
            case "type":
                guard args.count == 2, args[1].utf16.count <= 200,
                      NSWorkspace.shared.frontmostApplication?.bundleIdentifier == "com.apple.Terminal" else {
                    throw ProbeError.unavailable("owned_foreground_terminal_required")
                }
                let units = Array(args[1].utf16)
                let down = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: true)
                units.withUnsafeBufferPointer { down?.keyboardSetUnicodeString(stringLength: units.count, unicodeString: $0.baseAddress!) }
                down?.post(tap: .cghidEventTap)
                let up = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: false)
                units.withUnsafeBufferPointer { up?.keyboardSetUnicodeString(stringLength: units.count, unicodeString: $0.baseAddress!) }
                up?.post(tap: .cghidEventTap)
                CGEvent(keyboardEventSource: nil, virtualKey: 36, keyDown: true)?.post(tap: .cghidEventTap)
                CGEvent(keyboardEventSource: nil, virtualKey: 36, keyDown: false)?.post(tap: .cghidEventTap)
                publish(["physical_keyboard_input": true])
            case "capture":
                guard args.count == 2 else { throw ProbeError.unavailable("capture_root_required") }
                let root = URL(fileURLWithPath: args[1])
                let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
                guard let display = content.displays.first else { throw ProbeError.unavailable("capture_display_missing") }
                let observer = try ProductCapture(path: root.appendingPathComponent("native-output.s16le").path)
                let config = SCStreamConfiguration()
                config.width = 640; config.height = 400
                config.minimumFrameInterval = CMTime(value: 1, timescale: 15)
                config.capturesAudio = true; config.excludesCurrentProcessAudio = false
                config.sampleRate = 48000; config.channelCount = 2
                let stream = SCStream(filter: SCContentFilter(display: display, excludingWindows: []), configuration: config, delegate: observer)
                let queue = DispatchQueue(label: "TEST.pi.product.capture")
                try stream.addStreamOutput(observer, type: .screen, sampleHandlerQueue: queue)
                try stream.addStreamOutput(observer, type: .audio, sampleHandlerQueue: queue)
                try await stream.startCapture()
                try await Task.sleep(nanoseconds: 300_000_000)
                let baseline = observer.measurements()
                try JSONSerialization.data(withJSONObject: baseline).write(to: root.appendingPathComponent("capture-ready.json"), options: .atomic)
                let deadline = Date().addingTimeInterval(30)
                while !FileManager.default.fileExists(atPath: root.appendingPathComponent("capture-stop").path) && Date() < deadline {
                    try await Task.sleep(nanoseconds: 50_000_000)
                }
                try await stream.stopCapture()
                try observer.close()
                publish(["baseline": baseline, "measurements": observer.measurements()])
            default: throw ProbeError.unavailable("unknown_operation")
            }
            exit(0)
        } catch {
            publish(["failure": String(describing: error), "desktop_qualified": false])
            exit(2)
        }
    }
    @MainActor static func main() {
        let app = NSApplication.shared
        app.setActivationPolicy(.prohibited)
        Task { @MainActor in await execute() }
        app.run()
    }
}
