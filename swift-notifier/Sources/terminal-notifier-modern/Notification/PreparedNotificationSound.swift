import AVFoundation
import Foundation
import CryptoKit

// Prepares complete, reusable resources before handing a basename to macOS.
enum PreparedNotificationSound {
    enum PreparationError: Error {
        case invalidVolume
        case invalidSource
        case invalidAudio
        case unsupportedDuration
        case invalidDestination
    }

    static let maximumSourceBytes = 32 * 1024 * 1024

    // A failure throws and removes this attempt's output; it never selects a
    // default sound. The destination is caller-owned, outside the signed bundle.
    static func prepare(source: URL, volume: Double, destinationDirectory: URL) throws -> URL {
        guard volume.isFinite, (0...1).contains(volume) else {
            throw PreparationError.invalidVolume
        }
        let sourceValues = try source.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey, .fileSizeKey])
        guard source.isFileURL, sourceValues.isRegularFile == true,
              sourceValues.isSymbolicLink != true,
              let size = sourceValues.fileSize, size > 0, size <= maximumSourceBytes else {
            throw PreparationError.invalidSource
        }
        try FileManager.default.createDirectory(at: destinationDirectory, withIntermediateDirectories: true)
        let destinationValues = try destinationDirectory.resourceValues(forKeys: [.isDirectoryKey, .isSymbolicLinkKey])
        guard destinationDirectory.isFileURL, destinationValues.isDirectory == true,
              destinationValues.isSymbolicLink != true else {
            throw PreparationError.invalidDestination
        }
        // Decode exactly the bytes used to derive the cache name, even if the
        // source changes while preparation is in progress.
        let handle = try FileHandle(forReadingFrom: source)
        defer { try? handle.close() }
        let data = handle.readData(ofLength: maximumSourceBytes + 1)
        guard !data.isEmpty, data.count <= maximumSourceBytes else { throw PreparationError.invalidSource }
        var hash = SHA256()
        hash.update(data: data)
        hash.update(data: Data("pcm16-v1:\(volume == 0 ? 0 : volume.bitPattern)".utf8))
        let digest = hash.finalize().map { String(format: "%02x", $0) }.joined()
        let outputURL = destinationDirectory.appendingPathComponent("agent-notifications-\(digest).caf")
        if FileManager.default.fileExists(atPath: outputURL.path) {
            try validateCached(outputURL)
            return outputURL
        }
        let snapshot = destinationDirectory.appendingPathComponent(".input-\(UUID().uuidString).\(source.pathExtension)")
        try data.write(to: snapshot, options: .atomic)
        defer { try? FileManager.default.removeItem(at: snapshot) }
        let input = try AVAudioFile(forReading: snapshot, commonFormat: .pcmFormatFloat32, interleaved: false)
        let format = input.processingFormat
        guard format.sampleRate.isFinite, format.sampleRate > 0,
              format.channelCount > 0, format.channelCount <= 32 else {
            throw PreparationError.invalidAudio
        }
        guard input.length > 0, Double(input.length) / format.sampleRate < 30 else {
            throw PreparationError.unsupportedDuration
        }
        guard let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: 4096) else {
            throw PreparationError.invalidAudio
        }
        let stagingURL = destinationDirectory.appendingPathComponent(".preparing-\(UUID().uuidString).caf")
        var succeeded = false
        defer { if !succeeded { try? FileManager.default.removeItem(at: stagingURL) } }
        // The scope closes AVAudioFile before returning its URL to the caller.
        try write(input: input, buffer: buffer, outputURL: stagingURL, volume: Float(volume))
        try validateCached(stagingURL)
        do { try FileManager.default.moveItem(at: stagingURL, to: outputURL) }
        catch {
            // A concurrent preparation may have published the same content.
            guard FileManager.default.fileExists(atPath: outputURL.path) else { throw error }
            try validateCached(outputURL)
            try FileManager.default.removeItem(at: stagingURL)
        }
        succeeded = true
        return outputURL
    }

    private static func validateCached(_ url: URL) throws {
        let values = try url.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
        guard values.isRegularFile == true, values.isSymbolicLink != true else { throw PreparationError.invalidAudio }
        let file = try AVAudioFile(forReading: url)
        let description = file.fileFormat.streamDescription.pointee
        guard description.mFormatID == kAudioFormatLinearPCM, description.mBitsPerChannel == 16,
              description.mFormatFlags & kAudioFormatFlagIsFloat == 0,
              file.length > 0, file.processingFormat.sampleRate > 0,
              Double(file.length) / file.processingFormat.sampleRate < 30 else {
            throw PreparationError.invalidAudio
        }
    }

    private static func write(input: AVAudioFile, buffer: AVAudioPCMBuffer, outputURL: URL, volume: Float) throws {
        let format = input.processingFormat
        let output = try AVAudioFile(forWriting: outputURL, settings: [
            AVFormatIDKey: kAudioFormatLinearPCM,
            AVSampleRateKey: format.sampleRate,
            AVNumberOfChannelsKey: format.channelCount,
            AVLinearPCMBitDepthKey: 16,
            AVLinearPCMIsFloatKey: false,
            AVLinearPCMIsBigEndianKey: false,
            AVLinearPCMIsNonInterleaved: false,
        ], commonFormat: .pcmFormatFloat32, interleaved: false)
        var frames: AVAudioFramePosition = 0
        while frames < input.length {
            let count = AVAudioFrameCount(min(AVAudioFramePosition(buffer.frameCapacity), input.length - frames))
            try input.read(into: buffer, frameCount: count)
            guard buffer.frameLength > 0 else { throw PreparationError.invalidAudio }
            frames += AVAudioFramePosition(buffer.frameLength)
            guard Double(frames) / format.sampleRate < 30 else {
                throw PreparationError.unsupportedDuration
            }
            guard let channels = buffer.floatChannelData else { throw PreparationError.invalidAudio }
            for channel in 0..<Int(format.channelCount) {
                for frame in 0..<Int(buffer.frameLength) {
                    let sample = channels[channel][frame]
                    guard sample.isFinite else { throw PreparationError.invalidAudio }
                    channels[channel][frame] = sample * volume
                }
            }
            try output.write(from: buffer)
        }
        guard frames > 0, frames == input.length else { throw PreparationError.invalidAudio }
    }
}
