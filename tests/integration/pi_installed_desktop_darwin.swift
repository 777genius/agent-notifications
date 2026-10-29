// TEST observer for the installed Pi product. Never sends a notification,
// plays a cue or performs the product's focus callback. Ordinary OS consent
// uses only a physical click on the uniquely observed candidate Allow dialog.
// ScreenCaptureKit/CGEvent primitives match pi_native_preflight_darwin.swift.
import AppKit
import ApplicationServices
import AVFoundation
import CoreAudio
import CoreMedia
import Darwin
import ScreenCaptureKit

enum ProbeError: Error { case unavailable(String) }

// NSRunningApplication.launchDate is unavailable for this normal direct child.
// The kernel birth time binds both observations without depending on LaunchServices.
func processStartIdentity(_ pid: pid_t) -> String? {
    guard pid > 0 else { return nil }
    var info = proc_bsdinfo()
    let size = Int32(MemoryLayout<proc_bsdinfo>.size)
    guard proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, size) == size,
          info.pbi_pid == UInt32(pid), info.pbi_start_tvsec > 0,
          info.pbi_start_tvusec < 1_000_000 else { return nil }
    return "\(info.pbi_start_tvsec):\(info.pbi_start_tvusec)"
}

func matchesProcessStart(_ pid: pid_t, _ expected: String) -> Bool {
    return !expected.isEmpty && processStartIdentity(pid) == expected
}

// Direct Terminal children are not NSRunningApplications. Keep their cleanup
// bound to the original kernel birth and executable, including on failed setup.
func terminalProcessIdentity(_ pid: pid_t, expectedStart: String? = nil) throws -> [String: Any] {
    guard pid > 0 else { throw ProbeError.unavailable("owned_terminal_pid_required") }
    guard let start = processStartIdentity(pid) else {
        if kill(pid, 0) == -1 && errno == ESRCH { return ["status": "absent", "pid": Int(pid)] }
        throw ProbeError.unavailable("terminal_process_identity_unverified")
    }
    if let expectedStart, start != expectedStart { return ["status": "replaced", "pid": Int(pid)] }
    // Darwin's 4*MAXPATHLEN C macro is not imported into Swift by this SDK.
    var path = [CChar](repeating: 0, count: 4096)
    let count = path.withUnsafeMutableBytes { proc_pidpath(pid, $0.baseAddress, UInt32($0.count)) }
    var info = proc_bsdinfo()
    let size = Int32(MemoryLayout<proc_bsdinfo>.size)
    guard count > 0, Int(count) < path.count, path.contains(0),
          proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, size) == size,
          info.pbi_pid == UInt32(pid), "\(info.pbi_start_tvsec):\(info.pbi_start_tvusec)" == start,
          matchesProcessStart(pid, start) else {
        throw ProbeError.unavailable("terminal_process_identity_changed_or_unverified")
    }
    let executable = URL(fileURLWithPath: String(cString: path)).resolvingSymlinksInPath().path
    return ["status": "observed", "pid": Int(pid), "parent_pid": Int(info.pbi_ppid),
            "process_start_identity": start, "executable_path": executable]
}

func signalOwnedTerminalProcess(_ pid: pid_t, start: String, executable: String, signal: Int32) throws -> [String: Any] {
    let identity = try terminalProcessIdentity(pid, expectedStart: start)
    if ["absent", "replaced"].contains(identity["status"] as? String ?? "") {
        return ["signal_sent": false, "original_process_collected": true]
    }
    guard identity["executable_path"] as? String == executable, matchesProcessStart(pid, start) else {
        throw ProbeError.unavailable("owned_terminal_process_identity_mismatch")
    }
    let result = kill(pid, signal)
    guard result == 0 || errno == ESRCH else { throw ProbeError.unavailable("owned_terminal_process_signal_failed") }
    return ["signal_sent": result == 0, "original_process_collected": result != 0]
}

