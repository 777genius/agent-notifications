import XCTest
@testable import terminal_notifier_modern

final class ArgumentParserTests: XCTestCase {

    func testParseValidArgs() throws {
        let config = try ArgumentParser.parse([
            "-title", "Hello",
            "-message", "World",
            "-activate", "com.apple.Terminal",
            "-group", "test-group"
        ])

        XCTAssertEqual(config.title, "Hello")
        XCTAssertEqual(config.message, "World")
        XCTAssertNil(config.subtitle)
        XCTAssertEqual(config.action, .activate(bundleID: "com.apple.Terminal"))
        XCTAssertEqual(config.group, "test-group")
        XCTAssertNil(config.threadID)
        XCTAssertFalse(config.timeSensitive)
        XCTAssertFalse(config.silent)
    }

    func testParseMinimalArgs() throws {
        let config = try ArgumentParser.parse([
            "-title", "Hello",
            "-message", "World"
        ])

        XCTAssertEqual(config.title, "Hello")
        XCTAssertEqual(config.message, "World")
        XCTAssertNil(config.subtitle)
        XCTAssertEqual(config.action, .none)
        XCTAssertNil(config.group)
        XCTAssertNil(config.threadID)
        XCTAssertFalse(config.timeSensitive)
        XCTAssertFalse(config.silent)
    }

    func testParseMissingTitle() {
        XCTAssertThrowsError(try ArgumentParser.parse([
            "-message", "World"
        ])) { error in
            XCTAssertTrue(error is ArgumentParserError)
            if case ArgumentParserError.missingTitle = error {
                // OK
            } else {
                XCTFail("Expected missingTitle error, got \(error)")
            }
        }
    }

    func testParseMissingMessage() {
        XCTAssertThrowsError(try ArgumentParser.parse([
            "-title", "Hello"
        ])) { error in
            XCTAssertTrue(error is ArgumentParserError)
            if case ArgumentParserError.missingMessage = error {
                // OK
            } else {
                XCTFail("Expected missingMessage error, got \(error)")
            }
        }
    }

    func testParseMissingValue() {
        XCTAssertThrowsError(try ArgumentParser.parse([
            "-title"
        ])) { error in
            XCTAssertTrue(error is ArgumentParserError)
            if case ArgumentParserError.missingValue(let flag) = error {
                XCTAssertEqual(flag, "-title")
            } else {
                XCTFail("Expected missingValue error, got \(error)")
            }
        }
    }

