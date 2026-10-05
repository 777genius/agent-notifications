// Ad-hoc TEST fixtures only. No Codex, Developer ID or fast-path qualification.
import Foundation
import Security
import Darwin

private struct ReadyChild: Decodable { let pid: Int32; let serial: [UInt32] }
private func failure(_ name: String) -> NSError { NSError(domain: name, code: 1) }
private func waitFor(timeout: TimeInterval = 5, _ condition: () -> Bool) throws {
    let until = ProcessInfo.processInfo.systemUptime + timeout
    while !condition() {
        guard ProcessInfo.processInfo.systemUptime < until else { throw failure("child_timeout") }
        Thread.sleep(forTimeInterval: 0.02)
    }
}
private func verdict(_ app: URL, child: Process, requirement: SecRequirement) -> [String: Int32] {
    let fullFlags = SecCSFlags(rawValue: kSecCSStrictValidate | kSecCSCheckAllArchitectures)
    var result: [String: Int32] = [:], disk: SecStaticCode?, guest: SecCode?, origin: SecStaticCode?
    result["fullCreate"] = SecStaticCodeCreateWithPath(app as CFURL, [], &disk)
    if let disk = disk { result["full"] = SecStaticCodeCheckValidity(disk, fullFlags, requirement) }
    let attributes = [kSecGuestAttributePid as String: NSNumber(value: child.processIdentifier)]
    result["guestLookup"] = SecCodeCopyGuestWithAttributes(nil, attributes as CFDictionary, [], &guest)
    if let guest = guest {
        result["dynamic"] = SecCodeCheckValidity(guest, [], requirement)
        result["copyStatic"] = SecCodeCopyStaticCode(guest, SecCSFlags(rawValue: kSecCSUseAllArchitectures), &origin)
        if let origin = origin {
            result["resourcesOnly"] = SecStaticCodeCheckValidity(origin,
                SecCSFlags(rawValue: fullFlags.rawValue | kSecCSDoNotValidateExecutable), requirement)
        }
    }
    return result
}
private func accepted(_ statuses: [String: Int32]) -> Bool {
    ["fullCreate", "full", "guestLookup", "dynamic", "copyStatic", "resourcesOnly"].allSatisfy { statuses[$0] == 0 }
}
private func textTail(_ data: Data) throws -> Int {
    func integer(_ offset: Int, _ count: Int) -> UInt64 {
        (0..<count).reduce(UInt64(0)) { $0 | UInt64(data[offset + $1]) << ($1 * 8) }
    }
    guard data.count >= 32, integer(0, 4) == 0xfeedfacf else { throw failure("unsupported_macho") }
    var offset = 32
    for _ in 0..<Int(integer(16, 4)) {
        guard offset + 8 <= data.count else { throw failure("invalid_macho_command") }
        let size = Int(integer(offset + 4, 4))
        guard size >= 8, offset + size <= data.count else { throw failure("invalid_macho_size") }
        if integer(offset, 4) == 0x19, size >= 72,
           String(decoding: data[(offset + 8)..<(offset + 24)].prefix(while: { $0 != 0 }), as: UTF8.self) == "__TEXT" {
            let end = integer(offset + 40, 8) + integer(offset + 48, 8)
            guard end > 32, end <= UInt64(data.count) else { throw failure("invalid_text_range") }
            return Int(end - 1)
        }
        offset += size
    }
    throw failure("text_segment_missing")
}
private func experiment(_ root: URL, _ phase: String) -> [String: Any] {
    var output: [String: Any] = ["control": phase, "mandatory": phase != "executable", "fastPathQualified": false]
    if phase != "executable" { output["passed"] = false }
    else { output["observationCompleted"] = false }
    let app = root.appendingPathComponent(phase + ".app")
    let executable = app.appendingPathComponent("Contents/MacOS/ProcessSerialRoutingProbe")
    let resource = app.appendingPathComponent("Contents/Resources/validation-resource.txt")
    let envelope = app.appendingPathComponent("Contents/_CodeSignature/CodeResources")
    let info = app.appendingPathComponent("Contents/Info.plist")
    let directory = root.appendingPathComponent("envelope-child-" + phase)
    var child: Process?, directoryCreated = false
    do {
        for path in [app, executable, resource, envelope, info] {
            guard path.path == path.resolvingSymlinksInPath().path else { throw failure("fixture_symlink") }
        }
        let metadata = try PropertyListSerialization.propertyList(from: Data(contentsOf: info), options: [], format: nil) as? [String: Any]
        guard let identifier = metadata?["CFBundleIdentifier"] as? String,
              identifier.hasPrefix("com.777genius.navigation-psn-test."), identifier.utf8.count < 200,
              identifier.utf8.allSatisfy({ (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) || $0 == 45 || $0 == 46 }) else { throw failure("fixture_identifier") }
        var requirement: SecRequirement?
        guard SecRequirementCreateWithString("identifier \"\(identifier)\"" as CFString, [], &requirement) == 0,
              let requirement = requirement else { throw failure("fixture_requirement") }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
        directoryCreated = true
        let receiver = Process(); receiver.executableURL = executable; receiver.arguments = ["--receiver", directory.path]
        child = receiver; try receiver.run()
        let ready = directory.appendingPathComponent("ready.json")
        try waitFor { FileManager.default.fileExists(atPath: ready.path) || !receiver.isRunning }
        guard receiver.isRunning, try JSONDecoder().decode(ReadyChild.self, from: Data(contentsOf: ready)).pid == receiver.processIdentifier else { throw failure("own_child_not_ready") }
        let before = verdict(app, child: receiver, requirement: requirement)
        output["before"] = before; output["aliveBeforeMutation"] = receiver.isRunning
        guard accepted(before), receiver.isRunning else { throw failure("baseline_rejected") }
        if phase == "resource" || phase == "envelope" {
            let target = phase == "resource" ? resource : envelope
            var bytes = try Data(contentsOf: target); bytes.append(Data("\n".utf8))
            try bytes.write(to: target, options: .atomic)
        } else if phase == "executable" {
            let bytes = try Data(contentsOf: executable), offset = try textTail(bytes)
            let file = try FileHandle(forWritingTo: executable); defer { try? file.close() }
            try file.seek(toOffset: UInt64(offset)); try file.write(contentsOf: Data([bytes[offset] ^ 1]))
            output["mutatedExecutableOffset"] = offset
        }
        output["aliveBeforeAfterChecks"] = receiver.isRunning
        guard receiver.isRunning else { throw failure("child_exited_after_mutation") }
        let after = verdict(app, child: receiver, requirement: requirement)
        output["after"] = after; output["aliveAfterChecks"] = receiver.isRunning
        let compositeAccepted = ["guestLookup", "dynamic", "copyStatic", "resourcesOnly"].allSatisfy { after[$0] == 0 }
        output["compositeAccepted"] = compositeAccepted
        let falsifier = phase == "executable" && receiver.isRunning && after["fullCreate"] == 0 &&
            after["full"] == errSecCSSignatureFailed && compositeAccepted
        output["nonEquivalenceObserved"] = falsifier
        if phase == "executable" {
            output["observationCompleted"] = true
            output["observationOutcome"] = falsifier ? "full_rejected_composite_accepted" : "no_falsifier_observed"
        } else {
            let expected: OSStatus = phase == "resource" ? errSecCSBadResource : errSecCSResourceDirectoryFailed
            if phase != "untouched" { output["expectedIntegrityError"] = expected }
            output["passed"] = receiver.isRunning && (phase == "untouched" ? accepted(after) :
                after["fullCreate"] == 0 && after["full"] == expected && after["guestLookup"] == 0 &&
                after["dynamic"] == 0 && after["copyStatic"] == 0 && after["resourcesOnly"] == expected)
        }
    } catch { output["errorDomain"] = (error as NSError).domain; output["errorCode"] = (error as NSError).code }
    if directoryCreated {
        var cleanupFailed = false
        do {
            try Data().write(to: directory.appendingPathComponent("stop"))
            if let child = child, child.isRunning { try waitFor { !child.isRunning } }
        } catch { cleanupFailed = true }
        // Signal only the Process launched above. A failed graceful stop still
        // requires bounded escalation before this finite TEST probe returns.
        if let child = child, child.isRunning {
            output["childTerminationRequested"] = true
            child.terminate()
            try? waitFor(timeout: 1) { !child.isRunning }
            if child.isRunning {
                output["childKillRequested"] = Darwin.kill(child.processIdentifier, SIGKILL) == 0
                try? waitFor(timeout: 1) { !child.isRunning }
            }
        }
        let childStopped = child?.isRunning != true
        output["childStopped"] = childStopped
        if cleanupFailed || !childStopped { output["cleanupFailed"] = true; output["passed"] = false }
    }
    return output
}
var report: [String: Any] = ["productionClientActivated": false, "fastPathQualified": false,
    "passedScope": "resource_controls_and_cleanup", "passed": false]
do {
    guard CommandLine.arguments.count == 2 else { throw failure("fixture_root_required") }
    let root = URL(fileURLWithPath: CommandLine.arguments[1]).standardizedFileURL
    guard root.lastPathComponent.hasPrefix("navigation-psn-test-"), root.path == root.resolvingSymlinksInPath().path,
          try Data(contentsOf: root.appendingPathComponent("fixture.marker")) == Data("synthetic PSN routing only\n".utf8) else { throw failure("fixture_root_rejected") }
    let results = ["untouched", "resource", "envelope", "executable"].map { experiment(root, $0) }
    report["controls"] = results
    report["passed"] = results.prefix(3).allSatisfy { $0["passed"] as? Bool == true } && results.allSatisfy { $0["cleanupFailed"] == nil }
} catch { report["errorDomain"] = (error as NSError).domain }
FileHandle.standardOutput.write(try JSONSerialization.data(withJSONObject: report, options: [.sortedKeys]))
FileHandle.standardOutput.write(Data([10]))
exit(report["passed"] as? Bool == true ? 0 : 1)
