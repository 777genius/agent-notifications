// TEST observer for the installed Pi product. Never sends a notification,
// plays a cue or performs the product's focus callback. Ordinary OS consent
// uses only a physical click on the uniquely observed candidate Allow dialog.
// ScreenCaptureKit/CGEvent primitives match pi_native_preflight_darwin.swift.
import AppKit
import ApplicationServices
import AVFoundation
import CoreAudio
import CoreMedia
import CryptoKit
import Darwin
import ScreenCaptureKit
import Security

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
@MainActor func consentControlTree(_ root: AXUIElement, box: CGRect) -> [String: Any] {
    var nodes = [[String: Any]](), visited = 0, capped = false
    func bounded(_ value: String, _ limit: Int = 96) -> String {
        let bytes = Array(value.utf8.prefix(limit))
        var count = bytes.count
        while count > 0 {
            if let text = String(bytes: bytes.prefix(count), encoding: .utf8) { return text }
            count -= 1
        }
        return ""
    }
    func visit(_ element: AXUIElement, _ depth: Int, _ parent: Int?) {
        guard visited < 64, depth < 12, nodes.count < 16 else { capped = true; return }
        visited += 1
        var nextParent = parent
        if let frame = rectangle(element), box.contains(CGPoint(x: frame.midX, y: frame.midY)) {
            var actions: CFArray?
            let status = AXUIElementCopyActionNames(element, &actions)
            let index = nodes.count
            var row: [String: Any] = ["index": index, "depth": depth,
                "role": bounded(attribute(element, kAXRoleAttribute) as? String ?? "", 64),
                "bounds": [Double(frame.minX), Double(frame.minY), Double(frame.width), Double(frame.height)],
                "action_query_status": status.rawValue,
                "actions": (actions as? [String] ?? []).prefix(4).map { bounded($0, 64) }]
            if let parent { row["parent"] = parent }
            for (key, name) in [("title", kAXTitleAttribute), ("description", kAXDescriptionAttribute),
                                ("help", kAXHelpAttribute), ("identifier", "AXIdentifier")] {
                if let value = attribute(element, name) as? String { row[key] = bounded(value) }
            }
            nodes.append(row)
            nextParent = index
        }
        for child in attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? [] { visit(child, depth + 1, nextParent) }
    }
    visit(root, 0, nil)
    return ["scope": "verified_unique_permission_card_only", "nodes": nodes, "visited_nodes": visited,
            "traversal_capped": capped, "maximum_nodes": 16, "maximum_visited_nodes": 64, "maximum_depth": 12,
            "maximum_attribute_bytes": 96, "desktop_qualified": false]
}

struct ConsentOption { let element: AXUIElement; let box: CGRect }

