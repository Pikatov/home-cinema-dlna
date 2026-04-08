import AppKit
import Foundation

@MainActor
final class ServerController: ObservableObject {
    @Published var mediaDir: String
    @Published var isRunning: Bool = false
    @Published var isBusy: Bool = false
    @Published var statusMessage: String = "Choose a folder and start the server."
    @Published var endpoint: String = "http://127.0.0.1:8080"
    @Published var startedAtText: String = "Not running"
    @Published var mediaFolderName: String = "Not selected"
    @Published var logPath: String = ""

    let serverPort: String = "8080"
    private let mediaDirDefaultsKey = "mediaDir"

    init() {
        let stored = UserDefaults.standard.string(forKey: mediaDirDefaultsKey) ?? ""
        let fallback = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Movies", isDirectory: true).path
        self.mediaDir = stored.isEmpty ? fallback : stored
        self.mediaFolderName = URL(fileURLWithPath: mediaDir).lastPathComponent
        self.logPath = stateDirectoryURL.appendingPathComponent("server.log").path
        ensureStateDirectory()
    }

    var serverBinaryURL: URL {
        bundleMacOSDirectoryURL.appendingPathComponent("HomeCinemaServer", isDirectory: false)
    }

    var startScriptURL: URL {
        scriptsDirectoryURL.appendingPathComponent("start_server.sh", isDirectory: false)
    }

    var stopScriptURL: URL {
        scriptsDirectoryURL.appendingPathComponent("stop_server.sh", isDirectory: false)
    }

    var statusScriptURL: URL {
        scriptsDirectoryURL.appendingPathComponent("status_server.sh", isDirectory: false)
    }

    private var scriptsDirectoryURL: URL {
        Bundle.main.resourceURL!.appendingPathComponent("scripts", isDirectory: true)
    }

    private var bundleMacOSDirectoryURL: URL {
        Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS", isDirectory: true)
    }

