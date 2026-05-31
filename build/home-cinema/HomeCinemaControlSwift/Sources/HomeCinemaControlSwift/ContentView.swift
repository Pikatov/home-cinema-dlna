import AppKit
import SwiftUI

// MARK: - Design tokens

private enum Layout {
    static let pagePadding: CGFloat = 28
    static let sectionSpacing: CGFloat = 18
    static let panelPadding: CGFloat = 24
    static let panelCorner: CGFloat = 22
    static let cardCorner: CGFloat = 16
    static let tileCorner: CGFloat = 18
    static let dockHeight: CGFloat = 76
    static let pageMaxWidth: CGFloat = 960
}

// MARK: - ContentView

struct ContentView: View {
    @State private var controller = ServerController()
    @AppStorage("selectedTheme") private var selectedThemeRawValue: String = ThemePreference.auto.rawValue
    @Environment(\.colorScheme) private var systemColorScheme
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var heroPulse: Bool = false
    @State private var showResetConfirm: Bool = false
    @State private var tick: Int = 0  // секундный «таймер» для live-таймкодов

    private var theme: AppTheme {
        ThemePreference(rawValue: selectedThemeRawValue)?
            .resolvedTheme(for: systemColorScheme) ?? .light
    }

    private var themePreference: ThemePreference {
        ThemePreference(rawValue: selectedThemeRawValue) ?? .auto
    }

    var body: some View {
        ZStack(alignment: .bottom) {
            AmbientBackdrop(theme: theme, pulse: heroPulse)
                .ignoresSafeArea()

            ScrollView(showsIndicators: false) {
                VStack(alignment: .leading, spacing: Layout.sectionSpacing) {
                    PageHeader(theme: theme,
                               themePreference: $selectedThemeRawValue,
                               version: appVersionText)
                        .padding(.top, 4)

                    HeroPanel(controller: controller,
                              theme: theme,
                              heroPulse: heroPulse,
                              tick: tick)

                    if !controller.liveSessions.isEmpty {
                        ConnectedDevicesPanel(controller: controller, theme: theme, tick: tick)
                    } else if controller.isRunning {
                        IdleDevicesHint(theme: theme)
                    }

                    ProgressGrid(controller: controller, theme: theme)

                    // Bottom-padding под floating dock.
                    Color.clear.frame(height: Layout.dockHeight + 24)
                }
                .padding(.horizontal, Layout.pagePadding)
                .padding(.top, Layout.pagePadding)
                .frame(maxWidth: Layout.pageMaxWidth, alignment: .leading)
                .frame(maxWidth: .infinity)
            }
            .scrollContentBackground(.hidden)

            // Floating dock
            FloatingDock(controller: controller,
                         theme: theme,
                         onChooseFolder: pickFolder,
                         onToggle: toggleServer,
                         onReset: { showResetConfirm = true },
                         onOpenLogs: controller.openLogs)
                .padding(.horizontal, Layout.pagePadding)
                .padding(.bottom, 14)

            // Undo banner — поверх dock'а
            if let snapshot = controller.undoSnapshot {
                UndoBanner(snapshot: snapshot, theme: theme, onUndo: controller.undoLastReset)
                    .padding(.horizontal, Layout.pagePadding)
                    .padding(.bottom, Layout.dockHeight + 24)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .preferredColorScheme(themePreference == .auto ? nil : theme.colorScheme)
        .background(WindowChromeConfigurator(theme: theme))
        .animation(.spring(response: 0.45, dampingFraction: 0.85),
                   value: controller.undoSnapshot?.expiresAt)
        .alert("Reset all saved progress?", isPresented: $showResetConfirm) {
            Button("Cancel", role: .cancel) {}
            Button("Reset", role: .destructive) {
                controller.resetProgress()
            }
        } message: {
            Text("You'll have \(Int(controller.undoWindow)) seconds to undo from the banner.")
        }
        .task {
            controller.refreshStatus()
            schedulePulse()
        }
        .task(id: scenePhase) {
            // Polling-цикл живёт только пока окно активно (.active).
            // При .inactive/.background мы прекращаем POST'ы и анимации —
            // экономия энергии ноута.
            guard scenePhase == .active else { return }
            controller.refreshStatus()
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                if Task.isCancelled { return }
                tick &+= 1
                await controller.pollLightStats()
            }
        }
    }

    // MARK: – Actions

    private func toggleServer() {
        if controller.isRunning {
            controller.stopServer()
        } else {
            if controller.mediaDir.isEmpty {
                pickFolder()
            }
            if !controller.mediaDir.isEmpty {
                controller.startServer()
            }
        }
    }

    private func pickFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"

        if panel.runModal() == .OK, let url = panel.url {
            controller.setMediaDir(url.path)
            controller.applyRunningMediaDirChange()
        }
    }

