import Foundation

// Trusted local adapter data, never model tool arguments. Identity is pinned by
// setup, and reverified at click time; location is not an authority by itself.
struct DesktopThreadAction: Codable, Equatable {
    let type: String
    let schemaVersion: Int
    let threadID: String
    let routeKind: String
    let bundleID: String
    let teamID: String
    let applicationPath: String
    let correlationID: String

    func validate() throws {
        guard type == "desktop_thread_v1", schemaVersion == 1,
              routeKind == "codex_thread", bundleID == "com.openai.codex",
              !threadID.isEmpty, threadID != ".", threadID != "..",
              teamID.utf8.count == 10,
              teamID.utf8.allSatisfy({ (65...90).contains($0) || (48...57).contains($0) }),
              UUID(uuidString: correlationID) != nil,
              applicationPath.hasPrefix("/"), applicationPath.hasSuffix(".app"),
              URL(fileURLWithPath: applicationPath).standardizedFileURL.path == applicationPath
        else { throw NativeReason.malformed_request }
        try NativeCodec.text(threadID, limit: 256)
        try NativeCodec.text(applicationPath, limit: 4096)
    }

    var url: URL {
        // Encode UTF-8 bytes, allowing only unreserved characters in one segment.
        let segment = threadID.utf8.map { byte -> String in
            if (65...90).contains(byte) || (97...122).contains(byte) ||
                (48...57).contains(byte) || [45, 46, 95, 126].contains(byte) {
                return String(UnicodeScalar(byte))
            }
            return String(format: "%%%02X", byte)
        }.joined()
        return URL(string: "codex://threads/" + segment)!
    }

    static func decode(_ data: Data) throws -> DesktopThreadAction {
        let object = try StrictJSON.object(data)
        guard Set(object.keys) == Set(["type", "schemaVersion", "threadID", "routeKind",
            "bundleID", "teamID", "applicationPath", "correlationID"]) else {
            throw NativeReason.malformed_request
        }
        let action = try JSONDecoder().decode(Self.self, from: data)
        try action.validate()
        return action
    }
}

enum NativeAction: Codable, Equatable {
    case none
    case desktopThread(DesktopThreadAction)

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let string = try? container.decode(String.self), string == "none" { self = .none; return }
        let action = try container.decode(DesktopThreadAction.self)
        try action.validate()
        self = .desktopThread(action)
    }
    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .none: try container.encode("none")
        case .desktopThread(let action): try action.validate(); try container.encode(action)
        }
    }
}
