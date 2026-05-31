import AppKit
import Foundation
import Observation

/// `ServerController` — единственная state-машина приложения.
/// Использует `@Observable` (macOS 14+): SwiftUI re-renders точечно по
/// прочитанным свойствам, без `@Published`-инвалидации всего ViewModel.
@Observable
@MainActor
final class ServerController {
    /// LiveSession — UI-сторонняя модель сессии «сейчас играет», парсит
    /// elapsedSeconds в готовый формат.
    struct LiveSession: Identifiable, Hashable {
        let id: UInt64
        let kind: String     // "direct" | "resume" | "tv"
        let title: String
        let client: String
        let device: String   // friendly name из User-Agent ("Samsung TV", "VLC", ...)
        let elapsedSeconds: Double
        let durationSeconds: Double

        var progressFraction: Double? {
            guard durationSeconds > 0, elapsedSeconds > 0 else { return nil }
            return min(1.0, max(0.0, elapsedSeconds / durationSeconds))
        }

        var timecode: String {
            Self.formatPlayback(seconds: elapsedSeconds)
        }

        var totalTimecode: String {
            guard durationSeconds > 0 else { return "" }
            return Self.formatPlayback(seconds: durationSeconds)
        }

        var kindLabel: String {
            switch kind {
            case "direct": return "DIRECT"
            case "resume": return "RESUME"
            case "tv":     return "TRANSCODE"
            default:       return kind.uppercased()
            }
        }

        static func formatPlayback(seconds: Double) -> String {
            guard seconds > 0 else { return "0:00" }
            let total = Int(seconds.rounded())
            let h = total / 3600
            let m = (total % 3600) / 60
            let s = total % 60
            if h > 0 { return String(format: "%d:%02d:%02d", h, m, s) }
            return String(format: "%d:%02d", m, s)
        }
    }

    struct ProgressItem: Identifiable, Hashable {
        let id: String
        let fileName: String
        let folderName: String
        let timecode: String       // «watched» — текущая позиция (h:mm:ss)
        let totalTimecode: String  // длительность фильма (h:mm:ss), пусто если неизвестна
        let updatedText: String
        let updatedAt: Date?
        let progressFraction: Double?
    }

    /// Snapshot стёртого прогресса для undo. Хранит сериализованный JSON,
    /// который запишется обратно в `progress.json` если пользователь нажмёт
    /// «отменить» в течение `undoWindow`.
    struct UndoSnapshot {
        let payload: Data
        let cleared: Int
        let expiresAt: Date
    }

    private struct StoredProgressEntry: Codable {
        let position: Int64?
        let size: Int64?
        let seconds: Double?
        let durationSeconds: Double?
        let updated: String?
    }

    // MARK: – Observable state

    var mediaDir: String
    var isRunning: Bool = false
    var isBusy: Bool = false
    var statusMessage: String = "Choose a folder."
    var endpoint: String = "http://127.0.0.1:8080"
    var startedAtText: String = "—"
    var mediaFolderName: String = "Not selected"
    var logPath: String = ""
    var progressCount: Int = 0
    var progressUpdatedText: String = "No saved progress"
    var progressItems: [ProgressItem] = []
    /// Сколько /video/ стримов сейчас активны. Подсвечивает «On Air»-индикатор
    /// в hero-блоке. Обновляется через /stats каждые 2 с.
    var activeStreams: Int = 0
    /// Список «сейчас играет» из /stats — каждая запись = открытый стрим.
    /// Используется для Now Playing-карточек в Liquid Glass UI.
    var liveSessions: [LiveSession] = []
    /// Snapshot для undo. Истечение — `expiresAt`; через `undoWindow` после
    /// сброса прогресса snapshot молча сбрасывается.
    var undoSnapshot: UndoSnapshot?

    let serverPort: String = "8080"
    let undoWindow: TimeInterval = 5

    private let mediaDirDefaultsKey = "mediaDir"
    private var undoExpiryTask: Task<Void, Never>?

