import XCTest
import Darwin
@testable import terminal_notifier_modern

final class OwnedCommandStdioTests: XCTestCase {
    // Process-wide descriptor capture is limited to spawn, with immediate parent
    // restoration. The only child is a harmless shell in a disposable fixture.
    func testMacOwnedChildCannotReadOrWriteCallerStdio() throws {
        guard ProcessInfo.processInfo.environment["NOTIFIER_TEST_OWNED_CHILD"] == "1" else {
            throw XCTSkip("Coordinator macOS opt-in: NOTIFIER_TEST_OWNED_CHILD=1")
        }
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: false)
        defer { try? FileManager.default.removeItem(at: root) }
        let input = root.appendingPathComponent("stdin")
        let output = root.appendingPathComponent("stdout")
        let errors = root.appendingPathComponent("stderr")
        try Data("private-caller-input\n".utf8).write(to: input)
        try Data().write(to: output)
        try Data().write(to: errors)
        let fixture = try [input, output, errors].map { url -> FileHandle in
            try FileHandle(forUpdating: url)
        }
        defer { fixture.forEach { try? $0.close() } }
        let command = "printf 'child-stdout'; printf 'child-stderr' >&2; " +
            "if IFS= read -r line; then printf 'stdin-readable:%s' \"$line\"; fi"
        let pid = try XCTUnwrap(try spawnCapturingStdio(command, fixture: fixture))
        var reaped = false
        defer {
            if !reaped {
                // The unreaped leader still reserves our owned process group.
                _ = kill(-pid, SIGKILL)
                var status: Int32 = 0
                _ = waitpid(pid, &status, 0)
            }
        }
        var status: Int32 = 0
        var result: pid_t = 0
        let deadline = Date().addingTimeInterval(3)
        repeat {
            result = waitpid(pid, &status, WNOHANG)
            if result == pid { reaped = true; break }
            if result == -1 { break }
            usleep(10_000)
        } while Date() < deadline
        XCTAssertEqual(result, pid, "harmless shell must terminate within the test budget")
        XCTAssertEqual(status, 0, "isolated child must still execute normally")
        XCTAssertEqual(try Data(contentsOf: output), Data(), "child stdout leaked into the caller protocol")
        XCTAssertEqual(try Data(contentsOf: errors), Data(), "child stderr leaked into delivery diagnostics")
        XCTAssertEqual(try fixture[0].offset(), 0, "child consumed caller protocol input")
    }

    private func spawnCapturingStdio(_ command: String, fixture: [FileHandle]) throws -> pid_t? {
        var saved: [Int32] = []
        defer { saved.forEach { _ = Darwin.close($0) } }
        for descriptor in [STDIN_FILENO, STDOUT_FILENO, STDERR_FILENO] {
            let duplicate = fcntl(descriptor, F_DUPFD_CLOEXEC, 3)
            guard duplicate >= 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
            saved.append(duplicate)
        }
        defer {
            // Restore before any assertion, wait or XCTest output.
            for descriptor in [STDIN_FILENO, STDOUT_FILENO, STDERR_FILENO] {
                precondition(dup2(saved[Int(descriptor)], descriptor) == descriptor)
            }
        }
        for descriptor in [STDIN_FILENO, STDOUT_FILENO, STDERR_FILENO] {
            guard dup2(fixture[Int(descriptor)].fileDescriptor, descriptor) == descriptor else {
                throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
            }
        }
        return SpawnedCommand.System.live.spawn(command)
    }
}
