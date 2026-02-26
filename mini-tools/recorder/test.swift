import AVFoundation
import Foundation

// Headless test: record 2 seconds at native format (no converter)
let wavPath = "/tmp/mini-test-recording.wav"

let engine = AVAudioEngine()
let inputNode = engine.inputNode
let nativeFormat = inputNode.outputFormat(forBus: 0)
print("native: \(nativeFormat)")

let fileSettings: [String: Any] = [
    AVFormatIDKey: kAudioFormatLinearPCM,
    AVSampleRateKey: nativeFormat.sampleRate,
    AVNumberOfChannelsKey: 1,
    AVLinearPCMBitDepthKey: 16,
    AVLinearPCMIsFloatKey: false,
]
let audioFile = try! AVAudioFile(forWriting: URL(fileURLWithPath: wavPath), settings: fileSettings)

var tapCount = 0
inputNode.installTap(onBus: 0, bufferSize: 4096, format: nativeFormat) { buffer, _ in
    tapCount += 1
    do {
        try audioFile.write(from: buffer)
    } catch {
        print("write error: \(error)")
    }
}

engine.prepare()
try! engine.start()
print("recording for 2 seconds at native \(nativeFormat.sampleRate)Hz...")

Thread.sleep(forTimeInterval: 2.0)

inputNode.removeTap(onBus: 0)
engine.stop()

let info = try! FileManager.default.attributesOfItem(atPath: wavPath)
let size = info[.size] as! UInt64
print("done: \(tapCount) taps, file size: \(size) bytes")