    init() {
        let stored = UserDefaults.standard.string(forKey: mediaDirDefaultsKey) ?? ""
        let fallback = FileManager.default
            .homeDirectoryForCurrentUser
            .appendingPathComponent("Movies", isDirectory: true).path
        self.mediaDir = stored.isEmpty ? fallback : stored
        self.mediaFolderName = URL(fileURLWithPath: mediaDir).lastPathComponent
        self.logPath = stateDirectoryURL.appendingPathComponent("server.log").path
        ensureStateDirectory()
    }

    // MARK: – Paths

    var serverBinaryURL: URL {
        bundleMacOSDirectoryURL.appendingPathComponent("HomeCinemaServer", isDirectory: false)
    }
    var startScriptURL: URL { scriptsDirectoryURL.appendingPathComponent("start_server.sh") }
    var stopScriptURL: URL { scriptsDirectoryURL.appendingPathComponent("stop_server.sh") }
    var statusScriptURL: URL { scriptsDirectoryURL.appendingPathComponent("status_server.sh") }
    var progressFileURL: URL { stateDirectoryURL.appendingPathComponent("progress.json") }

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

    // MARK: – Public API

    func setMediaDir(_ newValue: String) {
        mediaDir = newValue
        mediaFolderName = URL(fileURLWithPath: newValue).lastPathComponent
        UserDefaults.standard.set(newValue, forKey: mediaDirDefaultsKey)
    }

    func refreshStatus() {
        Task { await refreshStatusNow() }
    }

