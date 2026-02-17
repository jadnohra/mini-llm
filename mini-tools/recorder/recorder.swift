#!/usr/bin/env swift
// mini-tools/recorder — Native macOS voice recorder for mini stt
// Compile: swiftc -O -o recorder recorder.swift -framework AVFoundation -framework AppKit
// Usage:   ./recorder [--position center|mouse] [--output /tmp/out.mp3]
// Returns: Prints "done", "restart", or "cancel" to stdout

import AVFoundation
import AppKit

// ── Audio Recorder ──────────────────────────────────────

class AudioRecorder: NSObject {
    private var engine = AVAudioEngine()
    private var audioFile: AVAudioFile?
    private var levelUpdateTimer: Timer?
    var onLevel: ((Float) -> Void)?
    var outputPath: String

    init(outputPath: String) {
        self.outputPath = outputPath
        super.init()
    }

    func start() throws {
        let url = URL(fileURLWithPath: outputPath)
        let inputNode = engine.inputNode
        let format = inputNode.outputFormat(forBus: 0)

        // Write as WAV (lossless, Whisper-friendly), convert to MP3 later if needed
        let settings: [String: Any] = [
            AVFormatIDKey: kAudioFormatLinearPCM,
            AVSampleRateKey: 16000.0,
            AVNumberOfChannelsKey: 1,
            AVLinearPCMBitDepthKey: 16,
            AVLinearPCMIsFloatKey: false,
        ]
        let outputFormat = AVAudioFormat(settings: settings)!
        let converter = AVAudioConverter(from: format, to: outputFormat)!

        audioFile = try AVAudioFile(forWriting: url, settings: settings)

        inputNode.installTap(onBus: 0, bufferSize: 4096, format: format) { [weak self] buffer, _ in
            guard let self = self, let file = self.audioFile else { return }

            // Calculate level from input buffer
            let channelData = buffer.floatChannelData?[0]
            let frameLength = Int(buffer.frameLength)
            var rms: Float = 0
            if let data = channelData {
                for i in 0..<frameLength {
                    rms += data[i] * data[i]
                }
                rms = sqrtf(rms / Float(frameLength))
            }
            DispatchQueue.main.async {
                self.onLevel?(rms)
            }

            // Convert and write
            let frameCount = AVAudioFrameCount(
                Double(buffer.frameLength) * outputFormat.sampleRate / format.sampleRate
            )
            guard frameCount > 0 else { return }
            let convertedBuffer = AVAudioPCMBuffer(pcmFormat: outputFormat, frameCapacity: frameCount)!
            var error: NSError?
            converter.convert(to: convertedBuffer, error: &error) { _, outStatus in
                outStatus.pointee = .haveData
                return buffer
            }
            do {
                try file.write(from: convertedBuffer)
            } catch {}
        }

        engine.prepare()
        try engine.start()
    }

    func stop() {
        engine.inputNode.removeTap(onBus: 0)
        engine.stop()
        audioFile = nil
    }
}

// ── Level Meter View ────────────────────────────────────

class LevelMeterView: NSView {
    var level: Float = 0 {
        didSet { needsDisplay = true }
    }

    // Keep recent levels for spectrum-like display
    private var levelHistory: [Float] = Array(repeating: 0, count: 32)
    private var historyIndex = 0

    func pushLevel(_ l: Float) {
        level = l
        levelHistory[historyIndex % levelHistory.count] = l
        historyIndex += 1
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        let bg = NSColor(calibratedWhite: 0.12, alpha: 1)
        bg.setFill()
        NSBezierPath.fill(bounds)

        let barCount = levelHistory.count
        let barWidth = bounds.width / CGFloat(barCount) - 1
        let maxHeight = bounds.height - 4

        for i in 0..<barCount {
            // Read from oldest to newest
            let idx = (historyIndex + i) % barCount
            let val = CGFloat(min(levelHistory[idx] * 8, 1.0)) // amplify for visibility

            let barHeight = max(2, val * maxHeight)
            let x = CGFloat(i) * (barWidth + 1) + 1
            let y = (bounds.height - barHeight) / 2

            let intensity = val
            let color: NSColor
            if intensity > 0.7 {
                color = NSColor(calibratedRed: 1.0, green: 0.3, blue: 0.2, alpha: 0.9)
            } else if intensity > 0.3 {
                color = NSColor(calibratedRed: 1.0, green: 0.75, blue: 0.2, alpha: 0.9)
            } else {
                color = NSColor(calibratedRed: 0.2, green: 0.8, blue: 0.4, alpha: 0.7)
            }
            color.setFill()

            let rect = NSRect(x: x, y: y, width: barWidth, height: barHeight)
            let path = NSBezierPath(roundedRect: rect, xRadius: 1, yRadius: 1)
            path.fill()
        }
    }
}

// ── Recording Dot View ──────────────────────────────────

class RecordingDotView: NSView {
    var isRecording = false {
        didSet { needsDisplay = true }
    }
    private var pulseAlpha: CGFloat = 1.0
    private var pulseTimer: Timer?

