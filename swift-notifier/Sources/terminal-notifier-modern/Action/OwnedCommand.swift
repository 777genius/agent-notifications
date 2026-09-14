import Foundation
import Darwin

protocol OwnedCommand: AnyObject {
    // Completion means spawn failed or the owned child was reaped, not timeout.
    func start(completion: @escaping () -> Void)
    func cancel()
}

// All methods/timers are main-queue confined. POSIX_SPAWN_SETPGROUP establishes
// ownership atomically in the child before exec; no parent-side setpgid race.
final class SpawnedCommand: OwnedCommand {
    // Small syscall seam: fixtures exercise this owner's real escalation/reap
    // control flow without spawning or signalling anything.
    struct System {
        let spawn: (String) -> pid_t?
        let signalGroup: (pid_t, Int32) -> Void
        let reap: (pid_t) -> Bool
        let leaderExited: (pid_t) -> Bool
        let groupDrained: (pid_t) -> Bool

        init(spawn: @escaping (String) -> pid_t?,
             signalGroup: @escaping (pid_t, Int32) -> Void,
             reap: @escaping (pid_t) -> Bool,
             leaderExited: @escaping (pid_t) -> Bool,
             groupDrained: @escaping (pid_t) -> Bool) {
            self.spawn = spawn; self.signalGroup = signalGroup; self.reap = reap
            self.leaderExited = leaderExited; self.groupDrained = groupDrained
        }
        static let live = System(spawn: { (command: String) -> pid_t? in
            var attributes: posix_spawnattr_t?
            guard posix_spawnattr_init(&attributes) == 0 else { return nil }
            defer { posix_spawnattr_destroy(&attributes) }
            guard posix_spawnattr_setflags(&attributes, Int16(POSIX_SPAWN_SETPGROUP)) == 0,
                  posix_spawnattr_setpgroup(&attributes, 0) == 0 else { return nil }
            let arguments = ["/bin/sh", "-c", command].map { value in value.withCString { strdup($0) } }
            defer { arguments.forEach { free($0) } }
            guard arguments.allSatisfy({ $0 != nil }) else { return nil }
            // Foundation exposes the inherited environment without relying on the
            // private CRT _NSGetEnviron symbol, unavailable to Swift here.
            let environment = ProcessInfo.processInfo.environment.map { entry in "\(entry.key)=\(entry.value)".withCString { strdup($0) } }
            defer { environment.forEach { free($0) } }
            guard environment.allSatisfy({ $0 != nil }) else { return nil }
            var envp = environment + [nil]
            var argv = arguments + [nil]
            var pid: pid_t = 0
            let result = argv.withUnsafeMutableBufferPointer { buffer in
                envp.withUnsafeMutableBufferPointer { environmentBuffer in
                    posix_spawn(&pid, "/bin/sh", nil, &attributes, buffer.baseAddress!, environmentBuffer.baseAddress!)
                }
            }
            return result == 0 ? pid : nil
        }, signalGroup: { pid, signal in
            _ = kill(-pid, signal)
        }, reap: { pid in
            var status: Int32 = 0
            let result = waitpid(pid, &status, WNOHANG)
            return result == pid || (result == -1 && errno == ECHILD)
        }, leaderExited: { pid in
            var info = siginfo_t()
            // WNOWAIT reserves the leader PID (and hence our PGID) until drain.
            return waitid(P_PID, id_t(pid), &info, WEXITED | WNOHANG | WNOWAIT) == 0
                && info.si_pid == pid
        }, groupDrained: { pid in
            // A zombie leader alone makes kill(-pgid, 0) useless for this test.
            // Require a kernel snapshot containing only our zombie leader. Other
            // members (even zombies) conservatively retain ownership. Any error,
            // truncation or oversized group is unknown, never evidence of drain.
            var mib: [Int32] = [CTL_KERN, KERN_PROC, KERN_PROC_PGRP, pid]
            var bytes = 0
            guard sysctl(&mib, 4, nil, &bytes, nil, 0) == 0 else { return false }
            let stride = MemoryLayout<kinfo_proc>.stride
            let capacity = bytes / stride + 16
            guard capacity <= 4096 else { return false }
            var entries = [kinfo_proc](repeating: kinfo_proc(), count: capacity)
            bytes = capacity * stride
            let result = entries.withUnsafeMutableBytes { buffer in
                sysctl(&mib, 4, buffer.baseAddress, &bytes, nil, 0)
            }
            guard result == 0, bytes < capacity * stride, bytes % stride == 0 else { return false }
            let snapshot = entries.prefix(bytes / stride)
            // Require the reserved leader to be visible; an empty/filtered result
            // must not accidentally prove that an owned group has drained.
            guard snapshot.count == 1, let leader = snapshot.first else { return false }
            return leader.kp_proc.p_pid == pid && Int32(leader.kp_proc.p_stat) == SZOMB
        })
    }
    private let command: String
    private let system: System
    private let schedule: CallbackLifecycle.Schedule
    private var pid: pid_t = 0
    var processID: pid_t { pid }
    private var completion: (() -> Void)?
    private var cancelled = false
    private var escalating = false
    private var started = false
    init(_ command: String, system: System = .live, schedule: @escaping CallbackLifecycle.Schedule = { delay, work in
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }) { self.command = command; self.system = system; self.schedule = schedule }

    func start(completion: @escaping () -> Void) {
        precondition(!started)
        started = true
        guard !cancelled else { completion(); return }
        self.completion = completion
        guard let spawned = system.spawn(command), spawned > 0 else { finish(); return }
        pid = spawned
        poll()
    }
    func cancel() {
        guard !cancelled else { return }
        cancelled = true
        guard pid > 0 else { return }
        escalating = true
        system.signalGroup(pid, SIGTERM)
        // Keep the leader unreaped until the final group signal: its reserved PID
        // prevents a recycled PID/group from being signalled by the delayed timer.
        schedule(0.25) { [self] in
            guard pid > 0 else { return }
            system.signalGroup(pid, SIGKILL)
            escalating = false
            poll()
        }
    }
    private func poll() {
        guard pid > 0, !escalating else { return }
        // Normal exit is only observed, never consumed while helpers may run.
        // They retain the original callback deadline and may finish successfully.
        // After bounded cancellation the last group signal has already happened.
        if (cancelled || (system.leaderExited(pid) && system.groupDrained(pid))) && system.reap(pid) {
            pid = 0
            finish()
        } else {
            // No blocking wait on the callback queue. If the kernel cannot yet
            // reap a killed child, ownership continues to inhibit idle exit.
            schedule(0.05) { [self] in poll() }
        }
    }
    private func finish() {
        let done = completion
        completion = nil
        done?()
    }
}