    private var stateDirectoryURL: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return base.appendingPathComponent("HomeCinema", isDirectory: true)
    }

    func setMediaDir(_ newValue: String) {
        mediaDir = newValue
        mediaFolderName = URL(fileURLWithPath: newValue).lastPathComponent
        UserDefaults.standard.set(newValue, forKey: mediaDirDefaultsKey)
    }

    func refreshStatus() {
        Task {
            await refreshStatusNow()
        }
    }

    func startServer() {
        guard !mediaDir.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            statusMessage = "Choose a folder before starting the server."
            return
        }
        ensureStateDirectory()
        isBusy = true
        statusMessage = "Starting Home Cinema..."

        Task {
            let result = await runHelper(at: startScriptURL, arguments: [
                serverBinaryURL.path,
                mediaDir,
                serverPort,
                stateDirectoryURL.path
            ])
            if result.exitCode != 0 {
                isBusy = false
                isRunning = false
                statusMessage = result.combinedOutput.isEmpty ? "Start failed." : "Start failed: \(result.combinedOutput)"
                return
            }

            try? await Task.sleep(nanoseconds: 700_000_000)
            await refreshStatusNow()
            isBusy = false
            if isRunning {
                statusMessage = "Server is running."
            }
        }
    }

    func stopServer() {
        ensureStateDirectory()
        isBusy = true
        statusMessage = "Stopping Home Cinema..."

        Task {
            let result = await runHelper(at: stopScriptURL, arguments: [stateDirectoryURL.path])
            try? await Task.sleep(nanoseconds: 500_000_000)
            await refreshStatusNow()
            isBusy = false
            if result.exitCode == 0 {
                statusMessage = "Server stopped."
            } else {
                statusMessage = result.combinedOutput.isEmpty ? "Stop failed." : "Stop failed: \(result.combinedOutput)"
            }
        }
    }

    func applyRunningMediaDirChange() {
        guard isRunning else {
            statusMessage = "Folder updated."
            return
        }

        Task {
            let ok = await updateRunningMediaDir()
            statusMessage = ok ? "Folder updated while server is running." : "Could not update folder on the running server."
            await refreshStatusNow()
        }
    }

    func openLogs() {
        ensureStateDirectory()
        let target = stateDirectoryURL.appendingPathComponent("server.log")
        if !FileManager.default.fileExists(atPath: target.path) {
            FileManager.default.createFile(atPath: target.path, contents: nil)
        }
        NSWorkspace.shared.open(target)
    }

    private func refreshStatusNow() async {
        ensureStateDirectory()
        let helperResult = await runHelper(at: statusScriptURL, arguments: [stateDirectoryURL.path])
        let values = parseKeyValueOutput(helperResult.standardOutput)
        let running = values["running"] == "1"

        isRunning = running
        logPath = values["log_path"] ?? stateDirectoryURL.appendingPathComponent("server.log").path
        endpoint = "http://127.0.0.1:\(serverPort)"
        if let folder = values["media_dir"], !folder.isEmpty, mediaDir.isEmpty {
            setMediaDir(folder)
        }

        if running {
            do {
                let status = try await fetchServerStatus()
                endpoint = status.endpoint
                startedAtText = format(dateString: status.startedAt)
                mediaFolderName = status.mediaDirName
                if let current = status.mediaDir, !current.isEmpty {
                    setMediaDir(current)
                }
                if !isBusy {
                    statusMessage = "Server is running."
                }
            } catch {
                startedAtText = "Starting..."
                if !isBusy {
                    statusMessage = "Server process is running, waiting for HTTP status..."
                }
            }
        } else {
            startedAtText = "Not running"
            let currentName = URL(fileURLWithPath: mediaDir).lastPathComponent
            mediaFolderName = currentName.isEmpty ? "Not selected" : currentName
            if !isBusy {
                statusMessage = "Server is stopped."
            }
        }
    }

    private func updateRunningMediaDir() async -> Bool {
        guard let url = URL(string: "http://127.0.0.1:\(serverPort)/set-media-dir") else {
            return false
        }

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 2
        request.setValue("application/x-www-form-urlencoded; charset=utf-8", forHTTPHeaderField: "Content-Type")
        request.httpBody = "mediaDir=\(percentEncode(mediaDir))".data(using: .utf8)

        do {
            _ = try await URLSession.shared.data(for: request)
            return true
        } catch {
            return false
        }
    }

    private func fetchServerStatus() async throws -> ServerStatusPayload {
        let url = URL(string: "http://127.0.0.1:\(serverPort)/")!
        var request = URLRequest(url: url)
        request.timeoutInterval = 2
        let (data, _) = try await URLSession.shared.data(for: request)
        return try JSONDecoder().decode(ServerStatusPayload.self, from: data)
    }

    private func percentEncode(_ value: String) -> String {
        var allowed = CharacterSet.urlQueryAllowed
        allowed.remove(charactersIn: "&+=?")
        return value.addingPercentEncoding(withAllowedCharacters: allowed) ?? value
    }

    private func parseKeyValueOutput(_ output: String) -> [String: String] {
        var values: [String: String] = [:]
        for line in output.split(separator: "\n") {
            let parts = line.split(separator: "=", maxSplits: 1)
            guard parts.count == 2 else { continue }
            values[String(parts[0])] = String(parts[1])
        }
        return values
    }

    private func ensureStateDirectory() {
        try? FileManager.default.createDirectory(at: stateDirectoryURL, withIntermediateDirectories: true)
    }

    private func format(dateString: String) -> String {
        let parser = ISO8601DateFormatter()
        guard let date = parser.date(from: dateString) else {
            return dateString
        }
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        formatter.timeStyle = .short
        return formatter.string(from: date)
    }

    private func runHelper(at executableURL: URL, arguments: [String]) async -> HelperResult {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let process = Process()
                process.executableURL = executableURL
                process.arguments = arguments

                let stdout = Pipe()
                let stderr = Pipe()
                process.standardOutput = stdout
                process.standardError = stderr

                do {
                    try process.run()
                    process.waitUntilExit()
                } catch {
                    continuation.resume(returning: HelperResult(
                        standardOutput: "",
                        standardError: error.localizedDescription,
                        exitCode: 1
                    ))
                    return
                }

                let stdoutData = stdout.fileHandleForReading.readDataToEndOfFile()
                let stderrData = stderr.fileHandleForReading.readDataToEndOfFile()
                let stdoutText = String(data: stdoutData, encoding: .utf8) ?? ""
                let stderrText = String(data: stderrData, encoding: .utf8) ?? ""

                continuation.resume(returning: HelperResult(
                    standardOutput: stdoutText.trimmingCharacters(in: .whitespacesAndNewlines),
                    standardError: stderrText.trimmingCharacters(in: .whitespacesAndNewlines),
                    exitCode: process.terminationStatus
                ))
            }
        }
    }
}

struct ServerStatusPayload: Decodable {
    let name: String
    let mediaDir: String?
    let mediaDirName: String
    let endpoint: String
    let startedAt: String
}

struct HelperResult {
    let standardOutput: String
    let standardError: String
    let exitCode: Int32

    var combinedOutput: String {
        [standardOutput, standardError]
            .filter { !$0.isEmpty }
            .joined(separator: "\n")
    }
}