@MainActor func notificationPermissionCards(_ names: [String], includeControls: Bool = false,
                                            optionsObserver: ((AXUIElement) -> Void)? = nil) -> [[String: Any]] {
    let body = "Notifications may include alerts, sounds, and icon badges."
    var result = [[String: Any]]()
    for app in NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.notificationcenterui") {
        guard !app.isTerminated, app.bundleURL?.resolvingSymlinksInPath().path == "/System/Library/CoreServices/NotificationCenter.app",
              let ownerStart = processStartIdentity(app.processIdentifier) else { continue }
        let root = AXUIElementCreateApplication(app.processIdentifier)
        let visible = onScreenWindows(app.processIdentifier)
        var remaining = 1200
        var capped = false
        let firstCard = result.count
        typealias Contents = (names: Set<String>, titles: Int, bodies: Int, allows: [CGRect], denials: [CGRect], options: [ConsentOption])
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
            var options = [ConsentOption]()
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
                options.append(ConsentOption(element: element, box: box))
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
               [box.minX, box.minY, box.width, box.height].allSatisfy({ $0.isFinite }),
               box.width >= 100, box.width <= 600, box.height >= 40, box.height <= 400,
               visible.contains(where: { $0.contains(box) }) {
                let scopedAllows = allows.filter { box.contains(CGPoint(x: $0.midX, y: $0.midY)) }
                let scopedDenials = denials.filter { box.contains(CGPoint(x: $0.midX, y: $0.midY)) }
                let scopedOptions = options.filter { box.contains(CGPoint(x: $0.box.midX, y: $0.box.midY)) }
                var card: [String: Any] = ["os_dialog_bundle": "com.apple.notificationcenterui",
                    "os_dialog_pid": Int(app.processIdentifier), "os_owner_start_identity": ownerStart,
                    "candidate_name": candidate.first!, "fixed_body": body,
                    "card_role": role,
                    "on_screen_owner_verified": true, "bounds": [Double(box.minX), Double(box.minY), Double(box.width), Double(box.height)]]
                card["_element"] = element
                if scopedAllows.count == 1 && scopedDenials.count == 1 {
                    let button = scopedAllows[0]
                    card["allow_bounds"] = [Double(button.minX), Double(button.minY), Double(button.width), Double(button.height)]
                    card["denial_button_observed"] = true
                }
                if scopedOptions.count == 1 {
                    let button = scopedOptions[0].box
                    card["options_bounds"] = [Double(button.minX), Double(button.minY), Double(button.width), Double(button.height)]
                    card["_options_element"] = scopedOptions[0].element
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
    return result.map { value in
        var card = value
        let element = card.removeValue(forKey: "_element") as! AXUIElement
        if let options = card.removeValue(forKey: "_options_element"), result.count == 1 {
            optionsObserver?(options as! AXUIElement)
        }
        if includeControls && result.count == 1, let bounds = card["bounds"] as? [Double] {
            card["scoped_control_tree"] = consentControlTree(element,
                box: CGRect(x: bounds[0], y: bounds[1], width: bounds[2], height: bounds[3]))
        }
        return card
    }
}

@MainActor func notificationConsent(_ cards: [[String: Any]]) -> [[String: Any]] {
    // A generic whole-window Allow scan must never bypass the exact card.
    cards.compactMap { card -> [String: Any]? in
        guard let button = card["allow_bounds"], card["denial_button_observed"] as? Bool == true else { return nil }
        var dialog = card
        dialog["bounds"] = button
        dialog["allow_button"] = "Allow"
        return dialog
    }
}

struct ConsentActionContext: Decodable {
    let app: String
    let helperPID: Int32
    let helperStart: String
    let requestPID: Int32
    let requestStart: String
    let requestExecutable: String
    let deadlineMonotonic: Double
    let clock: String
    let ownerPID: Int32
    let ownerStart: String
    let candidateName: String
    let cardBounds: [Double]
    let menuWindowID: Int?
    let menuBounds: [Double]?
    let optionsBounds: [Double]?
}

func consentPopupWindows(_ pid: pid_t) -> [[String: Any]] {
    guard let rows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] else {
        return [["owner_pid": Int(pid), "invalid_inventory": true]]
    }
    return rows.compactMap { row in
        guard (row[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == pid,
              (row[kCGWindowLayer as String] as? NSNumber)?.intValue == Int(CGWindowLevelForKey(.popUpMenuWindow)) else { return nil }
        if let alpha = row[kCGWindowAlpha as String] as? NSNumber, alpha.doubleValue == 0 { return nil }
        // Count every visible owner popup before candidate size admission.
        // Malformed and oversized popups must still defeat an empty inventory.
        var popup: [String: Any] = ["window_id": (row[kCGWindowNumber as String] as? NSNumber)?.intValue ?? 0,
            "owner_pid": Int(pid), "layer": Int(CGWindowLevelForKey(.popUpMenuWindow)),
            "positive_alpha_verified": ((row[kCGWindowAlpha as String] as? NSNumber)?.doubleValue ?? 0) > 0]
        if let bounds = row[kCGWindowBounds as String] as? [String: Any],
           let x = bounds["X"] as? NSNumber, let y = bounds["Y"] as? NSNumber,
           let width = bounds["Width"] as? NSNumber, let height = bounds["Height"] as? NSNumber,
           [x, y, width, height].allSatisfy({ $0.doubleValue.isFinite }), width.doubleValue > 0, height.doubleValue > 0 {
            popup["bounds"] = [x.doubleValue, y.doubleValue, width.doubleValue, height.doubleValue]
        } else { popup["invalid_bounds"] = true }
        return popup
    }
}

func consentBox(_ bounds: [Double]) -> CGRect? {
    guard bounds.count == 4, bounds.allSatisfy({ $0.isFinite }), bounds[2] > 0, bounds[3] > 0 else { return nil }
    return CGRect(x: bounds[0], y: bounds[1], width: bounds[2], height: bounds[3])
}

@MainActor struct ConsentMenu {
    let receipt: [String: Any]
    let allow: AXUIElement
    let allowBox: CGRect
}

// Select the AXMenu belonging to the exact popup created by this Options click.
// Only after that association is proven do we inspect its two menu items.
@MainActor func consentMenu(_ pid: pid_t, windowID: Int, bounds: [Double], optionsBounds: [Double],
                             options: AXUIElement) throws -> ConsentMenu {
    let windows = consentPopupWindows(pid)
    guard windows.count == 1, windows[0]["window_id"] as? Int == windowID,
          windows[0]["positive_alpha_verified"] as? Bool == true,
          windows[0]["bounds"] as? [Double] == bounds,
          let box = consentBox(bounds), let opener = consentBox(optionsBounds),
          box.width <= 400, box.height <= 400,
          box.minX < opener.maxX, box.maxX > opener.minX,
          min(abs(box.minY - opener.maxY), abs(box.maxY - opener.minY)) <= 24 else {
        throw ProbeError.unavailable("original_options_popup_window_changed")
    }
    func menus(_ roots: [AXUIElement], limit: Int) throws -> [AXUIElement] {
        var remaining = limit, capped = false, found = [AXUIElement]()
        func visit(_ element: AXUIElement, _ depth: Int) {
            guard remaining > 0, depth < 20 else { capped = true; return }
            remaining -= 1
            var owner = pid_t(0)
            guard AXUIElementGetPid(element, &owner) == .success, owner == pid else { return }
            if attribute(element, kAXRoleAttribute) as? String == kAXMenuRole,
               let frame = rectangle(element), box.contains(frame) {
                if !found.contains(where: { CFEqual($0, element) }) { found.append(element) }
                return
            }
            for child in attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? [] { visit(child, depth + 1) }
        }
        for root in roots { visit(root, 0) }
        guard !capped else { throw ProbeError.unavailable("consent_menu_search_capped") }
        return found
    }
    var relation = "options_shown_menu"
    // This public relationship is an array, not a single AXUIElement.
    var found = try menus(attribute(options, kAXShownMenuUIElementAttribute) as? [AXUIElement] ?? [], limit: 64)
    if found.isEmpty {
        relation = "options_children"
        found = try menus(attribute(options, kAXChildrenAttribute) as? [AXUIElement] ?? [], limit: 64)
    }
    if found.isEmpty {
        relation = "captured_new_popup_window"
        // The fixed owner/window identity comes from the before/after click
        // inventory. This searches for that AXMenu, never a root-level Allow.
        found = try menus([AXUIElementCreateApplication(pid)], limit: 1200)
    }
    guard found.count == 1 else { throw ProbeError.unavailable("unique_original_options_ax_menu_required") }
    var remaining = 64, capped = false, items = 0
    var allows = [(AXUIElement, CGRect)](), denials = [(AXUIElement, CGRect)]()
    func visit(_ element: AXUIElement, _ depth: Int) {
        guard remaining > 0, depth < 8 else { capped = true; return }
        remaining -= 1
        if attribute(element, kAXRoleAttribute) as? String == kAXMenuItemRole {
            items += 1
            let text = [kAXTitleAttribute, kAXValueAttribute, kAXDescriptionAttribute].compactMap { attribute(element, $0) as? String }
            var owner = pid_t(0)
            if AXUIElementGetPid(element, &owner) == .success, owner == pid,
               attribute(element, kAXEnabledAttribute) as? Bool == true,
               let frame = rectangle(element), box.contains(frame) {
                if text.contains("Allow") { allows.append((element, frame)) }
                if text.contains("Don't Allow") || text.contains("Don’t Allow") { denials.append((element, frame)) }
            }
        }
        for child in attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? [] { visit(child, depth + 1) }
    }
    visit(found[0], 0)
    guard !capped, items == 2, allows.count == 1, denials.count == 1,
          !allows[0].1.intersects(denials[0].1) else {
        throw ProbeError.unavailable("original_options_menu_allow_and_denial_unverified")
    }
    let allow = allows[0].1, denial = denials[0].1
    return ConsentMenu(receipt: ["association": relation, "window_id": windowID, "owner_pid": Int(pid),
        "bounds": bounds, "options_bounds": optionsBounds, "menu_role": kAXMenuRole, "menu_item_count": items,
        "allow_bounds": [Double(allow.minX), Double(allow.minY), Double(allow.width), Double(allow.height)],
        "denial_bounds": [Double(denial.minX), Double(denial.minY), Double(denial.width), Double(denial.height)],
        "denial_button_observed": true], allow: allows[0].0, allowBox: allow)
}

@MainActor func requireTopmostConsentItem(_ menu: ConsentMenu, owner: pid_t) throws {
    var hit: AXUIElement?
    guard AXUIElementCopyElementAtPosition(AXUIElementCreateSystemWide(), Float(menu.allowBox.midX), Float(menu.allowBox.midY), &hit) == .success,
          var current = hit else { throw ProbeError.unavailable("topmost_consent_menu_item_unavailable") }
    var pid = pid_t(0)
    guard AXUIElementGetPid(current, &pid) == .success, pid == owner else {
        throw ProbeError.unavailable("topmost_consent_menu_owner_changed")
    }
    for _ in 0..<8 {
        if CFEqual(current, menu.allow) { return }
        guard let parent = attribute(current, kAXParentAttribute), CFGetTypeID(parent) == AXUIElementGetTypeID() else { break }
        current = parent as! AXUIElement
    }
    throw ProbeError.unavailable("topmost_consent_allow_item_changed")
}

func consentMonotonicTime() throws -> Double {
    // CPython 3.12 time.monotonic() uses this same system-wide Mach epoch.
    var info = mach_timebase_info_data_t()
    guard mach_timebase_info(&info) == KERN_SUCCESS, info.denom > 0 else {
        throw ProbeError.unavailable("shared_consent_monotonic_clock_unavailable")
    }
    return Double(mach_absolute_time()) * Double(info.numer) / Double(info.denom) / 1_000_000_000
}

func terminalAutomationUID(_ pid: pid_t) -> Int? {
    var info = proc_bsdinfo()
    let size = Int32(MemoryLayout<proc_bsdinfo>.size)
    guard proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, size) == size, info.pbi_pid == UInt32(pid) else { return nil }
    return Int(info.pbi_uid)
}

@MainActor func terminalAutomationActors(_ pid: pid_t, pythonPID: pid_t) throws -> [String: Any] {
    let original = try terminalProcessIdentity(pid)
    let osascript = URL(fileURLWithPath: "/usr/bin/osascript").resolvingSymlinksInPath().path
    guard original["status"] as? String == "observed", original["parent_pid"] as? Int == Int(pythonPID),
          original["executable_path"] as? String == osascript else {
        return ["admitted": false, "reason": "original_osascript_identity_unverified"]
    }
    var current = pid, seen = Set<pid_t>(), chain = [[String: Any]]()
    for _ in 0..<32 {
        guard current > 1, !seen.contains(current) else { break }
        seen.insert(current)
        // Ancestors owned by another user (Terminal's root-owned login) are not
        // always inspectable; an unverifiable ancestor only withholds admission.
        guard var row = try? terminalProcessIdentity(current) else {
            return ["admitted": false, "reason": "automation_ancestor_unobservable", "chain": chain]
        }
        var info = proc_bsdinfo()
        let size = Int32(MemoryLayout<proc_bsdinfo>.size)
        guard row["status"] as? String == "observed", proc_pidinfo(current, PROC_PIDTBSDINFO, 0, &info, size) == size,
              row["process_start_identity"] as? String == "\(info.pbi_start_tvsec):\(info.pbi_start_tvusec)" else {
            return ["admitted": false, "reason": "automation_ancestor_changed", "chain": chain]
        }
        row["uid"] = Int(info.pbi_uid)
        chain.append(row)
        if (current == pid || current == pythonPID) && info.pbi_uid != getuid() {
            return ["admitted": false, "reason": "original_osascript_or_python_user_mismatch", "chain": chain]
        }
        if let path = row["executable_path"] as? String, path == "/opt/hca/hosted-compute-agent" {
            return ["admitted": info.pbi_uid == getuid(), "requestPID": Int(pid),
                    "reason": info.pbi_uid == getuid() ? "same_user_requester" : "privileged_requester_role_requires_measured_admission",
                    "requestStart": original["process_start_identity"]!, "pythonPID": Int(pythonPID),
                    "requesterPID": Int(current), "requesterStart": row["process_start_identity"]!,
                    "requesterPath": path, "requesterUID": Int(info.pbi_uid), "chain": chain]
        }
        current = pid_t(info.pbi_ppid)
    }
    return ["admitted": false, "reason": "responsible_hosted_compute_agent_not_observed", "chain": chain]
}

func verifiedAppleApplication(_ url: URL, identifier: String) -> Bool {
    let path = url.resolvingSymlinksInPath().path
    guard path == url.path, path.hasSuffix(".app"),
          path.hasPrefix("/System/Library/") || path.hasPrefix("/System/Applications/"),
          identifier.hasPrefix("com.apple."), identifier.range(of: "^[A-Za-z0-9.-]+$", options: .regularExpression) != nil,
          Bundle(url: url)?.bundleIdentifier == identifier else { return false }
    var requirement: SecRequirement?, code: SecStaticCode?
    guard SecRequirementCreateWithString("anchor apple and identifier \"\(identifier)\"" as CFString, [], &requirement) == errSecSuccess,
          SecStaticCodeCreateWithPath(url as CFURL, [], &code) == errSecSuccess,
          let requirement, let code else { return false }
    return SecStaticCodeCheckValidity(code, SecCSFlags(rawValue: kSecCSStrictValidate), requirement) == errSecSuccess
}

@MainActor struct TerminalAutomationDialog {
    let owner: NSRunningApplication
    let ownerStart: String
    let box: CGRect
    let allow: AXUIElement
    let allowBox: CGRect
    let denialBox: CGRect
    let targetPID: pid_t
    let targetStart: String
    var receipt: [String: Any] {
        ["ownerPID": Int(owner.processIdentifier), "ownerStart": ownerStart,
         "ownerPath": owner.bundleURL!.path, "ownerBundle": owner.bundleIdentifier!,
         "allowBounds": [Double(allowBox.minX), Double(allowBox.minY), Double(allowBox.width), Double(allowBox.height)],
         "denialBounds": [Double(denialBox.minX), Double(denialBox.minY), Double(denialBox.width), Double(denialBox.height)],
         "targetPID": Int(targetPID), "targetStart": targetStart,
         "ownerUID": Int(getuid()), "targetUID": Int(getuid()),
         "requester": "hosted-compute-agent", "target": "com.apple.Terminal", "apple_signature_verified": true]
    }
}

@MainActor func terminalAutomationDialogs() -> [TerminalAutomationDialog] {
    let terminalPath = "/System/Applications/Utilities/Terminal.app"
    let ownerBundle = "com.apple.UserNotificationCenter"
    let ownerPath = "/System/Library/CoreServices/UserNotificationCenter.app"
    let targets = NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.Terminal").filter {
        !$0.isTerminated && $0.isFinishedLaunching && $0.bundleURL?.resolvingSymlinksInPath().path == terminalPath
    }
    guard targets.count == 1, terminalAutomationUID(targets[0].processIdentifier) == Int(getuid()),
          let targetStart = processStartIdentity(targets[0].processIdentifier),
          verifiedAppleApplication(URL(fileURLWithPath: terminalPath), identifier: "com.apple.Terminal"),
          let targetIdentity = try? terminalProcessIdentity(targets[0].processIdentifier),
          targetIdentity["executable_path"] as? String == terminalPath + "/Contents/MacOS/Terminal" else { return [] }
    let titles = ["“hosted-compute-agent” wants access to control “Terminal”.",
                  "\"hosted-compute-agent\" wants access to control \"Terminal\"."]
    let bodies = ["Allowing control will provide access to documents and data in “Terminal”, and to perform actions within that app.",
                  "Allowing control will provide access to documents and data in \"Terminal\", and to perform actions within that app."]
    var candidates = [TerminalAutomationDialog]()
    for app in NSRunningApplication.runningApplications(withBundleIdentifier: ownerBundle) {
        guard let url = app.bundleURL, let identifier = app.bundleIdentifier, identifier == ownerBundle,
              url.resolvingSymlinksInPath().path == ownerPath,
              app.executableURL?.resolvingSymlinksInPath().path == ownerPath + "/Contents/MacOS/UserNotificationCenter",
              !app.isTerminated, terminalAutomationUID(app.processIdentifier) == Int(getuid()),
              let start = processStartIdentity(app.processIdentifier) else { continue }
        let visible = onScreenWindows(app.processIdentifier)
        guard visible.contains(where: { $0.width >= 200 && $0.width <= 650 && $0.height >= 150 && $0.height <= 500 }) else { continue }
        var remaining = 1200, capped = false
        let first = candidates.count
        func visit(_ element: AXUIElement, _ depth: Int) -> (String, [(AXUIElement, CGRect)], [CGRect]) {
            guard remaining > 0, depth < 20 else { capped = true; return ("", [], []) }
            remaining -= 1
            let text = [kAXTitleAttribute, kAXValueAttribute, kAXDescriptionAttribute].compactMap { attribute(element, $0) as? String }
            var labels = text.joined(separator: " "), allows = [(AXUIElement, CGRect)](), denials = [CGRect]()
            if attribute(element, kAXRoleAttribute) as? String == kAXButtonRole,
               attribute(element, kAXEnabledAttribute) as? Bool == true, let frame = rectangle(element),
               visible.contains(where: { $0.contains(frame) }) {
                if text.contains("Allow") { allows.append((element, frame)) }
                if text.contains("Don't Allow") || text.contains("Don’t Allow") { denials.append(frame) }
            }
            for child in attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? [] {
                let nested = visit(child, depth + 1)
                labels += " " + nested.0; allows += nested.1; denials += nested.2
            }
            labels = labels.components(separatedBy: .whitespacesAndNewlines).filter { !$0.isEmpty }.joined(separator: " ")
            if titles.contains(where: { labels.contains($0) }), bodies.contains(where: { labels.contains($0) }),
               allows.count == 1, denials.count == 1, let frame = rectangle(element),
               frame.width >= 200, frame.width <= 650, frame.height >= 150, frame.height <= 500,
               frame.contains(allows[0].1), frame.contains(denials[0]), !allows[0].1.intersects(denials[0]),
               visible.contains(where: { $0.contains(frame) }) {
                candidates.append(TerminalAutomationDialog(owner: app, ownerStart: start, box: frame,
                    allow: allows[0].0, allowBox: allows[0].1, denialBox: denials[0],
                    targetPID: targets[0].processIdentifier, targetStart: targetStart))
            }
            return (String(labels.prefix(8192)), allows, denials)
        }
        _ = visit(AXUIElementCreateApplication(app.processIdentifier), 0)
        if capped { return [] }
        if candidates.count > first {
            let identity = try? terminalProcessIdentity(app.processIdentifier)
            if !verifiedAppleApplication(url, identifier: identifier)
                || identity?["executable_path"] as? String != app.executableURL?.resolvingSymlinksInPath().path
                || identity?["process_start_identity"] as? String != start { candidates.removeSubrange(first...) }
        }
    }
    var result = [TerminalAutomationDialog]()
    for candidate in candidates where !candidates.contains(where: {
        $0.owner.processIdentifier == candidate.owner.processIdentifier && candidate.box.contains($0.box)
        && $0.box.width * $0.box.height < candidate.box.width * candidate.box.height
    }) {
        if !result.contains(where: { $0.owner.processIdentifier == candidate.owner.processIdentifier && $0.box == candidate.box }) { result.append(candidate) }
    }
    return result
}

struct TerminalAutomationDialogIdentity: Decodable {
    let ownerPID: Int32; let ownerStart: String; let ownerPath: String; let ownerBundle: String
    let allowBounds: [Double]; let denialBounds: [Double]; let targetPID: Int32; let targetStart: String
}
struct TerminalAutomationContext: Decodable {
    let requestPID: Int32; let requestStart: String; let pythonPID: Int32
    let requesterPID: Int32; let requesterStart: String; let requesterPath: String; let requesterUID: Int
    let deadlineMonotonic: Double; let clock: String; let officialHostAdmitted: Bool?
    let dialog: TerminalAutomationDialogIdentity?
}

@MainActor func terminalAutomation(_ args: [String]) throws -> [String: Any] {
    guard args.count == 2, args[1].utf8.count <= 16384, let data = args[1].data(using: .utf8),
          let context = try? JSONDecoder().decode(TerminalAutomationContext.self, from: data),
          context.clock == "mach_absolute_time", context.deadlineMonotonic.isFinite else {
        throw ProbeError.unavailable("original_terminal_automation_context_required")
    }
    let now = try consentMonotonicTime()
    guard context.deadlineMonotonic > now, context.deadlineMonotonic - now <= 20 else {
        throw ProbeError.unavailable("original_terminal_automation_deadline_expired")
    }
    let deadline = min(context.deadlineMonotonic, now + 3)
    func requireActors() throws {
        let fresh = try terminalAutomationActors(context.requestPID, pythonPID: context.pythonPID)
        guard try consentMonotonicTime() < deadline, fresh["admitted"] as? Bool == true,
              fresh["requestStart"] as? String == context.requestStart,
              fresh["requesterPID"] as? Int == Int(context.requesterPID), fresh["requesterStart"] as? String == context.requesterStart,
              fresh["requesterPath"] as? String == context.requesterPath, fresh["requesterUID"] as? Int == context.requesterUID else {
            throw ProbeError.unavailable("original_terminal_request_or_requester_changed")
        }
    }
    try requireActors()
    let dialogs = terminalAutomationDialogs()
    var result: [String: Any] = ["dialogs": dialogs.map { $0.receipt }, "physical_allow_click": false]
    if args[0] == "allow-terminal-automation" {
        #if !arch(arm64)
        throw ProbeError.unavailable("official_arm_terminal_automation_host_required")
        #endif
        let env = ProcessInfo.processInfo.environment
        guard context.officialHostAdmitted == true, env["GITHUB_REPOSITORY"] == "777genius/agent-notifications",
              env["GITHUB_ACTOR"] == "777genius", env["GITHUB_TRIGGERING_ACTOR"] == "777genius",
              env["GITHUB_EVENT_NAME"] == "workflow_dispatch", env["GITHUB_RUN_ATTEMPT"] == "1", env["ImageOS"] == "macos15",
              ProcessInfo.processInfo.operatingSystemVersion.majorVersion == 15,
              dialogs.count == 1, let expected = context.dialog else {
            throw ProbeError.unavailable("official_original_terminal_automation_dialog_required")
        }
        let dialog = dialogs[0], receipt = dialog.receipt
        guard dialog.owner.processIdentifier == expected.ownerPID, dialog.ownerStart == expected.ownerStart,
              receipt["ownerPath"] as? String == expected.ownerPath, receipt["ownerBundle"] as? String == expected.ownerBundle,
              receipt["allowBounds"] as? [Double] == expected.allowBounds, receipt["denialBounds"] as? [Double] == expected.denialBounds,
              dialog.targetPID == expected.targetPID, dialog.targetStart == expected.targetStart else {
            throw ProbeError.unavailable("original_terminal_automation_dialog_changed")
        }
        let control = ConsentMenu(receipt: [:], allow: dialog.allow, allowBox: dialog.allowBox)
        let point = CGPoint(x: dialog.allowBox.midX, y: dialog.allowBox.midY)
        try requireActors()
        guard matchesProcessStart(expected.ownerPID, expected.ownerStart), matchesProcessStart(expected.targetPID, expected.targetStart),
              CGWarpMouseCursorPosition(point) == .success else { throw ProbeError.unavailable("original_terminal_automation_owner_changed") }
        try requireActors()
        try requireTopmostConsentItem(control, owner: expected.ownerPID)
        guard let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left),
              let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left) else {
            throw ProbeError.unavailable("physical_terminal_automation_click_unavailable")
        }
        try requireActors()
        down.post(tap: .cghidEventTap); up.post(tap: .cghidEventTap)
        result.merge(["physical_allow_click": true, "topmost_allow_item_verified": true, "original_request_verified": true,
            "denial_button_observed": true, "requestPID": Int(context.requestPID), "requestStart": context.requestStart,
            "requesterPID": Int(context.requesterPID), "requesterStart": context.requesterStart]) { _, new in new }
    }
    return result
}

