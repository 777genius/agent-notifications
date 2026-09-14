import Foundation

struct NotificationConfig {
    let title: String
    let message: String
    let subtitle: String?
    let action: ClickAction
    let group: String?
    let threadID: String?
    let timeSensitive: Bool
    let silent: Bool
}

enum ArgumentParserError: Error, CustomStringConvertible {
    case missingTitle
    case missingMessage
    case missingValue(String)

    var description: String {
        switch self {
        case .missingTitle:
            return "Missing required argument: -title"
        case .missingMessage:
            return "Missing required argument: -message"
        case .missingValue(let flag):
            return "Missing value for argument: \(flag)"
        }
    }
}

enum ArgumentParser {

    static func parse(_ arguments: [String]) throws -> NotificationConfig {
        var title: String?
        var message: String?
        var subtitle: String?
        var activate: String?
        var execute: String?
        var group: String?
        var threadID: String?
        var timeSensitive = false
        var silent = false

        var i = 0
        while i < arguments.count {
            let arg = arguments[i]

            switch arg {
            case "-title":
                guard i + 1 < arguments.count else {
                    throw ArgumentParserError.missingValue("-title")
                }
                i += 1
                title = arguments[i]

            case "-message":
                guard i + 1 < arguments.count else {
                    throw ArgumentParserError.missingValue("-message")
                }
                i += 1
                message = arguments[i]

            case "-subtitle":
                guard i + 1 < arguments.count else {
                    throw ArgumentParserError.missingValue("-subtitle")
                }
                i += 1
                subtitle = arguments[i]

            case "-activate":
                guard i + 1 < arguments.count else {
                    throw ArgumentParserError.missingValue("-activate")
                }
                i += 1
                activate = arguments[i]

            case "-execute":
                guard i + 1 < arguments.count else {
                    throw ArgumentParserError.missingValue("-execute")
                }
                i += 1
                execute = arguments[i]

            case "-group":
                guard i + 1 < arguments.count else {
                    throw ArgumentParserError.missingValue("-group")
                }
                i += 1
                group = arguments[i]

            case "-threadID":
                guard i + 1 < arguments.count else {
                    throw ArgumentParserError.missingValue("-threadID")
                }
                i += 1
                threadID = arguments[i]

            case "-timeSensitive":
                timeSensitive = true

            case "-nosound":
                silent = true

            default:
                break
            }

            i += 1
        }

        guard let titleValue = title else {
            throw ArgumentParserError.missingTitle
        }

        guard let messageValue = message else {
            throw ArgumentParserError.missingMessage
        }

        let action: ClickAction
        if let bundleID = activate, let command = execute {
            action = .executeAndActivate(command: command, bundleID: bundleID)
        } else if let bundleID = activate {
            action = .activate(bundleID: bundleID)
        } else if let command = execute {
            action = .execute(command: command)
        } else {
            action = .none
        }

        return NotificationConfig(
            title: titleValue,
            message: messageValue,
            subtitle: subtitle,
            action: action,
            group: group,
            threadID: threadID,
            timeSensitive: timeSensitive,
            silent: silent
        )
    }

    // Consume values before classifying options. A value is literal even when it
    // happens to be another flag (including the LaunchServices marker).
    static func optionPositions(_ arguments: [String]) -> [String] {
        let valued: Set<String> = ["-title", "-message", "-subtitle", "-activate",
                                   "-execute", "-group", "-threadID",
                                   "--request-file", "--receipt-file"]
        var options: [String] = []
        var i = 0
        while i < arguments.count {
            let value = arguments[i]
            options.append(value)
            i += valued.contains(value) ? 2 : 1
        }
        return options
    }

    static func isSendMode(_ arguments: [String]) -> Bool {
        return optionPositions(arguments).contains("-title")
    }

    /// Recognizes only a standalone help invocation (`-help` or `--help` as the
    /// sole argument). This avoids false positives when `-help`/`--help` is
    /// passed as the *value* of `-title`/`-message`/etc. in a real send invocation.
    static func isHelpRequest(_ arguments: [String]) -> Bool {
        guard arguments.count == 1 else { return false }
        return arguments[0] == "-help" || arguments[0] == "--help"
    }
}
