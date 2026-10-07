import Foundation

enum NativeStatus: String, Codable { case rejected, suppressed, submitted, unknown }
enum NativeReason: String, Codable, Error {
    case malformed_request, unsupported_version, unsupported_action, invalid_file
    case expired, activation_required, permission_denied, unsupported_notifier
    case os_rejected, os_accepted, timeout
}

struct NativeRequest: Codable {
    let schemaVersion: Int
    let correlationID: String
    let nonce: String
    let bootID: String
    let notAfter: Double // mach_continuous_time converted to seconds; includes sleep
    let title: String
    let body: String
    let subtitle: String?
    let category: Category
    let action: NativeAction
    let silent: Bool
    enum Category: String, Codable { case info, attention, progress }
}

struct NativeReceipt: Codable {
    let schemaVersion: Int
    let correlationID: String
    let nonce: String
    let notificationID: String
    let status: NativeStatus
    let reason: NativeReason
    let retrySafe: Bool

    init(correlationID: String, nonce: String, status: NativeStatus, reason: NativeReason) {
        schemaVersion = 1
        self.correlationID = correlationID
        self.nonce = nonce
        notificationID = correlationID
        self.status = status
        self.reason = reason
        retrySafe = false // native does not own replay admission
    }
}

struct NativeCapabilities: Codable, Equatable {
    let schemaVersion: Int
    let protocolVersions: [Int]
    let actionKinds: [String]
    let receiptSupport: Bool
    let backend: String
    let explicitFeatureEnabledByDefault: Bool

    init() {
        schemaVersion = 1
        protocolVersions = [1]
        actionKinds = ["none", "desktop_thread_v1"]
        receiptSupport = true
        backend = "macos.usernotifications"
        explicitFeatureEnabledByDefault = false
    }
}

enum NativeCodec {
    static let maxBytes = 16 * 1024

    static func text(_ value: String, limit: Int, multiline: Bool = false) throws {
        guard value.utf8.count <= limit,
              value.unicodeScalars.allSatisfy({ scalar in
                  // Foundation's controlCharacters also includes format (Cf),
                  // such as emoji ZWJ and Persian ZWNJ. Preserve literal format
                  // scalars; reject actual control (Cc) except body LF/TAB.
                  scalar.properties.generalCategory != .control ||
                  (multiline && (scalar.value == 10 || scalar.value == 9))
              }), multiline || !value.unicodeScalars.contains(where: {
                  CharacterSet.newlines.contains($0)
              }) else { throw NativeReason.malformed_request }
    }

    static func decodeRequest(_ data: Data) throws -> NativeRequest {
        let object = try StrictJSON.object(data)
        let keys: Set<String> = ["schemaVersion", "correlationID", "nonce", "bootID",
                                  "notAfter", "title", "body", "subtitle", "category", "action", "silent"]
        guard Set(object.keys).isSubset(of: keys) else { throw NativeReason.malformed_request }
        guard let version = object["schemaVersion"] as? NSNumber,
              CFGetTypeID(version) != CFBooleanGetTypeID(), version.doubleValue == 1 else {
            throw NativeReason.unsupported_version
        }
        if object["action"] as? String != "none" {
            guard let action = object["action"] as? [String: Any] else { throw NativeReason.unsupported_action }
            _ = try DesktopThreadAction.decode(JSONSerialization.data(withJSONObject: action))
        }
        let request: NativeRequest
        do { request = try JSONDecoder().decode(NativeRequest.self, from: data) }
        catch { throw NativeReason.malformed_request }
        guard UUID(uuidString: request.correlationID) != nil,
              UUID(uuidString: request.nonce) != nil,
              !request.bootID.isEmpty, request.bootID.utf8.count <= 256,
              request.notAfter.isFinite, request.notAfter > 0 else { throw NativeReason.malformed_request }
        if case .desktopThread(let action) = request.action {
            guard action.correlationID == request.correlationID else { throw NativeReason.malformed_request }
        }
        try text(request.title, limit: 256)
        try text(request.body, limit: 4096, multiline: true)
        if let subtitle = request.subtitle { try text(subtitle, limit: 256) }
        return request
    }

    static func decodeCapabilities(_ data: Data) throws -> NativeCapabilities {
        let object = try StrictJSON.object(data)
        let expected = try StrictJSON.object(JSONEncoder().encode(NativeCapabilities()))
        guard Set(object.keys) == Set(expected.keys),
              let decoded = try? JSONDecoder().decode(NativeCapabilities.self, from: data),
              decoded == NativeCapabilities() else {
            throw NativeReason.unsupported_version
        }
        return NativeCapabilities()
    }