    private func schedulePulse() {
        guard !reduceMotion else { return }
        withAnimation(.easeInOut(duration: 2.4).repeatForever(autoreverses: true)) {
            heroPulse = true
        }
    }

    private var appVersionText: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.8"
    }
}

// MARK: - Ambient backdrop

/// Радиальные градиенты + cream paper / film black. Liquid Glass читается
/// только над неравномерным фоном — иначе стекло выглядит как просто белый
/// прямоугольник. Грейн опустили (Metal-shader был бы overkill) — два
/// смещённых радиальных пятна дают достаточно «жизни».
private struct AmbientBackdrop: View {
    let theme: AppTheme
    let pulse: Bool

    var body: some View {
        ZStack {
            theme.paper

            // Warm amber wash в верхнем-левом углу — там же hero-блок.
            GeometryReader { geo in
                let w = geo.size.width
                let h = geo.size.height
                Circle()
                    .fill(
                        RadialGradient(
                            colors: [theme.ambientWarm, .clear],
                            center: .center,
                            startRadius: 0,
                            endRadius: max(w, h) * 0.55
                        )
                    )
                    .frame(width: w * 1.1, height: h * 1.1)
                    .offset(x: -w * 0.35 + (pulse ? 12 : -12),
                            y: -h * 0.25 + (pulse ? -8 : 8))
                    .blur(radius: 24)

                Circle()
                    .fill(
                        RadialGradient(
                            colors: [theme.ambientCool, .clear],
                            center: .center,
                            startRadius: 0,
                            endRadius: max(w, h) * 0.40
                        )
                    )
                    .frame(width: w * 0.9, height: h * 0.9)
                    .offset(x: w * 0.35, y: h * 0.30)
                    .blur(radius: 28)
            }
            .allowsHitTesting(false)
        }
    }
}

// MARK: - Page header

/// Компактный header — единая строка с лого-меткой и picker'ом темы.
/// Hero-блок берёт на себя задачу сообщать состояние, дублировать его
/// крупным display'ем не нужно.
private struct PageHeader: View {
    let theme: AppTheme
    @Binding var themePreference: String
    let version: String

    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            HStack(spacing: 8) {
                Text("Home Cinema")
                    .font(Typography.body(13))
                    .tracking(0.6)
                    .foregroundStyle(theme.primaryText.opacity(0.78))
                Rectangle()
                    .fill(theme.subtleText.opacity(0.28))
                    .frame(width: 1, height: 10)
                Text("v\(version)")
                    .font(Typography.mono(10))
                    .tracking(0.3)
                    .foregroundStyle(theme.subtleText.opacity(0.85))
            }
            Spacer(minLength: 12)
            ThemePicker(selection: $themePreference, theme: theme)
        }
    }
}

// MARK: - Theme picker

private struct ThemePicker: View {
    @Binding var selection: String
    let theme: AppTheme

    private let options: [ThemePreference] = [.auto, .light, .dark]

    var body: some View {
        HStack(spacing: 2) {
            ForEach(options) { option in
                Button {
                    withAnimation(.easeOut(duration: 0.18)) {
                        selection = option.rawValue
                    }
                } label: {
                    label(for: option)
                        .frame(width: 30, height: 28)
                        .background {
                            if selection == option.rawValue {
                                RoundedRectangle(cornerRadius: 8, style: .continuous)
                                    .fill(theme.accent.opacity(0.18))
                                    .overlay {
                                        RoundedRectangle(cornerRadius: 8, style: .continuous)
                                            .stroke(theme.accent.opacity(0.50), lineWidth: 1)
                                    }
                            }
                        }
                        .contentShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                }
                .buttonStyle(.plain)
                .accessibilityLabel(option.title)
            }
        }
        .padding(4)
        .glassPill(theme: theme)
    }

