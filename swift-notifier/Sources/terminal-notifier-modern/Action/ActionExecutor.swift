import AppKit
import Foundation

protocol ActionExecuting {
    func execute(_ action: ClickAction, isActive: @escaping () -> Bool, completion: @escaping () -> Void)
    func execute(_ action: ClickAction, work: CallbackWork, completion: @escaping () -> Void)
}

extension ActionExecuting {
    func execute(_ action: ClickAction, work: CallbackWork, completion: @escaping () -> Void) {
        execute(action, isActive: { work.token.isActive }, completion: completion)
    }
}

final class ActionExecutor: ActionExecuting {
    // Only discovery runs on a worker; the returned activation and opener run on
    // main, behind the original callback's activity check. Fixtures substitute
    // these platform calls without replacing the activation control flow.
    struct ActivationSystem {
        let running: (String) -> (() -> Bool)?
        let lookup: (String) -> URL?
        let open: (URL, @escaping () -> Void) -> Void
        static let live = ActivationSystem(running: { bundleID in
            guard let app = NSRunningApplication.runningApplications(withBundleIdentifier: bundleID).first else { return nil }
            return {
                if #available(macOS 14.0, *) {
                    return app.activate(from: NSRunningApplication.current, options: [.activateIgnoringOtherApps])
                }
                return app.activate(options: [.activateIgnoringOtherApps])
            }
        }, lookup: { NSWorkspace.shared.urlForApplication(withBundleIdentifier: $0) }, open: { url, done in
            let configuration = NSWorkspace.OpenConfiguration()
            configuration.activates = true
            NSWorkspace.shared.openApplication(at: url, configuration: configuration) { _, _ in
                DispatchQueue.main.async { done() }
            }
        })
    }
    typealias Worker = (@escaping () -> Void) -> Void
    private let activationSystem: ActivationSystem
    private let discoveryWork: Worker
    private let deliverResult: Worker
    private let admission: PreflightAdmission
    private let makeCommand: (String) -> OwnedCommand
    private let activation: ((String, @escaping () -> Void) -> Void)?
    init(makeCommand: @escaping (String) -> OwnedCommand = { SpawnedCommand($0) },
         activation: ((String, @escaping () -> Void) -> Void)? = nil,
         activationSystem: ActivationSystem = .live,
         discoveryWork: @escaping Worker = { DispatchQueue.global(qos: .utility).async(execute: $0) },
         deliverResult: @escaping Worker = { DispatchQueue.main.async(execute: $0) },
         admission: PreflightAdmission = .shared) {
        self.makeCommand = makeCommand; self.activation = activation
        self.activationSystem = activationSystem; self.discoveryWork = discoveryWork
        self.deliverResult = deliverResult; self.admission = admission
    }
    func execute(_ action: ClickAction, work: CallbackWork, completion: @escaping () -> Void) {
        execute(action, isActive: { work.token.isActive }, work: work, completion: completion)
    }


    func execute(_ action: ClickAction, isActive: @escaping () -> Bool, completion: @escaping () -> Void) {
        execute(action, isActive: isActive, work: nil, completion: completion)
    }
    private func execute(_ action: ClickAction, isActive: @escaping () -> Bool,
                         work: CallbackWork?, completion: @escaping () -> Void) {
        guard isActive() else { completion(); return }
        switch action {
        case .activate(let bundleID):
            activateApp(bundleID: bundleID, token: work?.token, isActive: isActive, completion: completion)
        case .execute(let command):
            executeCommand(command, work: work, completion: completion)
        case .executeAndActivate(let command, let bundleID):
            executeCommand(command, work: work) { [self] in
                guard isActive() else { completion(); return }
                activateApp(bundleID: bundleID, token: work?.token, isActive: isActive, completion: completion)
            }
        case .none:
            completion()
        }
    }

    private func activateApp(bundleID: String, token: CallbackToken?,
                             isActive: @escaping () -> Bool, completion: @escaping () -> Void) {
        guard isActive() else { completion(); return }
        if let activation = activation { activation(bundleID, completion); return }
        guard admission.acquire() else { completion(); return }
        discoveryWork { [self] in
            let running = token?.isActive == false ? nil : activationSystem.running(bundleID)
            deliverResult { [self] in
                admission.release()
                guard isActive() else { completion(); return }
                // Preserve legacy running-first selection and its no-new-window
                // activation. A failed activation keeps the same guarded fallback.
                if let running = running {
                    guard isActive() else { completion(); return }
                    if running() { completion(); return }
                }
                lookupAndOpen(bundleID: bundleID, token: token, isActive: isActive, completion: completion)
            }
        }
    }

    private func lookupAndOpen(bundleID: String, token: CallbackToken?,
                               isActive: @escaping () -> Bool, completion: @escaping () -> Void) {
        guard isActive(), admission.acquire() else { completion(); return }
        discoveryWork { [self] in
            let url = token?.isActive == false ? nil : activationSystem.lookup(bundleID)
            deliverResult { [self] in
                // A stuck lookup holds its slot even after callback timeout.
                admission.release()
                guard isActive(), let url = url else { completion(); return }
                activationSystem.open(url, completion)
            }
        }
    }

    private func executeCommand(_ command: String, work: CallbackWork?, completion: @escaping () -> Void) {
        let child = makeCommand(command)
        let drained = work?.own(cancel: { child.cancel() }) ?? {}
        var finished = false
        child.start {
            guard !finished else { return }
            finished = true
            drained()
            completion()
        }
    }
}
