import AppKit
import Foundation
import UserNotifications

private let launchServicesMarker = "-launchedViaLaunchServices"
private let notificationTimeoutSeconds = 10.0

let arguments = Array(CommandLine.arguments.dropFirst())

if ArgumentParser.isHelpRequest(arguments) {
    print("Usage: terminal-notifier-modern -title <title> -message <message> [options]")
    print("")
    print("  -title          Notification title (required)")
    print("  -message        Notification body (required)")
    print("  -subtitle       Notification subtitle (e.g. branch and folder)")
    print("  -activate       Bundle ID of app to activate on click")
    print("  -execute        Shell command to run on click")
    print("  -group          Group ID (replaces notifications with same group)")
    print("  -threadID       Thread ID for grouping notifications in a stack")
    print("  -timeSensitive  Mark as time-sensitive (breaks through Focus Mode)")
    print("  -nosound        Suppress notification sound")
    exit(ExitCode.success)
} else if ArgumentParser.isSendMode(arguments) {
    runSendMode(arguments: arguments)
} else {
    runCallbackMode()
}

// MARK: - Send Mode

func runSendMode(arguments: [String]) {
    let config: NotificationConfig
    do {
        config = try ArgumentParser.parse(arguments)
    } catch {
        failAndExit(error)
        return
    }

    guard arguments.contains(launchServicesMarker) else {
        failAndExit("ClaudeNotifier must be launched via LaunchServices (use 'open -W -n ClaudeNotifier.app --args ...')")
        return
    }

    // Without valid app bundle metadata, UNUserNotificationCenter may crash with
    // "bundleProxyForCurrentProcess is nil".
    guard Bundle.main.bundleIdentifier != nil else {
        failAndExit("ClaudeNotifier bundle metadata unavailable; LaunchServices did not provide a bundle proxy")
        return
    }

    guard Bundle.main.bundleURL.pathExtension == "app" else {
        failAndExit("ClaudeNotifier is not running from an app bundle")
        return
    }

    let app = NSApplication.shared
    app.setActivationPolicy(.accessory)

    DispatchQueue.main.async {
        NotificationCategory.register()
        checkAuthAndSend(config: config)
    }

    app.run()
}

func failAndExit(_ error: Error) {
    fputs("Error: \(error)\n", stderr)
    if case PermissionError.denied = error {
        exit(ExitCode.permissionDenied)
    }
    exit(ExitCode.failed)
}

func failAndExit(_ message: String) {
    fputs("Error: \(message)\n", stderr)
    exit(ExitCode.failed)
}

func checkAuthAndSend(config: NotificationConfig) {
    PermissionManager.ensurePermission { result in
        DispatchQueue.main.async {
            switch result {
            case .success:
                sendNotification(config: config)
            case .failure(let error):
                failAndExit(error)
            }
        }
    }
}

func sendNotification(config: NotificationConfig) {
    let service = UNNotificationService()

    let timeoutWorkItem = DispatchWorkItem {
        failAndExit("UNUserNotificationCenter timed out after \(Int(notificationTimeoutSeconds)) seconds")
    }

    DispatchQueue.main.asyncAfter(deadline: .now() + notificationTimeoutSeconds, execute: timeoutWorkItem)

    service.send(config: config) { result in
        DispatchQueue.main.async {
            timeoutWorkItem.cancel()

            switch result {
            case .success:
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
                    exit(ExitCode.success)
                }
            case .failure(let error):
                failAndExit(error)
            }
        }
    }
}

// MARK: - Callback Mode

func runCallbackMode() {
    let app = NSApplication.shared
    app.setActivationPolicy(.accessory)

    let appDelegate = AppDelegate()
    app.delegate = appDelegate
    UNUserNotificationCenter.current().delegate = appDelegate

    DispatchQueue.main.asyncAfter(deadline: .now() + 10) {
        NSApplication.shared.terminate(nil)
    }

    withExtendedLifetime(appDelegate) {
        app.run()
    }
}