    static func decodeReceipt(_ data: Data, correlationID: String, nonce: String) throws -> NativeReceipt {
        let object = try StrictJSON.object(data)
        guard Set(object.keys) == Set(["schemaVersion", "correlationID", "nonce", "notificationID",
                                      "status", "reason", "retrySafe"]) else { throw NativeReason.malformed_request }
        let receipt: NativeReceipt
        do { receipt = try JSONDecoder().decode(NativeReceipt.self, from: data) }
        catch { throw NativeReason.malformed_request }
        guard receipt.schemaVersion == 1 else { throw NativeReason.unsupported_version }
        guard UUID(uuidString: correlationID) != nil, UUID(uuidString: nonce) != nil,
              receipt.correlationID == correlationID, receipt.nonce == nonce,
              receipt.notificationID == correlationID, !receipt.retrySafe,
              ((receipt.status == .submitted && receipt.reason == .os_accepted) ||
               (receipt.status == .unknown && receipt.reason == .timeout) ||
               (receipt.status == .rejected && receipt.reason != .os_accepted && receipt.reason != .timeout)) else { throw NativeReason.malformed_request }
        return receipt
    }
}

import CoreFoundation

// Foundation decoders may accept duplicate keys. Walk the bounded JSON syntax
// before decoding, comparing decoded keys (including escaped spellings).
struct StrictJSON {
    var bytes: [UInt8]
    var i = 0
    static func object(_ data: Data) throws -> [String: Any] {
        guard data.count <= NativeCodec.maxBytes, String(data: data, encoding: .utf8) != nil else {
            throw NativeReason.malformed_request
        }
        var scanner = StrictJSON(bytes: Array(data))
        try scanner.value(depth: 0)
        scanner.space()
        guard scanner.i == scanner.bytes.count,
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw NativeReason.malformed_request
        }
        return object
    }
    mutating func space() { while i < bytes.count && [9, 10, 13, 32].contains(bytes[i]) { i += 1 } }
    mutating func take(_ byte: UInt8) throws {
        space()
        guard i < bytes.count, bytes[i] == byte else { throw NativeReason.malformed_request }
        i += 1
    }
    mutating func string() throws -> String {
        space()
        let start = i
        try take(34)
        while i < bytes.count {
            let byte = bytes[i]; i += 1
            if byte == 34 {
                let data = Data(bytes[start..<i])
                guard let result = try? JSONSerialization.jsonObject(with: data, options: .fragmentsAllowed) as? String else {
                    throw NativeReason.malformed_request
                }
                return result
            }
            if byte == 92 {
                guard i < bytes.count else { throw NativeReason.malformed_request }
                if bytes[i] == 117 {
                    i += 1
                    let unit = try hexUnit()
                    if (0xD800...0xDBFF).contains(unit) {
                        guard i + 2 <= bytes.count, bytes[i] == 92, bytes[i + 1] == 117 else {
                            throw NativeReason.malformed_request
                        }
                        i += 2
                        guard (0xDC00...0xDFFF).contains(try hexUnit()) else { throw NativeReason.malformed_request }
                    } else if (0xDC00...0xDFFF).contains(unit) { throw NativeReason.malformed_request }
                } else { i += 1 }
            }
        }
        throw NativeReason.malformed_request
    }
    mutating func hexUnit() throws -> UInt16 {
        guard i + 4 <= bytes.count,
              let value = UInt16(String(decoding: bytes[i..<i+4], as: UTF8.self), radix: 16) else {
            throw NativeReason.malformed_request
        }
        i += 4
        return value
    }
    mutating func value(depth: Int) throws {
        space()
        guard depth < 16, i < bytes.count else { throw NativeReason.malformed_request }
        switch bytes[i] {
        case 123:
            i += 1; space()
            if i < bytes.count && bytes[i] == 125 { i += 1; return }
            var keys = Set<String>()
            while true {
                guard keys.insert(try string()).inserted else { throw NativeReason.malformed_request }
                try take(58); try value(depth: depth + 1); space()
                if i < bytes.count && bytes[i] == 125 { i += 1; return }
                try take(44)
            }
        case 91:
            i += 1; space()
            if i < bytes.count && bytes[i] == 93 { i += 1; return }
            while true {
                try value(depth: depth + 1); space()
                if i < bytes.count && bytes[i] == 93 { i += 1; return }
                try take(44)
            }
        case 34: _ = try string()
        default:
            let start = i
            while i < bytes.count && ![9, 10, 13, 32, 44, 93, 125].contains(bytes[i]) { i += 1 }
            guard i > start else { throw NativeReason.malformed_request }
        }
    }
}