    @ViewBuilder
    private func label(for option: ThemePreference) -> some View {
        switch option {
        case .auto:
            Text("A")
                .font(Typography.small(13))
                .foregroundStyle(theme.primaryText)
        case .light:
            Image(systemName: "sun.max.fill")
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(theme.primaryText)
        case .dark:
            Image(systemName: "moon.fill")
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(theme.primaryText)
        }
    }
}

// MARK: - Hero panel

private struct HeroPanel: View {
    let controller: ServerController
    let theme: AppTheme
    let heroPulse: Bool
    let tick: Int  // используется только для рендера каждую секунду

    var body: some View {
        VStack(alignment: .leading, spacing: 22) {
            HStack(alignment: .top, spacing: 22) {
                statusBlock
                Spacer(minLength: 14)
                endpointBlock
            }

            Divider().overlay(Color.white.opacity(0.08))

            HStack(spacing: 24) {
                metric(label: "LIBRARY",
                       value: controller.mediaFolderName,
                       systemImage: "folder.fill")
                metric(label: "PROGRESS",
                       value: controller.progressCount == 0 ? "—" : "\(controller.progressCount) saved",
                       systemImage: "bookmark.fill")
                metric(label: "STREAMS",
                       value: controller.activeStreams == 0 ? "—" : "\(controller.activeStreams) live",
                       systemImage: "dot.radiowaves.left.and.right")
            }
        }
        .padding(28)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            LinearGradient(
                colors: [theme.heroTop, theme.heroBottom],
                startPoint: .topLeading, endPoint: .bottomTrailing
            ),
            in: RoundedRectangle(cornerRadius: Layout.panelCorner, style: .continuous)
        )
        .overlay(
            RoundedRectangle(cornerRadius: Layout.panelCorner, style: .continuous)
                .strokeBorder(
                    LinearGradient(
                        colors: [theme.accent.opacity(0.30), Color.white.opacity(0.06)],
                        startPoint: .topLeading, endPoint: .bottomTrailing
                    ),
                    lineWidth: 1
                )
        )
        // «Свечение» янтаря в правом-верхнем углу, как film projector.
        .overlay(
            RadialGradient(
                colors: [theme.accent.opacity(heroPulse && controller.activeStreams > 0 ? 0.32 : 0.18),
                         Color.clear],
                center: .topTrailing,
                startRadius: 10,
                endRadius: 240
            )
            .clipShape(RoundedRectangle(cornerRadius: Layout.panelCorner, style: .continuous))
            .allowsHitTesting(false)
        )
        .shadow(color: theme.shadow, radius: 24, x: 0, y: 16)
    }

    private var statusBlock: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 10) {
                LiveDot(isRunning: controller.isRunning,
                        activeStreams: controller.activeStreams,
                        theme: theme,
                        pulse: heroPulse)
                Text(statusLabel)
                    .font(Typography.label(11))
                    .tracking(2.2)
                    .foregroundStyle(theme.heroPrimaryText)
            }

            Text(headline)
                .font(Typography.h1(28))
                .foregroundStyle(theme.heroPrimaryText)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)

            if isInformativeStatus {
                HStack(spacing: 8) {
                    if controller.isBusy {
                        ProgressView().controlSize(.small).tint(theme.heroSecondaryText)
                    }
                    Text(controller.statusMessage)
                        .font(Typography.body())
                        .foregroundStyle(theme.heroSecondaryText)
                        .lineLimit(2)
                }
                .transition(.opacity)
            }
        }
    }

    private var endpointBlock: some View {
        VStack(alignment: .trailing, spacing: 8) {
            Text("ENDPOINT")
                .font(Typography.label())
                .tracking(2.0)
                .foregroundStyle(theme.heroSubtleText)
            Text(controller.endpoint)
                .font(Typography.timecode(14))
                .foregroundStyle(theme.heroPrimaryText)
                .textSelection(.enabled)
                .lineLimit(1)
                .truncationMode(.middle)
            HStack(spacing: 6) {
                Image(systemName: "dot.radiowaves.left.and.right")
                Text("port \(controller.serverPort)")
            }
            .font(Typography.mono(11))
            .foregroundStyle(theme.heroSecondaryText)
        }
        .frame(maxWidth: 240, alignment: .trailing)
    }

    private var statusLabel: String {
        if !controller.isRunning { return "OFFLINE" }
        if controller.activeStreams > 0 { return "ON AIR" }
        return "ONLINE"
    }

    private var headline: String {
        if controller.activeStreams > 0 {
            let n = controller.activeStreams
            return n == 1 ? "Streaming one playback." : "Streaming \(n) playbacks."
        }
        if controller.isRunning {
            return "Library is reachable on your network."
        }
        return controller.mediaDir.isEmpty ? "Pick a folder to start." : "Ready to broadcast."
    }

    private var isInformativeStatus: Bool {
        let idle: Set<String> = ["Choose a folder.", "Library selected.", "Streaming now.", ""]
        return !idle.contains(controller.statusMessage) || controller.isBusy
    }

    private func metric(label: String, value: String, systemImage: String) -> some View {
        HStack(spacing: 10) {
            Image(systemName: systemImage)
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(theme.heroSubtleText)
                .frame(width: 22, height: 22)
                .background(Circle().fill(Color.white.opacity(0.06)))
            VStack(alignment: .leading, spacing: 2) {
                Text(label)
                    .font(Typography.label())
                    .tracking(1.6)
                    .foregroundStyle(theme.heroSubtleText)
                Text(value)
                    .font(Typography.h2(14))
                    .foregroundStyle(theme.heroPrimaryText)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

// MARK: - Live dot

private struct LiveDot: View {
    let isRunning: Bool
    let activeStreams: Int
    let theme: AppTheme
    let pulse: Bool

    var body: some View {
        ZStack {
            if isRunning && activeStreams > 0 {
                Circle()
                    .stroke(theme.accent.opacity(0.55), lineWidth: 2)
                    .frame(width: 22, height: 22)
                    .scaleEffect(pulse ? 1.5 : 0.95)
                    .opacity(pulse ? 0.0 : 0.85)
            }
            Circle()
                .fill(isRunning ? (activeStreams > 0 ? theme.accent : theme.success) : theme.danger)
                .frame(width: 10, height: 10)
                .shadow(color: (activeStreams > 0 ? theme.accent : theme.success).opacity(0.6),
                        radius: activeStreams > 0 ? 6 : 0)
        }
        .frame(width: 24, height: 24)
    }
}

// MARK: - Idle hint

/// Подсказка «сервер запущен, ждём подключения». Показывается между hero
/// и каталогом, когда стримов нет — чтобы пользователь не думал, что
/// раздел Connected Devices «забыли».
private struct IdleDevicesHint: View {
    let theme: AppTheme

    var body: some View {
        HStack(spacing: 14) {
            ZStack {
                Circle()
                    .stroke(theme.accent.opacity(0.30), lineWidth: 1.4)
                    .frame(width: 40, height: 40)
                Image(systemName: "antenna.radiowaves.left.and.right")
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(theme.accent)
            }
            VStack(alignment: .leading, spacing: 2) {
                Text("Waiting for a device")
                    .font(Typography.h2(14))
                    .foregroundStyle(theme.primaryText)
                Text("Open Home Cinema on your TV — connected devices will appear here.")
                    .font(Typography.body(12))
                    .foregroundStyle(theme.secondaryText)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer()
        }
        .padding(Layout.panelPadding)
        .glassPanel(theme: theme, cornerRadius: Layout.panelCorner, elevation: 0.6)
    }
}

// MARK: - Connected devices

/// Секция «подключённые устройства, идущие стримы». В дизайне HTML-mockup'а
/// устройство первично — это главный сигнал «кому-то отдаётся видео». Под
/// названием устройства — что играет и текущий таймкод.
private struct ConnectedDevicesPanel: View {
    let controller: ServerController
    let theme: AppTheme
    let tick: Int

    private let columns = [GridItem(.adaptive(minimum: 280, maximum: 480), spacing: 12)]

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            sectionHeader

            LazyVGrid(columns: columns, alignment: .leading, spacing: 12) {
                ForEach(controller.liveSessions) { session in
                    ConnectedDeviceCard(session: session, theme: theme)
                }
            }
        }
        .padding(Layout.panelPadding)
        .glassPanel(theme: theme, cornerRadius: Layout.panelCorner, elevation: 1.0)
    }

    private var sectionHeader: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: "antenna.radiowaves.left.and.right")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(theme.accent)
            Text("CONNECTED DEVICES")
                .font(Typography.label())
                .tracking(2.2)
                .foregroundStyle(theme.primaryText)
            Spacer()
            HStack(spacing: 6) {
                Circle().fill(theme.accent).frame(width: 5, height: 5)
                Text(controller.liveSessions.count == 1
                     ? "1 streaming"
                     : "\(controller.liveSessions.count) streaming")
                    .font(Typography.mono(11))
                    .foregroundStyle(theme.subtleText)
            }
        }
    }
}