    func startServer() {
        guard !mediaDir.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            statusMessage = "Choose a folder."
            return
        }
        ensureStateDirectory()
        isBusy = true
        statusMessage = "Starting..."

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
                statusMessage = "Streaming now."
            }
        }
    }

    func stopServer() {
        ensureStateDirectory()
        isBusy = true
        statusMessage = "Stopping..."

        Task {
            let result = await runHelper(at: stopScriptURL, arguments: [stateDirectoryURL.path])
            try? await Task.sleep(nanoseconds: 500_000_000)
            await refreshStatusNow()
            isBusy = false
            if result.exitCode == 0 {
                statusMessage = mediaDir.isEmpty ? "Choose a folder." : "Library selected."
            } else {
                statusMessage = result.combinedOutput.isEmpty ? "Stop failed." : "Stop failed: \(result.combinedOutput)"
            }
        }
    }

    func applyRunningMediaDirChange() {
        guard isRunning else {
            statusMessage = "Library selected."
            return
        }
        Task {
            let ok = await updateRunningMediaDir()
            statusMessage = ok ? "Library updated." : "Could not update the running server."
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

    /// Сбрасывает весь прогресс, но сохраняет snapshot — `undoLastReset()` в
    /// течение `undoWindow` секунд восстанавливает. Если сервер запущен, по
    /// сети идёт POST /reset-progress; в обоих режимах snapshot читается
    /// до сброса (из локального файла), чтобы undo был гарантирован.
    func resetProgress() {
        isBusy = true
        statusMessage = "Clearing progress..."

        Task {
            defer { isBusy = false }

            // Снимок ДО сброса — даже если сервер запущен, JSON-файл доступен.
            let snapshotData = (try? Data(contentsOf: progressFileURL)) ?? Data("{}".utf8)
            let priorEntries = loadStoredProgressEntries()

            do {
                let response: ActionResponsePayload
                if isRunning {
                    response = try await resetProgressNow()
                } else {
                    let cleared = try clearLocalProgress()
                    response = ActionResponsePayload(
                        status: "success",
                        message: cleared > 0
                            ? "Saved progress cleared for \(cleared) titles."
                            : "No saved progress found.",
                        cleared: cleared
                    )
                }
                await refreshStatusNow()

                if response.cleared > 0 {
                    armUndoSnapshot(.init(payload: snapshotData, cleared: response.cleared, expiresAt: Date().addingTimeInterval(undoWindow)))
                }

                statusMessage = response.message.isEmpty
                    ? (response.cleared > 0
                        ? "Saved progress cleared for \(response.cleared) titles."
                        : "No saved progress found.")
                    : response.message
                _ = priorEntries
            } catch {
                statusMessage = "Could not clear saved progress."
            }
        }
    }

    /// Восстанавливает прогресс из последнего snapshot'а, если он не истёк.
    /// Записывает JSON обратно в `progress.json` и тут же refreshStatus,
    /// чтобы UI догнал состояние. Сервер подхватит изменения автоматически
    /// (он держит свой кеш в памяти, но Load() на нём не вызвать удалённо;
    /// в режиме «server stopped» это работает идеально, а при работающем
    /// сервере undo действует на «холодное» состояние после рестарта).
    func undoLastReset() {
        guard let snapshot = undoSnapshot, Date() < snapshot.expiresAt else {
            undoSnapshot = nil
            return
        }
        do {
            try snapshot.payload.write(to: progressFileURL, options: .atomic)
            statusMessage = "Restored progress for \(snapshot.cleared) titles."
            undoSnapshot = nil
            undoExpiryTask?.cancel()
            applyLocalProgressSummary()
        } catch {
            statusMessage = "Could not restore progress."
        }
    }

    func deleteProgressItem(_ item: ProgressItem) {
        isBusy = true
        statusMessage = "Removing progress..."

        Task {
            defer { isBusy = false }

            do {
                if isRunning {
                    _ = try await deleteProgressNow(key: item.id)
                } else {
                    try deleteLocalProgressItem(key: item.id)
                }
                await refreshStatusNow()
                statusMessage = "Progress removed for \(item.fileName)."
            } catch {
                statusMessage = "Could not remove file progress."
            }
        }
    }

    /// Лёгкий poll: бьётся в /stats каждые 2 секунды только когда сервер
    /// запущен. Не вытягивает `refreshStatusNow` полностью (не запускает
    /// shell-скрипт `status_server.sh`), чтобы не нагружать систему.
    func pollLightStats() async {
        guard isRunning else { return }
        guard let url = URL(string: "http://127.0.0.1:\(serverPort)/stats") else { return }
        var request = URLRequest(url: url)
        request.timeoutInterval = 1.5
        do {
            let (data, _) = try await URLSession.shared.data(for: request)
            if let stats = try? JSONDecoder().decode(StatsPayload.self, from: data) {
                activeStreams = stats.activeStreams
                progressCount = stats.progressCount
                liveSessions = (stats.sessions ?? []).map { dto in
                    LiveSession(
                        id: dto.id,
                        kind: dto.kind,
                        title: dto.title,
                        client: dto.client,
                        device: dto.device ?? "Unknown device",
                        elapsedSeconds: dto.elapsedSeconds,
                        durationSeconds: dto.durationSeconds
                    )
                }
            }
        } catch {
            // Сеть упала — поллим дальше, на следующем такте пересоберёмся.
        }
    }

    // MARK: – Private helpers

    private func armUndoSnapshot(_ snapshot: UndoSnapshot) {
        undoSnapshot = snapshot
        undoExpiryTask?.cancel()
        let window = undoWindow
        undoExpiryTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(window * 1_000_000_000))
            await MainActor.run { [weak self] in
                guard let self else { return }
                if let snap = self.undoSnapshot, snap.expiresAt <= Date() {
                    self.undoSnapshot = nil
                }
            }
        }
    }

    private func refreshStatusNow() async {
        ensureStateDirectory()
        let helperResult = await runHelper(at: statusScriptURL, arguments: [stateDirectoryURL.path])
        let values = parseKeyValueOutput(helperResult.standardOutput)
        let running = values["running"] == "1"

        isRunning = running
        logPath = values["log_path"] ?? stateDirectoryURL.appendingPathComponent("server.log").path
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
                if let streams = status.activeStreams {
                    activeStreams = streams
                }
                applyLocalProgressSummary()
                if !isBusy {
                    statusMessage = "Streaming now."
                }
            } catch {
                startedAtText = "Starting..."
                applyLocalProgressSummary()
                if endpoint.isEmpty {
                    endpoint = "http://127.0.0.1:\(serverPort)"
                }
                if !isBusy {
                    statusMessage = "Finishing startup..."
                }
            }
        } else {
            endpoint = "http://127.0.0.1:\(serverPort)"
            startedAtText = "—"
            activeStreams = 0
            let currentName = URL(fileURLWithPath: mediaDir).lastPathComponent
            mediaFolderName = currentName.isEmpty ? "Not selected" : currentName
            applyLocalProgressSummary()
            if !isBusy {
                statusMessage = mediaDir.isEmpty ? "Choose a folder." : "Library selected."
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

    private func resetProgressNow() async throws -> ActionResponsePayload {
        let url = URL(string: "http://127.0.0.1:\(serverPort)/reset-progress")!
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 2
        let (data, _) = try await URLSession.shared.data(for: request)
        return try JSONDecoder().decode(ActionResponsePayload.self, from: data)
    }

    private func deleteProgressNow(key: String) async throws -> ActionResponsePayload {
        let url = URL(string: "http://127.0.0.1:\(serverPort)/delete-progress")!
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 2
        request.setValue("application/x-www-form-urlencoded; charset=utf-8", forHTTPHeaderField: "Content-Type")
        request.httpBody = "key=\(percentEncode(key))".data(using: .utf8)
        let (data, _) = try await URLSession.shared.data(for: request)
        return try JSONDecoder().decode(ActionResponsePayload.self, from: data)
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

    private func formatProgressUpdate(_ dateString: String?) -> String {
        guard let dateString, !dateString.isEmpty else {
            return "No saved progress"
        }
        return "Updated \(format(dateString: dateString))"
    }

    private func applyLocalProgressSummary() {
        let entries = loadStoredProgressEntries()
        let summary = localProgressSummary(from: entries)
        progressCount = summary.count
        progressUpdatedText = formatProgressUpdate(summary.lastUpdated)
        progressItems = localProgressItems(from: entries)
    }

    private func loadLocalProgressSummary() -> (count: Int, lastUpdated: String?) {
        localProgressSummary(from: loadStoredProgressEntries())
    }

    private func clearLocalProgress() throws -> Int {
        ensureStateDirectory()
        let summary = loadLocalProgressSummary()
        try Data("{}".utf8).write(to: progressFileURL, options: .atomic)
        progressCount = 0
        progressUpdatedText = "No saved progress"
        progressItems = []
        return summary.count
    }

    private func deleteLocalProgressItem(key: String) throws {
        var raw = loadStoredProgressEntries()
        guard !raw.isEmpty else {
            throw NSError(domain: "HomeCinema", code: 404)
        }
        guard raw.removeValue(forKey: key) != nil else {
            throw NSError(domain: "HomeCinema", code: 404)
        }
        let encoded = try JSONEncoder().encode(raw)
        try encoded.write(to: progressFileURL, options: [.atomic])
        applyLocalProgressSummary()
    }

    private func loadStoredProgressEntries() -> [String: StoredProgressEntry] {
        // Только READ. Раньше здесь app перезаписывал progress.json, выкидывая
        // записи для файлов, которых не нашёл под app.mediaDir. Если app.mediaDir
        // временно расходился с серверным (момент старта до синхронизации
        // через /, или первый запуск с дефолтной папкой), все «чужие» записи
        // прогресса затирались — пользователь видел пустой Recently Watched и
        // лишался накопленных позиций. Чистку отсутствующих файлов теперь
        // делает только сервер (см. progressStore.PruneMissingFiles).
        guard
            let data = try? Data(contentsOf: progressFileURL),
            let raw = try? JSONDecoder().decode([String: StoredProgressEntry].self, from: data)
        else {
            return [:]
        }
        return raw
    }

    private func localProgressSummary(from raw: [String: StoredProgressEntry]) -> (count: Int, lastUpdated: String?) {
        var lastUpdated: String?
        for value in raw.values {
            guard let updated = value.updated else { continue }
            if lastUpdated == nil || updated > lastUpdated! {
                lastUpdated = updated
            }
        }
        return (raw.count, lastUpdated)
    }

    private func localProgressItems(from raw: [String: StoredProgressEntry]) -> [ProgressItem] {
        raw
            .map { key, entry in
                let fileName = URL(fileURLWithPath: key).lastPathComponent
                let folderName = URL(fileURLWithPath: NSString(string: key).deletingLastPathComponent).lastPathComponent
                return (
                    item: ProgressItem(
                        id: key,
                        fileName: fileName.isEmpty ? key : fileName,
                        folderName: folderName.isEmpty ? mediaFolderName : folderName,
                        timecode: formatProgressTimecode(entry),
                        totalTimecode: formatTotalDuration(entry),
                        updatedText: formatProgressUpdate(entry.updated),
                        updatedAt: parseISODate(entry.updated),
                        progressFraction: computeProgressFraction(entry)
                    ),
                    updated: parseISODate(entry.updated)
                )
            }
            .sorted { lhs, rhs in
                let leftDate = lhs.updated ?? .distantPast
                let rightDate = rhs.updated ?? .distantPast
                if leftDate != rightDate {
                    return leftDate > rightDate
                }
                let fileOrder = lhs.item.fileName.localizedCaseInsensitiveCompare(rhs.item.fileName)
                if fileOrder != .orderedSame {
                    return fileOrder == .orderedAscending
                }
                return lhs.item.id < rhs.item.id
            }
            .map(\.item)
    }

    private func progressItemExists(forKey key: String) -> Bool {
        guard let url = progressItemURL(forKey: key) else { return true }
        return FileManager.default.fileExists(atPath: url.path)
    }

    private func progressItemURL(forKey key: String) -> URL? {
        if key.hasPrefix("/") {
            return URL(fileURLWithPath: key)
        }
        if mediaDir.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return nil
        }
        return URL(fileURLWithPath: mediaDir, isDirectory: true).appendingPathComponent(key)
    }

    private func formatTotalDuration(_ entry: StoredProgressEntry) -> String {
        guard let duration = entry.durationSeconds, duration > 0 else { return "" }
        return formatPlayback(seconds: duration)
    }

    private func formatProgressTimecode(_ entry: StoredProgressEntry) -> String {
        if let seconds = entry.seconds, seconds > 0 {
            return formatPlayback(seconds: seconds)
        }
        if
            let position = entry.position,
            let size = entry.size,
            let duration = entry.durationSeconds,
            size > 0,
            duration > 0
        {
            let seconds = duration * Double(position) / Double(size)
            return formatPlayback(seconds: seconds)
        }
        return ""
    }

    private func formatPlayback(seconds: Double) -> String {
        guard seconds > 0 else { return "" }
        let total = Int(seconds.rounded())
        let hours = total / 3600
        let minutes = (total % 3600) / 60
        let secs = total % 60
        if hours > 0 {
            return String(format: "%d:%02d:%02d", hours, minutes, secs)
        }
        return String(format: "%d:%02d", minutes, secs)
    }

    private func computeProgressFraction(_ entry: StoredProgressEntry) -> Double? {
        if let seconds = entry.seconds, let duration = entry.durationSeconds, duration > 0, seconds > 0 {
            return min(1.0, max(0.0, seconds / duration))
        }
        if let position = entry.position, let size = entry.size, size > 0, position > 0 {
            return min(1.0, max(0.0, Double(position) / Double(size)))
        }
        return nil
    }

    private func parseISODate(_ dateString: String?) -> Date? {
        guard let dateString, !dateString.isEmpty else { return nil }
        return ISO8601DateFormatter().date(from: dateString)
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
    let progressCount: Int
    let progressUpdatedAt: String?
    let activeStreams: Int?
}

struct StatsPayload: Decodable {
    let activeStreams: Int
    let progressCount: Int
    let version: String
    let startedAt: String
    let sessions: [SessionDTO]?
}

struct SessionDTO: Decodable {
    let id: UInt64
    let kind: String
    let title: String
    let client: String
    let device: String?  // optional для совместимости со старыми /stats без device
    let startedAt: String
    let elapsedSeconds: Double
    let durationSeconds: Double
}

struct ActionResponsePayload: Decodable {
    let status: String
    let message: String
    let cleared: Int
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
