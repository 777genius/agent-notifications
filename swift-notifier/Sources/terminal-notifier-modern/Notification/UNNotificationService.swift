import AppKit
import Foundation
import UserNotifications

enum NotificationServiceError: Error, CustomStringConvertible {
    case encodingFailed
    case sendFailed(Error)

    var description: String {
        switch self {
        case .encodingFailed:
            return "Failed to encode click action to JSON"
        case .sendFailed(let error):
            return "Failed to send notification: \(error.localizedDescription)"
        }
    }
}

final class UNNotificationService: NotificationSending {

    private let center: UNUserNotificationCenter
    private let soundDirectory: URL

    init(center: UNUserNotificationCenter = .current(), soundDirectory: URL = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Sounds")) {
        self.center = center
        self.soundDirectory = soundDirectory
    }

    func send(config: NotificationConfig, completion: @escaping (Result<Void, Error>) -> Void) {
        send(config: config, admitted: { true }, completion: completion)
    }

    func send(config: NotificationConfig, admitted: () -> Bool, completion: @escaping (Result<Void, Error>) -> Void) {
        // Set app icon explicitly so macOS picks it up for notifications
        if let iconURL = Bundle.main.url(forResource: "AppIcon", withExtension: "icns"),
           let icon = NSImage(contentsOf: iconURL) {
            NSApplication.shared.applicationIconImage = icon
        }

        let content: UNMutableNotificationContent
        do { content = try Self.makeContent(config: config, soundDirectory: soundDirectory) }
        catch { completion(.failure(error)); return }

        let identifier = config.group ?? UUID().uuidString
        let request = UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
        guard admitted() else { completion(.failure(NativeReason.expired)); return }
        center.add(request) { error in
            if let error = error { completion(.failure(NotificationServiceError.sendFailed(error))) }
            else { completion(.success(())) }
        }
    }

    static func makeContent(config: NotificationConfig, soundDirectory: URL) throws -> UNMutableNotificationContent {
        let content = UNMutableNotificationContent()
        content.title = config.title
        content.body = config.message
        if config.soundVolume == 0 {
            content.sound = nil
        } else if let path = config.soundFile {
            // Go keeps -nosound for older helpers; explicit custom audio is
            // supplied only when the caller's effective sound policy allows it.
            let prepared = try PreparedNotificationSound.prepare(source: URL(fileURLWithPath: path), volume: config.soundVolume, destinationDirectory: soundDirectory)
            content.sound = UNNotificationSound(named: UNNotificationSoundName(rawValue: prepared.lastPathComponent))
        } else if config.silent {
            content.sound = nil
        } else {
            content.sound = .default
        }
        content.categoryIdentifier = "CLAUDE_NOTIFICATION"

        if let subtitle = config.subtitle {
            content.subtitle = subtitle
        }

        if let threadID = config.threadID {
            content.threadIdentifier = threadID
        }

        if #available(macOS 12.0, *) {
            content.interruptionLevel = .active
        }

        if let actionJSON = config.action.toJSON() {
            content.userInfo["action"] = actionJSON
        }

        return content
    }
}