// Diagnostic only: follow the original, real product click's public Screen
// Recording prompt. This never grants permission or substitutes for focus.
struct FocusPermissionContext: Decodable {
    let helperPath: String; let helperSHA256: String
    let originPID: Int32; let originStart: String; let originExecutable: String
    let deadlineMonotonic: Double; let clock: String; let screenshotPath: String
    let originalHelperBaselineEmpty: Bool; let originalPhysicalClickObserved: Bool
}

@MainActor struct FocusScreenDialog {
    let app: NSRunningApplication; let start: String; let element: AXUIElement; let box: CGRect
    let open: AXUIElement; let openBox: CGRect; let denyBox: CGRect
}

@MainActor func focusScreenDialog(deadline: Double) throws -> FocusScreenDialog? {
    var candidates = [FocusScreenDialog](), remaining = 1200, windowsSeen = 0
    for app in NSWorkspace.shared.runningApplications {
        guard try consentMonotonicTime() < deadline else { throw ProbeError.unavailable("screen_dialog_inventory_capped") }
        guard !app.isTerminated, let url = app.bundleURL, let identifier = app.bundleIdentifier,
              identifier.hasPrefix("com.apple."), url.path.hasPrefix("/System/"),
              terminalAutomationUID(app.processIdentifier) == Int(getuid()),
              let start = processStartIdentity(app.processIdentifier) else { continue }
        let visible = onScreenWindows(app.processIdentifier).filter { $0.width >= 200 && $0.width <= 650 && $0.height >= 120 && $0.height <= 500 }
        guard !visible.isEmpty else { continue }
        let root = AXUIElementCreateApplication(app.processIdentifier)
        guard AXUIElementSetMessagingTimeout(root, 0.04) == .success,
              let windows = attribute(root, kAXWindowsAttribute) as? [AXUIElement], windows.count <= 16 else {
            throw ProbeError.unavailable("screen_dialog_inventory_unknown")
        }
        for window in windows {
            guard let windowBox = rectangle(window), visible.contains(windowBox) else { continue }
            // The measured OS alert has this public window title. A different
            // explicit title cannot supply this dialog; absent titles are scanned.
            if let title = attribute(window, kAXTitleAttribute) as? String, !title.isEmpty, title != "Screen Recording" { continue }
            windowsSeen += 1
            guard windowsSeen <= 16 else { throw ProbeError.unavailable("screen_dialog_inventory_capped") }
            var capped = false
            func visit(_ element: AXUIElement, _ depth: Int) -> (String, [(AXUIElement, CGRect)], [CGRect]) {
                guard remaining > 0, depth < 18, let now = try? consentMonotonicTime(), now < deadline else { capped = true; return ("", [], []) }
                remaining -= 1
                var pid = pid_t(0)
                guard AXUIElementGetPid(element, &pid) == .success, pid == app.processIdentifier else { capped = true; return ("", [], []) }
                let role = attribute(element, kAXRoleAttribute) as? String ?? ""
                let texts = [kAXTitleAttribute, kAXValueAttribute, kAXDescriptionAttribute].compactMap { attribute(element, $0) as? String }
                var labels = texts.joined(separator: " "), opens = [(AXUIElement, CGRect)](), denials = [CGRect]()
                if role == kAXButtonRole, attribute(element, kAXEnabledAttribute) as? Bool == true,
                   let box = rectangle(element), windowBox.contains(box) {
                    if texts.contains("Open System Settings") { opens.append((element, box)) }
                    if texts.contains("Deny") { denials.append(box) }
                }
                var raw: CFTypeRef?
                let status = AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &raw)
                if status == .success, let children = raw as? [AXUIElement], children.count <= 64 {
                    for child in children {
                        let nested = visit(child, depth + 1)
                        labels += " " + nested.0; opens += nested.1; denials += nested.2
                    }
                } else if !(status == .attributeUnsupported && ["AXStaticText", "AXImage", "AXButton"].contains(role)) { capped = true }
                labels = labels.components(separatedBy: .whitespacesAndNewlines).filter { !$0.isEmpty }.joined(separator: " ")
                let titles = ["“ClaudeNotifier” would like to record this computer’s screen and audio.",
                              "\"ClaudeNotifier\" would like to record this computer's screen and audio."]
                let body = "Grant access to this application in Privacy & Security settings, located in System Settings."
                if titles.contains(where: { labels.contains($0) }), labels.contains(body), opens.count == 1, denials.count == 1,
                   let box = rectangle(element), windowBox.contains(box), box.contains(opens[0].1), box.contains(denials[0]),
                   !opens[0].1.intersects(denials[0]), box.width >= 200, box.height >= 120 {
                    candidates.append(FocusScreenDialog(app: app, start: start, element: element, box: box,
                        open: opens[0].0, openBox: opens[0].1, denyBox: denials[0]))
                }
                if labels.utf8.count > 16384 { capped = true; return ("", [], []) }
                return (labels, opens, denials)
            }
            _ = visit(window, 0)
            if capped { throw ProbeError.unavailable("screen_dialog_inventory_incomplete") }
        }
    }
    let smallest = candidates.filter { candidate in !candidates.contains {
        $0.app.processIdentifier == candidate.app.processIdentifier && candidate.box.contains($0.box)
            && $0.box.width * $0.box.height < candidate.box.width * candidate.box.height
    }}
    var unique = [FocusScreenDialog]()
    for candidate in smallest where !unique.contains(where: { $0.app.processIdentifier == candidate.app.processIdentifier && $0.box == candidate.box }) { unique.append(candidate) }
    guard unique.count <= 1 else { throw ProbeError.unavailable("screen_dialog_ambiguous") }
    guard let dialog = unique.first else { return nil }
    guard let url = dialog.app.bundleURL, let identifier = dialog.app.bundleIdentifier,
          verifiedAppleApplication(url, identifier: identifier),
          let identity = try? terminalProcessIdentity(dialog.app.processIdentifier),
          identity["executable_path"] as? String == dialog.app.executableURL?.resolvingSymlinksInPath().path,
          identity["process_start_identity"] as? String == dialog.start else { throw ProbeError.unavailable("screen_dialog_owner_unverified") }
    return dialog
}

