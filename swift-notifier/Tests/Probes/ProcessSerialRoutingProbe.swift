// Isolated native P2 prototype: only children of this test executable receive
// synthetic GURL events. It never discovers/opens Codex or other user apps.
import AppKit
import Carbon
import Foundation

struct Ready: Codable { let pid: Int32; let serial: [UInt32] }
func save<T: Encodable>(_ value: T, to path: URL) throws {
    try JSONEncoder().encode(value).write(to: path, options: .atomic)
}
func serialForSelf() throws -> [UInt32] {
    var serial: [UInt32] = [0, 0]
    let status = serial.withUnsafeMutableBufferPointer { nav_get_process_serial(getpid(), $0.baseAddress!) }
    guard status == 0 else { throw NSError(domain: "PSN", code: Int(status)) }
    return serial
}
final class Receiver: NSObject, NSApplicationDelegate {
    let directory: URL
    init(_ directory: URL) { self.directory = directory }
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSAppleEventManager.shared().setEventHandler(self, andSelector: #selector(receive(_:reply:)),
            forEventClass: AEEventClass(kInternetEventClass), andEventID: AEEventID(kAEGetURL))
        do {
            try save([String](), to: directory.appendingPathComponent("events.json"))
            try save(Ready(pid: getpid(), serial: serialForSelf()), to: directory.appendingPathComponent("ready.json"))
        }
        catch { exit(2) }
    }
    @objc func receive(_ event: NSAppleEventDescriptor, reply: NSAppleEventDescriptor) {
        guard let url = event.paramDescriptor(forKeyword: AEKeyword(keyDirectObject))?.stringValue,
              url.hasPrefix("notification-navigation-test://") else { exit(3) }
        do {
            let log = directory.appendingPathComponent("events.json")
            var events = try JSONDecoder().decode([String].self, from: Data(contentsOf: log))
            events.append(url)
            try save(events, to: log)
        } catch { exit(4) }
    }
}
func waitFor(_ condition: () throws -> Bool, seconds: Double = 5) throws {
    let deadline = Date().addingTimeInterval(seconds)
    while try !condition() {
        guard Date() < deadline else { throw NSError(domain: "fixture_timeout", code: 1) }
        Thread.sleep(forTimeInterval: 0.02)
    }
}
func startReceiver(app: URL, directory: URL) throws -> (Process, Ready) {
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
    let child = Process()
    child.executableURL = app.appendingPathComponent("Contents/MacOS/ProcessSerialRoutingProbe")
    child.arguments = ["--receiver", directory.path]
    try child.run()
    let ready = directory.appendingPathComponent("ready.json")
    try waitFor { ready.path.withCString { access($0, F_OK) } == 0 || !child.isRunning }
    guard child.isRunning else { throw NSError(domain: "fixture_exit", code: Int(child.terminationStatus)) }
    return (child, try JSONDecoder().decode(Ready.self, from: Data(contentsOf: ready)))
}
func stop(_ child: Process, _ directory: URL) throws {
    try Data().write(to: directory.appendingPathComponent("stop"))
    try waitFor { !child.isRunning }
    child.waitUntilExit()
}
func events(_ directory: URL) throws -> [String] {
    try JSONDecoder().decode([String].self,
        from: Data(contentsOf: directory.appendingPathComponent("events.json")))
}
func send(_ serial: [UInt32], _ url: String) -> [String: Int32] {
    var permission: Int32 = 0, reply: Int32 = 0
    let status = serial.withUnsafeBufferPointer { words in
        url.withCString { nav_send_process_serial(words.baseAddress!, $0, &permission, &reply) }
    }
    return ["sendStatus": status, "permissionStatus": permission, "replyError": reply]
}
let arguments = Array(CommandLine.arguments.dropFirst())
guard arguments.count == 2, ["--receiver", "--controller"].contains(arguments[0]) else { exit(1) }
let directory = URL(fileURLWithPath: arguments[1]).standardizedFileURL
// Operator-created fixture root has its own marker; no real project paths accepted.
let root = arguments[0] == "--controller" ? directory : directory.deletingLastPathComponent()
guard root.lastPathComponent.hasPrefix("navigation-psn-test-"),
      FileManager.default.fileExists(atPath: root.appendingPathComponent("fixture.marker").path) else { exit(1) }
if arguments[0] == "--receiver" {
    let app = NSApplication.shared
    app.setActivationPolicy(.prohibited)
    let receiver = Receiver(directory)
    app.delegate = receiver
    let lifetime = Date().addingTimeInterval(30)
    let timer = Timer(timeInterval: 0.02, repeats: true) { _ in
        if Date() >= lifetime || FileManager.default.fileExists(atPath: directory.appendingPathComponent("stop").path) { app.terminate(nil) }
    }
    RunLoop.main.add(timer, forMode: .common)
    withExtendedLifetime((receiver, timer)) { app.run() }
} else {
    let app = root.appendingPathComponent("Receiver.app")
    let a = root.appendingPathComponent("a"), b = root.appendingPathComponent("b"), restarted = root.appendingPathComponent("a-restarted")
    let (processA, readyA) = try startReceiver(app: app, directory: a)
    let copy = root.appendingPathComponent("ReceiverCopy.app")
    let (processB, readyB) = try startReceiver(app: copy, directory: b)
    defer { try? stop(processA, a); try? stop(processB, b) }
    let aliveBeforeFirst = processA.isRunning && processB.isRunning
    let first = send(readyA.serial, "notification-navigation-test://selected-a")
    if first["sendStatus"] == 0 { try waitFor { try events(a).count == 1 } }
    let activeA = try events(a), activeB = try events(b)
    let aliveAfterFirst = processA.isRunning && processB.isRunning
    try stop(processA, a)
    let (processNew, readyNew) = try startReceiver(app: app, directory: restarted)
    defer { try? stop(processNew, restarted) }
    let aliveBeforeStale = processNew.isRunning && processB.isRunning
    let stale = send(readyA.serial, "notification-navigation-test://stale-a")
    Thread.sleep(forTimeInterval: 0.2) // Bounded observation for unexpected receiver effect.
    let newEvents = try events(restarted), otherEvents = try events(b)
    let aliveAfterStale = processNew.isRunning && processB.isRunning
    let observersAlive = aliveBeforeFirst && aliveAfterFirst && aliveBeforeStale && aliveAfterStale
    let output: [String: Any] = ["observersAlive": observersAlive, "probe": "psn_selected_and_stale_receiver", "first": first,
        "selectedEvents": activeA, "otherEventsBeforeRestart": activeB,
        "stale": stale, "restartedEvents": newEvents, "otherEventsAfterRestart": otherEvents,
        "serialChangedAfterRestart": readyA.serial != readyNew.serial,
        "productionClientActivated": false, "twoDistinctCopies": true]
    let passed = observersAlive && first["sendStatus"] == 0 && first["replyError"] == 0 &&
        activeA == ["notification-navigation-test://selected-a"] && activeB.isEmpty &&
        readyA.serial != readyB.serial && readyA.serial != readyNew.serial &&
        stale["sendStatus"] == -600 && newEvents.isEmpty && otherEvents.isEmpty
    let data = try JSONSerialization.data(withJSONObject: output, options: [.sortedKeys])
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([10]))
    if !passed { throw NSError(domain: "routing_contract_failed", code: 1) }
}
