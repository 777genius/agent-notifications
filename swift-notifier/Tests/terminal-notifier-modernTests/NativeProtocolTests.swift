import XCTest
@testable import terminal_notifier_modern

final class NativeProtocolTests: XCTestCase {
    let correlation = "00000000-0000-4000-8000-000000000001"
    let nonce = "00000000-0000-4000-8000-000000000002"
    func payload(_ changes: [String: Any] = [:]) throws -> Data {
        var object: [String: Any] = ["schemaVersion": 1, "correlationID": correlation,
            "nonce": nonce, "bootID": "boot", "notAfter": 110.0, "title": "title",
            "body": "body", "category": "info", "action": "none", "silent": true]
        object.merge(changes) { _, new in new }
        return try JSONSerialization.data(withJSONObject: object, options: .sortedKeys)
    }
    func testLiteralAndByteBoundaries() throws {
        for literal in ["--help", "-help", "-title", "-launchedViaLaunchServices", "-execute", "[important]", "'\"$(echo data)"] {
            let request = try NativeCodec.decodeRequest(payload(["title": literal, "body": literal, "subtitle": literal]))
            XCTAssertEqual(request.title, literal)
            XCTAssertEqual(request.body, literal)
            XCTAssertEqual(request.subtitle, literal)
        }
        XCTAssertNoThrow(try NativeCodec.decodeRequest(payload(["title": String(repeating: "😀", count: 64), "body": String(repeating: "é", count: 2048)])))
        XCTAssertThrowsError(try NativeCodec.decodeRequest(payload(["title": String(repeating: "😀", count: 65)])))
        XCTAssertThrowsError(try NativeCodec.decodeRequest(payload(["body": String(repeating: "é", count: 2049)])))
        XCTAssertNoThrow(try NativeCodec.decodeRequest(payload(["body": "line\nnext\tcolumn"])))
        for value in ["line\nnext", "nul\0", "escape\u{1b}", "line\u{2028}next"] {
            XCTAssertThrowsError(try NativeCodec.decodeRequest(payload(["title": value])))
        }
    }
    func testJoinedUnicodeIsPreservedWithByteLimits() throws {
        for value in ["👩‍💻", "👨‍👩‍👧‍👦", "می\u{200c}روم", "\u{1f3f4}\u{e0067}\u{e0062}\u{e007f}"] {
            let request = try NativeCodec.decodeRequest(payload(["title": value, "body": value, "subtitle": value]))
            XCTAssertEqual(Array(request.title.utf8), Array(value.utf8))
            XCTAssertEqual(Array(request.body.utf8), Array(value.utf8))
            XCTAssertEqual(Array(try XCTUnwrap(request.subtitle).utf8), Array(value.utf8))
            for (field, limit) in [("title", 256), ("subtitle", 256), ("body", 4096)] {
                let boundary = String(repeating: value, count: limit / value.utf8.count)
                    + String(repeating: "x", count: limit % value.utf8.count)
                XCTAssertNoThrow(try NativeCodec.decodeRequest(payload([field: boundary])))
                XCTAssertThrowsError(try NativeCodec.decodeRequest(payload([field: boundary + "x"])))
            }
        }
        for field in ["title", "body", "subtitle"] {
            for control in ["\0", "\u{1b}", "\u{85}"] {
                XCTAssertThrowsError(try NativeCodec.decodeRequest(payload([field: control])))
            }
        }
        for field in ["title", "subtitle"] {
            for linebreak in ["\n", "\r", "\u{2028}", "\u{2029}"] {
                XCTAssertThrowsError(try NativeCodec.decodeRequest(payload([field: linebreak])))
            }
        }
    }