@MainActor func focusPermissionScreenshot(_ path: String, deadline: Double) async throws -> Bool {
    let url = URL(fileURLWithPath: path), parent = url.deletingLastPathComponent()
    guard url.lastPathComponent == "focus-permission-pane.png", parent.lastPathComponent == "case-completion",
          parent.path == parent.resolvingSymlinksInPath().path,
          parent.deletingLastPathComponent().lastPathComponent.hasPrefix("TEST-pi-installed-macos-") else {
        throw ProbeError.unavailable("screen_snapshot_owned_path_required")
    }
    let fd = open(path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0o600)
    guard fd >= 0 else { throw ProbeError.unavailable("screen_snapshot_path_exists") }
    close(fd)
    let child = Process()
    child.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
    child.arguments = ["-x", path]
    child.standardInput = FileHandle.nullDevice; child.standardOutput = FileHandle.nullDevice; child.standardError = FileHandle.nullDevice
    try child.run()
    defer {
        if child.isRunning { kill(child.processIdentifier, SIGKILL) }
        child.waitUntilExit()
    }
    let stop = min(deadline, (try consentMonotonicTime()) + 0.6)
    while try consentMonotonicTime() < stop && child.isRunning { try await Task.sleep(nanoseconds: 10_000_000) }
    if child.isRunning { kill(child.processIdentifier, SIGKILL) }
    // Foundation retains/reaps this exact child; never rediscover another PID.
    child.waitUntilExit()
    return child.terminationStatus == 0
}

