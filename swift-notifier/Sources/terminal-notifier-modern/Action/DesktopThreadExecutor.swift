import AppKit
import Security

typealias DesktopOpenResult = CallbackOutcome
protocol ApplicationDiscovering {
    func selectedApplication(path: String) -> URL?
    func registeredApplications(bundleID: String) -> [URL]
}
protocol ApplicationVerifying { func verify(_ url: URL, bundleID: String, teamID: String) -> Bool }
protocol DesktopURLOpening { func open(_ url: URL, application: URL, completion: @escaping (Bool) -> Void) }

struct ConfiguredApplicationDiscovery: ApplicationDiscovering {
    func registeredApplications(bundleID: String) -> [URL] {
        if #available(macOS 12.0, *) {
            return NSWorkspace.shared.urlsForApplications(withBundleIdentifier: bundleID)
        }
        return [] // Older supported systems require the configured location.
    }
    func selectedApplication(path: String) -> URL? {
        let url = URL(fileURLWithPath: path)
        var directory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &directory), directory.boolValue else { return nil }
        return url.resolvingSymlinksInPath().standardizedFileURL
    }
}
struct SignedApplicationVerifier: ApplicationVerifying {
    func verify(_ url: URL, bundleID: String, teamID: String) -> Bool {
        guard bundleID == "com.openai.codex", Bundle(url: url)?.bundleIdentifier == bundleID else { return false }
        // Apple-anchored Developer ID distribution; expected team comes from explicit
        // operator configuration. Ad-hoc, unsigned and other teams fail closed.
        let expression = "anchor apple generic and identifier \"com.openai.codex\" and certificate leaf[subject.OU] = \"\(teamID)\" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists"
        var requirement: SecRequirement?
        var code: SecStaticCode?
        guard SecRequirementCreateWithString(expression as CFString, [], &requirement) == errSecSuccess,
              SecStaticCodeCreateWithPath(url as CFURL, [], &code) == errSecSuccess,
              let code = code, let requirement = requirement else { return false }
        return SecStaticCodeCheckValidity(code, SecCSFlags(rawValue: kSecCSCheckAllArchitectures | kSecCSStrictValidate), requirement) == errSecSuccess
    }
}
struct WorkspaceDesktopOpener: DesktopURLOpening {
    func open(_ url: URL, application: URL, completion: @escaping (Bool) -> Void) {
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = true
        NSWorkspace.shared.open([url], withApplicationAt: application, configuration: configuration) { app, error in
            DispatchQueue.main.async { completion(error == nil && app != nil) }
        }
    }
}
// A refused admission schedules no job. Slots include queued/running work and
// are released only when the underlying synchronous preflight returns.
final class PreflightAdmission {
    static let shared = PreflightAdmission(limit: 2)
    private let lock = NSLock()
    private let limit: Int
    private var count = 0
    init(limit: Int) { precondition(limit > 0); self.limit = limit }
    func acquire() -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard count < limit else { return false }
        count += 1; return true
    }
    func release() { lock.lock(); count -= 1; lock.unlock() }
}

final class DesktopThreadExecutor {
    private let discovery: ApplicationDiscovering
    private let verifier: ApplicationVerifying
    private let opener: DesktopURLOpening
    typealias Work = (@escaping () -> Void) -> Void
    private let verificationWork: Work
    private let deliverResult: Work
    private let admission: PreflightAdmission
    init(discovery: ApplicationDiscovering = ConfiguredApplicationDiscovery(),
         verifier: ApplicationVerifying = SignedApplicationVerifier(),
         opener: DesktopURLOpening = WorkspaceDesktopOpener(),
         verificationWork: @escaping Work = { work in DispatchQueue.global(qos: .utility).async(execute: work) },
         deliverResult: @escaping Work = { work in DispatchQueue.main.async(execute: work) },
         admission: PreflightAdmission = .shared) {
        self.discovery = discovery; self.verifier = verifier; self.opener = opener
        self.verificationWork = verificationWork; self.deliverResult = deliverResult
        self.admission = admission
    }
    func execute(_ action: DesktopThreadAction, token: CallbackToken = CallbackToken(),
                 isActive: @escaping () -> Bool = { true }, completion: @escaping (DesktopOpenResult) -> Void) {
        guard token.isActive && isActive() else { completion(.open_unknown); return }
        do { try action.validate() }
        catch { completion(.malformed_action); return }
        guard admission.acquire() else { completion(.open_unknown); return }
        verificationWork { [self] in
            let result = preflight(action, token: token)
            deliverResult { [self] in
                admission.release()
                // isActive is a compatibility seam, called only on the callback queue.
                guard token.isActive && isActive() else { completion(.open_unknown); return }
                switch result {
                case .success(let app):
                    opener.open(action.url, application: app) { completion($0 ? .open_requested : .open_failed) }
                case .failure(let outcome): completion(outcome)
                }
            }
        }
    }
    private enum PreflightResult { case success(URL), failure(CallbackOutcome) }
    private func preflight(_ action: DesktopThreadAction, token: CallbackToken) -> PreflightResult {
        guard token.isActive else { return .failure(.open_unknown) }
        let app = discovery.selectedApplication(path: action.applicationPath)
        guard token.isActive else { return .failure(.open_unknown) }
        if let app = app {
            guard app.path == action.applicationPath else { return .failure(.application_moved) }
            let valid = verifier.verify(app, bundleID: action.bundleID, teamID: action.teamID)
            guard token.isActive else { return .failure(.open_unknown) }
            return valid ? .success(app) : .failure(.identity_mismatch)
        }
        let registered = discovery.registeredApplications(bundleID: action.bundleID)
        guard token.isActive else { return .failure(.open_unknown) }
        guard registered.count <= 16 else { return .failure(.application_ambiguous) }
        var paths = Set<String>()
        for url in registered {
            guard token.isActive else { return .failure(.open_unknown) }
            let valid = verifier.verify(url, bundleID: action.bundleID, teamID: action.teamID)
            guard token.isActive else { return .failure(.open_unknown) }
            if valid { paths.insert(url.resolvingSymlinksInPath().standardizedFileURL.path) }
        }
        return .failure(paths.count > 1 ? .application_ambiguous :
            (paths.isEmpty ? .application_missing : .application_moved))
    }
}