    func testStrictJSONAndVersions() throws {
        let valid = try payload()
        XCTAssertThrowsError(try NativeCodec.decodeRequest(Data([0xff])))
        XCTAssertThrowsError(try NativeCodec.decodeRequest(Data(repeating: 32, count: 16385)))
        for changes in [["schemaVersion": 999], ["schemaVersion": true], ["unknown": 1], ["action": "desktop_thread_v1"], ["action": "execute"], ["nonce": "bad"]] as [[String: Any]] {
            XCTAssertThrowsError(try NativeCodec.decodeRequest(payload(changes)))
        }
        let json = String(decoding: valid, as: UTF8.self)
        for prefix in [#""title":"duplicate","# , #""ti\u0074le":"duplicate","#] {
            XCTAssertThrowsError(try NativeCodec.decodeRequest(Data(("{" + prefix + json.dropFirst()).utf8)))
        }
        for escape in [#"\uD800"#, #"\uDC00"#, #"\uD800\u0041"#] {
            let malformed = json.replacingOccurrences(of: #""body":"body""#, with: "\"body\":\"" + escape + "\"")
            XCTAssertThrowsError(try NativeCodec.decodeRequest(Data(malformed.utf8)))
        }
    }
    func testReceiptCorrelationAndRoundtrip() throws {
        let receipt = NativeReceipt(correlationID: correlation, nonce: nonce, status: .submitted, reason: .os_accepted)
        let data = try JSONEncoder().encode(receipt)
        XCTAssertEqual(try NativeCodec.decodeReceipt(data, correlationID: correlation, nonce: nonce).status, .submitted)
        XCTAssertThrowsError(try NativeCodec.decodeReceipt(data, correlationID: nonce, nonce: nonce))
        XCTAssertThrowsError(try NativeCodec.decodeReceipt(data, correlationID: correlation, nonce: correlation))
        XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("body"))
    }
    func testCapabilitiesArePureAndConservative() throws {
        let data = try JSONEncoder().encode(NativeCapabilities())
        XCTAssertNoThrow(try NativeCodec.decodeCapabilities(data))
        XCTAssertThrowsError(try NativeCodec.decodeCapabilities(Data("{}".utf8)))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["actionKinds"] as? [String], ["none", "desktop_thread_v1"])
        XCTAssertEqual(object["explicitFeatureEnabledByDefault"] as? Bool, false)
    }
    func testRepositoryWireFixtures() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().appendingPathComponent("Fixtures")
        let request = try NativeCodec.decodeRequest(Data(contentsOf: root.appendingPathComponent("native-v1.request.json")))
        XCTAssertEqual(request.title, "--help")
        _ = try NativeCodec.decodeReceipt(Data(contentsOf: root.appendingPathComponent("native-v1.receipt.json")),
                                           correlationID: request.correlationID, nonce: request.nonce)
        _ = try NativeCodec.decodeCapabilities(Data(contentsOf: root.appendingPathComponent("native-v1.capabilities.json")))
    }

    final class Spy: NativeBackend {
        var probes = 0
        var sends = 0
        var permissionCompletion: ((NativePermission) -> Void)?
        var addCompletion: ((Bool) -> Void)?
        func permission(_ completion: @escaping (NativePermission) -> Void) { probes += 1; permissionCompletion = completion }
        func add(_ request: NativeRequest, completion: @escaping (Bool) -> Void) { sends += 1; addCompletion = completion }
    }
    func testInvalidInputHasNoEffects() throws {
        for data in [Data("bad".utf8), try payload(["schemaVersion": 2]), try payload(["correlationID": nonce]), try payload(["body": "\0"])] {
            let spy = Spy()
            var receipts: [NativeReceipt] = []
            let delivery = StructuredDelivery(backend: spy, bootID: "boot", now: { 100 }, emit: { receipts.append($0) })
            delivery.start(data: data, correlationID: correlation, nonce: nonce)
            XCTAssertEqual(spy.probes, 0)
            XCTAssertEqual(spy.sends, 0)
            XCTAssertEqual(receipts.count, 1)
            XCTAssertEqual(receipts.first?.status, .rejected)
        }
    }
    func testExpiredAndDifferentBootNeverProbe() throws {
        for changes in [["notAfter": 99], ["notAfter": 116], ["bootID": "other"]] as [[String: Any]] {
            let spy = Spy()
            var receipts: [NativeReceipt] = []
            let delivery = StructuredDelivery(backend: spy, bootID: "boot", now: { 100 }, emit: { receipts.append($0) })
            delivery.start(data: try payload(changes), correlationID: correlation, nonce: nonce)
            XCTAssertEqual(spy.probes, 0)
            XCTAssertEqual(receipts.first?.reason, .expired)
        }
    }
    func testPermissionAndDelayedProbe() throws {
        for permission in [NativePermission.denied, .undetermined, .allowed] {
            let spy = Spy()
            var time = 100.0
            var receipts: [NativeReceipt] = []
            let delivery = StructuredDelivery(backend: spy, bootID: "boot", now: { time }, emit: { receipts.append($0) })
            delivery.start(data: try payload(), correlationID: correlation, nonce: nonce)
            if case .allowed = permission { time = 111 }
            spy.permissionCompletion?(permission)
            XCTAssertEqual(spy.sends, 0)
            XCTAssertEqual(receipts.count, 1)
            XCTAssertEqual(receipts.first?.status, .rejected)
        }
    }
    func testPositiveCompletionOnlyAndTimeoutExactlyOnce() throws {
        for timeout in [false, true] {
            let spy = Spy()
            var receipts: [NativeReceipt] = []
            let delivery = StructuredDelivery(backend: spy, bootID: "boot", now: { 100 }, emit: { receipts.append($0) })
            delivery.start(data: try payload(), correlationID: correlation, nonce: nonce)
            spy.permissionCompletion?(.allowed)
            spy.permissionCompletion?(.denied) // duplicate probe must not override an in-flight add
            XCTAssertTrue(receipts.isEmpty)
            XCTAssertEqual(spy.sends, 1)
            if timeout { delivery.timeout() }
            spy.addCompletion?(true)
            spy.addCompletion?(false)
            spy.permissionCompletion?(.allowed)
            delivery.timeout()
            XCTAssertEqual(spy.sends, 1)
            XCTAssertEqual(receipts.count, 1)
            XCTAssertEqual(receipts.first?.status, timeout ? .unknown : .submitted)
        }
    }
    func testLateAckIsUnknownAndKnownFailureIsRejected() throws {
        for late in [false, true] {
            let spy = Spy()
            var time = 100.0
            var receipts: [NativeReceipt] = []
            let delivery = StructuredDelivery(backend: spy, bootID: "boot", now: { time }, emit: { receipts.append($0) })
            delivery.start(data: try payload(), correlationID: correlation, nonce: nonce)
            spy.permissionCompletion?(.allowed)
            if late { time = 111 }
            spy.addCompletion?(late)
            XCTAssertEqual(receipts.first?.status, late ? .unknown : .rejected)
        }
    }
}
