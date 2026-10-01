import AppKit
import os.log

// Both current and legacy notification delegates share this single process owner.
enum ProcessCallbackLifecycle {
    private static let log = OSLog(subsystem: "com.777genius.agent-notifications", category: "callback")
    static let shared = CallbackLifecycle(schedule: { delay, work in
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }, exit: { NSApplication.shared.terminate(nil) }, diagnostic: {
        // Payload contains only fixed phases/outcomes, correlation UUID and elapsed time.
        os_log("%{public}@", log: log, type: .default, $0)
        fputs("\($0)\n", stderr)
    })
}