func testDirectChildIdentity() throws -> [String: Any] {
    let child = Process()
    child.executableURL = URL(fileURLWithPath: "/bin/sleep")
    child.arguments = ["20"]
    try child.run()
    defer {
        if child.isRunning { kill(child.processIdentifier, SIGKILL) }
        child.waitUntilExit()
    }
    let pid = child.processIdentifier
    guard let identity = processStartIdentity(pid), matchesProcessStart(pid, identity),
          !matchesProcessStart(pid, "1:0") else {
        throw ProbeError.unavailable("direct_child_kernel_identity_unavailable_or_unstable")
    }
    let observed = try terminalProcessIdentity(pid)
    let executable = URL(fileURLWithPath: "/bin/sleep").resolvingSymlinksInPath().path
    guard observed["executable_path"] as? String == executable,
          observed["parent_pid"] as? Int == Int(getpid()),
          try signalOwnedTerminalProcess(pid, start: "1:0", executable: executable, signal: SIGTERM)["signal_sent"] as? Bool == false,
          child.isRunning else { throw ProbeError.unavailable("mismatched_terminal_birth_signalled") }
    do {
        _ = try signalOwnedTerminalProcess(pid, start: identity, executable: "/TEST/foreign", signal: SIGKILL)
        throw ProbeError.unavailable("mismatched_terminal_path_signalled")
    } catch ProbeError.unavailable("owned_terminal_process_identity_mismatch") { }
    guard child.isRunning,
          try signalOwnedTerminalProcess(pid, start: identity, executable: executable, signal: SIGTERM)["signal_sent"] as? Bool == true else {
        throw ProbeError.unavailable("owned_terminal_child_not_signalled")
    }
    child.waitUntilExit()
    guard !matchesProcessStart(pid, identity),
          try signalOwnedTerminalProcess(pid, start: identity, executable: executable, signal: SIGKILL)["signal_sent"] as? Bool == false else {
        throw ProbeError.unavailable("terminated_child_kernel_identity_still_matched")
    }
    return ["scope": "native_direct_child_kernel_identity_only", "stable_identity": true,
            "mismatched_identity_rejected": true, "reaped_child_identity_rejected": true,
            "terminal_birth_and_path_signal_guards": true,
            "child_collected": !child.isRunning, "desktop_qualified": false]
}

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
              ((row[kCGWindowAlpha as String] as? NSNumber)?.doubleValue ?? 0) > 0,
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