private struct ConnectedDeviceCard: View {
    let session: ServerController.LiveSession
    let theme: AppTheme

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .center, spacing: 12) {
                deviceIcon
                VStack(alignment: .leading, spacing: 2) {
                    Text(session.device)
                        .font(Typography.h2(15))
                        .foregroundStyle(theme.primaryText)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Text(session.client)
                        .font(Typography.mono(11))
                        .foregroundStyle(theme.subtleText)
                        .lineLimit(1)
                }
                Spacer(minLength: 6)
                kindBadge
            }

            Divider().overlay(theme.glassEdge)

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Image(systemName: "play.fill")
                        .font(.system(size: 9, weight: .bold))
                        .foregroundStyle(theme.accent)
                    Text("PLAYING")
                        .font(Typography.label(9))
                        .tracking(1.6)
                        .foregroundStyle(theme.subtleText)
                }
                Text(session.title)
                    .font(Typography.body(13))
                    .foregroundStyle(theme.primaryText)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: Layout.cardCorner, style: .continuous)
                .fill(theme.tileSurface)
        )
        .overlay(
            RoundedRectangle(cornerRadius: Layout.cardCorner, style: .continuous)
                .stroke(theme.glassEdge, lineWidth: 1)
        )
    }

    private var deviceIcon: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(theme.accent.opacity(0.14))
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .stroke(theme.accent.opacity(0.32), lineWidth: 1)
            Image(systemName: iconName)
                .font(.system(size: 16, weight: .semibold))
                .foregroundStyle(theme.accent)
        }
        .frame(width: 38, height: 38)
    }

    private var iconName: String {
        let d = session.device.lowercased()
        switch true {
        case d.contains("tv"), d.contains("bravia"), d.contains("aquos"):
            return "tv.fill"
        case d.contains("roku"), d.contains("apple tv"), d.contains("chromecast"),
             d.contains("shield"), d.contains("fire tv"):
            return "appletvremote.gen4"
        case d.contains("xbox"), d.contains("playstation"):
            return "gamecontroller.fill"
        case d.contains("iphone"):
            return "iphone"
        case d.contains("ipad"):
            return "ipad"
        case d.contains("android"):
            return "candybarphone"
        case d.contains("mac"):
            return "laptopcomputer"
        case d.contains("windows"):
            return "pc"
        case d.contains("linux"):
            return "terminal.fill"
        case d.contains("kodi"), d.contains("vlc"), d.contains("infuse"):
            return "play.rectangle.fill"
        default:
            return "antenna.radiowaves.left.and.right"
        }
    }

    private var kindBadge: some View {
        Text(session.kindLabel)
            .font(Typography.label(9))
            .tracking(1.6)
            .foregroundStyle(badgeColor)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(
                Capsule(style: .continuous)
                    .fill(badgeColor.opacity(0.12))
            )
            .overlay(
                Capsule(style: .continuous)
                    .stroke(badgeColor.opacity(0.30), lineWidth: 0.8)
            )
    }

    private var badgeColor: Color {
        switch session.kind {
        case "resume": return theme.accent
        case "tv":     return theme.secondaryAccent
        default:       return theme.success
        }
    }
}

