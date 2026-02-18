#!/usr/bin/env swift
// mini-tools/recorder — Terminal-style voice recorder for mini stt
// Compile: swiftc -O -o recorder recorder.swift -framework AVFoundation -framework AppKit
// Usage:   ./recorder [--position center|mouse] [--output /tmp/out.m4a] [--stt-url URL]
// Returns: Prints transcribed text (if --stt-url) or "done"/"cancel" to stdout

import AVFoundation
import AppKit

// NSApp.stop() only sets a flag — the run loop won't check it until it
// receives a real event. Post a dummy event to wake it up immediately.
func appStop() {
    NSApp.stop(nil)
    let event = NSEvent.otherEvent(with: .applicationDefined, location: .zero,
        modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil,
        subtype: 0, data1: 0, data2: 0)
    if let event = event { NSApp.postEvent(event, atStart: true) }
}

// ── Colors ─────────────────────────────────────────────

struct Term {
    static let bg       = NSColor(srgbRed: 1.0, green: 0.98, blue: 0.94, alpha: 1.0) // warm cream
    static let fg       = NSColor(srgbRed: 0.15, green: 0.15, blue: 0.17, alpha: 1.0)
    static let dim      = NSColor(srgbRed: 0.55, green: 0.55, blue: 0.58, alpha: 1.0)
    static let font     = NSFont.monospacedSystemFont(ofSize: 13, weight: .regular)
    static let fontBold = NSFont.monospacedSystemFont(ofSize: 13, weight: .bold)
    static let fontSm   = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
    static let fontXs   = NSFont.monospacedSystemFont(ofSize: 9, weight: .regular)
}

// ── Audio Recorder ─────────────────────────────────────

class AudioRecorder: NSObject {
    private var engine = AVAudioEngine()
    private var audioFile: AVAudioFile?
    private let encodingQueue = DispatchQueue(label: "mini.audio-encode", qos: .userInitiated)
    var onLevel: ((Float) -> Void)?
    var outputPath: String

    init(outputPath: String) {
        self.outputPath = outputPath
        super.init()
    }

    private func copyBuffer(_ buffer: AVAudioPCMBuffer) -> AVAudioPCMBuffer? {
        guard let copy = AVAudioPCMBuffer(pcmFormat: buffer.format, frameCapacity: buffer.frameLength) else {
            return nil
        }
        copy.frameLength = buffer.frameLength
        if let src = buffer.floatChannelData?[0], let dst = copy.floatChannelData?[0] {
            memcpy(dst, src, Int(buffer.frameLength) * MemoryLayout<Float>.stride)
        }
        return copy
    }

    func start() throws {
        let url = URL(fileURLWithPath: outputPath)
        let inputNode = engine.inputNode
        let nativeFormat = inputNode.outputFormat(forBus: 0)

        let fileSettings: [String: Any] = [
            AVFormatIDKey: kAudioFormatMPEG4AAC,
            AVSampleRateKey: nativeFormat.sampleRate,
            AVNumberOfChannelsKey: 1,
            AVEncoderBitRateKey: 64000,
        ]
        audioFile = try AVAudioFile(forWriting: url, settings: fileSettings)

        inputNode.installTap(onBus: 0, bufferSize: 4096, format: nativeFormat) { [weak self] buffer, _ in
            guard let self = self else { return }

            // RMS — just math, real-time safe
            let channelData = buffer.floatChannelData?[0]
            let frameLength = Int(buffer.frameLength)
            var rms: Float = 0
            if let data = channelData {
                for i in 0..<frameLength { rms += data[i] * data[i] }
                rms = sqrtf(rms / Float(frameLength))
            }
            DispatchQueue.main.async { self.onLevel?(rms) }

            // Copy buffer and dispatch encoding off real-time thread
            guard let copy = self.copyBuffer(buffer) else { return }
            self.encodingQueue.async { [weak self] in
                guard let file = self?.audioFile else { return }
                do { try file.write(from: copy) } catch {}
            }
        }

        engine.prepare()
        try engine.start()
    }

    func stop() {
        engine.inputNode.removeTap(onBus: 0)
        engine.stop()
        // Flush all pending writes before closing
        encodingQueue.sync {
            audioFile = nil
        }
    }
}

// ── Waveform View (Core Graphics bars) ──────────────────

class WaveformView: NSView {
    private let barCount = 32
    private var levels: [Float]
    private var index = 0
    private var peak: Float = 0.005

    override init(frame: NSRect) {
        levels = Array(repeating: 0, count: 32)
        super.init(frame: frame)
        wantsLayer = true
        layer?.backgroundColor = NSColor.clear.cgColor
    }
    required init?(coder: NSCoder) { fatalError() }

    func pushLevel(_ l: Float) {
        if l > peak { peak = l }
        peak = max(peak * 0.993, 0.005)
        levels[index % barCount] = l
        index += 1
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }
        ctx.clear(bounds)

        let gap: CGFloat = 1.5
        let barW = (bounds.width - gap * CGFloat(barCount - 1)) / CGFloat(barCount)
        let maxH = bounds.height

