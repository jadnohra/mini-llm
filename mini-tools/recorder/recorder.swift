#!/usr/bin/env swift
// mini-tools/recorder — Terminal-style voice recorder for mini stt
// Compile: swiftc -O -o recorder recorder.swift -framework AVFoundation -framework AppKit
// Usage:   ./recorder [--position center|mouse] [--output /tmp/out.wav]
// Returns: Prints "done" or "cancel" to stdout

import AVFoundation
import AppKit

// ── Colors ─────────────────────────────────────────────

struct Term {
    static let bg       = NSColor(srgbRed: 1.0, green: 0.98, blue: 0.94, alpha: 1.0) // warm cream
    static let fg       = NSColor(srgbRed: 0.15, green: 0.15, blue: 0.17, alpha: 1.0)
    static let dim      = NSColor(srgbRed: 0.55, green: 0.55, blue: 0.58, alpha: 1.0)
    static let green    = NSColor(srgbRed: 0.18, green: 0.60, blue: 0.35, alpha: 1.0)
    static let red      = NSColor(srgbRed: 0.88, green: 0.22, blue: 0.22, alpha: 1.0)
    static let amber    = NSColor(srgbRed: 0.80, green: 0.60, blue: 0.15, alpha: 1.0)
    static let brand    = NSColor(srgbRed: 0.40, green: 0.40, blue: 0.43, alpha: 1.0)
    static let font     = NSFont.monospacedSystemFont(ofSize: 13, weight: .regular)
    static let fontBold = NSFont.monospacedSystemFont(ofSize: 13, weight: .bold)
    static let fontSm   = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
    static let fontXs   = NSFont.monospacedSystemFont(ofSize: 9, weight: .regular)
}

// ── Audio Recorder ─────────────────────────────────────

class AudioRecorder: NSObject {
    private var engine = AVAudioEngine()
    private var audioFile: AVAudioFile?
    var onLevel: ((Float) -> Void)?
    var outputPath: String

    init(outputPath: String) {
        self.outputPath = outputPath
        super.init()
    }

