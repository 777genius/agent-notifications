// Synthetic observer prerequisites only. No Pi, product notification or permission prompt.
import AppKit
import ApplicationServices
import AVFoundation
import CoreAudio
import CoreMedia
import ScreenCaptureKit

final class Capture: NSObject, SCStreamOutput, SCStreamDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private var samples = 0
    private var frames = 0
    private var peak: Float = 0

    func stream(_ stream: SCStream, didOutputSampleBuffer buffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard buffer.isValid else { return }
        lock.lock()
        defer { lock.unlock() }
        if type == .screen {
            frames += 1
            return
        }
        guard type == .audio, let description = buffer.formatDescription else { return }
        let format = AVAudioFormat(cmAudioFormatDescription: description)
        let count = AVAudioFrameCount(buffer.numSamples)
        guard let pcm = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: count) else { return }
        pcm.frameLength = count
        let status = CMSampleBufferCopyPCMDataIntoAudioBufferList(buffer, at: 0, frameCount: Int32(count), into: pcm.mutableAudioBufferList)
        guard status == noErr, let channels = pcm.floatChannelData else { return }
        samples += Int(count)
        for channel in 0..<Int(format.channelCount) {
            for index in 0..<Int(count) {
                let value = format.isInterleaved ? channels[0][index * Int(format.channelCount) + channel] : channels[channel][index]
                if value.isFinite { peak = max(peak, abs(value)) }
            }
        }
    }

    func measurements() -> [String: Any] {
        lock.lock()
        defer { lock.unlock() }
        return ["audio_samples": samples, "audio_peak": Double(peak), "screen_frames": frames]
    }
}

@MainActor final class ClickView: NSView {
    var clicked = false
    override func mouseUp(with event: NSEvent) { clicked = true }
}

@main struct PiNativePreflight {
    @MainActor static func execute() async {
        let session = CGSessionCopyCurrentDictionary() as? [String: Any] ?? [:]
        let onConsole = session["kCGSSessionOnConsoleKey"] as? Bool ?? false
        let locked = session["CGSSessionScreenIsLocked"] as? Bool ?? false
        var output = AudioDeviceID(0)
        var size = UInt32(MemoryLayout<AudioDeviceID>.size)
        var address = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDefaultOutputDevice,
                                                mScope: kAudioObjectPropertyScopeGlobal,
                                                mElement: kAudioObjectPropertyElementMain)
        let audioStatus = AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &output)
        let screenPermission = CGPreflightScreenCaptureAccess()
        let inputPermission = AXIsProcessTrusted() && CGPreflightPostEventAccess()
        var checks: [String: Bool] = [
            "interactive_gui": onConsole && !locked && NSScreen.main != nil,
            "two_windows": false, "foreground_observer": false, "input_observer": false,
            "audio_endpoint": audioStatus == noErr && output != kAudioObjectUnknown,
            "audio_capture": false, "known_cue_observed": false,
        ]
        var measurements: [String: Any] = ["screen_permission": screenPermission, "input_permission": inputPermission,
                                            "audio_status": audioStatus, "on_console": onConsole, "screen_locked": locked]
        // Refuse before fixtures/capture when this user session lacks permission.
        if checks["interactive_gui"] == true && screenPermission && inputPermission && checks["audio_endpoint"] == true {
            let views = [ClickView(), ClickView()]
            let windows = (0..<2).map { index -> NSWindow in
                let window = NSWindow(contentRect: NSRect(x: 120 + index * 440, y: 180, width: 400, height: 220),
                                      styleMask: [.titled, .closable], backing: .buffered, defer: false)
                window.title = "TEST Pi observer " + String(index)
                window.contentView = views[index]
                window.isReleasedWhenClosed = false
                window.makeKeyAndOrderFront(nil)
                return window
            }
            defer { windows.forEach { $0.close() } }
            NSApp.activate(ignoringOtherApps: true)
            try? await Task.sleep(nanoseconds: 200_000_000)
            checks["two_windows"] = windows[0].windowNumber != windows[1].windowNumber && windows.allSatisfy { $0.isVisible }
            windows[0].makeKeyAndOrderFront(nil)
            try? await Task.sleep(nanoseconds: 200_000_000)
            let activePID = NSWorkspace.shared.frontmostApplication?.processIdentifier
            checks["foreground_observer"] = activePID == ProcessInfo.processInfo.processIdentifier && NSApp.keyWindow === windows[0]
            if let screen = NSScreen.main {
                let point = CGPoint(x: windows[0].frame.midX, y: screen.frame.maxY - windows[0].frame.midY)
                CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)
                CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)
                try? await Task.sleep(nanoseconds: 200_000_000)
                checks["input_observer"] = views[0].clicked && !views[1].clicked
            }
            do {
                let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
                guard let display = content.displays.first else { throw NSError(domain: "TEST", code: 1) }
                let observer = Capture()
                let config = SCStreamConfiguration()
                config.width = 640
                config.height = 400
                config.minimumFrameInterval = CMTime(value: 1, timescale: 15)
                config.capturesAudio = true
                config.excludesCurrentProcessAudio = false
                config.sampleRate = 48000
                config.channelCount = 2
                let filter = SCContentFilter(display: display, excludingWindows: [])
                let stream = SCStream(filter: filter, configuration: config, delegate: observer)
                let queue = DispatchQueue(label: "TEST.pi.observer.capture")
                try stream.addStreamOutput(observer, type: .screen, sampleHandlerQueue: queue)
                try stream.addStreamOutput(observer, type: .audio, sampleHandlerQueue: queue)
                try await stream.startCapture()
                try await Task.sleep(nanoseconds: 250_000_000)
                let sound = NSSound(contentsOfFile: CommandLine.arguments[1], byReference: true)
                let started = sound?.play() ?? false
                try await Task.sleep(nanoseconds: 1_500_000_000)
                try await stream.stopCapture()
                let observed = observer.measurements()
                measurements.merge(observed) { _, new in new }
                checks["audio_capture"] = (observed["audio_samples"] as? Int ?? 0) >= 4800 && (observed["screen_frames"] as? Int ?? 0) > 0
                checks["known_cue_observed"] = started && (observed["audio_peak"] as? Double ?? 0) > 0.01
            } catch {
                measurements["capture_error_code"] = (error as NSError).code
            }
        }
        let data = try! JSONSerialization.data(withJSONObject: ["checks": checks, "measurements": measurements], options: [.sortedKeys])
        FileHandle.standardOutput.write(data)
        FileHandle.standardOutput.write(Data([10]))
        NSApp.terminate(nil)
    }

    @MainActor static func main() {
        guard CommandLine.arguments.count == 2 else { exit(2) }
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        Task { @MainActor in await execute() }
        app.run()
    }
}
