import AppKit
import UserNotifications

final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {

    let lifecycle = ProcessCallbackLifecycle.shared
    private let actionExecutor: ActionExecuting

    init(actionExecutor: ActionExecuting = ActionExecutor()) {
        self.actionExecutor = actionExecutor
        super.init()
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
