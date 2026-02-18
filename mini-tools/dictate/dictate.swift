#!/usr/bin/env swift
// mini-tools/dictate — Menubar dictation app for any terminal
// Compile: swiftc -O -o dictate dictate.swift -framework AppKit -framework Carbon
// Usage:   mini dictate (or ./dictate [--stt-url http://localhost:PORT/transcribe])
// Hotkey:  Ctrl+Shift+M — records voice, transcribes on Mini, pastes into focused app
// Requires: Accessibility permissions (for simulating paste)

import AppKit
import Carbon

// ── Paste simulation ───────────────────────────────────

func simulatePaste() {
    usleep(100_000) // 100ms settle time
    let source = CGEventSource(stateID: .hidSystemState)
    let keyDown = CGEvent(keyboardEventSource: source, virtualKey: 0x09, keyDown: true)
    keyDown?.flags = .maskCommand
    keyDown?.post(tap: .cghidEventTap)
    let keyUp = CGEvent(keyboardEventSource: source, virtualKey: 0x09, keyDown: false)
    keyUp?.flags = .maskCommand
    keyUp?.post(tap: .cghidEventTap)
}

// ── Status HUD ─────────────────────────────────────────


// ── Find recorder binary ───────────────────────────────

func findRecorder() -> String? {
    let home = ProcessInfo.processInfo.environment["HOME"] ?? ""
    for path in [
        home + "/repos/jad/mini-llm/mini-tools/recorder/recorder",
        home + "/mini-llm/mini-tools/recorder/recorder",
    ] {
        if FileManager.default.isExecutableFile(atPath: path) {
            return path
        }
    }
    return nil
}

// ── App Delegate ───────────────────────────────────────

class DictateApp: NSObject, NSApplicationDelegate {
    var statusItem: NSStatusItem!
    let sttURL: String
    var isRunning = false
    var hotKeyRef: EventHotKeyRef?

    init(sttURL: String) {
        self.sttURL = sttURL
        super.init()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Menubar icon — click to dictate
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        updateIcon(state: "idle")

        if let button = statusItem.button {
            button.action = #selector(iconClicked(_:))
            button.target = self
            button.sendAction(on: [.leftMouseUp, .rightMouseUp])
        }

        registerHotKey()
        print("dictate: ready — Ctrl+Shift+M or click [m] in menubar")
    }

    func updateIcon(state: String) {
        guard let button = statusItem.button else { return }
        let color: NSColor
        let text: String
        switch state {
        case "recording":
            color = .red
            text = "m"
        case "transcribing":
            color = .orange
            text = "m"
        default:
            color = .labelColor
            text = "m"
        }
        button.attributedTitle = NSAttributedString(
            string: text,
            attributes: [
                .font: NSFont.monospacedSystemFont(ofSize: 12, weight: .bold),
                .foregroundColor: color,
            ]
        )
    }