// MARK: - Recently watched grid

private struct ProgressGrid: View {
    let controller: ServerController
    let theme: AppTheme

    private let columns = [GridItem(.adaptive(minimum: 260, maximum: 420), spacing: 12)]

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text("RECENTLY WATCHED")
                    .font(Typography.label())
                    .tracking(2.2)
                    .foregroundStyle(theme.primaryText)
                Spacer()
                Text(controller.progressCount == 0 ? "Empty"
                                                   : "\(controller.progressCount) \(controller.progressCount == 1 ? "title" : "titles")")
                    .font(Typography.mono(11))
                    .foregroundStyle(theme.subtleText)
            }

            if controller.progressItems.isEmpty {
                EmptyStateView(theme: theme)
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                LazyVGrid(columns: columns, alignment: .leading, spacing: 12) {
                    ForEach(controller.progressItems) { item in
                        ProgressTile(item: item, theme: theme) {
                            controller.deleteProgressItem(item)
                        }
                    }
                }
            }
        }
        .padding(Layout.panelPadding)
        .glassPanel(theme: theme, cornerRadius: Layout.panelCorner)
    }
}

private struct EmptyStateView: View {
    let theme: AppTheme
    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: "film")
                .font(.system(size: 26, weight: .regular))
                .foregroundStyle(theme.subtleText)
                .frame(width: 44, height: 44)
                .background(Circle().fill(theme.glassEdge))
            VStack(alignment: .leading, spacing: 4) {
                Text("Nothing watched yet")
                    .font(Typography.h2())
                    .foregroundStyle(theme.primaryText)
                Text("Resume positions will show up here.")
                    .font(Typography.body())
                    .foregroundStyle(theme.secondaryText)
            }
            Spacer()
        }
        .padding(.vertical, 6)
    }
}

