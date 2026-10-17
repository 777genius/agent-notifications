import XCTest
import UserNotifications
@testable import terminal_notifier_modern

final class NotificationServiceTests: XCTestCase {
    func testCustomSoundContentRespectsMuteAndFocusAndRejectsPreparationFailure() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        var config = NotificationConfig(title: "Test", message: "Body", subtitle: nil, action: .none, group: nil, threadID: nil, timeSensitive: true, silent: false, soundFile: "/missing/sound.mp3", soundVolume: 0)
        let zero = try UNNotificationService.makeContent(config: config, soundDirectory: root)
        XCTAssertNil(zero.sound)
        if #available(macOS 12.0, *) { XCTAssertEqual(zero.interruptionLevel, .active) }
        XCTAssertFalse(FileManager.default.fileExists(atPath: root.path))
        config.soundVolume = 0.25
        XCTAssertThrowsError(try UNNotificationService.makeContent(config: config, soundDirectory: root))
        let silent = NotificationConfig(title: "Test", message: "Body", subtitle: nil, action: .none, group: nil, threadID: nil, timeSensitive: true, silent: true)
        XCTAssertNil(try UNNotificationService.makeContent(config: silent, soundDirectory: root).sound)
        let repository = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        config.soundFile = repository.appendingPathComponent("sounds/task-complete.mp3").path
        let custom = try UNNotificationService.makeContent(config: config, soundDirectory: root)
        XCTAssertNotNil(custom.sound)
        if #available(macOS 12.0, *) { XCTAssertEqual(custom.interruptionLevel, .active) }
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.path).count, 1)
        let compatible = NotificationConfig(title: "Test", message: "Body", subtitle: nil, action: .none, group: nil, threadID: nil, timeSensitive: true, silent: true, soundFile: config.soundFile, soundVolume: 0.25)
        XCTAssertNotNil(try UNNotificationService.makeContent(config: compatible, soundDirectory: root).sound)
    }

    func testNotificationConfigCreation() {
        let config = NotificationConfig(
            title: "Test Title",
            message: "Test Message",
            subtitle: "main · my-project",
            action: .activate(bundleID: "com.apple.Terminal"),
            group: "test-group",
            threadID: "session-123",
            timeSensitive: true,
            silent: false
        )

        XCTAssertEqual(config.title, "Test Title")
        XCTAssertEqual(config.message, "Test Message")
        XCTAssertEqual(config.subtitle, "main · my-project")
        XCTAssertEqual(config.action, .activate(bundleID: "com.apple.Terminal"))
        XCTAssertEqual(config.group, "test-group")
        XCTAssertEqual(config.threadID, "session-123")
        XCTAssertTrue(config.timeSensitive)
        XCTAssertFalse(config.silent)
    }

    func testNotificationConfigWithNilGroup() {
        let config = NotificationConfig(
            title: "Test",
            message: "Body",
            subtitle: nil,
            action: .none,
            group: nil,
            threadID: nil,
            timeSensitive: false,
            silent: true
        )

        XCTAssertNil(config.group)
        XCTAssertNil(config.subtitle)
        XCTAssertNil(config.threadID)
        XCTAssertEqual(config.action, .none)
        XCTAssertFalse(config.timeSensitive)
        XCTAssertTrue(config.silent)
    }

    func testExitCodes() {
        XCTAssertEqual(ExitCode.success, 0)
        XCTAssertEqual(ExitCode.invalidArgs, 1)
        XCTAssertEqual(ExitCode.permissionDenied, 2)
        XCTAssertEqual(ExitCode.failed, 3)
    }

    func testActionToJSONForUserInfo() {
        let action = ClickAction.activate(bundleID: "com.apple.Terminal")
        let json = action.toJSON()

        XCTAssertNotNil(json, "Action should produce valid JSON for userInfo storage")

        let decoded = ClickAction.fromJSON(json!)
        XCTAssertEqual(decoded, action, "Round-trip through JSON should preserve the action")
    }
}