// The first macOS authorization UI can be a collapsed Notification Center
// card. Scope both its display identity and controls to the smallest AX subtree
// carrying the exact title/body; unrelated cards never supply an Allow button.
@MainActor func notificationPermissionCards(_ names: [String]) -> [[String: Any]] {
    let body = "Notifications may include alerts, sounds, and icon badges."
    var result = [[String: Any]]()
    for app in NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.notificationcenterui") {
        let root = AXUIElementCreateApplication(app.processIdentifier)
        let visible = onScreenWindows(app.processIdentifier)
        var remaining = 1200
        var capped = false
        let firstCard = result.count
        typealias Contents = (names: Set<String>, titles: Int, bodies: Int, allows: [CGRect], denials: [CGRect], options: [CGRect])
        func visit(_ element: AXUIElement, _ depth: Int) -> Contents {
            guard remaining > 0, depth < 20 else { capped = true; return ([], 0, 0, [], [], []) }
            remaining -= 1
            let text = [kAXValueAttribute, kAXTitleAttribute, kAXDescriptionAttribute].compactMap {
                attribute(element, $0) as? String
            }
            var candidate = Set(names.filter { name in
                text.contains("“" + name + "” Notifications") || text.contains("\"" + name + "\" Notifications")
            })
            let ownTitle = candidate.isEmpty ? 0 : 1
            let ownBody = text.contains(body) ? 1 : 0
            var childTitles = 0, childBodies = 0
            var allows = [CGRect]()
            var options = [CGRect]()
            var denials = [CGRect]()
            let firstChildCard = result.count
            let role = attribute(element, kAXRoleAttribute) as? String ?? ""
            if [kAXButtonRole, kAXMenuItemRole].contains(role), text.contains("Allow"),
               let box = rectangle(element), visible.contains(where: { $0.contains(CGPoint(x: box.midX, y: box.midY)) }) {
                allows.append(box)
            }
            if [kAXButtonRole, kAXMenuItemRole].contains(role),
               text.contains("Don't Allow") || text.contains("Don’t Allow"), let box = rectangle(element),
               visible.contains(where: { $0.contains(CGPoint(x: box.midX, y: box.midY)) }) {
                denials.append(box)
            }
            if [kAXButtonRole, kAXMenuButtonRole].contains(role), text.contains("Options"),
               let box = rectangle(element), visible.contains(where: { $0.contains(CGPoint(x: box.midX, y: box.midY)) }) {
                options.append(box)
            }
            for child in attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? [] {
                let state = visit(child, depth + 1)
                candidate.formUnion(state.names); childTitles += state.titles; childBodies += state.bodies
                allows.append(contentsOf: state.allows); denials.append(contentsOf: state.denials)
                options.append(contentsOf: state.options)
            }
            // Repeated ancestor attributes describe the same logical text.
            let titles = max(ownTitle, childTitles), bodies = max(ownBody, childBodies)
            if depth > 0, [kAXGroupRole, kAXButtonRole].contains(role), candidate.count == 1,
               titles == 1, bodies == 1, let box = rectangle(element),
               visible.contains(where: { $0.contains(box) }) {
                let scopedAllows = allows.filter { box.contains(CGPoint(x: $0.midX, y: $0.midY)) }
                let scopedDenials = denials.filter { box.contains(CGPoint(x: $0.midX, y: $0.midY)) }
                let scopedOptions = options.filter { box.contains(CGPoint(x: $0.midX, y: $0.midY)) }
                var card: [String: Any] = ["os_dialog_bundle": "com.apple.notificationcenterui",
                    "os_dialog_pid": Int(app.processIdentifier), "candidate_name": candidate.first!, "fixed_body": body,
                    "card_role": role,
                    "on_screen_owner_verified": true, "bounds": [Double(box.minX), Double(box.minY), Double(box.width), Double(box.height)]]
                if scopedAllows.count == 1 && scopedDenials.count == 1 {
                    let button = scopedAllows[0]
                    card["allow_bounds"] = [Double(button.minX), Double(button.minY), Double(button.width), Double(button.height)]
                    card["denial_button_observed"] = true
                }
                if scopedOptions.count == 1 {
                    let button = scopedOptions[0]
                    card["options_bounds"] = [Double(button.minX), Double(button.minY), Double(button.width), Double(button.height)]
                }
                let childCards = Array(result[firstChildCard...])
                let actionable = card["allow_bounds"] != nil || card["options_bounds"] != nil
                if childCards.isEmpty {
                    result.append(card)
                } else if childCards.count == 1 && actionable
                            && childCards[0]["allow_bounds"] == nil && childCards[0]["options_bounds"] == nil {
                    // Buttons may be siblings of the title/body text group.
                    // Use its nearest actionable group with the same sole anchors.
                    result.removeSubrange(firstChildCard...)
                    result.append(card)
                }
            }
            return (candidate, titles, bodies, allows, denials, options)
        }
        _ = visit(root, 0)
        if capped { result.removeSubrange(firstCard...) }
    }
    return result
}