private struct ProgressTile: View {
    let item: ServerController.ProgressItem
    let theme: AppTheme
    let onDelete: () -> Void

    @State private var hovered = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(item.fileName)
                        .font(Typography.h2(14))
                        .foregroundStyle(theme.primaryText)
                        .lineLimit(2)
                        .multilineTextAlignment(.leading)
                        .fixedSize(horizontal: false, vertical: true)
                    Text(item.folderName)
                        .font(Typography.body(11))
                        .foregroundStyle(theme.subtleText)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Spacer(minLength: 6)
                Button(action: onDelete) {
                    Image(systemName: "xmark")
                        .font(.system(size: 9, weight: .bold))
                        .foregroundStyle(hovered ? theme.danger : theme.subtleText)
                        .frame(width: 22, height: 22)
                        .background(
                            Circle()
                                .fill(hovered ? theme.danger.opacity(0.12) : theme.glassEdge)
                        )
                }
                .buttonStyle(.plain)
                .opacity(hovered ? 1 : 0.85)
            }

            // Прогресс-бар с подписью «watched X of Y».
            VStack(alignment: .leading, spacing: 6) {
                ProgressBar(fraction: item.progressFraction ?? 0, theme: theme)
                    .frame(height: 4)
                HStack(spacing: 6) {
                    Text("Watched")
                        .font(Typography.label(9))
                        .tracking(1.6)
                        .foregroundStyle(theme.subtleText)
                    if !item.timecode.isEmpty {
                        Text(item.timecode)
                            .font(Typography.timecode(12))
                            .foregroundStyle(theme.primaryText)
                    }
                    if !item.totalTimecode.isEmpty {
                        Text("of")
                            .font(Typography.label(9))
                            .tracking(1.6)
                            .foregroundStyle(theme.subtleText)
                        Text(item.totalTimecode)
                            .font(Typography.timecode(12))
                            .foregroundStyle(theme.subtleText)
                    }
                    Spacer()
                    Text(item.updatedText)
                        .font(Typography.small())
                        .foregroundStyle(theme.subtleText)
                        .lineLimit(1)
                }
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: Layout.tileCorner, style: .continuous)
                .fill(theme.tileSurface)
        )
        .overlay(
            RoundedRectangle(cornerRadius: Layout.tileCorner, style: .continuous)
                .stroke(hovered ? theme.glassEdgeStrong : theme.glassEdge, lineWidth: 1)
        )
        .scaleEffect(hovered ? 1.005 : 1.0)
        .animation(.easeOut(duration: 0.15), value: hovered)
        .onHover { hovered = $0 }
    }
}