        for i in 0..<barCount {
            let idx = (index + i) % barCount
            let raw = levels[idx] / peak
            let val = CGFloat(min(log10(1 + raw * 9), 1.0))
            let h = max(val * maxH, 2) // minimum 2px so bars are always visible
            let x = CGFloat(i) * (barW + gap)
            let y = (maxH - h) / 2 // center vertically

            let alpha = 0.35 + val * 0.65 // dim when quiet, bright when loud
            ctx.setFillColor(Term.fg.withAlphaComponent(alpha).cgColor)

            let rect = CGRect(x: x, y: y, width: barW, height: h)
            let path = CGPath(roundedRect: rect, cornerWidth: barW / 2, cornerHeight: barW / 2, transform: nil)
            ctx.addPath(path)
            ctx.fillPath()
        }
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
        dotLabel.textColor = Term.fg

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
            dotLabel.textColor = Term.fg
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
            self.dotLabel.textColor = on ? Term.fg : Term.fg.withAlphaComponent(0.3)
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

    var sttURL: String?
    var copyToClipboard = false
    var statusText = "Transcribing"

    @objc func doneClicked() {
        let tStart = DispatchTime.now()
        result = "done"
        stopRecording()
        let tStop = DispatchTime.now()
        fputs(String(format: "  stop recording: %dms\n", (tStop.uptimeNanoseconds - tStart.uptimeNanoseconds) / 1_000_000), stderr)

        if let url = sttURL {
            showTranscribing()

            DispatchQueue.global(qos: .userInitiated).async { [self] in
                let tQueued = DispatchTime.now()
                fputs(String(format: "  dispatch start: %dms\n", (tQueued.uptimeNanoseconds - tStop.uptimeNanoseconds) / 1_000_000), stderr)
                let t0 = DispatchTime.now()

                // Quick health check — only to update UI status
                let baseURL = url.replacingOccurrences(of: "/transcribe", with: "")
                let hc = Process()
                hc.executableURL = URL(fileURLWithPath: "/usr/bin/curl")
                hc.arguments = ["-sf", "--max-time", "1", baseURL + "/health"]
                let hcPipe = Pipe()
                hc.standardOutput = hcPipe
                hc.standardError = FileHandle.nullDevice
                if let _ = try? hc.run() {
                    hc.waitUntilExit()
                    let hcData = hcPipe.fileHandleForReading.readDataToEndOfFile()
                    if let status = String(data: hcData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines),
                       status == "idle" {
                        DispatchQueue.main.async { self.statusText = "Loading model" }
                    }
                }
                let t1 = DispatchTime.now()
                fputs(String(format: "  health check: %dms\n", (t1.uptimeNanoseconds - t0.uptimeNanoseconds) / 1_000_000), stderr)

                let t2 = DispatchTime.now()

                var text = ""
                var error: String?

                // Use curl — URLSession adds seconds of overhead on localhost HTTP
                let curl = Process()
                curl.executableURL = URL(fileURLWithPath: "/usr/bin/curl")
                curl.arguments = [
                    "-sf", "--max-time", "120",
                    "-H", "Content-Type: audio/m4a",
                    "-H", "X-Language: en",
                    "--data-binary", "@\(recorder.outputPath)",
                    url,
                ]
                let curlPipe = Pipe()
                curl.standardOutput = curlPipe
                curl.standardError = FileHandle.nullDevice
                do {
                    try curl.run()
                    curl.waitUntilExit()
                    if curl.terminationStatus == 0 {
                        let data = curlPipe.fileHandleForReading.readDataToEndOfFile()
                        text = String(data: data, encoding: .utf8)?
                            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    } else {
                        error = "curl failed (status \(curl.terminationStatus))"
                    }
                } catch let err {
                    error = err.localizedDescription
                }
                let t3 = DispatchTime.now()
                fputs(String(format: "  http post: %dms\n", (t3.uptimeNanoseconds - t2.uptimeNanoseconds) / 1_000_000), stderr)

                if let error = error {
                    fputs(String(format: "  error: %@ (total: %dms)\n", error, (DispatchTime.now().uptimeNanoseconds - tStart.uptimeNanoseconds) / 1_000_000), stderr)
                    DispatchQueue.main.async {
                        self.showError(error)
                        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { appStop() }
                    }
                    return
                }

                let t4 = DispatchTime.now()
                if !text.isEmpty {
                    print(text)
                }

                // Clipboard + stop on main thread to ensure pasteboard write completes
                let shouldCopy = self.copyToClipboard && !text.isEmpty
                let capturedText = text
                DispatchQueue.main.sync {
                    if shouldCopy {
                        let pb = NSPasteboard.general
                        pb.clearContents()
                        pb.setString(capturedText, forType: .string)
                    }
                }
                let t5 = DispatchTime.now()
                fputs(String(format: "  clipboard: %dms\n", (t5.uptimeNanoseconds - t4.uptimeNanoseconds) / 1_000_000), stderr)
                fputs(String(format: "  TOTAL: %dms\n", (t5.uptimeNanoseconds - tStart.uptimeNanoseconds) / 1_000_000), stderr)

                DispatchQueue.main.async { appStop() }
            }
        } else {
            appStop()
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
        let transLabel = NSTextField(labelWithString: "⠋ Transcribing")
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
        dotLabel.textColor = Term.fg
        hintLabel.stringValue = msg
    }

    @objc func cancelClicked() {
        result = "cancel"
        stopRecording()
        appStop()
    }

    func windowWillClose(_ notification: Notification) {
        result = "cancel"
        stopRecording()
        appStop()
    }
}

// ── Main ────────────────────────────────────────────────

struct RecorderConfig {
    var output = "/tmp/mini-stt-recording.m4a"
    var position = "center"
    var sttURL: String? = nil
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
        case "--stt-url":
            i += 1; if i < args.count { cfg.sttURL = args[i] }
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
recorderWindow.sttURL = config.sttURL
recorderWindow.copyToClipboard = config.copy
let action = recorderWindow.run()

print(action)
exit(action == "cancel" ? 1 : 0)
