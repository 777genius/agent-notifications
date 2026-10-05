// Disposable ad-hoc fixture evidence only. This does not qualify Developer ID,
// Codex, executable resources, GURL handling or a production navigation route.
import Foundation
import Security

private struct ResourceReceiverReady: Decodable {
    let pid: Int32
    let serial: [UInt32]
}

private func probeFailure(_ name: String) -> NSError {
    NSError(domain: name, code: 1)
}

private func waitForResourceReceiver(_ condition: () -> Bool) throws {
    let deadline = ProcessInfo.processInfo.systemUptime + 5
    while !condition() {
        guard ProcessInfo.processInfo.systemUptime < deadline else {
            throw probeFailure("resource_receiver_timeout")
        }
        Thread.sleep(forTimeInterval: 0.02)
    }
}

// Always create a fresh static object, so the second verdict cannot reuse a
// static object's cached successful resource validation from before mutation.
private func staticVerdict(_ app: URL, requirement: SecRequirement, phase: String,
                           report: inout [String: Any]) -> OSStatus? {
    var code: SecStaticCode?
    let created = SecStaticCodeCreateWithPath(app as CFURL, [], &code)
    report[phase + "StaticCreateStatus"] = created
    guard created == errSecSuccess, let code = code else { return nil }
    let flags = SecCSFlags(rawValue: kSecCSStrictValidate | kSecCSCheckAllArchitectures)
    let status = SecStaticCodeCheckValidity(code, flags, requirement)
    report[phase + "StaticValidationStatus"] = status
    if phase == "before", status == errSecSuccess {
        var information: CFDictionary?
        let copied = SecCodeCopySigningInformation(code, [], &information)
        report["fixtureSigningInfoStatus"] = copied
        let dictionary = information as? [String: Any]
        let signatureFlags = dictionary?[kSecCodeInfoFlags as String] as? NSNumber
        // Public SDK CSCommon.h: kSecCodeSignatureAdhoc is the 0x0002 bit.
        let adHoc = signatureFlags.map { $0.uint32Value & 0x0002 != 0 } ?? false
        report["fixtureAdHoc"] = adHoc
        guard copied == errSecSuccess, adHoc else { return nil }
    }
    return status
}

var report: [String: Any] = [
    "probe": "ad_hoc_fixture_dynamic_vs_static_resource_validation",
    "productionClientActivated": false,
    "developerIDQualified": false,
    "codexQualified": false,
    "eventsSent": 0,
    "passed": false
]
var child: Process?
var childDirectory: URL?
var contractPassed = false

