import AppKit
import UserNotifications

final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {

    let lifecycle = ProcessCallbackLifecycle.shared
    private let actionExecutor: ActionExecuting

    init(actionExecutor: ActionExecuting = ActionExecutor()) {
        self.actionExecutor = actionExecutor
        super.init()
    }

    // Both delegate properties are weak. The app.run owner must retain the
    // returned delegate, and install it before AppKit finishes launching.
    static func install(on app: NSApplication) -> AppDelegate {
        let delegate = AppDelegate()
        app.delegate = delegate
        UNUserNotificationCenter.current().delegate = delegate
        return delegate
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let handle = { [self] in
            CallbackHandler(lifecycle: lifecycle, legacy: actionExecutor).receive(
                identifier: response.actionIdentifier,
                defaultIdentifier: UNNotificationDefaultActionIdentifier,
                notificationID: response.notification.request.identifier,
                userInfo: response.notification.request.content.userInfo,
                completion: completionHandler)
        }
        if Thread.isMainThread { handle() }
        else { DispatchQueue.main.async(execute: handle) }
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        if #available(macOS 11.0, *) {
            completionHandler([.banner, .sound])
        } else {
            completionHandler([.alert, .sound])
        }
    }
}
