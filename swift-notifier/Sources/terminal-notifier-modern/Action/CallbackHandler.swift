import Foundation

// The delegates and fixtures use this same production routing/ownership boundary.
final class CallbackHandler {
    private let lifecycle: CallbackLifecycle
    private let desktop: DesktopThreadExecutor
    private let legacy: ActionExecuting
    init(lifecycle: CallbackLifecycle, desktop: DesktopThreadExecutor = DesktopThreadExecutor(),
         legacy: ActionExecuting = ActionExecutor()) {
        self.lifecycle = lifecycle; self.desktop = desktop; self.legacy = legacy
    }
    func receive(identifier: String, defaultIdentifier: String, notificationID: String,
                 userInfo: [AnyHashable: Any], completion: @escaping () -> Void) {
        let route = CallbackRoute.decode(identifier: identifier, defaultIdentifier: defaultIdentifier,
                                         notificationID: notificationID, userInfo: userInfo)
        // Decode a target independently of click selection so a valid dismissed
        // notification is still attributable; malformed input never supplies IDs.
        let validated = CallbackRoute.decode(identifier: defaultIdentifier, defaultIdentifier: defaultIdentifier,
                                             notificationID: notificationID, userInfo: userInfo)
        lifecycle.acceptOwned(correlation: validated.correlation, completion: completion) { [self] done, work in
            switch route {
            case .desktop(let action): desktop.execute(action, token: work.token, completion: done)
            case .legacy(let action): legacy.execute(action, work: work) { done(.legacy_completed) }
            case .malformed: done(.malformed_action)
            case .ignored: done(.ignored)
            }
        }
    }
}