@MainActor func inspectFocusPermission(_ args: [String]) async throws -> [String: Any] {
    let started = try consentMonotonicTime(), env = ProcessInfo.processInfo.environment
    guard args.count == 2, args[1].utf8.count <= 16384, let data = args[1].data(using: .utf8),
          let context = try? JSONDecoder().decode(FocusPermissionContext.self, from: data), context.clock == "mach_absolute_time",
          context.deadlineMonotonic.isFinite, context.deadlineMonotonic > started, context.deadlineMonotonic - started <= 6,
          context.originalHelperBaselineEmpty, context.originalPhysicalClickObserved,
          env["PI_MACOS_FOCUS_INSPECTION_ADMITTED"] == "1", env["GITHUB_REPOSITORY"] == "777genius/agent-notifications",
          env["GITHUB_EVENT_NAME"] == "workflow_dispatch", env["GITHUB_ACTOR"] == "777genius", env["GITHUB_TRIGGERING_ACTOR"] == "777genius",
          env["GITHUB_RUN_ATTEMPT"] == "1", env["PI_MACOS_RUNNER_ENVIRONMENT"] == "github-hosted", env["ImageOS"] == "macos15",
          getuid() != 0, ProcessInfo.processInfo.operatingSystemVersion.majorVersion == 15 else { throw ProbeError.unavailable("original_admitted_focus_inspection_required") }
    let deadline = context.deadlineMonotonic, actionDeadline = deadline - 0.8
    let preparation = SettingsPresentationPreparation()
    var result: [String: Any] = ["scope": "original_click_screen_permission_inspection", "diagnostic_only": true,
        "qualification_input": false, "desktop_qualified": false, "maximum_seconds": 6, "status": "unavailable",
        "physical_open_settings_click": false, "permission_control_action_performed": false,
        "responsible_row_association": "display_name_only_unverified", "screenshot_child_collected": true]
    do {
        let helperURL = URL(fileURLWithPath: context.helperPath), executable = helperURL.appendingPathComponent("Contents/MacOS/terminal-notifier-modern")
        guard helperURL.path == helperURL.resolvingSymlinksInPath().path, helperURL.pathExtension == "app",
              Bundle(url: helperURL)?.bundleIdentifier == "com.777genius.agent-notifications",
              context.helperSHA256.count == 64,
              let size = try? executable.resourceValues(forKeys: [.fileSizeKey, .isRegularFileKey]),
              size.isRegularFile == true, let count = size.fileSize, count > 0, count <= 16 * 1024 * 1024,
              let helperData = try? Data(contentsOf: executable, options: .mappedIfSafe), helperData.count <= 16 * 1024 * 1024,
              SHA256.hash(data: helperData).map({ String(format: "%02x", $0) }).joined() == context.helperSHA256 else {
            throw ProbeError.unavailable("original_pinned_helper_required")
        }
        var code: SecStaticCode?
        guard SecStaticCodeCreateWithPath(helperURL as CFURL, [], &code) == errSecSuccess, let code,
              SecStaticCodeCheckValidity(code, SecCSFlags(rawValue: kSecCSStrictValidate), nil) == errSecSuccess else { throw ProbeError.unavailable("original_helper_signature_required") }
        let settingsPath = "/System/Applications/System Settings.app"
        guard verifiedAppleApplication(URL(fileURLWithPath: settingsPath), identifier: "com.apple.systempreferences") else { throw ProbeError.unavailable("signed_apple_settings_required") }
        let before = NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.systempreferences")
        guard before.count <= 1 else { throw ProbeError.unavailable("unique_settings_instance_required") }
        for app in before {
            guard let windows = attribute(AXUIElementCreateApplication(app.processIdentifier), kAXWindowsAttribute) as? [AXUIElement], windows.isEmpty else { throw ProbeError.unavailable("screen_settings_preexisting_window") }
        }
        result["settings_process_present_before"] = !before.isEmpty
        var captured: (NSRunningApplication, String)?
        let captureDeadline = min(actionDeadline, started + 1)
        while try consentMonotonicTime() < captureDeadline {
            let apps = NSWorkspace.shared.runningApplications.filter { !$0.isTerminated && $0.bundleURL?.resolvingSymlinksInPath() == helperURL && $0.executableURL?.resolvingSymlinksInPath() == executable }
            if apps.count == 1, let start = processStartIdentity(apps[0].processIdentifier), terminalAutomationUID(apps[0].processIdentifier) == Int(getuid()) { captured = (apps[0], start); break }
            if apps.count > 1 { throw ProbeError.unavailable("original_callback_helper_ambiguous") }
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        guard let (helper, helperStart) = captured else { throw ProbeError.unavailable("original_callback_helper_unavailable") }
        result["captured_helper_pid"] = Int(helper.processIdentifier); result["captured_helper_start"] = helperStart
        func requireActors() throws {
            let origin = try terminalProcessIdentity(context.originPID, expectedStart: context.originStart)
            guard try consentMonotonicTime() < actionDeadline, origin["status"] as? String == "observed",
                  origin["executable_path"] as? String == context.originExecutable, terminalAutomationUID(context.originPID) == Int(getuid()),
                  !helper.isTerminated, matchesProcessStart(helper.processIdentifier, helperStart),
                  terminalAutomationUID(helper.processIdentifier) == Int(getuid()),
                  (try? terminalProcessIdentity(helper.processIdentifier)["executable_path"] as? String) == executable.path else { throw ProbeError.unavailable("original_focus_actors_changed") }
        }
        var found: FocusScreenDialog?
        while try consentMonotonicTime() < actionDeadline {
            try requireActors()
            found = try focusScreenDialog(deadline: actionDeadline)
            if found != nil { break }
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        guard let dialog = found else { throw ProbeError.unavailable("original_screen_dialog_unavailable") }
        result["dialog"] = ["owner_pid": Int(dialog.app.processIdentifier), "owner_start": dialog.start,
            "owner_bundle": dialog.app.bundleIdentifier!, "owner_path": dialog.app.bundleURL!.path, "apple_signature_verified": true,
            "fixed_requester": "ClaudeNotifier", "fixed_body_matched": true, "distinct_open_and_deny": true,
            "open_bounds": [Double(dialog.openBox.minX), Double(dialog.openBox.minY), Double(dialog.openBox.width), Double(dialog.openBox.height)]]
        try settingsPhysicalClick(dialog.open, box: dialog.openBox, owner: dialog.app.processIdentifier, deadline: actionDeadline) {
            try requireActors()
            guard matchesProcessStart(dialog.app.processIdentifier, dialog.start),
                  let fresh = try focusScreenDialog(deadline: actionDeadline), fresh.app.processIdentifier == dialog.app.processIdentifier,
                  fresh.start == dialog.start, CFEqual(fresh.element, dialog.element), CFEqual(fresh.open, dialog.open),
                  fresh.box == dialog.box, fresh.openBox == dialog.openBox, fresh.denyBox == dialog.denyBox else { throw ProbeError.unavailable("original_screen_dialog_changed") }
        }
        result["physical_open_settings_click"] = true
        while try consentMonotonicTime() < actionDeadline {
            let apps = NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.systempreferences")
            if apps.count == 1 {
                let app = apps[0]
                if before.isEmpty || before[0].processIdentifier == app.processIdentifier,
                   app.bundleURL?.path == settingsPath, terminalAutomationUID(app.processIdentifier) == Int(getuid()), let start = processStartIdentity(app.processIdentifier),
                   let windows = attribute(AXUIElementCreateApplication(app.processIdentifier), kAXWindowsAttribute) as? [AXUIElement], windows.count == 1, let box = rectangle(windows[0]) {
                    preparation.app = app; preparation.start = start; preparation.window = windows[0]; preparation.windowBox = box
                    preparation.receipt["owned_window_observed"] = true
                    try preparation.requireWindow()
                    break
                }
            }
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        try preparation.requireWindow()
        guard let app = preparation.app else { throw ProbeError.unavailable("owned_settings_window_not_observed") }
        result["settings_owner_pid"] = Int(app.processIdentifier); result["settings_owner_start"] = preparation.start
        result["pane"] = settingsInspectionTree(app, deadline: actionDeadline).1
        result["screenshot_captured"] = try await focusPermissionScreenshot(context.screenshotPath, deadline: deadline - 0.35)
        result["status"] = "inspected"
    } catch {
        result["failure_class"] = error is ProbeError ? "probe_refusal" : "native_exception"
        if let value = error as? ProbeError, case .unavailable(let code) = value { result["refusal"] = code }
    }
    if preparation.window != nil {
        do { try await preparation.close(deadline: deadline) }
        catch { result["cleanup_failure_class"] = error is ProbeError ? "probe_refusal" : "native_exception" }
    }
    result["owned_window_observed"] = preparation.window != nil
    result["owned_window_disappeared"] = preparation.receipt["owned_window_disappeared"] as? Bool ?? false
    result["close_click_performed"] = preparation.receipt["close_click_performed"] as? Bool ?? false
    result["settings_process_collected"] = false
    result["elapsed_seconds"] = (try consentMonotonicTime()) - started
    return result
}

@MainActor struct SettingsInspectionNode {
    let element: AXUIElement
    let role: String
    let parent: Int?
    let box: CGRect?
    let exactNotifications: Bool
    let exactPolicyLabel: String?
    let press: Bool
    let inert: Bool
    let childrenVerified: Bool
    var subtreeComplete: Bool
}

@MainActor func settingsPassiveNavigationLeaf(_ element: AXUIElement, role: String, actions: [String]) -> (safe: Bool, status: AXError, settable: Bool) {
    var settable = DarwinBoolean(false)
    let status = AXUIElementIsAttributeSettable(element, kAXValueAttribute as CFString, &settable)
    // A contextual-menu action is never invoked by this fixture's left click.
    // Unknown/state-changing actions and writable values remain inadmissible.
    let safe = ["AXStaticText", "AXImage"].contains(role) && actions.allSatisfy { $0 == kAXShowMenuAction }
        && status == .success && !settable.boolValue
    return (safe, status, settable.boolValue)
}

@MainActor func settingsInspectionTree(_ app: NSRunningApplication, deadline: Double,
                                       retainedSidebar: AXUIElement? = nil, retainedDetail: AXUIElement? = nil) -> ([SettingsInspectionNode], [String: Any]) {
    let root = AXUIElementCreateApplication(app.processIdentifier)
    let timeout = AXUIElementSetMessagingTimeout(root, 0.05)
    var nodes = [SettingsInspectionNode](), rows = [[String: Any]](), capped = timeout != .success
    var capReasons = Set<String>()
    if capped { capReasons.insert("messaging_timeout") }
    let roles: Set<String> = ["AXApplication", "AXWindow", "AXGroup", "AXSplitGroup", "AXScrollArea", "AXOutline", "AXList",
        "AXRow", "AXStaticText", "AXButton", "AXCheckBox", "AXPopUpButton", "AXRadioButton", "AXRadioGroup", "AXToolbar",
        "AXScrollBar", "AXSlider", "AXTextField", "AXTable", "AXColumn", "AXCell", "AXImage", "AXHeading"]
    @discardableResult func visit(_ element: AXUIElement, parent: Int?, depth: Int) -> Bool {
        guard let now = try? consentMonotonicTime(), now < deadline else { capped = true; capReasons.insert("deadline"); return false }
        guard nodes.count < 256, depth < 18 else { capped = true; capReasons.insert("node_or_depth_limit"); return false }
        let rawRole = attribute(element, kAXRoleAttribute) as? String ?? ""
        let role = roles.contains(rawRole) ? rawRole : "other"
        let texts = [kAXTitleAttribute, kAXDescriptionAttribute, kAXValueAttribute].compactMap { attribute(element, $0) as? String }
        var tags = Set<String>()
        let labels = ["Notifications": "notifications", "Notification Center": "notification_center",
            "Application Notifications": "application_notifications", "Allow notifications": "allow_notifications",
            "Allow Notifications": "allow_notifications", "Notifications Off": "notifications_off", "Banners": "banners",
            "Alerts": "alerts", "None": "none", "Sound": "sound", "Sounds": "sound", "Focus": "focus",
            "Screen Recording": "screen_recording", "Screen & System Audio Recording": "screen_system_audio_recording",
            "Privacy & Security": "privacy_security", "ClaudeNotifier": "candidate_claude_notifier", "Agent Notifications": "candidate_agent_notifications",
            "Quit & Reopen": "quit_reopen", "Later": "later", "Open System Settings": "open_system_settings", "Deny": "deny",
            "Do Not Disturb": "do_not_disturb", "Always": "always", "When Unlocked": "when_unlocked", "Never": "never"]
        for text in texts {
            if let tag = labels[text] { tags.insert(tag) }
            let folded = text.lowercased()
            if folded.contains("mirroring") || (folded.contains("sharing") && folded.contains("display")) { tags.insert("mirroring_or_sharing") }
            if folded.contains("display") && folded.contains("sleep") { tags.insert("display_sleeping") }
            if folded.contains("screen") && folded.contains("locked") { tags.insert("screen_locked") }
            if folded.contains("play sound") { tags.insert("play_sound") }
            if folded.contains("alert style") { tags.insert("alert_style") }
            if folded.contains("notifications silenced") { tags.insert("notifications_silenced") }
        }
        var actionNames: CFArray?
        let actionStatus = AXUIElementCopyActionNames(element, &actionNames)
        let actions = actionStatus == .success ? actionNames as? [String] : nil
        var childValues: CFTypeRef?
        let childStatus = AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &childValues)
        let children = childStatus == .success ? childValues as? [AXUIElement] : nil
        let leafRoles = ["AXStaticText", "AXImage", "AXCheckBox", "AXPopUpButton", "AXRadioButton", "AXSlider", "AXTextField", "AXButton", "AXHeading"]
        let unsupportedLeaf = childStatus == .attributeUnsupported && leafRoles.contains(role)
        // A known static leaf may explicitly lack children. Missing values,
        // malformed arrays and failed container reads never prove completeness.
        if children == nil && !unsupportedLeaf { capped = true; capReasons.insert("children_unavailable") }
        let pressed = actions?.contains(kAXPressAction) == true
        let passive = ["AXStaticText", "AXImage"].contains(role) && actions != nil
            ? settingsPassiveNavigationLeaf(element, role: role, actions: actions!) : nil
        let inert = ((["AXCell", "AXGroup"].contains(role) && actions?.isEmpty == true) || passive?.safe == true)
            && (children != nil || (unsupportedLeaf && ["AXStaticText", "AXImage"].contains(role)))
        let index = nodes.count, box = rectangle(element)
        let policyLabels = ["Allow notifications when mirroring or sharing the display": "mirroring_or_sharing",
            "Allow notifications when the display is sleeping": "display_sleeping",
            "Allow notifications when the screen is locked": "screen_locked"]
        nodes.append(SettingsInspectionNode(element: element, role: role, parent: parent, box: box,
            exactNotifications: texts.contains("Notifications"), exactPolicyLabel: texts.compactMap { policyLabels[$0] }.first,
            press: pressed, inert: inert,
            childrenVerified: children != nil, subtreeComplete: children != nil || unsupportedLeaf))
        var row: [String: Any] = ["index": index, "depth": depth, "role": role, "fixed_labels": tags.sorted(),
            "navigation_press_action": pressed, "inert_navigation_descendant": inert,
            "children_query_status": childStatus.rawValue, "children_array_verified": children != nil,
            "action_query_status": actionStatus.rawValue, "action_array_verified": actions != nil,
            "subtree_complete": children != nil || unsupportedLeaf]
        let publicActions = ["press": kAXPressAction, "increment": kAXIncrementAction, "decrement": kAXDecrementAction,
            "confirm": kAXConfirmAction, "pick": kAXPickAction, "show_menu": kAXShowMenuAction]
        row["public_action_bits"] = publicActions.mapValues { actions?.contains($0) == true }
        row["unclassified_action_count"] = actions?.filter { !publicActions.values.contains($0) }.count ?? 0
        if let passive {
            row["value_settable_query_status"] = passive.status.rawValue
            if passive.status == .success { row["value_settable"] = passive.settable }
        }
        if let parent { row["parent"] = parent }
        if let box { row["bounds"] = [Double(box.minX), Double(box.minY), Double(box.width), Double(box.height)] }
        for (key, name) in [(kAXEnabledAttribute, "enabled"), (kAXSelectedAttribute, "selected")] {
            if let value = attribute(element, key), CFGetTypeID(value) == CFBooleanGetTypeID() { row[name] = (value as! NSNumber).boolValue }
        }
        if let value = attribute(element, kAXValueAttribute) {
            if CFGetTypeID(value) == CFBooleanGetTypeID() { row["value_boolean"] = (value as! NSNumber).boolValue }
            else if ["AXCheckBox", "AXRadioButton"].contains(role), let number = value as? NSNumber,
                    [0.0, 1.0].contains(number.doubleValue) { row["value_enum"] = number.doubleValue == 1 ? "on" : "off" }
            else if let text = value as? String, let fixed = labels[text] { row["value_enum"] = fixed }
        }
        rows.append(row)
        guard let children else { return unsupportedLeaf }
        let childLimit = ["AXOutline", "AXList"].contains(role) ? 64 : 32
        var complete = children.count <= childLimit
        if !complete { capped = true; capReasons.insert("children_limit") }
        for child in children.prefix(childLimit) { if !visit(child, parent: index, depth: depth + 1) { complete = false } }
        nodes[index].subtreeComplete = complete
        rows[index]["subtree_complete"] = complete
        return complete
    }
    if timeout == .success, let retainedSidebar {
        visit(retainedSidebar, parent: nil, depth: 0)
    } else if timeout == .success, let retainedDetail {
        visit(retainedDetail, parent: nil, depth: 0)
    } else if timeout == .success, let windows = attribute(root, kAXWindowsAttribute) as? [AXUIElement] {
        if windows.count > 4 { capped = true; capReasons.insert("window_limit") }
        for window in windows.prefix(4) { visit(window, parent: nil, depth: 0) }
    } else { capped = true; capReasons.insert("windows_unavailable") }
    return (nodes, ["nodes": rows, "traversal_capped": capped, "maximum_nodes": 256, "maximum_depth": 18,
        "maximum_children": 64, "cap_reasons": capReasons.sorted(), "ax_timeout_status": timeout.rawValue,
        "tree_scope": retainedSidebar != nil ? "retained_sidebar" : retainedDetail != nil ? "retained_detail" : "app_windows"])
}

@MainActor func settingsPhysicalClick(_ element: AXUIElement, box: CGRect, owner: pid_t, deadline: Double,
                                      verify: () throws -> Void) throws {
    try verify()
    let point = CGPoint(x: box.midX, y: box.midY)
    guard try consentMonotonicTime() < deadline, CGWarpMouseCursorPosition(point) == .success else {
        throw ProbeError.unavailable("settings_owned_pointer_unavailable")
    }
    var hit: AXUIElement?
    guard AXUIElementCopyElementAtPosition(AXUIElementCreateSystemWide(), Float(point.x), Float(point.y), &hit) == .success,
          var current = hit else { throw ProbeError.unavailable("settings_owned_hit_unavailable") }
    var matched = false
    for _ in 0..<4 {
        var pid = pid_t(0)
        guard AXUIElementGetPid(current, &pid) == .success, pid == owner else { break }
        if CFEqual(current, element) { matched = true; break }
        guard let role = attribute(current, kAXRoleAttribute) as? String, ["AXStaticText", "AXImage"].contains(role),
              let parent = attribute(current, kAXParentAttribute), CFGetTypeID(parent) == AXUIElementGetTypeID() else { break }
        current = parent as! AXUIElement
    }
    try verify()
    guard matched, rectangle(element) == box, try consentMonotonicTime() < deadline,
          let cursor = CGEvent(source: nil)?.location, abs(cursor.x - point.x) <= 1, abs(cursor.y - point.y) <= 1,
          let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left),
          let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left) else {
        throw ProbeError.unavailable("settings_owned_click_refused")
    }
    down.post(tap: .cghidEventTap); up.post(tap: .cghidEventTap)
}

