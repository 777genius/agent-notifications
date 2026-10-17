import AVFoundation
import XCTest
@testable import terminal_notifier_modern

final class PreparedNotificationSoundTests: XCTestCase {
    func testCacheReusesCompleteOutputAndSeparatesContentAndVolume() throws {
        try withDirectory { directory in
            let source = try fixture(in: directory)
            let destination = directory.appendingPathComponent("Library/Sounds")
            let first = try PreparedNotificationSound.prepare(source: source, volume: 0.25, destinationDirectory: destination)
            let firstData = try Data(contentsOf: first)
            let firstDate = try first.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate
            let second = try PreparedNotificationSound.prepare(source: source, volume: 0.25, destinationDirectory: destination)
            XCTAssertEqual(first, second)
            XCTAssertEqual(try Data(contentsOf: second), firstData)
            XCTAssertEqual(try second.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate, firstDate)
            let louder = try PreparedNotificationSound.prepare(source: source, volume: 1, destinationDirectory: destination)
            XCTAssertNotEqual(first, louder)
            _ = try fixture(in: directory, frames: 256)
            let changed = try PreparedNotificationSound.prepare(source: source, volume: 0.25, destinationDirectory: destination)
            XCTAssertNotEqual(first, changed)
            XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: destination.path).count, 3)
            try Data("corrupt cache".utf8).write(to: changed)
            XCTAssertThrowsError(try PreparedNotificationSound.prepare(source: source, volume: 0.25, destinationDirectory: destination))
        }
    }
    private func withDirectory(_ body: (URL) throws -> Void) throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
        defer { try? FileManager.default.removeItem(at: directory) }
        try body(directory)
    }

    private func fixture(in directory: URL, frames: AVAudioFrameCount = 128) throws -> URL {
        let url = directory.appendingPathComponent("source.caf")
        let format = try XCTUnwrap(AVAudioFormat(standardFormatWithSampleRate: 8000, channels: 2))
        let buffer = try XCTUnwrap(AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames))
        buffer.frameLength = frames
        let channels = try XCTUnwrap(buffer.floatChannelData)
        for frame in 0..<Int(frames) {
            channels[0][frame] = frame.isMultiple(of: 2) ? 0.5 : -0.5
            channels[1][frame] = frame.isMultiple(of: 2) ? -0.25 : 0.25
        }
        var settings = format.settings
        settings[AVLinearPCMIsNonInterleaved] = false
        let output = try AVAudioFile(forWriting: url, settings: settings)
        try output.write(from: buffer)
        return url
    }

    func testPackagedMP3PreparesAsSixteenBitCAF() throws {
        let repository = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let source = repository.appendingPathComponent("sounds/task-complete.mp3")
        try withDirectory { directory in
            let input = try AVAudioFile(forReading: source, commonFormat: .pcmFormatFloat32, interleaved: false)
            let prepared = try PreparedNotificationSound.prepare(source: source, volume: 0.25, destinationDirectory: directory)
            let output = try AVAudioFile(forReading: prepared)
            XCTAssertEqual(output.length, input.length)
            XCTAssertEqual(output.fileFormat.streamDescription.pointee.mBitsPerChannel, 16)
            XCTAssertEqual(output.fileFormat.streamDescription.pointee.mFormatID, kAudioFormatLinearPCM)
        }
    }

    // A regression in gain, channel handling, duration or the actual file format
    // changes decoded samples/frame count, independently of notification APIs.
    func testPreparedCAFContainsScaledPCMAndAllFrames() throws {
        try withDirectory { directory in
            let source = try fixture(in: directory, frames: 8193)
            for volume in [0.0, 0.25, 1.0] {
                let prepared = try PreparedNotificationSound.prepare(source: source, volume: volume, destinationDirectory: directory)
                let file = try AVAudioFile(forReading: prepared, commonFormat: .pcmFormatFloat32, interleaved: false)
                XCTAssertEqual(file.fileFormat.streamDescription.pointee.mFormatID, kAudioFormatLinearPCM)
                XCTAssertEqual(file.fileFormat.streamDescription.pointee.mBitsPerChannel, 16)
                XCTAssertEqual(file.fileFormat.streamDescription.pointee.mFormatFlags & kAudioFormatFlagIsFloat, 0)
                XCTAssertEqual(file.length, 8193)
                XCTAssertEqual(file.processingFormat.sampleRate, 8000)
                XCTAssertEqual(file.processingFormat.channelCount, 2)
                let buffer = try XCTUnwrap(AVAudioPCMBuffer(pcmFormat: file.processingFormat, frameCapacity: 8193))
                try file.read(into: buffer)
                XCTAssertEqual(buffer.frameLength, 8193)
                let channels = try XCTUnwrap(buffer.floatChannelData)
                for frame in 0..<8193 {
                    let sign: Float = frame.isMultiple(of: 2) ? 1 : -1
                    XCTAssertEqual(channels[0][frame], sign * 0.5 * Float(volume), accuracy: 1.0 / 32768)
                    XCTAssertEqual(channels[1][frame], -sign * 0.25 * Float(volume), accuracy: 1.0 / 32768)
                }
            }
        }
    }

    func testRejectsInvalidVolumeInputAndThirtySecondSoundWithoutOutput() throws {
        try withDirectory { directory in
            let source = try fixture(in: directory)
            for volume in [Double.nan, .infinity, -.infinity, -0.01, 1.01] {
                XCTAssertThrowsError(try PreparedNotificationSound.prepare(source: source, volume: volume, destinationDirectory: directory))
            }
            let malformed = directory.appendingPathComponent("invalid.caf")
            try Data("invalid audio".utf8).write(to: malformed)
            XCTAssertThrowsError(try PreparedNotificationSound.prepare(source: malformed, volume: 1, destinationDirectory: directory))
            let link = directory.appendingPathComponent("link.caf")
            try FileManager.default.createSymbolicLink(at: link, withDestinationURL: source)
            XCTAssertThrowsError(try PreparedNotificationSound.prepare(source: link, volume: 1, destinationDirectory: directory))
            let longSource = try fixture(in: directory, frames: 30 * 8000)
            XCTAssertThrowsError(try PreparedNotificationSound.prepare(source: longSource, volume: 1, destinationDirectory: directory))
            let outputs = try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
                .filter { $0.lastPathComponent.hasPrefix("agent-notifications-") || $0.lastPathComponent.hasPrefix(".preparing-") || $0.lastPathComponent.hasPrefix(".input-") }
            XCTAssertTrue(outputs.isEmpty)
        }
    }
}
