import SwiftUI

struct ContentView: View {
    @State private var mediaDir: String = UserDefaults.standard.string(forKey: "mediaDir") ?? ""
    @State private var isRunning: Bool = false
    @State private var statusMessage: String = ""
    @State private var autoTick = Timer.publish(every: 5, on: .main, in: .common).autoconnect()
    @State private var serverProcess: Process? = nil
    @State private var startCheckActive: Bool = false

    private var baseDir: String {
        // Directory that holds the executable (Contents/MacOS)
        if let execDir = Bundle.main.executableURL?.deletingLastPathComponent().path {
            return execDir
        }
        // Fallback
        return Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS").path
    }
    private let serverName = "HomeCinemaServer"
    private let screenSession = "homecinema"
    private let serverPort = "8080"

    var body: some View {
        ZStack {
            LinearGradient(colors: [
                Color(red: 0.10, green: 0.14, blue: 0.25),
                Color(red: 0.04, green: 0.05, blue: 0.12),
                Color(red: 0.01, green: 0.02, blue: 0.06)
            ], startPoint: .topLeading, endPoint: .bottomTrailing)
                .ignoresSafeArea()
            VStack(spacing: 18) {
                header
                card
                controls
                if !statusMessage.isEmpty {
                    Text(statusMessage)
                        .font(.footnote)
                        .foregroundColor(.white.opacity(0.7))
                }
                Spacer()
            }
            .padding(24)
        }
        .onAppear { refreshStatus() }
        .onReceive(autoTick) { _ in
            refreshStatus()
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Home Cinema Control")
                .font(.system(size: 26, weight: .bold, design: .rounded))
                .foregroundColor(.white)
            Text("DLNA control for your server")
                .foregroundColor(.white.opacity(0.7))
                .font(.subheadline)
        }
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Circle()
                    .fill(isRunning ? Color.green : Color.red)
                    .frame(width: 12, height: 12)
                Text(isRunning ? "Running" : "Stopped")
                    .foregroundColor(.white)
                    .fontWeight(.semibold)
                Spacer()
                Text("Port \(serverPort)")
                    .foregroundColor(.white.opacity(0.7))
                    .font(.caption)
            }
            Divider().background(Color.white.opacity(0.12))
            Text("Folder")
                .foregroundColor(.white.opacity(0.7))
                .font(.caption)
            Text(mediaDir.isEmpty ? "Not selected" : mediaDir)
                .foregroundColor(.white)
                .font(.callout)
                .lineLimit(2)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(18)
        .frame(maxWidth: .infinity)
        .background(.ultraThinMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .stroke(Color.white.opacity(0.12), lineWidth: 1)
        )
        .shadow(color: Color.black.opacity(0.35), radius: 18, x: 0, y: 12)
    }

    private var controls: some View {
        VStack(spacing: 12) {
            HStack(spacing: 12) {
                Button(action: toggleServer) {
                    label(icon: isRunning ? "square.fill" : "play.fill",
                          text: isRunning ? "Stop" : "Start",
                          color: isRunning ? .red : .green)
                }
                Button(action: pickFolder) {
                    label(icon: "folder.fill", text: "Folder", color: .blue)
                }
            }
            HStack(spacing: 12) {
                Button(action: refreshStatus) {
                    label(icon: "arrow.clockwise", text: "Refresh", color: .orange)
                }
                Button(action: openLogs) {
                    label(icon: "doc.text", text: "Logs", color: .purple)
                }
            }
        }
    }

    private func label(icon: String, text: String, color: Color) -> some View {
        HStack {
            Image(systemName: icon)
                .font(.system(size: 16, weight: .bold))
            Text(text)
                .fontWeight(.semibold)
        }
        .foregroundColor(.white)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity)
        .background(
            LinearGradient(colors: [color.opacity(0.95), color.opacity(0.7)],
                           startPoint: .topLeading, endPoint: .bottomTrailing)
        )
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
        .shadow(color: color.opacity(0.35), radius: 12, x: 0, y: 10)
    }

    // MARK: - Actions
    private func toggleServer() {
        if isRunning {
            stopServerAsync()
        } else {
            if mediaDir.isEmpty { pickFolder() }
            if !mediaDir.isEmpty { startServerAsync() }
        }
    }

    private func startServerAsync() {
        guard FileManager.default.fileExists(atPath: "\(baseDir)/\(serverName)") else {
            statusMessage = "Binary not found in \(baseDir)"
            return
        }
        guard !mediaDir.isEmpty else {
            statusMessage = "Select folder first"
            return
        }
        statusMessage = "Starting server..."
        startCheckActive = true
        DispatchQueue.global(qos: .userInitiated).async {
            let serverPath = "\(self.baseDir)/\(self.serverName)"
            let logPath = "/tmp/homecinema.log"
            let fallbackLog = "/tmp/homecinema-ui.log"

            for path in [logPath, fallbackLog] {
                if !FileManager.default.fileExists(atPath: path) {
                    FileManager.default.createFile(atPath: path, contents: nil)
                }
            }

            // Launch via bash to mimic manual run (most reliable so far)
            let cmd = """
            cd "\(self.baseDir)" && \
            /bin/chmod +x "\(serverPath)" && \
            /usr/bin/nohup "\(serverPath)" --media-dir "\(self.mediaDir)" >> "\(logPath)" 2>&1 & echo $!
            """
            let result = self.runShell(cmd)

            DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                if result.exitCode == 0 {
                    let pid = result.output.trimmingCharacters(in: .whitespacesAndNewlines)
                    self.statusMessage = pid.isEmpty ? "Running" : "Running (pid \(pid))"
                    self.isRunning = true
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                        self.verifyRunningAfterStart()
                    }
                } else {
                    self.statusMessage = "Start failed: \(result.output)"
                    self.isRunning = false
                    self.startCheckActive = false
                }
            }
        }
    }

    private func verifyRunningAfterStart() {
        if !startCheckActive { return }
        DispatchQueue.global(qos: .utility).async {
            let ok = self.runShell("/usr/bin/pgrep \(self.serverName)").exitCode == 0
            if !ok {
                let tail = self.runShell("tail -n 8 /tmp/homecinema.log || tail -n 8 /tmp/homecinema-ui.log").output.trimmingCharacters(in: .whitespacesAndNewlines)
                DispatchQueue.main.async {
                    self.isRunning = false
                    self.statusMessage = tail.isEmpty ? "Start failed (see /tmp/homecinema.log)" : "Start failed: \(tail)"
                    self.startCheckActive = false
                }
                return
            }
            DispatchQueue.main.async {
                self.startCheckActive = false
            }
        }
    }

    private func stopServerAsync() {
        statusMessage = "Stopping server..."
        startCheckActive = false
        DispatchQueue.global(qos: .userInitiated).async {
            self.serverProcess?.terminate()
            self.serverProcess = nil
            let cmd = "/usr/bin/killall \(self.serverName) || true"
            let res = self.runShell(cmd)
            DispatchQueue.main.async {
                self.statusMessage = res.exitCode == 0 ? "Stopped" : "Stop failed: \(res.output)"
                self.isRunning = false
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                    self.refreshStatus()
                }
            }
        }
    }

    private func refreshStatus() {
        DispatchQueue.global(qos: .utility).async {
            let cmd = "/usr/bin/pgrep \(self.serverName)"
            let ok = self.runShell(cmd).exitCode == 0
            DispatchQueue.main.async {
                self.isRunning = ok
                self.statusMessage = ok ? "Running" : "Stopped"
            }
        }
    }

    private func openLogs() {
        let logPath = "/tmp/homecinema.log"
        let fallbackLog = "/tmp/homecinema-ui.log"
        let path = FileManager.default.fileExists(atPath: logPath) ? logPath : fallbackLog
        _ = runShell("touch \"\(path)\"; open \"\(path)\"")
    }

    private func pickFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        if panel.runModal() == .OK, let url = panel.url {
            mediaDir = url.path
            UserDefaults.standard.set(mediaDir, forKey: "mediaDir")
            if isRunning { updateRunningMediaDir() }
        }
    }

    private func updateRunningMediaDir() {
        let cmd = "curl -s -X POST --data-urlencode mediaDir=\"\(mediaDir)\" http://127.0.0.1:\(serverPort)/set-media-dir >/dev/null"
        _ = runShell(cmd)
    }

    // MARK: - Shell
    @discardableResult
    private func runShell(_ command: String) -> (output: String, exitCode: Int32) {
        let task = Process()
        task.launchPath = "/bin/bash"
        task.arguments = ["-c", command]
        let pipe = Pipe()
        task.standardOutput = pipe
        task.standardError = pipe
        task.launch()
        task.waitUntilExit()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let output = String(data: data, encoding: .utf8) ?? ""
        return (output, task.terminationStatus)
    }
}

struct ContentView_Previews: PreviewProvider {
    static var previews: some View {
        ContentView()
    }
}