    func start() throws {
        let url = URL(fileURLWithPath: outputPath)
        let inputNode = engine.inputNode
        let nativeFormat = inputNode.outputFormat(forBus: 0)

        let fileSettings: [String: Any] = [
            AVFormatIDKey: kAudioFormatLinearPCM,
            AVSampleRateKey: nativeFormat.sampleRate,
            AVNumberOfChannelsKey: 1,
            AVLinearPCMBitDepthKey: 16,
            AVLinearPCMIsFloatKey: false,
        ]
        audioFile = try AVAudioFile(forWriting: url, settings: fileSettings)

        inputNode.installTap(onBus: 0, bufferSize: 4096, format: nativeFormat) { [weak self] buffer, _ in
            guard let self = self, let file = self.audioFile else { return }

            let channelData = buffer.floatChannelData?[0]
            let frameLength = Int(buffer.frameLength)
            var rms: Float = 0
            if let data = channelData {
                for i in 0..<frameLength { rms += data[i] * data[i] }
                rms = sqrtf(rms / Float(frameLength))
            }
            DispatchQueue.main.async { self.onLevel?(rms) }

            do { try file.write(from: buffer) } catch {}
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

// ── Waveform View (terminal block characters) ──────────

class WaveformView: NSView {
    private let blocks: [Character] = [" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"]
    private var levels: [Float] = Array(repeating: 0, count: 36)
    private var index = 0
    private var peak: Float = 0.001 // adaptive peak for normalization

    func pushLevel(_ l: Float) {
        // Track peak with slow decay for adaptive normalization
        if l > peak { peak = l }
        peak = max(peak * 0.995, 0.001) // decay toward silence

        levels[index % levels.count] = l
        index += 1
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        Term.bg.setFill()
        NSBezierPath.fill(bounds)

        var waveform = ""
        for i in 0..<levels.count {
            let idx = (index + i) % levels.count
            // Normalize against adaptive peak, apply log scale for better dynamics
            let raw = levels[idx] / peak
            let val = min(log10(1 + raw * 9), 1.0) // log scale: quiet sounds more visible
            let blockIdx = Int(val * Float(blocks.count - 1))
            waveform.append(blocks[blockIdx])
        }

        let attrs: [NSAttributedString.Key: Any] = [
            .font: Term.font,
            .foregroundColor: Term.green,
        ]
        let str = NSAttributedString(string: waveform, attributes: attrs)
        str.draw(at: NSPoint(x: 0, y: (bounds.height - 16) / 2))
    }
}

// ── Key-accepting Panel ────────────────────────────────

class KeyPanel: NSPanel {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

// ── Main Window ────────────────────────────────────────

class RecorderWindow: NSObject, NSWindowDelegate {
    let window: NSPanel
    let recorder: AudioRecorder
    let waveform: WaveformView
    let dotLabel: NSTextField
    let hintLabel: NSTextField
    var result = "cancel"
    var pulseTimer: Timer?
    var elapsed: TimeInterval = 0
    var elapsedTimer: Timer?
    let timeLabel: NSTextField

    init(outputPath: String, position: String) {
        let width: CGFloat = 300
        let height: CGFloat = 56

        // Borderless floating window
        window = KeyPanel(
            contentRect: NSRect(x: 0, y: 0, width: width, height: height),
            styleMask: [.nonactivatingPanel, .hudWindow],
            backing: .buffered,
            defer: false
        )
        window.level = .floating
        window.isReleasedWhenClosed = false
        window.hidesOnDeactivate = false
        window.isOpaque = false
        window.backgroundColor = Term.bg

        // Round corners
        window.contentView?.wantsLayer = true
        window.contentView?.layer?.cornerRadius = 14
        window.contentView?.layer?.masksToBounds = true
        window.contentView?.layer?.backgroundColor = Term.bg.cgColor

        recorder = AudioRecorder(outputPath: outputPath)

        // Layout — recording:
        //     ●  00:03  ▁▂▃▅▇▅▃▂▁▂▃▅▆▅▃▂▁▂▃▅▆▅▃▁
        //            esc cancel · ⏎ done

        let pad: CGFloat = 12
        let row1Y: CGFloat = 30
        let row2Y: CGFloat = 8

        // Recording dot
        dotLabel = NSTextField(labelWithString: "●")
        dotLabel.frame = NSRect(x: pad, y: row1Y, width: 16, height: 16)
        dotLabel.font = Term.fontSm
        dotLabel.textColor = Term.red

        // Time
        timeLabel = NSTextField(labelWithString: "00:00")
        timeLabel.frame = NSRect(x: pad + 18, y: row1Y, width: 42, height: 16)
        timeLabel.font = Term.fontSm
        timeLabel.textColor = Term.dim

        // Waveform — fills remaining width
        let wfX = pad + 62
        waveform = WaveformView(frame: NSRect(x: wfX, y: row1Y - 2, width: width - wfX - pad, height: 20))

        // Keyboard hints
        hintLabel = NSTextField(labelWithString: "esc · ⏎ done")
        hintLabel.frame = NSRect(x: 0, y: row2Y, width: width, height: 14)
        hintLabel.font = Term.fontXs
        hintLabel.textColor = Term.dim
        hintLabel.alignment = .center

        super.init()

        window.delegate = self

        // Position
        if let screen = NSScreen.main {
            let sf = screen.visibleFrame
            let mouse = NSEvent.mouseLocation
            var origin: NSPoint
            if position == "center" {
                origin = NSPoint(
                    x: sf.midX - width / 2,
                    y: max(sf.minY, min(mouse.y + 30, sf.maxY - height))
                )
            } else {
                origin = NSPoint(
                    x: max(sf.minX, min(mouse.x - width / 2, sf.maxX - width)),
                    y: max(sf.minY, min(mouse.y + 30, sf.maxY - height))
                )
            }
            window.setFrameOrigin(origin)
        }

        window.hasShadow = true

        let cv = window.contentView!
        cv.addSubview(dotLabel)
        cv.addSubview(timeLabel)
        cv.addSubview(waveform)
        cv.addSubview(hintLabel)

        // Invisible buttons for key equivalents
        let doneBtn = NSButton(frame: .zero)
        doneBtn.isTransparent = true
        doneBtn.keyEquivalent = "\r"
        doneBtn.target = self
        doneBtn.action = #selector(doneClicked)
        cv.addSubview(doneBtn)

        let cancelBtn = NSButton(frame: .zero)
        cancelBtn.isTransparent = true
        cancelBtn.keyEquivalent = "\u{1b}"
        cancelBtn.target = self
        cancelBtn.action = #selector(cancelClicked)
        cv.addSubview(cancelBtn)

        recorder.onLevel = { [weak self] level in
            self?.waveform.pushLevel(level)
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
        elapsed = 0
        do {
            try recorder.start()
            startPulse()
            startTimer()
        } catch {
            dotLabel.stringValue = "✕ err"
            dotLabel.textColor = Term.red
            hintLabel.stringValue = error.localizedDescription
        }
    }

    func stopRecording() {
        recorder.stop()
        stopPulse()
        stopTimer()
    }

    func startPulse() {
        pulseTimer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { [weak self] _ in
            guard let self = self else { return }
            let on = Int(Date().timeIntervalSince1970 * 2) % 2 == 0
            self.dotLabel.textColor = on ? Term.red : Term.red.withAlphaComponent(0.3)
        }
    }

    func stopPulse() {
        pulseTimer?.invalidate()
        pulseTimer = nil
    }

    func startTimer() {
        elapsedTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            guard let self = self else { return }
            self.elapsed += 1
            let m = Int(self.elapsed) / 60
            let s = Int(self.elapsed) % 60
            self.timeLabel.stringValue = String(format: "%02d:%02d", m, s)
        }
    }

    func stopTimer() {
        elapsedTimer?.invalidate()
        elapsedTimer = nil
    }

    var miniPath: String?
    var copyToClipboard = false

    var statusText = "transcribing"

    @objc func doneClicked() {
        result = "done"
        stopRecording()

        if let mini = miniPath {
            // Transcribe in-place — keep window open
            showTranscribing()
            DispatchQueue.global(qos: .userInitiated).async { [self] in
                let stt = Process()
                stt.executableURL = URL(fileURLWithPath: mini)
                var args = ["stt", "--file", recorder.outputPath]
                if copyToClipboard { args.append("--copy") }
                stt.arguments = args
                let pipe = Pipe()
                stt.standardOutput = pipe
                // Read stderr to detect cold start status
                let errPipe = Pipe()
                stt.standardError = errPipe

                do { try stt.run() } catch {
                    DispatchQueue.main.async {
                        self.showError("stt failed")
                        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { NSApp.stop(nil) }
                    }
                    return
                }

                // Read stderr in a dedicated thread (readabilityHandler is unreliable with waitUntilExit)
                let errHandle = errPipe.fileHandleForReading
                DispatchQueue.global(qos: .utility).async { [weak self] in
                    while true {
                        let chunk = errHandle.availableData
                        if chunk.isEmpty { break } // EOF
                        guard let text = String(data: chunk, encoding: .utf8) else { continue }
                        if text.contains("server not running") {
                            DispatchQueue.main.async { self?.statusText = "loading model" }
                        } else if text.contains("transcribe:") {
                            DispatchQueue.main.async { self?.statusText = "transcribing" }
                        }
                    }
                }

                stt.waitUntilExit()

                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                let text = String(data: data, encoding: .utf8)?
                    .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

                // Print text to stdout for the caller
                if !text.isEmpty { print(text) }

                DispatchQueue.main.async { NSApp.stop(nil) }
            }
        } else {
            NSApp.stop(nil)
        }
    }

    private let spinnerFrames = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]

    func showTranscribing() {
        dotLabel.isHidden = true
        timeLabel.isHidden = true
        hintLabel.isHidden = true
        stopPulse()
        waveform.isHidden = true

        let w = window.frame.width
        let h = window.frame.height
        let transLabel = NSTextField(labelWithString: "⠋ transcribing")
        transLabel.frame = NSRect(x: 0, y: (h - 18) / 2, width: w, height: 18)
        transLabel.alignment = .center
        transLabel.font = Term.font
        transLabel.textColor = Term.dim
        window.contentView?.addSubview(transLabel)

        var tick = 0
        Timer.scheduledTimer(withTimeInterval: 0.1, repeats: true) { [weak self] _ in
            guard let self = self else { return }
            tick += 1
            let frame = self.spinnerFrames[tick % self.spinnerFrames.count]
            transLabel.stringValue = "\(frame) \(self.statusText)"
        }
    }

    func showError(_ msg: String) {
        dotLabel.stringValue = "✕ err"
        dotLabel.textColor = Term.red
        hintLabel.stringValue = msg
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

struct RecorderConfig {
    var output = "/tmp/mini-stt-recording.wav"
    var position = "center"
    var miniPath: String? = nil
    var copy = false
}

func parseArgs() -> RecorderConfig {
    var cfg = RecorderConfig()
    let args = CommandLine.arguments
    var i = 1
    while i < args.count {
        switch args[i] {
        case "--output", "-o":
            i += 1; if i < args.count { cfg.output = args[i] }
        case "--position", "-p":
            i += 1; if i < args.count { cfg.position = args[i] }
        case "--mini-path":
            i += 1; if i < args.count { cfg.miniPath = args[i] }
        case "--copy":
            cfg.copy = true
        default: break
        }
        i += 1
    }
    return cfg
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)

let config = parseArgs()
let recorderWindow = RecorderWindow(outputPath: config.output, position: config.position)
recorderWindow.miniPath = config.miniPath
recorderWindow.copyToClipboard = config.copy
let action = recorderWindow.run()

print(action)
exit(action == "cancel" ? 1 : 0)
