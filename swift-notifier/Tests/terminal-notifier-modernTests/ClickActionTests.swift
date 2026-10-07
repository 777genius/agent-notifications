import XCTest
@testable import terminal_notifier_modern

final class ClickActionTests: XCTestCase {

    func testActivateEncodeDecode() throws {
        let action = ClickAction.activate(bundleID: "com.apple.Terminal")

        let json = action.toJSON()
        XCTAssertNotNil(json)

        let decoded = ClickAction.fromJSON(json!)
        XCTAssertNotNil(decoded)
        XCTAssertEqual(decoded, action)
    }

    func testExecuteEncodeDecode() throws {
        let action = ClickAction.execute(command: "tmux select-pane -t main:0.2")

        let json = action.toJSON()
        XCTAssertNotNil(json)

        let decoded = ClickAction.fromJSON(json!)
        XCTAssertNotNil(decoded)
        XCTAssertEqual(decoded, action)
    }

    func testExecuteAndActivateEncodeDecode() throws {
        let action = ClickAction.executeAndActivate(
            command: "tmux select-pane -t main:0.2",
            bundleID: "com.apple.Terminal"
        )

        let json = action.toJSON()
        XCTAssertNotNil(json)

        let decoded = ClickAction.fromJSON(json!)
        XCTAssertNotNil(decoded)
        XCTAssertEqual(decoded, action)
    }

    func testNoneEncodeDecode() throws {
        let action = ClickAction.none

        let json = action.toJSON()
        XCTAssertNotNil(json)

        let decoded = ClickAction.fromJSON(json!)
        XCTAssertNotNil(decoded)
        XCTAssertEqual(decoded, action)
    }

    func testInvalidJSON() {
        XCTAssertNil(ClickAction.fromJSON("invalid json"))
        XCTAssertNil(ClickAction.fromJSON(""))
        XCTAssertNil(ClickAction.fromJSON("{}"))
    }

    func testJSONFormat() throws {
        let action = ClickAction.activate(bundleID: "com.apple.Terminal")
        let json = action.toJSON()!

        XCTAssertTrue(json.contains("\"type\""))
        XCTAssertTrue(json.contains("\"activate\""))
        XCTAssertTrue(json.contains("\"value\""))
        XCTAssertTrue(json.contains("com.apple.Terminal"))
    }

    func testEquatable() {
        XCTAssertEqual(
            ClickAction.activate(bundleID: "com.app.id"),
            ClickAction.activate(bundleID: "com.app.id")
        )
        XCTAssertNotEqual(
            ClickAction.activate(bundleID: "com.app.id"),
            ClickAction.activate(bundleID: "com.other.id")
        )
        XCTAssertNotEqual(
            ClickAction.activate(bundleID: "com.app.id"),
            ClickAction.none
        )
        XCTAssertEqual(ClickAction.none, ClickAction.none)
        XCTAssertNotEqual(
            ClickAction.execute(command: "a"),
            ClickAction.execute(command: "b")
        )
    }

    func testSpecialCharactersInCommand() throws {
        let command = "tmux send-keys -t 'my session:0.1' 'echo \"hello world\"' Enter"
        let action = ClickAction.execute(command: command)

        let json = action.toJSON()
        XCTAssertNotNil(json)

        let decoded = ClickAction.fromJSON(json!)
        XCTAssertEqual(decoded, action)
    }
}

extension ClickActionTests {
    func testLegacyCompatibilityFixtures() throws {
        struct Fixture: Decodable { let json: String; let valid: Bool }
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().appendingPathComponent("Fixtures/legacy-actions.json")
        let fixtures = try JSONDecoder().decode([Fixture].self, from: Data(contentsOf: url))
        XCTAssertEqual(fixtures.count, 8)
        for fixture in fixtures {
            let action = ClickAction.fromJSON(fixture.json)
            XCTAssertEqual(action != nil, fixture.valid)
            if let action = action {
                XCTAssertEqual(ClickAction.fromJSON(try XCTUnwrap(action.toJSON())), action)
            }
        }
    }
}