do {
    let arguments = Array(CommandLine.arguments.dropFirst())
    guard arguments.count == 1 else { throw probeFailure("fixture_root_required") }
    let root = URL(fileURLWithPath: arguments[0]).standardizedFileURL
    guard root.path == root.resolvingSymlinksInPath().standardizedFileURL.path,
          root.lastPathComponent.hasPrefix("navigation-psn-test-") else {
        throw probeFailure("fixture_root_rejected")
    }
    let marker = root.appendingPathComponent("fixture.marker")
    guard marker.path == marker.resolvingSymlinksInPath().standardizedFileURL.path,
          try Data(contentsOf: marker) == Data("synthetic PSN routing only\n".utf8) else {
        throw probeFailure("fixture_marker_rejected")
    }
    let app = root.appendingPathComponent("ResourceReceiver.app")
    let executable = app.appendingPathComponent("Contents/MacOS/ProcessSerialRoutingProbe")
    let resource = app.appendingPathComponent("Contents/Resources/validation-resource.txt")
    let info = app.appendingPathComponent("Contents/Info.plist")
    for path in [app, executable, resource, info] {
        guard path.path == path.resolvingSymlinksInPath().standardizedFileURL.path else {
            throw probeFailure("fixture_symlink_rejected")
        }
    }
    let metadata = try PropertyListSerialization.propertyList(
        from: Data(contentsOf: info), options: [], format: nil) as? [String: Any]
    guard let bundleID = metadata?["CFBundleIdentifier"] as? String,
          bundleID.hasPrefix("com.777genius.navigation-psn-test."),
          bundleID.utf8.count <= 200,
          bundleID.utf8.allSatisfy({
              (48...57).contains($0) || (65...90).contains($0) ||
                  (97...122).contains($0) || $0 == 45 || $0 == 46
          }),
          metadata?["CFBundleExecutable"] as? String == "ProcessSerialRoutingProbe" else {
        throw probeFailure("fixture_identity_rejected")
    }
    var requirement: SecRequirement?
    let requirementStatus = SecRequirementCreateWithString(
        "identifier \"\(bundleID)\"" as CFString, [], &requirement)
    report["fixtureRequirementStatus"] = requirementStatus
    guard requirementStatus == errSecSuccess, let requirement = requirement else {
        throw probeFailure("fixture_requirement_failed")
    }
    report["fixtureIdentifierRequirement"] = bundleID
    let originalResource = try Data(contentsOf: resource)
    guard staticVerdict(app, requirement: requirement, phase: "before", report: &report) == errSecSuccess else {
        throw probeFailure("baseline_static_validation_failed")
    }

    let directory = root.appendingPathComponent("resource-receiver")
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
    childDirectory = directory
    let receiver = Process()
    receiver.executableURL = executable
    receiver.arguments = ["--receiver", directory.path]
    child = receiver
    try receiver.run()
    let readyPath = directory.appendingPathComponent("ready.json")
    try waitForResourceReceiver {
        FileManager.default.fileExists(atPath: readyPath.path) || !receiver.isRunning
    }
    guard receiver.isRunning else { throw probeFailure("fixture_receiver_exited") }
    let ready = try JSONDecoder().decode(ResourceReceiverReady.self, from: Data(contentsOf: readyPath))
    guard ready.pid == receiver.processIdentifier, ready.serial.count == 2 else {
        throw probeFailure("fixture_child_identity_mismatch")
    }
    var guest: SecCode?
    let attributes = [kSecGuestAttributePid as String: NSNumber(value: receiver.processIdentifier)]
    let lookup = SecCodeCopyGuestWithAttributes(nil, attributes as CFDictionary, [], &guest)
    report["guestLookupStatus"] = lookup
    guard lookup == errSecSuccess, let guest = guest else {
        throw probeFailure("fixture_guest_lookup_failed")
    }
    report["aliveBeforeDynamic"] = receiver.isRunning
    let beforeDynamic = SecCodeCheckValidity(guest, [], requirement)
    report["beforeDynamicValidationStatus"] = beforeDynamic
    let aliveBeforeMutation = receiver.isRunning
    report["aliveBeforeMutation"] = aliveBeforeMutation
    guard beforeDynamic == errSecSuccess, aliveBeforeMutation else {
        throw probeFailure("baseline_dynamic_validation_failed")
    }

    var modifiedResource = originalResource
    modifiedResource.append(Data("\nmodified by isolated resource validation probe\n".utf8))
    try modifiedResource.write(to: resource, options: .atomic)
    let resourceChanged = try Data(contentsOf: resource) != originalResource
    report["resourceChanged"] = resourceChanged
    let afterStatic = staticVerdict(app, requirement: requirement, phase: "after", report: &report)
    let aliveAfterStatic = receiver.isRunning
    report["aliveAfterStatic"] = aliveAfterStatic
    guard aliveAfterStatic else { throw probeFailure("fixture_receiver_exited_after_mutation") }
    var freshGuest: SecCode?
    report["aliveBeforeFreshGuestLookup"] = receiver.isRunning
    let freshLookup = SecCodeCopyGuestWithAttributes(nil, attributes as CFDictionary, [], &freshGuest)
    report["freshGuestLookupStatus"] = freshLookup
    let aliveBeforeFreshDynamic = receiver.isRunning
    report["aliveBeforeFreshDynamic"] = aliveBeforeFreshDynamic
    guard freshLookup == errSecSuccess, let freshGuest = freshGuest, aliveBeforeFreshDynamic else {
        throw probeFailure("fresh_fixture_guest_lookup_failed")
    }
    let afterDynamic = SecCodeCheckValidity(freshGuest, [], requirement)
    report["afterDynamicValidationStatus"] = afterDynamic
    report["freshDynamicValidationStatus"] = afterDynamic
    let aliveAfterDynamic = receiver.isRunning
    report["aliveAfterDynamic"] = aliveAfterDynamic
    contractPassed = resourceChanged && aliveAfterStatic && aliveAfterDynamic &&
        afterStatic == errSecCSBadResource && afterDynamic == errSecSuccess
} catch {
    let error = error as NSError
    report["errorDomain"] = error.domain
    report["errorCode"] = error.code
}

// Signal only this probe's child. The reused receiver also expires after 30s;
// no PID lookup, broad cleanup, application termination or resource restoration.
if let directory = childDirectory {
    do {
        try Data().write(to: directory.appendingPathComponent("stop"))
        report["stopRequested"] = true
        if let receiver = child, receiver.isRunning {
            try waitForResourceReceiver { !receiver.isRunning }
        }
        report["childStopped"] = child?.isRunning != true
    } catch {
        report["cleanupFailed"] = true
        contractPassed = false
    }
}
report["passed"] = contractPassed
let output = try JSONSerialization.data(withJSONObject: report, options: [.sortedKeys])
FileHandle.standardOutput.write(output)
FileHandle.standardOutput.write(Data([10]))
exit(contractPassed ? 0 : 1)