    func startPulsing() {
        isRecording = true
        pulseTimer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in
            guard let self = self else { return }
            self.pulseAlpha = 0.4 + 0.6 * CGFloat(0.5 + 0.5 * sin(Date().timeIntervalSince1970 * 4))
            self.needsDisplay = true
        }
    }

    func stopPulsing() {
        isRecording = false
        pulseTimer?.invalidate()
        pulseTimer = nil
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        if isRecording {
            let color = NSColor(calibratedRed: 1.0, green: 0.15, blue: 0.15, alpha: pulseAlpha)
            color.setFill()
        } else {
            NSColor.gray.setFill()
        }
        let size: CGFloat = 10
        let dot = NSRect(
            x: (bounds.width - size) / 2,
            y: (bounds.height - size) / 2,
            width: size,
            height: size
        )
        NSBezierPath(ovalIn: dot).fill()
    }
}

// ── Main Window ─────────────────────────────────────────

class RecorderWindow: NSObject, NSWindowDelegate {
    let window: NSPanel
    let recorder: AudioRecorder
    let levelMeter: LevelMeterView
    let recordingDot: RecordingDotView
    let statusLabel: NSTextField
    var result = "cancel"

    init(outputPath: String, position: String) {
        let width: CGFloat = 320
        let height: CGFloat = 140

        // Create floating panel
        window = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: width, height: height),
            styleMask: [.titled, .closable, .nonactivatingPanel, .hudWindow],
            backing: .buffered,
            defer: false
        )
        window.title = "mini stt"
        window.level = .floating
        window.isReleasedWhenClosed = false
        window.titlebarAppearsTransparent = true

        recorder = AudioRecorder(outputPath: outputPath)
        levelMeter = LevelMeterView(frame: NSRect(x: 16, y: 50, width: width - 32, height: 36))
        recordingDot = RecordingDotView(frame: NSRect(x: 16, y: 100, width: 20, height: 20))

        statusLabel = NSTextField(labelWithString: "Recording...")
        statusLabel.frame = NSRect(x: 40, y: 98, width: 200, height: 22)
        statusLabel.font = NSFont.systemFont(ofSize: 14, weight: .medium)
        statusLabel.textColor = .white

        super.init()

        window.delegate = self

        // Position window
        if let screen = NSScreen.main {
            let screenFrame = screen.visibleFrame
            var origin: NSPoint
            if position == "mouse" {
                let mouse = NSEvent.mouseLocation
                origin = NSPoint(
                    x: min(mouse.x, screenFrame.maxX - width),
                    y: min(mouse.y, screenFrame.maxY - height)
                )
            } else {
                // Center of screen
                origin = NSPoint(
                    x: screenFrame.midX - width / 2,
                    y: screenFrame.midY - height / 2
                )
            }
            window.setFrameOrigin(origin)
        }

        // Buttons
        let doneBtn = NSButton(title: "Done", target: self, action: #selector(doneClicked))
        doneBtn.bezelStyle = .rounded
        doneBtn.keyEquivalent = "\r" // Enter key
        doneBtn.frame = NSRect(x: width - 86, y: 10, width: 70, height: 30)

        let restartBtn = NSButton(title: "Redo", target: self, action: #selector(restartClicked))
        restartBtn.bezelStyle = .rounded
        restartBtn.frame = NSRect(x: width - 160, y: 10, width: 70, height: 30)

        let cancelBtn = NSButton(title: "Cancel", target: self, action: #selector(cancelClicked))
        cancelBtn.bezelStyle = .rounded
        cancelBtn.keyEquivalent = "\u{1b}" // Escape key
        cancelBtn.frame = NSRect(x: 16, y: 10, width: 70, height: 30)

        let contentView = window.contentView!
        contentView.addSubview(levelMeter)
        contentView.addSubview(recordingDot)
        contentView.addSubview(statusLabel)
        contentView.addSubview(doneBtn)
        contentView.addSubview(restartBtn)
        contentView.addSubview(cancelBtn)

        // Wire up level meter
        recorder.onLevel = { [weak self] level in
            self?.levelMeter.pushLevel(level)
        }
    }

    func run() -> String {
        startRecording()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        NSApp.run()
        return result
    }

    func startRecording() {
        do {
            try recorder.start()
            recordingDot.startPulsing()
            statusLabel.stringValue = "Recording..."
        } catch {
            statusLabel.stringValue = "Mic error: \(error.localizedDescription)"
        }
    }

    func stopRecording() {
        recorder.stop()
        recordingDot.stopPulsing()
    }

    @objc func doneClicked() {
        result = "done"
        stopRecording()
        NSApp.stop(nil)
    }

    @objc func restartClicked() {
        stopRecording()
        // Small delay to ensure file handle is released
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) { [self] in
            startRecording()
        }
    }

    @objc func cancelClicked() {
        result = "cancel"
        stopRecording()
        NSApp.stop(nil)
    }

    func windowWillClose(_ notification: Notification) {
        result = "cancel"
        stopRecording()
        NSApp.stop(nil)
    }
}

// ── Main ────────────────────────────────────────────────

func parseArgs() -> (output: String, position: String) {
    var output = "/tmp/mini-stt-recording.wav"
    var position = "center"
    let args = CommandLine.arguments

    var i = 1
    while i < args.count {
        switch args[i] {
        case "--output", "-o":
            i += 1
            if i < args.count { output = args[i] }
        case "--position", "-p":
            i += 1
            if i < args.count { position = args[i] }
        default:
            break
        }
        i += 1
    }
    return (output, position)
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)

let config = parseArgs()
let recorderWindow = RecorderWindow(outputPath: config.output, position: config.position)
let action = recorderWindow.run()

// Print result to stdout for the Go CLI to read
print(action)
exit(action == "cancel" ? 1 : 0)