    func testParseExecuteAction() throws {
        let config = try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body",
            "-execute", "tmux select-pane -t main:0.2"
        ])

        XCTAssertEqual(config.action, .execute(command: "tmux select-pane -t main:0.2"))
    }

    func testParseBothActivateAndExecute() throws {
        let config = try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body",
            "-activate", "com.apple.Terminal",
            "-execute", "echo hello"
        ])

        XCTAssertEqual(config.action, .executeAndActivate(command: "echo hello", bundleID: "com.apple.Terminal"))
    }

    func testParseUnknownFlagsIgnored() throws {
        let config = try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body",
            "-unknown", "value"
        ])

        XCTAssertEqual(config.title, "Test")
        XCTAssertEqual(config.message, "Body")
    }

    func testIsSendMode() {
        XCTAssertTrue(ArgumentParser.isSendMode(["-title", "Hello", "-message", "World"]))
        XCTAssertFalse(ArgumentParser.isSendMode([]))
        XCTAssertFalse(ArgumentParser.isSendMode(["-message", "World"]))
    }

    func testParseGroupWithSpecialCharacters() throws {
        let config = try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body",
            "-group", "claude-notif-1234567890"
        ])

        XCTAssertEqual(config.group, "claude-notif-1234567890")
    }

    func testParseSubtitle() throws {
        let config = try ArgumentParser.parse([
            "-title", "Completed",
            "-message", "Task done",
            "-subtitle", "main · my-project"
        ])

        XCTAssertEqual(config.subtitle, "main · my-project")
    }

    func testParseThreadID() throws {
        let config = try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body",
            "-threadID", "session-abc-123"
        ])

        XCTAssertEqual(config.threadID, "session-abc-123")
    }

    func testParseTimeSensitive() throws {
        let config = try ArgumentParser.parse([
            "-title", "API Error",
            "-message", "Rate limited",
            "-timeSensitive"
        ])

        XCTAssertTrue(config.timeSensitive)
    }

    func testParseTimeSensitiveNotSet() throws {
        let config = try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body"
        ])

        XCTAssertFalse(config.timeSensitive)
    }

    func testParseNosound() throws {
        let config = try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body",
            "-nosound"
        ])

        XCTAssertTrue(config.silent)
    }

    func testParseAllNewOptions() throws {
        let config = try ArgumentParser.parse([
            "-title", "Completed [peak]",
            "-message", "Created 3 files",
            "-subtitle", "main · notification_plugin_go",
            "-activate", "com.mitchellh.ghostty",
            "-group", "claude-notif-123",
            "-threadID", "session-xyz",
            "-timeSensitive",
            "-nosound"
        ])

        XCTAssertEqual(config.title, "Completed [peak]")
        XCTAssertEqual(config.message, "Created 3 files")
        XCTAssertEqual(config.subtitle, "main · notification_plugin_go")
        XCTAssertEqual(config.action, .activate(bundleID: "com.mitchellh.ghostty"))
        XCTAssertEqual(config.group, "claude-notif-123")
        XCTAssertEqual(config.threadID, "session-xyz")
        XCTAssertTrue(config.timeSensitive)
        XCTAssertTrue(config.silent)
    }

    func testParseMissingSubtitleValue() {
        XCTAssertThrowsError(try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body",
            "-subtitle"
        ])) { error in
            if case ArgumentParserError.missingValue(let flag) = error {
                XCTAssertEqual(flag, "-subtitle")
            } else {
                XCTFail("Expected missingValue error, got \(error)")
            }
        }
    }

    func testParseMissingThreadIDValue() {
        XCTAssertThrowsError(try ArgumentParser.parse([
            "-title", "Test",
            "-message", "Body",
            "-threadID"
        ])) { error in
            if case ArgumentParserError.missingValue(let flag) = error {
                XCTAssertEqual(flag, "-threadID")
            } else {
                XCTFail("Expected missingValue error, got \(error)")
            }
        }
    }

    // MARK: - isHelpRequest

    func testIsHelpRequestStandaloneShortFlag() {
        XCTAssertTrue(ArgumentParser.isHelpRequest(["-help"]))
    }

    func testIsHelpRequestStandaloneLongFlag() {
        XCTAssertTrue(ArgumentParser.isHelpRequest(["--help"]))
    }

    func testIsHelpRequestFalseForNoArgs() {
        XCTAssertFalse(ArgumentParser.isHelpRequest([]))
    }

    func testIsHelpRequestFalseWhenHelpIsTitleValue() {
        // "-title -help -message World" is a complete, legitimate send
        // invocation where the title text happens to be "-help". It must
        // not be misdetected as a help request.
        let arguments = ["-title", "-help", "-message", "World"]
        XCTAssertFalse(ArgumentParser.isHelpRequest(arguments))
        XCTAssertTrue(ArgumentParser.isSendMode(arguments))

        let config = try? ArgumentParser.parse(arguments)
        XCTAssertEqual(config?.title, "-help")
        XCTAssertEqual(config?.message, "World")
    }

    func testIsHelpRequestFalseWhenHelpIsMessageValue() {
        // "-title Hello -message --help" likewise must be parsed as a
        // legitimate send invocation, not swallowed by the help path.
        let arguments = ["-title", "Hello", "-message", "--help"]
        XCTAssertFalse(ArgumentParser.isHelpRequest(arguments))
        XCTAssertTrue(ArgumentParser.isSendMode(arguments))

        let config = try? ArgumentParser.parse(arguments)
        XCTAssertEqual(config?.title, "Hello")
        XCTAssertEqual(config?.message, "--help")
    }

    func testIsHelpRequestFalseForNormalSendInvocation() {
        let arguments = ["-title", "Hello", "-message", "World", "-launchedViaLaunchServices"]
        XCTAssertFalse(ArgumentParser.isHelpRequest(arguments))
        XCTAssertTrue(ArgumentParser.isSendMode(arguments))
    }

    func testIsHelpRequestFalseForNoArgsCallbackMode() {
        // No-args invocation (callback mode) must not be treated as a
        // help request and must not be treated as send mode either.
        XCTAssertFalse(ArgumentParser.isHelpRequest([]))
        XCTAssertFalse(ArgumentParser.isSendMode([]))
    }
}
