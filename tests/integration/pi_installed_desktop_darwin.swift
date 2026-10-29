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