@MainActor final class SettingsPresentationPreparation {
    var app: NSRunningApplication?
    var start = ""
    var window: AXUIElement?
    var windowBox: CGRect?
    var receipt: [String: Any] = ["scope": "fresh_owned_public_notification_presentation_preparation",
        "qualification_input": false, "desktop_qualified": false, "status": "refused", "maximum_seconds": 6,
        "settings_instance_was_new": false, "owned_window_observed": false, "owned_window_disappeared": false,
        "close_click_performed": false, "settings_process_collected": false, "sharing_click_performed": false]
    func current() -> Bool {
        guard let app else { return false }
        return !app.isTerminated && matchesProcessStart(app.processIdentifier, start)
            && terminalAutomationUID(app.processIdentifier) == Int(getuid())
            && (try? terminalProcessIdentity(app.processIdentifier)["executable_path"] as? String)
                == "/System/Applications/System Settings.app/Contents/MacOS/System Settings"
    }
    func requireWindow() throws {
        guard current(), let app, let window, let box = windowBox,
              let windows = attribute(AXUIElementCreateApplication(app.processIdentifier), kAXWindowsAttribute) as? [AXUIElement],
              windows.count == 1, CFEqual(windows[0], window), rectangle(window) == box,
              onScreenWindows(app.processIdentifier).contains(box) else { throw ProbeError.unavailable("original_owned_settings_window_changed") }
    }
    func close(deadline: Double) async throws {
        guard let app, let window else { throw ProbeError.unavailable("owned_settings_window_not_observed") }
        try requireWindow()
        guard let value = attribute(window, kAXCloseButtonAttribute), CFGetTypeID(value) == AXUIElementGetTypeID() else {
            throw ProbeError.unavailable("owned_settings_close_button_unavailable")
        }
        let button = value as! AXUIElement
        guard let box = rectangle(button), attribute(button, kAXRoleAttribute) as? String == kAXButtonRole,
              attribute(button, kAXSubroleAttribute) as? String == kAXCloseButtonSubrole else {
            throw ProbeError.unavailable("owned_settings_close_button_unverified")
        }
        try settingsPhysicalClick(button, box: box, owner: app.processIdentifier, deadline: deadline) {
            try self.requireWindow()
            guard let fresh = attribute(window, kAXCloseButtonAttribute), CFEqual(fresh, button),
                  attribute(button, kAXRoleAttribute) as? String == kAXButtonRole,
                  attribute(button, kAXSubroleAttribute) as? String == kAXCloseButtonSubrole,
                  attribute(button, kAXEnabledAttribute) as? Bool == true else { throw ProbeError.unavailable("owned_settings_close_button_changed") }
        }
        receipt["close_click_performed"] = true
        while try consentMonotonicTime() < deadline {
            let windows = attribute(AXUIElementCreateApplication(app.processIdentifier), kAXWindowsAttribute) as? [AXUIElement]
            if onScreenWindows(app.processIdentifier).isEmpty && (windows?.contains(where: { CFEqual($0, window) }) == false || app.isTerminated) {
                receipt["owned_window_disappeared"] = true
                receipt["settings_process_running_after_cleanup"] = current()
                return
            }
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        throw ProbeError.unavailable("owned_settings_window_disappearance_unverified")
    }
}

@MainActor struct SettingsPolicyPair {
    let label: SettingsInspectionNode
    let button: SettingsInspectionNode
    let group: SettingsInspectionNode
}

@MainActor func settingsPolicyPairs(_ nodes: [SettingsInspectionNode]) -> [String: SettingsPolicyPair]? {
    let labels = ["mirroring_or_sharing": "Allow notifications when mirroring or sharing the display",
        "display_sleeping": "Allow notifications when the display is sleeping", "screen_locked": "Allow notifications when the screen is locked"]
    var pairs = [String: SettingsPolicyPair]()
    for (kind, _) in labels {
        let matches = nodes.enumerated().compactMap { index, label -> SettingsPolicyPair? in
            guard label.exactPolicyLabel == kind, label.role == "AXStaticText", let parent = label.parent,
                  nodes[parent].role == "AXGroup", nodes[parent].childrenVerified, nodes[parent].subtreeComplete,
                  index + 1 < nodes.count, let textBox = label.box else { return nil }
            let button = nodes[index + 1]
            guard button.parent == parent, button.role == "AXCheckBox", button.subtreeComplete,
                  button.press, let box = button.box, box.minX >= textBox.maxX,
                  textBox.minY <= box.midY, box.midY <= textBox.maxY else { return nil }
            return SettingsPolicyPair(label: label, button: button, group: nodes[parent])
        }
        guard matches.count == 1 else { return nil }
        pairs[kind] = matches[0]
    }
    guard let sharing = pairs["mirroring_or_sharing"], pairs.values.allSatisfy({ CFEqual($0.group.element, sharing.group.element) }) else { return nil }
    return pairs
}

@MainActor func prepareNotificationPresentation(_ nodes: [SettingsInspectionNode], preparation: SettingsPresentationPreparation,
                                                deadline: Double) async throws {
    let labels = ["mirroring_or_sharing": "Allow notifications when mirroring or sharing the display",
        "display_sleeping": "Allow notifications when the display is sleeping", "screen_locked": "Allow notifications when the screen is locked"]
    guard let pairs = settingsPolicyPairs(nodes), let app = preparation.app, let sharing = pairs["mirroring_or_sharing"] else {
        throw ProbeError.unavailable("sharing_policy_exact_control_required")
    }
    func frame() throws -> [String: String] {
        try preparation.requireWindow()
        guard try consentMonotonicTime() < deadline,
              let children = attribute(sharing.group.element, kAXChildrenAttribute) as? [AXUIElement] else {
            throw ProbeError.unavailable("sharing_policy_original_group_changed")
        }
        var states = [String: String]()
        for (kind, pair) in pairs {
            var pid = pid_t(0), labelPID = pid_t(0), groupPID = pid_t(0)
            guard AXUIElementGetPid(pair.button.element, &pid) == .success, pid == app.processIdentifier,
                  AXUIElementGetPid(pair.label.element, &labelPID) == .success, labelPID == pid,
                  AXUIElementGetPid(pair.group.element, &groupPID) == .success, groupPID == pid,
                  attribute(pair.group.element, kAXRoleAttribute) as? String == "AXGroup",
                  attribute(pair.label.element, kAXRoleAttribute) as? String == "AXStaticText",
                  let index = children.firstIndex(where: { CFEqual($0, pair.label.element) }), index + 1 < children.count,
                  CFEqual(children[index + 1], pair.button.element), rectangle(pair.label.element) == pair.label.box,
                  rectangle(pair.button.element) == pair.button.box, rectangle(pair.group.element) == pair.group.box,
                  [kAXTitleAttribute, kAXDescriptionAttribute, kAXValueAttribute].contains(where: { attribute(pair.label.element, $0) as? String == labels[kind] }),
                  attribute(pair.button.element, kAXRoleAttribute) as? String == "AXCheckBox",
                  attribute(pair.button.element, kAXEnabledAttribute) as? Bool == true,
                  let value = attribute(pair.button.element, kAXValueAttribute) as? NSNumber,
                  [0.0, 1.0].contains(value.doubleValue) else { throw ProbeError.unavailable("sharing_policy_original_control_changed") }
            states[kind] = value.doubleValue == 1 ? "on" : "off"
        }
        return states
    }
    let before = try frame()
    preparation.receipt["policy_before"] = before
    preparation.receipt["sharing_checkbox_bounds"] = sharing.button.box.map { [Double($0.minX), Double($0.minY), Double($0.width), Double($0.height)] }
    if before["mirroring_or_sharing"] == "off" {
        guard let box = sharing.button.box else { throw ProbeError.unavailable("sharing_policy_control_bounds_unavailable") }
        try settingsPhysicalClick(sharing.button.element, box: box, owner: app.processIdentifier, deadline: deadline) {
            guard try frame() == before else { throw ProbeError.unavailable("sharing_policy_state_changed_before_click") }
        }
        preparation.receipt["sharing_click_performed"] = true
        let settle = min(deadline, (try consentMonotonicTime()) + 0.75)
        while try consentMonotonicTime() < settle {
            if try frame()["mirroring_or_sharing"] == "on" { break }
            try await Task.sleep(nanoseconds: 20_000_000)
        }
    }
    let after = try frame()
    preparation.receipt["policy_after"] = after
    guard after["mirroring_or_sharing"] == "on", before["display_sleeping"] == after["display_sleeping"],
          before["screen_locked"] == after["screen_locked"] else { throw ProbeError.unavailable("sharing_policy_on_or_unchanged_controls_unverified") }
    preparation.receipt["policy_on_verified"] = true
}

@MainActor func prepareGlobalNotificationPresentation(_ args: [String]) async throws -> [String: Any] {
    let started = try consentMonotonicTime()
    let environment = ProcessInfo.processInfo.environment
    guard args.count == 2, let deadline = Double(args[1]), deadline.isFinite, deadline > started, deadline - started <= 6,
          environment["PI_MACOS_PRESENTATION_ADMITTED"] == "1",
          environment["GITHUB_REPOSITORY"] == "777genius/agent-notifications", environment["GITHUB_EVENT_NAME"] == "workflow_dispatch",
          environment["GITHUB_ACTOR"] == "777genius", environment["GITHUB_TRIGGERING_ACTOR"] == "777genius",
          environment["GITHUB_RUN_ATTEMPT"] == "1", environment["PI_MACOS_RUNNER_ENVIRONMENT"] == "github-hosted",
          environment["ImageOS"] == "macos15", getuid() != 0 else { throw ProbeError.unavailable("explicit_fresh_owner_presentation_preparation_required") }
    let preparation = SettingsPresentationPreparation()
    do {
        preparation.receipt["inspection_before_cleanup"] = try await inspectGlobalNotificationSettings(args, preparation: preparation)
    } catch {
        preparation.receipt["primary_failure_class"] = error is ProbeError ? "probe_refusal" : "native_exception"
        if let refusal = error as? ProbeError, case .unavailable(let code) = refusal { preparation.receipt["primary_refusal"] = code }
    }
    do { try await preparation.close(deadline: deadline) }
    catch {
        preparation.receipt["cleanup_failure_class"] = error is ProbeError ? "probe_refusal" : "native_exception"
        if let refusal = error as? ProbeError, case .unavailable(let code) = refusal { preparation.receipt["cleanup_refusal"] = code }
    }
    if preparation.receipt["primary_failure_class"] == nil, preparation.receipt["cleanup_failure_class"] == nil,
       preparation.receipt["policy_on_verified"] as? Bool == true, preparation.receipt["owned_window_disappeared"] as? Bool == true {
        preparation.receipt["status"] = "prepared"
    }
    preparation.receipt["elapsed_seconds"] = (try consentMonotonicTime()) - started
    return preparation.receipt
}

@MainActor func inspectGlobalNotificationSettings(_ args: [String], preparation: SettingsPresentationPreparation? = nil) async throws -> [String: Any] {
    let now = try consentMonotonicTime()
    guard args.count == 2, let deadline = Double(args[1]), deadline.isFinite, deadline > now, deadline - now <= 6 else {
        throw ProbeError.unavailable("original_settings_inspection_deadline_required")
    }
    let identifier = "com.apple.systempreferences", path = "/System/Applications/System Settings.app"
    let url = URL(fileURLWithPath: path)
    guard verifiedAppleApplication(url, identifier: identifier) else { throw ProbeError.unavailable("signed_apple_settings_required") }
    let before = NSRunningApplication.runningApplications(withBundleIdentifier: identifier).map { Int($0.processIdentifier) }
    guard before.count <= 1 else { throw ProbeError.unavailable("unique_settings_instance_required") }
    if preparation != nil && !before.isEmpty { throw ProbeError.unavailable("new_owned_settings_instance_required") }
    let configuration = NSWorkspace.OpenConfiguration()
    configuration.activates = true
    configuration.createsNewApplicationInstance = false
    let pane = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension")!
    let app = try await NSWorkspace.shared.open([pane], withApplicationAt: url, configuration: configuration)
    guard app.bundleIdentifier == identifier, app.bundleURL?.resolvingSymlinksInPath().path == path,
          app.executableURL?.resolvingSymlinksInPath().path == path + "/Contents/MacOS/System Settings",
          terminalAutomationUID(app.processIdentifier) == Int(getuid()), let start = processStartIdentity(app.processIdentifier) else {
        throw ProbeError.unavailable("settings_owner_identity_unverified")
    }
    preparation?.app = app
    preparation?.start = start
    preparation?.receipt["settings_instance_was_new"] = true
    func current() -> Bool {
        !app.isTerminated && matchesProcessStart(app.processIdentifier, start)
            && terminalAutomationUID(app.processIdentifier) == Int(getuid())
            && (try? terminalProcessIdentity(app.processIdentifier)["executable_path"] as? String) == path + "/Contents/MacOS/System Settings"
    }
    while current() && onScreenWindows(app.processIdentifier).isEmpty {
        if try consentMonotonicTime() >= deadline { break }
        try await Task.sleep(nanoseconds: 50_000_000)
    }
    guard current(), !onScreenWindows(app.processIdentifier).isEmpty, try consentMonotonicTime() < deadline else {
        throw ProbeError.unavailable("settings_window_unavailable")
    }
    if let preparation {
        guard let windows = attribute(AXUIElementCreateApplication(app.processIdentifier), kAXWindowsAttribute) as? [AXUIElement],
              windows.count == 1, let box = rectangle(windows[0]) else { throw ProbeError.unavailable("single_new_owned_settings_window_required") }
        preparation.window = windows[0]; preparation.windowBox = box
        preparation.receipt["owned_window_observed"] = true
        try preparation.requireWindow()
    }
    func pageReady(_ nodes: [SettingsInspectionNode]) -> Bool {
        guard current(), let focused = attribute(AXUIElementCreateApplication(app.processIdentifier), kAXFocusedWindowAttribute),
              CFGetTypeID(focused) == AXUIElementGetTypeID(),
              attribute(focused as! AXUIElement, kAXTitleAttribute) as? String == "Notifications" else { return false }
        let windows = nodes.indices.filter { nodes[$0].role == "AXWindow" && CFEqual(nodes[$0].element, focused) }
        guard windows.count == 1 else { return false }
        for kind in ["mirroring_or_sharing", "display_sleeping", "screen_locked"] {
            let labels = nodes.filter { $0.role == "AXStaticText" && $0.exactPolicyLabel == kind }
            guard labels.count == 1 else { return false }
            var parent = labels[0].parent, inWindow = false
            for _ in 0..<18 {
                guard let index = parent else { break }
                if index == windows[0] { inWindow = true; break }
                parent = nodes[index].parent
            }
            if !inWindow { return false }
        }
        guard current(), let observed = try? consentMonotonicTime() else { return false }
        return observed < deadline
    }
    var first = settingsInspectionTree(app, deadline: deadline)
    let readyDeadline = min(deadline, (try consentMonotonicTime()) + 2)
    var ready = pageReady(first.0)
    while !ready && current() {
        if try consentMonotonicTime() >= readyDeadline { break }
        try await Task.sleep(nanoseconds: 50_000_000)
        first = settingsInspectionTree(app, deadline: deadline)
        ready = pageReady(first.0)
    }
    var result: [String: Any] = ["scope": "readonly_global_notification_settings", "diagnostic_only": true,
        "qualification_input": false, "desktop_qualified": false, "maximum_seconds": 6,
        "owner_pid": Int(app.processIdentifier), "owner_start_identity": start, "owner_uid": Int(getuid()),
        "owner_bundle": identifier, "owner_path": path, "apple_signature_verified": true, "settings_left_open": true,
        "settings_present_before": before, "opened_new_settings_instance": !before.contains(Int(app.processIdentifier)),
        "initial_tree": first.1, "notifications_page_ready": ready,
        "notifications_navigation_performed": false, "sidebar_scroll_performed": false,
        "observed_notifications_sidebar_count": 0, "observed_notifications_navigation_rows": 0]
    result["elapsed_seconds"] = (try consentMonotonicTime()) - now
    preparation?.receipt["inspection_before_cleanup"] = result
    // The fixed pane URL opens the actual policy page without sidebar input.
    // Preserve the original six-second budget for preparation and owned cleanup.
    if let preparation {
        guard ready else { throw ProbeError.unavailable("notifications_policy_page_not_ready") }
        try await prepareNotificationPresentation(first.0, preparation: preparation, deadline: deadline)
    }
    result["elapsed_seconds"] = (try consentMonotonicTime()) - now
    return result
}

@MainActor func performConsentAction(_ args: [String]) async throws -> [String: Any] {
    let operation = args[0]
    let contexted = operation != "notification-consent"
    let action = contexted && operation != "observe-notification-consent-menu"
    let names = Array(args.dropFirst(contexted ? 2 : 1))
    guard (1...3).contains(names.count), names.allSatisfy({ !$0.isEmpty && $0.count <= 80
        && !$0.unicodeScalars.contains(where: { $0.value < 32 }) }) else {
        throw ProbeError.unavailable("verified_permission_display_identity_required")
    }
    var cards = notificationPermissionCards(names)
    var dialogs = notificationConsent(cards)
    var result: [String: Any] = [:]
    if contexted {
        guard args.count >= 3, args[1].utf8.count <= 8192,
              let data = args[1].data(using: .utf8),
              let context = try? JSONDecoder().decode(ConsentActionContext.self, from: data),
              context.app.hasPrefix("/"), context.app.hasSuffix(".app"), context.helperPID > 0,
              context.requestPID > 0, context.ownerPID > 0, !context.helperStart.isEmpty,
              !context.requestStart.isEmpty, !context.ownerStart.isEmpty, context.requestExecutable.hasPrefix("/"),
              context.deadlineMonotonic.isFinite, context.clock == "mach_absolute_time", names.contains(context.candidateName),
              context.cardBounds.count == 4, context.cardBounds.allSatisfy({ $0.isFinite }),
              context.cardBounds[2] > 0, context.cardBounds[3] > 0 else {
            throw ProbeError.unavailable("original_consent_actors_card_and_budget_required")
        }
        let now = try consentMonotonicTime()
        guard context.deadlineMonotonic > now, context.deadlineMonotonic - now <= 130 else {
            throw ProbeError.unavailable("original_consent_deadline_expired_or_clock_mismatched")
        }
        let deadline = min(context.deadlineMonotonic, now + 3)
        let app = URL(fileURLWithPath: context.app).resolvingSymlinksInPath().path
        let executable = URL(fileURLWithPath: app).appendingPathComponent("Contents/MacOS/terminal-notifier-modern").path
        let originalBox = CGRect(x: context.cardBounds[0], y: context.cardBounds[1], width: context.cardBounds[2], height: context.cardBounds[3])
        func requireActors() throws {
            let request = try terminalProcessIdentity(context.requestPID, expectedStart: context.requestStart)
            let helper = try terminalProcessIdentity(context.helperPID, expectedStart: context.helperStart)
            let helpers = NSWorkspace.shared.runningApplications.filter {
                $0.bundleIdentifier == "com.777genius.agent-notifications" && !$0.isTerminated
                && $0.bundleURL?.resolvingSymlinksInPath().path == app
                && $0.executableURL?.resolvingSymlinksInPath().path == executable
            }
            guard try consentMonotonicTime() < deadline,
                  request["status"] as? String == "observed", request["executable_path"] as? String == context.requestExecutable,
                  helper["status"] as? String == "observed", helper["executable_path"] as? String == executable,
                  helper["parent_pid"] as? Int == Int(context.requestPID), helpers.count == 1,
                  helpers[0].processIdentifier == context.helperPID, helpers[0].isFinishedLaunching,
                  matchesProcessStart(context.helperPID, context.helperStart),
                  matchesProcessStart(context.ownerPID, context.ownerStart) else {
                throw ProbeError.unavailable("original_consent_request_helper_or_owner_changed")
            }
        }
        // Refresh the original card and its actual Options control before input.
        var actualOptions: AXUIElement?
        cards = notificationPermissionCards(names, includeControls: true, optionsObserver: { actualOptions = $0 })
        guard cards.count == 1, cards[0]["os_dialog_pid"] as? Int == Int(context.ownerPID),
              cards[0]["os_owner_start_identity"] as? String == context.ownerStart,
              cards[0]["candidate_name"] as? String == context.candidateName,
              let bounds = cards[0]["bounds"] as? [Double], bounds.count == 4,
              CGRect(x: bounds[0], y: bounds[1], width: bounds[2], height: bounds[3]).intersects(originalBox) else {
            throw ProbeError.unavailable("original_candidate_notification_consent_card_changed")
        }
        try requireActors()
        result = ["original_request_verified": true, "installed_helper_pid": Int(context.helperPID),
                  "installed_helper_start_identity": context.helperStart, "original_request_pid": Int(context.requestPID),
                  "original_request_start_identity": context.requestStart,
                  "deadline_monotonic": context.deadlineMonotonic, "clock": context.clock]
        var menu: ConsentMenu?
        if operation == "observe-notification-consent-menu" || (operation == "allow-notification-consent" && context.menuWindowID != nil) {
            guard let windowID = context.menuWindowID, windowID > 0,
                  let menuBounds = context.menuBounds, let optionsBounds = context.optionsBounds,
                  cards[0]["options_bounds"] as? [Double] == optionsBounds, let actualOptions else {
                throw ProbeError.unavailable("original_options_menu_context_required")
            }
            do {
                menu = try consentMenu(context.ownerPID, windowID: windowID, bounds: menuBounds,
                                       optionsBounds: optionsBounds, options: actualOptions)
                result["observed_menu"] = menu!.receipt
                var dialog = cards[0]
                dialog.merge(menu!.receipt) { _, new in new }
                dialog["bounds"] = menu!.receipt["allow_bounds"]
                dialog["allow_button"] = "Allow"
                dialogs = [dialog]
            } catch {
                if action { throw error }
                dialogs = []
                result["menu_observation_failure"] = String(describing: error)
            }
        }
        if action {
            let target: [Double]
            if operation == "hover-notification-consent" {
                target = bounds
            } else if operation == "open-notification-consent-options" {
                guard let options = cards[0]["options_bounds"] as? [Double] else {
                    throw ProbeError.unavailable("unique_candidate_notification_options_control_required")
                }
                target = options
            } else {
                if let menu {
                    target = menu.receipt["allow_bounds"] as! [Double]
                } else {
                    guard operation == "allow-notification-consent", cards[0]["denial_button_observed"] as? Bool == true,
                          let allow = cards[0]["allow_bounds"] as? [Double] else {
                        throw ProbeError.unavailable("candidate_consent_allow_and_denial_unavailable")
                    }
                    target = allow
                }
            }
            guard target.count == 4, target.allSatisfy({ $0.isFinite }), target[2] > 0, target[3] > 0 else {
                throw ProbeError.unavailable("actual_consent_control_bounds_unavailable")
            }
            let point = CGPoint(x: target[0] + target[2] / 2, y: target[1] + target[3] / 2)
            let targetBounds = menu?.receipt["bounds"] as? [Double] ?? bounds
            let box = consentBox(targetBounds)!
            guard box.contains(point), let before = CGEvent(source: nil)?.location else {
                throw ProbeError.unavailable("candidate_scoped_pointer_target_unavailable")
            }
            try requireActors()
            let menusBefore = operation == "open-notification-consent-options" ? consentPopupWindows(context.ownerPID) : []
            guard menusBefore.isEmpty else { throw ProbeError.unavailable("existing_notification_popup_is_ambiguous") }
            result["action_before_card"] = cards[0]
            // Position and event are separate public Quartz operations. Retain the
            // observed cursor position instead of assuming a posted move took effect.
            guard CGWarpMouseCursorPosition(point) == .success else {
                throw ProbeError.unavailable("physical_permission_pointer_position_failed")
            }
            if operation == "hover-notification-consent" {
                try requireActors()
                guard let event = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved,
                                         mouseCursorPosition: point, mouseButton: .left) else {
                    throw ProbeError.unavailable("physical_permission_hover_unavailable")
                }
                event.post(tap: .cghidEventTap)
            } else {
                try requireActors()
                if let menu {
                    try requireTopmostConsentItem(menu, owner: context.ownerPID)
                    result["topmost_allow_item_verified"] = true
                }
                guard let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left),
                      let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left) else {
                    throw ProbeError.unavailable("physical_permission_click_unavailable")
                }
                down.post(tap: .cghidEventTap); up.post(tap: .cghidEventTap)
            }
            try await Task.sleep(nanoseconds: 200_000_000)
            guard let after = CGEvent(source: nil)?.location else { throw ProbeError.unavailable("native_pointer_observation_unavailable") }
            result.merge(["action_target_bounds": target, "pointer_before": [Double(before.x), Double(before.y)],
                      "pointer_after": [Double(after.x), Double(after.y)], "pointer_target": [Double(point.x), Double(point.y)],
                      "pointer_at_target": abs(after.x - point.x) <= 1 && abs(after.y - point.y) <= 1,
                      "original_request_verified": true, "installed_helper_pid": Int(context.helperPID),
                      "installed_helper_start_identity": context.helperStart, "original_request_pid": Int(context.requestPID),
                      "original_request_start_identity": context.requestStart,
                      "deadline_monotonic": context.deadlineMonotonic, "clock": context.clock,
                      "denial_button_observed": menu != nil || cards[0]["denial_button_observed"] as? Bool == true]) { _, new in new }
            cards = notificationPermissionCards(names, includeControls: true)
            dialogs = notificationConsent(cards)
            if operation == "open-notification-consent-options" {
                let opened = consentPopupWindows(context.ownerPID)
                result["popup_windows_before_options"] = menusBefore
                result["popup_windows_after_options"] = opened
                if opened.count == 1 {
                    var evidence = opened[0]
                    evidence["options_bounds"] = target
                    evidence["new_after_original_options_click"] = true
                    result["opened_menu_window"] = evidence
                }
            }
        }
        if !action { try requireActors() }
    }
    result["dialogs"] = dialogs
    result["permission_cards"] = cards
    result["physical_pointer_hover"] = operation == "hover-notification-consent"
    result["physical_options_click"] = operation == "open-notification-consent-options"
    result["physical_allow_click"] = operation == "allow-notification-consent"
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
            case "terminal-automation-actors":
                guard args.count == 3, let pid = Int32(args[1]), let parent = Int32(args[2]), pid > 0, parent > 0 else {
                    throw ProbeError.unavailable("original_osascript_pid_required")
                }
                publish(try terminalAutomationActors(pid, pythonPID: parent))
            case "terminal-automation-consent", "allow-terminal-automation":
                publish(try terminalAutomation(args))
            case "inspect-global-notification-settings":
                publish(try await inspectGlobalNotificationSettings(args))
            case "prepare-global-notification-presentation":
                publish(try await prepareGlobalNotificationPresentation(args))
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
            case "inspect-focus-screen-permission": publish(try await inspectFocusPermission(args))
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
            case "notification-consent", "observe-notification-consent-menu", "hover-notification-consent", "open-notification-consent-options", "allow-notification-consent":
                publish(try await performConsentAction(args))
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
                guard args.count == 2 || (args.count == 3 && ["30", "45"].contains(args[2])) else { throw ProbeError.unavailable("capture_root_required") }
                let captureSeconds = args.count == 3 ? Double(args[2])! : 45
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
                let deadline = Date().addingTimeInterval(captureSeconds)
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
