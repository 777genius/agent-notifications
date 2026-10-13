// TEST-only same-PID exec observation; never discovers or launches a user app.
import AppKit
import Carbon
import Foundation
import Security

private let route = "notification-navigation-test://stale-before-exec"
private struct Ready: Codable { let pid: Int32; let serial: [UInt32]; let bundleID: String }
private func fail(_ label: String) -> NSError { NSError(domain: label, code: 1) }
private func write(_ object: Any, _ path: URL) throws {
    try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]).write(to: path, options: .atomic)
}
private func checkpoint(_ phase: String, _ values: [String: Any] = [:]) {
    var output = values; output["phase"] = phase; output["continuousMS"] = ContinuousClock.now() * 1000
    if let bytes = try? JSONSerialization.data(withJSONObject: output, options: [.sortedKeys]) {
        FileHandle.standardError.write(bytes); FileHandle.standardError.write(Data([10]))
    }
}
private func serial(_ pid: pid_t) -> (Int32, [UInt32]) {
    var words: [UInt32] = [0, 0]
    let status = words.withUnsafeMutableBufferPointer { nav_get_process_serial(pid, $0.baseAddress!) }
    return (status, words)
}
private func wait(_ seconds: Double = 5, _ condition: () -> Bool) throws {
    let end = ContinuousClock.now() + seconds
    while !condition() {
        guard ContinuousClock.now() < end else { throw fail("fixture_timeout") }
        Thread.sleep(forTimeInterval: 0.02)
    }
}
private func requirement(_ identifier: String) throws -> SecRequirement {
    guard identifier.hasPrefix("com.777genius.navigation-psn-test.exec-"), identifier.count < 160,
          identifier.utf8.allSatisfy({ (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) || $0 == 45 || $0 == 46 }) else { throw fail("fixture_identifier") }
    var value: SecRequirement?
    guard SecRequirementCreateWithString("identifier \"\(identifier)\"" as CFString, [], &value) == 0,
          let value = value else { throw fail("requirement_failed") }
    return value
}
private func guest(_ pid: pid_t) -> (Int32, SecCode?) {
    var value: SecCode?
    let status = SecCodeCopyGuestWithAttributes(nil, [kSecGuestAttributePid as String: pid] as CFDictionary, [], &value)
    return (status, value)
}
private final class Receiver: NSObject, NSApplicationDelegate {
    let root: URL, phase: String
    init(_ root: URL, _ phase: String) { self.root = root; self.phase = phase }
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSAppleEventManager.shared().setEventHandler(self, andSelector: #selector(receive(_:reply:)),
            forEventClass: AEEventClass(kInternetEventClass), andEventID: AEEventID(kAEGetURL))
        do {
            let (status, psn) = serial(getpid()); guard status == 0 else { throw fail("receiver_psn") }
            try write([], root.appendingPathComponent(phase + "-events.json"))
            try JSONEncoder().encode(Ready(pid: getpid(), serial: psn, bundleID: Bundle.main.bundleIdentifier ?? ""))
                .write(to: root.appendingPathComponent(phase + "-ready.json"), options: .atomic)
        } catch { checkpoint("receiver_setup_failed"); exit(2) }
    }
    @objc func receive(_ event: NSAppleEventDescriptor, reply: NSAppleEventDescriptor) {
        guard event.paramDescriptor(forKeyword: AEKeyword(keyDirectObject))?.stringValue == route else { exit(3) }
        do {
            let path = root.appendingPathComponent(phase + "-events.json")
            var events = try JSONDecoder().decode([String].self, from: Data(contentsOf: path))
            events.append(route); try write(events, path)
        } catch { exit(4) }
    }
    func replaceImage() {
        let path: String = root.appendingPathComponent("B.app/Contents/MacOS/Receiver").path
        let values: [String] = [path, "--receiver-b", root.path]
        let args = values.map { strdup($0) }
        defer { args.forEach { free($0) } }
        var pointers = args + [nil]
        checkpoint("owned_execv_before", ["pid": getpid()])
        pointers.withUnsafeMutableBufferPointer { _ = execv(path, $0.baseAddress!) }
        checkpoint("owned_execv_failed", ["errno": errno]); exit(5)
    }
}
@main
private struct ProcessSerialExecProbe {
    static func main() throws {
        guard CommandLine.arguments.count == 3 else { exit(1) }
        let mode = CommandLine.arguments[1], root = URL(fileURLWithPath: CommandLine.arguments[2]).standardizedFileURL
        let attributes = try FileManager.default.attributesOfItem(atPath: root.path)
        guard root.lastPathComponent.hasPrefix("navigation-exec-test-"), root.path == root.resolvingSymlinksInPath().path,
              attributes[.posixPermissions] as? Int == 0o700, attributes[.ownerAccountID] as? UInt32 == geteuid(),
              try Data(contentsOf: root.appendingPathComponent("fixture.marker")) == Data("same PID exec TEST only\n".utf8) else { exit(1) }
        if mode == "--receiver-a" || mode == "--receiver-b" {
            let app = NSApplication.shared; app.setActivationPolicy(.prohibited)
            let phase = mode == "--receiver-a" ? "a" : "b", receiver = Receiver(root, phase)
            let expiration = try JSONDecoder().decode(Double.self, from: Data(contentsOf: root.appendingPathComponent("expires.json")))
            app.delegate = receiver
            let timer = Timer(timeInterval: 0.02, repeats: true) { _ in
                if ContinuousClock.now() >= expiration || FileManager.default.fileExists(atPath: root.appendingPathComponent("stop").path) { app.terminate(nil) }
                else if phase == "a" && FileManager.default.fileExists(atPath: root.appendingPathComponent("exec-b").path) { receiver.replaceImage() }
            }
            RunLoop.main.add(timer, forMode: .common)
            withExtendedLifetime((receiver, timer)) { app.run() }; return
        }
        guard mode == "--controller" else { exit(1) }
        var report: [String: Any] = ["productionClientActivated": false, "atomicBindingQualified": false,
            "passedScope": "same_pid_exec_fixture_and_observation", "passed": false, "sendBridgeInvocations": 0]
        let started = ContinuousClock.now(), child = Process()
        var childStarted = false
        do {
            let apps = ["A", "B"].map { root.appendingPathComponent($0 + ".app") }
            let identifiers = apps.map { Bundle(url: $0)?.bundleIdentifier ?? "" }
            guard identifiers[0] != identifiers[1] else { throw fail("identity_not_distinct") }
            let reqA = try requirement(identifiers[0]), reqB = try requirement(identifiers[1])
            for (index, app) in apps.enumerated() {
                guard app.path == app.resolvingSymlinksInPath().path,
                      app.appendingPathComponent("Contents/MacOS/Receiver").path == app.appendingPathComponent("Contents/MacOS/Receiver").resolvingSymlinksInPath().path else { throw fail("fixture_symlink") }
                checkpoint("static_fixture_before", ["index": index])
                var disk: SecStaticCode?
                let create = SecStaticCodeCreateWithPath(app as CFURL, [], &disk)
                let status = disk.map { SecStaticCodeCheckValidity($0, SecCSFlags(rawValue: kSecCSStrictValidate | kSecCSCheckAllArchitectures), index == 0 ? reqA : reqB) }
                report[index == 0 ? "staticA" : "staticB"] = ["create": create, "validity": status.map { $0 as Any } ?? NSNull()]
                checkpoint("static_fixture_checked", ["index": index, "create": create, "validity": status ?? Int32.min])
                guard create == 0 && status == 0 else { throw fail("full_static_rejected") }
            }
            try JSONEncoder().encode(ContinuousClock.now() + 25).write(to: root.appendingPathComponent("expires.json"))
            child.executableURL = apps[0].appendingPathComponent("Contents/MacOS/Receiver")
            child.arguments = ["--receiver-a", root.path]
            let output = try FileHandle(forWritingTo: root.appendingPathComponent("child.stdout"))
            let errors = try FileHandle(forWritingTo: root.appendingPathComponent("child.stderr"))
            defer { try? output.close(); try? errors.close() }
            child.standardOutput = output; child.standardError = errors
            checkpoint("own_child_start_before"); try child.run(); childStarted = true
            let aFile = root.appendingPathComponent("a-ready.json"), bFile = root.appendingPathComponent("b-ready.json")
            try wait { FileManager.default.fileExists(atPath: aFile.path) || !child.isRunning }
            let a = try JSONDecoder().decode(Ready.self, from: Data(contentsOf: aFile))
            guard child.isRunning && a.pid == child.processIdentifier && a.bundleID == identifiers[0] else { throw fail("a_not_owned_ready") }
            checkpoint("a_psn_and_guest_before", ["pid": a.pid])
            let (pinStatus, pinned) = serial(a.pid), (lookupA, oldGuest) = guest(a.pid)
            let trustedA = oldGuest.map { SecCodeCheckValidity($0, [], reqA) }
            report["beforeExec"] = ["pid": a.pid, "PSN": pinned, "PSNStatus": pinStatus, "guestLookup": lookupA, "dynamicA": trustedA.map { $0 as Any } ?? NSNull()]
            checkpoint("a_identity_checked", ["pid": a.pid, "PSN": pinned, "PSNStatus": pinStatus, "guestLookup": lookupA, "dynamicA": trustedA ?? Int32.min])
            guard pinStatus == 0 && pinned == a.serial && lookupA == 0 && trustedA == 0, let oldGuest = oldGuest else { throw fail("a_identity_rejected") }
            try Data().write(to: root.appendingPathComponent("exec-b"))
            try wait { FileManager.default.fileExists(atPath: bFile.path) || !child.isRunning }
            let b = try JSONDecoder().decode(Ready.self, from: Data(contentsOf: bFile))
            checkpoint("b_psn_and_guest_before", ["pid": b.pid])
            let (psnStatus, afterPSN) = serial(a.pid), (lookupB, freshGuest) = guest(a.pid)
            let freshA = freshGuest.map { SecCodeCheckValidity($0, [], reqA) }, freshB = freshGuest.map { SecCodeCheckValidity($0, [], reqB) }
            let oldA = SecCodeCheckValidity(oldGuest, [], reqA), oldB = SecCodeCheckValidity(oldGuest, [], reqB)
            report["afterExec"] = ["pid": b.pid, "PSN": afterPSN, "PSNStatus": psnStatus, "guestLookup": lookupB,
                "freshDynamicA": freshA.map { $0 as Any } ?? NSNull(), "freshDynamicB": freshB.map { $0 as Any } ?? NSNull(), "oldGuestDynamicA": oldA, "oldGuestDynamicB": oldB,
                "samePID": b.pid == a.pid, "PSNUnchanged": afterPSN == pinned]
            checkpoint("b_identity_checked", ["pid": b.pid, "PSN": afterPSN, "PSNStatus": psnStatus, "guestLookup": lookupB, "freshA": freshA ?? Int32.min, "freshB": freshB ?? Int32.min])
            guard child.isRunning && b.pid == a.pid && b.bundleID == identifiers[1] && psnStatus == 0 && b.serial == afterPSN &&
                  lookupB == 0 && freshB == 0 && freshA == errSecCSReqFailed else { throw fail("same_pid_new_identity_not_proven") }
            report["aliveBeforeSend"] = child.isRunning
            checkpoint("before_sole_stale_send", ["pid": a.pid, "pinnedPSN": pinned])
            var permission: Int32 = 0, reply: Int32 = 0
            report["sendBridgeInvocations"] = 1
            let send = pinned.withUnsafeBufferPointer { words in route.withCString { nav_send_process_serial(words.baseAddress!, $0, &permission, &reply) } }
            report["send"] = ["status": send, "permissionPreflight": permission, "replyErrorBestEffort": reply]
            checkpoint("stale_send_returned", ["status": send, "permissionPreflight": permission, "replyErrorBestEffort": reply])
            Thread.sleep(forTimeInterval: 0.25)
            let aEvents = try JSONDecoder().decode([String].self, from: Data(contentsOf: root.appendingPathComponent("a-events.json")))
            let bEvents = try JSONDecoder().decode([String].self, from: Data(contentsOf: root.appendingPathComponent("b-events.json")))
            guard aEvents.isEmpty && (bEvents.isEmpty || bEvents == [route]) && child.isRunning else { throw fail("observer_invalid") }
            report["aliveAfterSend"] = true; report["receivedByB"] = bEvents == [route]
            report["outcome"] = bEvents == [route] ? "stale_psn_delivered_to_new_identity" : (send == -600 ? "old_psn_rejected_process_not_found" : "no_delivery_observed_inconclusive")
            report["observationCompleted"] = true; report["passed"] = true
        } catch { report["failure"] = (error as NSError).domain }
        try? Data().write(to: root.appendingPathComponent("stop"))
        do { if child.isRunning { try wait(5) { !child.isRunning } }; if childStarted { child.waitUntilExit() }; report["childStopped"] = !child.isRunning; report["ownChildReaped"] = childStarted }
        catch { report["cleanupFailed"] = true; report["passed"] = false }
        report["totalMS"] = (ContinuousClock.now() - started) * 1000
        checkpoint("final", ["childStopped": report["childStopped"] ?? false])
        FileHandle.standardOutput.write(try JSONSerialization.data(withJSONObject: report, options: [.sortedKeys]))
        FileHandle.standardOutput.write(Data([10])); exit(report["passed"] as? Bool == true ? 0 : 1)
    }
}
