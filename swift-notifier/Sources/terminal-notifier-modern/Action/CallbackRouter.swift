import Foundation

// Response selection is independent of UNNotificationResponse construction so
// fixtures exercise the same dispatch boundary used by the application delegate.
enum CallbackRoute {
    case desktop(DesktopThreadAction)
    case legacy(ClickAction)
    case ignored
    case malformed

    // Only a decoded typed action, including its match to the notification ID,
    // supplies trusted correlation. Legacy/malformed/dismiss input gets an event UUID.
    var correlation: UUID {
        if case .desktop(let action) = self,
           let id = UUID(uuidString: action.correlationID) { return id }
        return UUID()
    }

    static func decode(identifier: String, defaultIdentifier: String,
                       notificationID: String, userInfo: [AnyHashable: Any]) -> CallbackRoute {
        guard identifier == "OPEN" || identifier == defaultIdentifier else { return .ignored }
        // A present but invalid typed action must never fall through to legacy.
        if let value = userInfo["desktop_thread_v1"] {
            guard let json = value as? String,
                  let action = try? DesktopThreadAction.decode(Data(json.utf8)),
                  action.correlationID == notificationID else { return .malformed }
            return .desktop(action)
        }
        if let value = userInfo["action"] {
            guard let json = value as? String, let action = ClickAction.fromJSON(json) else { return .malformed }
            return .legacy(action)
        }
        return .ignored
    }
}