// MARK: - Floating dock

private struct FloatingDock: View {
    let controller: ServerController
    let theme: AppTheme
    let onChooseFolder: () -> Void
    let onToggle: () -> Void
    let onReset: () -> Void
    let onOpenLogs: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            DockButton(systemImage: controller.isRunning ? "stop.fill" : "play.fill",
                       title: controller.isRunning ? "Stop" : "Start",
                       isPrimary: true,
                       isLoading: controller.isBusy,
                       theme: theme,
                       action: onToggle)

            DockButton(systemImage: "folder.fill",
                       title: "Folder",
                       isPrimary: false,
                       isLoading: false,
                       theme: theme,
                       action: onChooseFolder)

            Divider().frame(height: 32).overlay(theme.glassEdge)

            DockIconButton(systemImage: "arrow.counterclockwise",
                           tone: .destructive,
                           theme: theme,
                           tooltip: "Reset Progress",
                           action: onReset)

            DockIconButton(systemImage: "doc.text.magnifyingglass",
                           tone: .neutral,
                           theme: theme,
                           tooltip: "Open Logs",
                           action: onOpenLogs)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .frame(maxWidth: 540)
        .glassPanel(theme: theme, cornerRadius: 28, elevation: 1.2, prominent: true)
        .frame(maxWidth: .infinity)
        .disabled(controller.isBusy)
    }
}

private struct DockButton: View {
    let systemImage: String
    let title: String
    let isPrimary: Bool
    let isLoading: Bool
    let theme: AppTheme
    let action: () -> Void

    @State private var hovered = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                ZStack {
                    if isLoading {
                        ProgressView()
                            .controlSize(.small)
                            .tint(isPrimary ? theme.accentForeground : theme.primaryText)
                    } else {
                        Image(systemName: systemImage)
                            .font(.system(size: 13, weight: .bold))
                            .foregroundStyle(isPrimary ? theme.accentForeground : theme.primaryText)
                    }
                }
                .frame(width: 20)

                Text(title)
                    .font(Typography.h2(13))
                    .foregroundStyle(isPrimary ? theme.accentForeground : theme.primaryText)
            }
            .padding(.horizontal, 18)
            .frame(height: 40)
            .background(
                Capsule(style: .continuous)
                    .fill(isPrimary
                          ? LinearGradient(colors: [theme.accent, theme.accentSoft],
                                           startPoint: .topLeading, endPoint: .bottomTrailing)
                          : LinearGradient(colors: [Color.clear, Color.clear],
                                           startPoint: .top, endPoint: .bottom))
            )
            .overlay(
                Capsule(style: .continuous)
                    .stroke(isPrimary ? Color.white.opacity(hovered ? 0.35 : 0.18) : theme.glassEdgeStrong,
                            lineWidth: 1)
            )
            .scaleEffect(hovered ? 1.03 : 1.0)
            .shadow(color: isPrimary ? theme.accent.opacity(hovered ? 0.45 : 0.30) : Color.clear,
                    radius: hovered ? 14 : 8, x: 0, y: 6)
        }
        .buttonStyle(.plain)
        .animation(.easeOut(duration: 0.12), value: hovered)
        .onHover { hovered = $0 }
    }
}

private struct DockIconButton: View {
    enum Tone { case neutral, destructive }
    let systemImage: String
    let tone: Tone
    let theme: AppTheme
    let tooltip: String
    let action: () -> Void