@MainActor func notificationConsent(_ names: [String], cards: [[String: Any]]) -> [[String: Any]] {
    let allowed = Set(["com.apple.UserNotificationCenter",
                       "com.apple.usernotificationsd", "com.777genius.agent-notifications"])
    var result = cards.compactMap { card -> [String: Any]? in
        guard let button = card["allow_bounds"] else { return nil }
        var dialog = card
        dialog["bounds"] = button
        dialog["allow_button"] = "Allow"
        return dialog
    }
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
            let candidate = names.first { name in labels.contains {
                $0 == name || $0.contains("“" + name + "”") || $0.contains("\"" + name + "\"")
            } }
            let mentionsNotification = labels.contains { $0.lowercased().contains("notification") }
            let hasDenial = labels.contains { $0 == "Don't Allow" || $0 == "Don’t Allow" }
            if let candidate, mentionsNotification && hasDenial && allowButtons.count == 1 {
                let box = allowButtons[0]
                result.append(["observer": "native_accessibility", "os_dialog_bundle": app.bundleIdentifier ?? "",
                               "os_dialog_pid": Int(app.processIdentifier), "candidate_name": candidate,
                               "allow_button": "Allow", "denial_button_observed": true,
                               "on_screen_owner_verified": true,
                               "bounds": [Double(box.minX), Double(box.minY), Double(box.width), Double(box.height)]])
            }
        }
    }
    return result
}