    @objc func iconClicked(_ sender: NSStatusBarButton) {
        let event = NSApp.currentEvent
        if event?.type == .rightMouseUp {
            // Right-click: show quit menu
            let menu = NSMenu()
            menu.addItem(NSMenuItem(title: "Quit Dictate", action: #selector(quit), keyEquivalent: ""))
            statusItem.menu = menu
            statusItem.button?.performClick(nil)
            // Clear menu so next left-click goes to action
            DispatchQueue.main.async { self.statusItem.menu = nil }
        } else {
            dictate()
        }
    }

    func registerHotKey() {
        var eventType = EventTypeSpec(
            eventClass: OSType(kEventClassKeyboard),
            eventKind: UInt32(kEventHotKeyPressed)
        )
        let selfPtr = Unmanaged.passUnretained(self).toOpaque()

        InstallEventHandler(
            GetApplicationEventTarget(),
            { (_, _, userData) -> OSStatus in
                guard let userData = userData else { return OSStatus(eventNotHandledErr) }
                let app = Unmanaged<DictateApp>.fromOpaque(userData).takeUnretainedValue()
                DispatchQueue.main.async { app.dictate() }
                return noErr
            },
            1,
            &eventType,
            selfPtr,
            nil
        )

        // Ctrl+Shift+M
        let hotKeyID = EventHotKeyID(signature: OSType(0x4D494E49), id: 1)
        let status = RegisterEventHotKey(
            UInt32(kVK_ANSI_M),
            UInt32(controlKey | shiftKey),
            hotKeyID,
            GetApplicationEventTarget(),
            0,
            &hotKeyRef
        )
        if status != noErr {
            print("dictate: warning — failed to register hotkey (status \(status))")
        }
    }

    func dictate() {
        guard !isRunning else { return }
        isRunning = true
        updateIcon(state: "recording")
        let dt0 = DispatchTime.now()

        guard let recorderPath = findRecorder() else {
            fputs("dictate: recorder binary not found\n", stderr)
            isRunning = false
            updateIcon(state: "idle")
            return
        }

        let audioPath = "/tmp/mini-dictate-\(Int(Date().timeIntervalSince1970 * 1000)).m4a"

        DispatchQueue.global(qos: .userInitiated).async { [self] in
            defer {
                try? FileManager.default.removeItem(atPath: audioPath)
                DispatchQueue.main.async {
                    self.isRunning = false
                    self.updateIcon(state: "idle")
                }
            }

            // Step 1: Record only — no transcription, exits instantly on Enter
            let rec = Process()
            rec.executableURL = URL(fileURLWithPath: recorderPath)
            rec.arguments = ["--output", audioPath]
            rec.standardOutput = FileHandle.nullDevice
            rec.standardError = FileHandle.standardError

            do { try rec.run() } catch {
                fputs("dictate: failed to launch recorder: \(error)\n", stderr)
                return
            }
            rec.waitUntilExit()
            let dt1 = DispatchTime.now()
            fputs(String(format: "dictate: recorded: %dms (status %d)\n", (dt1.uptimeNanoseconds - dt0.uptimeNanoseconds) / 1_000_000, rec.terminationStatus), stderr)

            if rec.terminationStatus != 0 {
                fputs("dictate: cancelled\n", stderr)
                return
            }

            // Recorder exited — focus has returned to the previous app

            // Step 2: Transcribe via curl (fast, no URLSession overhead)
            DispatchQueue.main.async { self.updateIcon(state: "transcribing") }

            let curl = Process()
            curl.executableURL = URL(fileURLWithPath: "/usr/bin/curl")
            curl.arguments = [
                "-sf", "--max-time", "120",
                "-H", "Content-Type: audio/m4a",
                "-H", "X-Language: en",
                "--data-binary", "@\(audioPath)",
                sttURL,
            ]
            let curlPipe = Pipe()
            curl.standardOutput = curlPipe
            curl.standardError = FileHandle.nullDevice

            var text = ""
            do {
                try curl.run()
                curl.waitUntilExit()
                if curl.terminationStatus == 0 {
                    let data = curlPipe.fileHandleForReading.readDataToEndOfFile()
                    text = String(data: data, encoding: .utf8)?
                        .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                }
            } catch {
                fputs("dictate: curl failed: \(error)\n", stderr)
                return
            }
            let dt2 = DispatchTime.now()
            fputs(String(format: "dictate: transcribed: %dms \"%@\"\n", (dt2.uptimeNanoseconds - dt1.uptimeNanoseconds) / 1_000_000, text), stderr)

            if text.isEmpty {
                fputs("dictate: empty transcription\n", stderr)
                return
            }

            // Step 3: Clipboard + paste (focus is already on the terminal)
            let pb = NSPasteboard.general
            pb.clearContents()
            pb.setString(text, forType: .string)

            DispatchQueue.main.async {
                simulatePaste()
                let dt3 = DispatchTime.now()
                fputs(String(format: "dictate: TOTAL: %dms\n", (dt3.uptimeNanoseconds - dt0.uptimeNanoseconds) / 1_000_000), stderr)
            }
        }
    }

    @objc func quit() {
        NSApp.terminate(nil)
    }
}

// ── Single instance via named pipe ──────────────────────

let pipePath = "/tmp/mini-dictate.pipe"
let pidFile = "/tmp/mini-dictate.pid"

func existingPid() -> Int32? {
    guard let contents = try? String(contentsOfFile: pidFile, encoding: .utf8),
          let pid = Int32(contents.trimmingCharacters(in: .whitespacesAndNewlines)),
          kill(pid, 0) == 0 else {
        return nil
    }
    return pid
}

func sendToExisting(_ command: String) {
    // Write command to named pipe — the running instance reads it
    guard let fh = FileHandle(forWritingAtPath: pipePath) else {
        print("dictate: pipe not available")
        return
    }
    fh.write((command + "\n").data(using: .utf8)!)
    fh.closeFile()
    print("dictate: sent '\(command)' to running instance")
}

func createPipe() {
    unlink(pipePath)
    mkfifo(pipePath, 0o644)
}

func cleanup() {
    unlink(pipePath)
    unlink(pidFile)
}

func writePid() {
    try? "\(ProcessInfo.processInfo.processIdentifier)".write(
        toFile: pidFile, atomically: true, encoding: .utf8)
}

/// Listen on the named pipe for commands from other instances.
func startPipeListener(app: DictateApp) {
    DispatchQueue.global(qos: .utility).async {
        while true {
            // open() blocks until a writer connects
            let fd = open(pipePath, O_RDONLY)
            guard fd >= 0 else { break }
            let fh = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
            let data = fh.readDataToEndOfFile()
            fh.closeFile()

            guard let msg = String(data: data, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines),
                  !msg.isEmpty else { continue }

            print("dictate: received '\(msg)'")
            if msg == "dictate" {
                DispatchQueue.main.async { app.dictate() }
            } else if msg == "quit" {
                DispatchQueue.main.async { NSApp.terminate(nil) }
            }
        }
    }
}

// ── Main ────────────────────────────────────────────────

if existingPid() != nil {
    // Already running — send dictate command and exit
    sendToExisting("dictate")
    exit(0)
}

createPipe()
writePid()

signal(SIGINT) { _ in cleanup(); exit(0) }
signal(SIGTERM) { _ in cleanup(); exit(0) }

let app = NSApplication.shared
app.setActivationPolicy(.accessory)

var sttURL = ""
let args = CommandLine.arguments
for i in 1..<args.count {
    if args[i] == "--stt-url" && i + 1 < args.count {
        sttURL = args[i + 1]
    }
}

if sttURL.isEmpty {
    fputs("dictate: --stt-url is required\n", stderr)
    exit(1)
}

let delegate = DictateApp(sttURL: sttURL)
app.delegate = delegate
startPipeListener(app: delegate)
app.run()

cleanup()