    @State private var hovered = false

    var body: some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.system(size: 13, weight: .bold))
                .foregroundStyle(foreground)
                .frame(width: 40, height: 40)
                .background(
                    Circle().fill(hovered ? hoverFill : Color.clear)
                )
                .overlay(
                    Circle().stroke(hovered ? hoverStroke : theme.glassEdge, lineWidth: 1)
                )
                .scaleEffect(hovered ? 1.05 : 1.0)
        }
        .buttonStyle(.plain)
        .help(tooltip)
        .animation(.easeOut(duration: 0.12), value: hovered)
        .onHover { hovered = $0 }
        .accessibilityLabel(tooltip)
    }

    private var foreground: Color {
        switch tone {
        case .destructive: return hovered ? theme.danger : theme.primaryText
        case .neutral:     return theme.primaryText
        }
    }
    private var hoverFill: Color {
        switch tone {
        case .destructive: return theme.danger.opacity(0.12)
        case .neutral:     return theme.glassEdge
        }
    }
    private var hoverStroke: Color {
        switch tone {
        case .destructive: return theme.danger.opacity(0.35)
        case .neutral:     return theme.glassEdgeStrong
        }
    }
}

// MARK: - Progress bar

private struct ProgressBar: View {
    let fraction: Double
    let theme: AppTheme

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(theme.glassEdge)
                Capsule()
                    .fill(LinearGradient(
                        colors: [theme.accent, theme.accentSoft],
                        startPoint: .leading, endPoint: .trailing
                    ))
                    .frame(width: geo.size.width * CGFloat(max(0.02, fraction)))
            }
        }
    }
}

// MARK: - Undo banner

private struct UndoBanner: View {
    let snapshot: ServerController.UndoSnapshot
    let theme: AppTheme
    let onUndo: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "arrow.uturn.backward")
                .font(.system(size: 14, weight: .bold))
                .foregroundStyle(theme.heroPrimaryText)
                .frame(width: 30, height: 30)
                .background(Circle().fill(Color.white.opacity(0.12)))

            VStack(alignment: .leading, spacing: 2) {
                Text("Progress cleared for \(snapshot.cleared) titles")
                    .font(Typography.h2(13))
                    .foregroundStyle(theme.heroPrimaryText)
                Text("Undo within \(secondsRemaining) sec")
                    .font(Typography.small())
                    .foregroundStyle(theme.heroSecondaryText)
            }

            Spacer()

            Button(action: onUndo) {
                Text("Undo")
                    .font(Typography.h2(13))
                    .foregroundStyle(theme.accentForeground)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 9)
                    .background(
                        Capsule(style: .continuous)
                            .fill(
                                LinearGradient(colors: [theme.accent, theme.accentSoft],
                                               startPoint: .topLeading, endPoint: .bottomTrailing)
                            )
                    )
            }
            .buttonStyle(.plain)
        }
        .padding(14)
        .frame(maxWidth: 480)
        .background(
            LinearGradient(colors: [theme.heroTop, theme.heroBottom],
                           startPoint: .leading, endPoint: .trailing),
            in: RoundedRectangle(cornerRadius: 18, style: .continuous)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .stroke(theme.accent.opacity(0.35), lineWidth: 1)
        )
        .shadow(color: theme.shadow, radius: 22, x: 0, y: 12)
    }

    private var secondsRemaining: Int {
        max(0, Int(snapshot.expiresAt.timeIntervalSinceNow.rounded()))
    }
}

// MARK: - Helpers needed by AppTheme

extension AppTheme {
    /// Secondary accent (для transcode-сессий — холодный сине-стальной).
    var secondaryAccent: Color {
        switch self {
        case .light: return Color(red: 0.18, green: 0.40, blue: 0.62)
        case .dark:  return Color(red: 0.50, green: 0.74, blue: 0.96)
        }
    }
}

// MARK: - Previews

#Preview("Light") {
    ContentView().frame(width: 940, height: 720)
}

#Preview("Dark") {
    ContentView().frame(width: 940, height: 720).preferredColorScheme(.dark)
}
