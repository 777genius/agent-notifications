import AppKit

// Both current and legacy notification delegates share this single process owner.
enum ProcessCallbackLifecycle {
    static let shared = CallbackLifecycle(schedule: { delay, work in
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }, exit: { NSApplication.shared.terminate(nil) }, diagnostic: {
        fputs("\($0)\n", stderr)
    })
}
