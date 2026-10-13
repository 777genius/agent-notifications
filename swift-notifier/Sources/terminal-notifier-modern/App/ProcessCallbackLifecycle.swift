import AppKit
import Darwin
import os.log

// Both current and legacy notification delegates share this single process owner.
enum ProcessCallbackLifecycle {
    private static let callbackLog = OSLog(subsystem: "com.777genius.agent-notifications",
                                           category: "notification-callback")
    static let shared: CallbackLifecycle = CallbackLifecycle(schedule: { delay, work in
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }, exit: {
        if let code = ProcessCallbackLifecycle.shared.exitCode { Darwin.exit(code) }
        NSApplication.shared.terminate(nil)
    }, diagnostic: {
        // Legacy LaunchServices callers treat stderr as a delivery failure.
        // Preserve callback evidence without turning a click into send fallback.
        os_log("%{public}@", log: callbackLog, type: .default, $0)
    })
}