// Failure-only metadata. Fixed token comparisons never serialize AX labels or
// CG window names. Limits describe observation coverage, never a consent result.
@MainActor func permissionDiagnostics(_ expected: String) -> [String: Any] {
    let allowed = Set(["com.apple.notificationcenterui", "com.apple.UserNotificationCenter",
                       "com.apple.usernotificationsd", "com.777genius.agent-notifications"])
    let deadline = ProcessInfo.processInfo.systemUptime + 2
    let timeout = AXUIElementSetMessagingTimeout(AXUIElementCreateSystemWide(), 0.05)
    let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    let cg: [[String: Any]] = windows.prefix(64).compactMap { row in
        guard let pid = row[kCGWindowOwnerPID as String] as? NSNumber,
              let number = row[kCGWindowNumber as String] as? NSNumber,
              let layer = row[kCGWindowLayer as String] as? NSNumber,
              let box = row[kCGWindowBounds as String] as? [String: Any],
              let x = box["X"] as? NSNumber, let y = box["Y"] as? NSNumber,
              let width = box["Width"] as? NSNumber, let height = box["Height"] as? NSNumber,
              [x, y, width, height].allSatisfy({ $0.doubleValue.isFinite }) else { return nil }
        return ["owner_pid": pid.intValue, "window_id": number.intValue, "layer": layer.intValue,
                "bounds": [x.doubleValue, y.doubleValue, width.doubleValue, height.doubleValue]]
    }
    let applications = NSWorkspace.shared.runningApplications.filter {
        allowed.contains($0.bundleIdentifier ?? "") && !$0.isTerminated
        && ($0.bundleIdentifier != "com.777genius.agent-notifications"
            || $0.bundleURL?.resolvingSymlinksInPath().path == expected)
    }
    var ax = [[String: Any]]()
    for app in applications.prefix(timeout == .success ? 8 : 0) {
        let root = AXUIElementCreateApplication(app.processIdentifier)
        var counts = [String: Int]()
        func count(_ name: String) -> (AXError, Int) {
            guard ProcessInfo.processInfo.systemUptime < deadline else { return (.cannotComplete, 0) }
            var value = CFIndex(0)
            let status = AXUIElementGetAttributeValueCount(root, name as CFString, &value)
            return (status, Int(value))
        }
        func value(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
            guard ProcessInfo.processInfo.systemUptime < deadline else { return nil }
            var result: CFTypeRef?
            let status = AXUIElementCopyAttributeValue(element, name as CFString, &result)
            counts[String(status.rawValue), default: 0] += 1
            return status == .success ? result : nil
        }
        let windowCount = count(kAXWindowsAttribute), childCount = count(kAXChildrenAttribute)
        let visible = onScreenWindows(app.processIdentifier)
        var visited = 0, capped = false, candidate = false, notification = false, denial = false
        var allows = 0, visibleAllows = 0
        func visit(_ element: AXUIElement, _ depth: Int) {
            guard visited < 256, depth < 12, ProcessInfo.processInfo.systemUptime < deadline else {
                capped = true; return
            }
            visited += 1
            var allow = false
            for name in [kAXValueAttribute, kAXTitleAttribute, kAXDescriptionAttribute] {
                guard let text = value(element, name) as? String else { continue }
                candidate = candidate || text.contains("Agent Notifications")
                notification = notification || text.lowercased().contains("notification")
                denial = denial || text == "Don't Allow" || text == "Don’t Allow"
                allow = allow || text == "Allow"
            }
            if allow && value(element, kAXRoleAttribute) as? String == kAXButtonRole {
                allows += 1
                if let box = rectangle(element), visible.contains(where: { $0.contains(CGPoint(x: box.midX, y: box.midY)) }) {
                    visibleAllows += 1
                }
            }
            guard visited < 256, ProcessInfo.processInfo.systemUptime < deadline else { capped = true; return }
            var amount = CFIndex(0)
            let status = AXUIElementGetAttributeValueCount(element, kAXChildrenAttribute as CFString, &amount)
            counts[String(status.rawValue), default: 0] += 1
            guard status == .success, amount > 0 else { return }
            let limit = min(Int(amount), 32, 256 - visited)
            capped = capped || Int(amount) > limit
            var children: CFArray?
            let childrenStatus = AXUIElementCopyAttributeValues(element, kAXChildrenAttribute as CFString, 0, limit, &children)
            counts[String(childrenStatus.rawValue), default: 0] += 1
            if childrenStatus == .success {
                for child in children as? [AXUIElement] ?? [] { visit(child, depth + 1) }
            }
        }
        visit(root, 0)
        ax.append(["bundle_id": app.bundleIdentifier ?? "", "pid": Int(app.processIdentifier),
                   "ax_windows_status": windowCount.0.rawValue, "ax_windows_count": windowCount.1,
                   "ax_root_children_status": childCount.0.rawValue, "ax_root_children_count": childCount.1,
                   "visited_nodes": visited, "traversal_capped": capped, "attribute_status_counts": counts,
                   "candidate_name_present": candidate, "notification_token_present": notification,
                   "denial_token_present": denial, "exact_allow_button_count": allows,
                   "on_screen_allow_button_count": visibleAllows])
    }
    return ["scope": "failure_metadata_only", "desktop_qualified": false,
            "cg_on_screen_windows": cg, "cg_window_count": windows.count, "cg_rows_capped": windows.count > 64,
            "ax_known_owners": ax, "ax_owners_capped": applications.count > 8,
            "ax_timeout_status": timeout.rawValue, "ax_traversal_skipped": timeout != .success, "time_limit_seconds": 2,
            "node_limit_per_owner": 256, "depth_limit": 12, "children_limit_per_node": 32,
            "time_limit_reached": ProcessInfo.processInfo.systemUptime >= deadline]
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
            case "test-process-identity":
                guard args.count == 1 else { throw ProbeError.unavailable("fixed_identity_test_only") }
                publish(try testDirectChildIdentity())
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
            case "terminal-process-identity", "signal-owned-terminal-process":
                guard args.count >= 2, let pid = Int32(args[1]), pid > 0 else {
                    throw ProbeError.unavailable("owned_terminal_pid_required")
                }
                if operation == "terminal-process-identity" {
                    guard args.count == 2 || args.count == 3 else { throw ProbeError.unavailable("terminal_identity_arguments_invalid") }
                    publish(try terminalProcessIdentity(pid, expectedStart: args.count == 3 ? args[2] : nil))
                } else {
                    guard args.count == 5, !args[2].isEmpty, args[3].hasPrefix("/"),
                          ["TERM", "KILL"].contains(args[4]) else { throw ProbeError.unavailable("owned_terminal_signal_arguments_invalid") }
                    publish(try signalOwnedTerminalProcess(pid, start: args[2], executable: args[3],
                                                          signal: args[4] == "TERM" ? SIGTERM : SIGKILL))
                }
            case "notification-center-state":
                guard args.count == 1 else { throw ProbeError.unavailable("fixed_system_notification_center_only") }
                let apps = NSWorkspace.shared.runningApplications.filter {
                    $0.bundleIdentifier == "com.apple.notificationcenterui" && !$0.isTerminated
                }.map { app -> [String: Any] in
                    ["pid": Int(app.processIdentifier), "bundle_id": app.bundleIdentifier ?? "",
                     "bundle_path": app.bundleURL?.resolvingSymlinksInPath().path ?? "",
                     "executable_path": app.executableURL?.resolvingSymlinksInPath().path ?? "",
                     "finished_launching": app.isFinishedLaunching,
                     "process_start_identity": processStartIdentity(app.processIdentifier) ?? ""]
                }
                publish(["applications": apps, "desktop_qualified": false])
            case "permission-diagnostics":
                guard args.count == 2, args[1].hasPrefix("/"), args[1].hasSuffix(".app") else {
                    throw ProbeError.unavailable("physical_installed_app_required")
                }
                publish(permissionDiagnostics(URL(fileURLWithPath: args[1]).resolvingSymlinksInPath().path))
            case "helper-processes", "activate-permission-helper":
                guard args.count == (operation == "helper-processes" ? 2 : 4),
                      args[1].hasPrefix("/"), args[1].hasSuffix(".app") else {
                    throw ProbeError.unavailable("physical_installed_app_required")
                }
                let expected = URL(fileURLWithPath: args[1]).resolvingSymlinksInPath().path
                let executable = URL(fileURLWithPath: expected).appendingPathComponent("Contents/MacOS/terminal-notifier-modern").path
                let helpers = NSWorkspace.shared.runningApplications.filter {
                    $0.bundleIdentifier == "com.777genius.agent-notifications"
                    && $0.bundleURL?.resolvingSymlinksInPath().path == expected
                    && $0.executableURL?.resolvingSymlinksInPath().path == executable
                    && !$0.isTerminated
                }
                func facts(_ app: NSRunningApplication) -> [String: Any] {
                    return ["pid": Int(app.processIdentifier), "active": app.isActive,
                            "finished_launching": app.isFinishedLaunching,
                            "process_start_identity": processStartIdentity(app.processIdentifier) ?? "",
                            "process_start_source": "kernel_proc_bsdinfo",
                            "activation_policy": app.activationPolicy.rawValue]
                }
                var result: [String: Any] = ["installed_helper_pids": helpers.map { Int($0.processIdentifier) },
                                            "installed_helpers": helpers.map { facts($0) }]
                if operation == "activate-permission-helper" {
                    let start = args[3].split(separator: ":", omittingEmptySubsequences: false)
                    guard let pid = Int32(args[2]), pid > 0, start.count == 2,
                          let seconds = UInt64(start[0]), seconds > 0,
                          let microseconds = UInt64(start[1]), microseconds < 1_000_000 else {
                        throw ProbeError.unavailable("observed_owned_helper_pid_required")
                    }
                    let exact = helpers.filter { $0.processIdentifier == pid
                        && !$0.isTerminated && matchesProcessStart(pid, args[3]) }
                    // An early completed request can disappear between the two
                    // observations. Never activate a replacement PID or relaunch.
                    if exact.count == 1 {
                        result["before"] = facts(exact[0])
                        result["activation_requested"] = exact[0].activate(options: [.activateIgnoringOtherApps])
                        result["after"] = facts(exact[0])
                    } else {
                        result["activation_requested"] = false
                        result["observed_helper_no_longer_running"] = true
                    }
                }
                publish(result)
            case "notification-consent", "hover-notification-consent", "expand-notification-consent",
                 "open-notification-consent-options", "allow-notification-consent":
                let names = Array(args.dropFirst())
                guard (1...3).contains(names.count), names.allSatisfy({ !$0.isEmpty && $0.count <= 80
                    && !$0.unicodeScalars.contains(where: { $0.value < 32 }) }) else {
                    throw ProbeError.unavailable("verified_permission_display_identity_required")
                }
                var cards = notificationPermissionCards(names)
                var dialogs = notificationConsent(names, cards: cards)
                let pointerBefore = CGEvent(source: nil)?.location
                var pointerTarget: CGPoint?
                if operation == "hover-notification-consent" {
                    guard cards.count == 1 else { throw ProbeError.unavailable("unique_candidate_notification_permission_card_required") }
                    let box = cards[0]["bounds"] as! [Double]
                    let point = CGPoint(x: box[0] + box[2] / 2, y: box[1] + box[3] / 2)
                    pointerTarget = point
                    guard let event = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved,
                                              mouseCursorPosition: point, mouseButton: .left) else {
                        throw ProbeError.unavailable("physical_permission_hover_unavailable")
                    }
                    event.post(tap: .cghidEventTap)
                }
                if ["allow-notification-consent", "open-notification-consent-options", "expand-notification-consent"].contains(operation) {
                    let box: [Double]
                    if operation == "open-notification-consent-options" {
                        guard cards.count == 1, let options = cards[0]["options_bounds"] as? [Double] else {
                            throw ProbeError.unavailable("unique_candidate_notification_options_control_required")
                        }
                        box = options
                    } else if operation == "expand-notification-consent" {
                        guard cards.count == 1, dialogs.isEmpty, cards[0]["options_bounds"] == nil else {
                            throw ProbeError.unavailable("unique_collapsed_notification_permission_card_required")
                        }
                        box = cards[0]["bounds"] as! [Double]
                    } else {
                        guard dialogs.count == 1 else { throw ProbeError.unavailable("unique_candidate_notification_consent_dialog_required") }
                        box = dialogs[0]["bounds"] as! [Double]
                    }
                    let point = CGPoint(x: box[0] + box[2] / 2, y: box[1] + box[3] / 2)
                    pointerTarget = point
                    guard let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left),
                          let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left) else {
                        throw ProbeError.unavailable("physical_permission_click_unavailable")
                    }
                    down.post(tap: .cghidEventTap); up.post(tap: .cghidEventTap)
                }
                if operation != "notification-consent" {
                    try await Task.sleep(nanoseconds: 200_000_000)
                    cards = notificationPermissionCards(names)
                    dialogs = notificationConsent(names, cards: cards)
                }
                let pointerAfter = CGEvent(source: nil)?.location
                let pointerObserved = pointerTarget != nil && pointerAfter != nil
                    && abs(pointerAfter!.x - pointerTarget!.x) <= 1 && abs(pointerAfter!.y - pointerTarget!.y) <= 1
                var receipt: [String: Any] = ["dialogs": dialogs, "permission_cards": cards,
                    "physical_pointer_hover": operation == "hover-notification-consent" && pointerObserved,
                    "physical_card_click": operation == "expand-notification-consent",
                    "physical_options_click": operation == "open-notification-consent-options",
                    "physical_allow_click": operation == "allow-notification-consent"]
                if let pointerBefore { receipt["pointer_before"] = [Double(pointerBefore.x), Double(pointerBefore.y)] }
                if let pointerTarget { receipt["pointer_target"] = [Double(pointerTarget.x), Double(pointerTarget.y)] }
                if let pointerAfter { receipt["pointer_after"] = [Double(pointerAfter.x), Double(pointerAfter.y)] }
                if pointerTarget != nil { receipt["pointer_target_observed"] = pointerObserved }
                publish(receipt)
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
