import AppKit
import SwiftUI

// MARK: - Button Styles (П.2 – hover + press feedback)

private struct ActionCardButtonStyle: ButtonStyle {
    var isHovered: Bool
    var reduceMotion: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed ? 0.97 : (isHovered ? 1.015 : 1.0))
            .brightness(isHovered && !configuration.isPressed ? 0.05 : 0.0)
            .animation(reduceMotion ? nil : .easeOut(duration: 0.15), value: configuration.isPressed)
    }
}

private struct SecondaryButtonHoverStyle: ButtonStyle {
    var isHovered: Bool
    var reduceMotion: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed ? 0.97 : (isHovered ? 1.015 : 1.0))
            .opacity(isHovered && !configuration.isPressed ? 0.88 : 1.0)
            .animation(reduceMotion ? nil : .easeOut(duration: 0.15), value: configuration.isPressed)
    }
}

// MARK: - ContentView

struct ContentView: View {
    @StateObject private var controller = ServerController()
    @AppStorage("selectedTheme") private var selectedThemeRawValue: String = ThemePreference.auto.rawValue
    @Environment(\.colorScheme) private var systemColorScheme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var autoTick = Timer.publish(every: 4, on: .main, in: .common).autoconnect()
    @State private var showingResetAlert = false

    // П.10 – blob animation states
    @State private var blob1Animate = false
    @State private var blob2Animate = false

    // П.2 – hover states for each button
    @State private var hoverStartStop = false
    @State private var hoverChooseFolder = false
    @State private var hoverReset = false
    @State private var hoverLogs = false

    private var themePreference: ThemePreference {
        get { ThemePreference(rawValue: selectedThemeRawValue) ?? .auto }
        set { selectedThemeRawValue = newValue.rawValue }
    }

    private var selectedTheme: AppTheme {
        themePreference.resolvedTheme(for: systemColorScheme)
    }

    private var appVersionText: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.7"
    }

    private var cardAnimation: Animation? {
        reduceMotion ? nil : .easeOut(duration: 0.22)
    }

    private var heroHeadline: String {
        controller.isRunning
            ? "Server available on your network."
            : (controller.mediaDir.isEmpty ? "Choose a folder." : "Library selected.")
    }

    // П.1 – показываем statusMessage только когда несёт новую информацию
    private var isStatusInteresting: Bool {
        let idle: Set<String> = ["Choose a folder.", "Library selected.", "Streaming now.", ""]
        return !idle.contains(controller.statusMessage)
    }

    private var statusMessageColor: Color {
        let msg = controller.statusMessage.lowercased()
        if msg.contains("failed") || msg.contains("error") || msg.contains("could not") {
            return Color(red: 1.0, green: 0.45, blue: 0.40)
        }
        if msg.contains("cleared") || msg.contains("removed") || msg.contains("updated") {
            return Color(red: 0.42, green: 0.88, blue: 0.58)
        }
        return Color.white.opacity(0.68)
    }

    var body: some View {
        ZStack {
            background

            ScrollView(showsIndicators: false) {
                VStack(spacing: 18) {
                    header
                    heroPanel
                    actionPanel
                    footer
                }
                .padding(24)
                .frame(maxWidth: 820)
                .frame(maxWidth: .infinity)
            }
        }
        .preferredColorScheme(themePreference == .auto ? nil : selectedTheme.colorScheme)
        .background(WindowChromeConfigurator(theme: selectedTheme))
        .animation(cardAnimation, value: controller.isRunning)
        .animation(cardAnimation, value: controller.isBusy)
        .animation(cardAnimation, value: controller.statusMessage)  // П.1 – анимируем появление статуса
        .alert("Reset all saved progress?", isPresented: $showingResetAlert) {
            Button("Cancel", role: .cancel) {}
            Button("Reset Progress", role: .destructive) {
                controller.resetProgress()
            }
        } message: {
            Text("This clears every saved resume position for movies with progress.")
        }
        .onAppear {
            controller.refreshStatus()
            startBlobAnimation()  // П.10
        }
        .onReceive(autoTick) { _ in
            controller.refreshStatus()
        }
    }

    // П.10 – запуск медленной осцилляции фоновых кругов
    private func startBlobAnimation() {
        guard !reduceMotion else { return }
        withAnimation(.easeInOut(duration: 18).repeatForever(autoreverses: true)) {
            blob1Animate = true
        }
        withAnimation(.easeInOut(duration: 24).repeatForever(autoreverses: true).delay(5)) {
            blob2Animate = true
        }
    }

    // MARK: – Background

    private var background: some View {
        ZStack {
            LinearGradient(
                colors: [selectedTheme.backgroundTop, selectedTheme.backgroundBottom],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
            .ignoresSafeArea()

            // П.10 – анимированные ambient blobs
            Circle()
                .fill(selectedTheme.accent.opacity(selectedTheme == .light ? 0.24 : 0.22))
                .frame(width: 360, height: 360)
                .blur(radius: 16)
                .offset(
                    x: -250 + (blob1Animate ? 28 : 0),
                    y: -150 + (blob1Animate ? 18 : 0)
                )

            Circle()
                .fill(selectedTheme.secondaryAccent.opacity(selectedTheme == .light ? 0.18 : 0.14))
                .frame(width: 320, height: 320)
                .blur(radius: 20)
                .offset(
                    x: 300 + (blob2Animate ? -24 : 0),
                    y: 110 + (blob2Animate ? -20 : 0)
                )
        }
    }

    // MARK: – Header

    private var header: some View {
        HStack(alignment: .top, spacing: 18) {
            VStack(alignment: .leading, spacing: 8) {
                Text("Home Cinema")
                    .font(.system(size: 34, weight: .bold, design: .rounded))
                    .foregroundStyle(selectedTheme.primaryText)

                Text("A glassy control deck for your DLNA server on macOS.")
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(selectedTheme.secondaryText)
            }

            Spacer(minLength: 12)

            HStack(spacing: 12) {
                Picker("Theme", selection: $selectedThemeRawValue) {
                    themePickerLabel(for: .auto).tag(ThemePreference.auto.rawValue)
                    themePickerLabel(for: .light).tag(ThemePreference.light.rawValue)
                    themePickerLabel(for: .dark).tag(ThemePreference.dark.rawValue)
                }
                .pickerStyle(.segmented)
                .frame(width: 170)
            }
        }
    }

    // MARK: – Hero Panel

    private var heroPanel: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .top, spacing: 18) {
                VStack(alignment: .leading, spacing: 10) {
                    statusChip

                    Text(heroHeadline)
                        .font(.system(size: 20, weight: .semibold, design: .rounded))
                        .foregroundStyle(Color.white.opacity(0.96))
                        .fixedSize(horizontal: false, vertical: true)

                    // П.1 – statusMessage с иконкой загрузки или цветом ошибки/успеха
                    if isStatusInteresting || controller.isBusy {
                        HStack(spacing: 6) {
                            if controller.isBusy {
                                ProgressView()
                                    .controlSize(.mini)
                                    .tint(Color.white.opacity(0.65))
                            }
                            Text(controller.statusMessage)
                                .font(.system(size: 13, weight: .medium, design: .rounded))
                                .foregroundStyle(statusMessageColor)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        .transition(.opacity.combined(with: .move(edge: .top)))
                    }
                }

                Spacer(minLength: 16)

                VStack(alignment: .trailing, spacing: 8) {
                    HStack(spacing: 8) {
                        Image(systemName: "dot.radiowaves.left.and.right")
                        Text("Port \(controller.serverPort)")
                    }
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Color.white.opacity(0.72))

                    Text(controller.endpoint)
                        .font(.system(size: 13, weight: .semibold, design: .monospaced))
                        .foregroundStyle(Color.white.opacity(0.96))
                        .multilineTextAlignment(.trailing)
                        .lineLimit(2)
                        .truncationMode(.middle)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                }
                .frame(maxWidth: 240, alignment: .trailing)
            }

            Divider()
                .overlay(Color.white.opacity(0.10))

            HStack(alignment: .top, spacing: 26) {
                VStack(alignment: .leading, spacing: 12) {
                    metricRow(title: "Media Folder", value: controller.mediaDir)
                    metricRow(title: "Progress", value: controller.progressCount == 0 ? "No saved progress" : "\(controller.progressCount) files")
                }

                VStack(alignment: .leading, spacing: 12) {
                    metricRow(title: "Updated", value: controller.progressUpdatedText)
                    metricRow(title: "Log File", value: controller.logPath)
                }
            }

            progressPanel
        }
        .padding(22)
        .background(
            LinearGradient(
                colors: [selectedTheme.heroTop, selectedTheme.heroBottom],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            ),
            in: RoundedRectangle(cornerRadius: 20, style: .continuous)  // П.11: was 34
        )
        .overlay(
            RoundedRectangle(cornerRadius: 20, style: .continuous)  // П.11: was 34
                .stroke(Color.white.opacity(0.12), lineWidth: 1)
        )
        .shadow(color: selectedTheme.shadowColor, radius: 22, x: 0, y: 18)
    }

    private var statusChip: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(controller.isRunning ? selectedTheme.success : selectedTheme.danger)
                .frame(width: 11, height: 11)

            Text(controller.isRunning ? "Online" : "Offline")
                .font(.system(size: 13, weight: .bold, design: .rounded))
                .foregroundStyle(Color.white.opacity(0.96))
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(
            Capsule(style: .continuous)
                .fill(Color.white.opacity(0.08))
        )
        .overlay(
            Capsule(style: .continuous)
                .stroke(Color.white.opacity(0.10), lineWidth: 1)
        )
    }

    // MARK: – Action Panel

    private var actionPanel: some View {
        VStack(spacing: 12) {
            HStack(spacing: 14) {
                // П.2 + П.3: hover/press + spinner для Start/Stop
                actionButton(
                    title: controller.isRunning ? "Stop Server" : "Start Server",
                    subtitle: controller.isRunning ? "Take the server offline" : "Bring your library online",
                    icon: controller.isRunning ? "stop.fill" : "play.fill",
                    gradient: controller.isRunning
                        ? [selectedTheme.danger.opacity(0.34), selectedTheme.danger.opacity(0.20)]
                        : [selectedTheme.success.opacity(0.32), selectedTheme.success.opacity(0.18)],
                    iconTint: Color.white.opacity(0.94),
                    showSpinner: controller.isBusy,
                    isHovered: $hoverStartStop,
                    action: toggleServer
                )
                .accessibilityLabel(controller.isRunning ? "Stop server" : "Start server")

                actionButton(
                    title: "Choose Folder",
                    subtitle: "Point the server to your movie library",
                    icon: "folder.fill",
                    gradient: [selectedTheme.buttonSurface, selectedTheme.buttonSurface.opacity(0.86)],
                    iconTint: selectedTheme.primaryText,
                    showSpinner: false,
                    isHovered: $hoverChooseFolder,
                    action: pickFolder
                )
                .accessibilityLabel("Choose folder")
            }

            HStack(spacing: 14) {
                secondaryButton(
                    title: "Reset Progress",
                    icon: "minus.circle",
                    isHovered: $hoverReset,
                    action: { showingResetAlert = true }
                )
                .accessibilityLabel("Reset progress")

                secondaryButton(
                    title: "Open Logs",
                    icon: "doc.text.magnifyingglass",
                    isHovered: $hoverLogs,
                    action: controller.openLogs
                )
                .accessibilityLabel("Open logs")
            }
        }
    }

    // MARK: – Footer

    private var footer: some View {
        HStack {
            Text("Home Cinema v.\(appVersionText)")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(selectedTheme.subtleText)

            Spacer()
        }
        .padding(.horizontal, 4)
    }

    // MARK: – Progress Panel

    private var progressPanel: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("SAVED PROGRESS")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(Color.white.opacity(0.68))

                Spacer()

                Text(controller.progressCount == 0 ? "0 files" : "\(controller.progressCount) files")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(Color.white.opacity(0.60))
            }

            if controller.progressItems.isEmpty {
                Text("No saved progress yet.")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(Color.white.opacity(0.84))
            } else {
                VStack(spacing: 8) {
                    ForEach(controller.progressItems.prefix(4)) { item in
                        progressRow(item: item)
                    }
                }
            }
        }
    }

    // MARK: – Subviews

    private func metricRow(title: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title.uppercased())
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(Color.white.opacity(0.68))

            Text(value.isEmpty ? "—" : value)
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(Color.white.opacity(0.96))
                .lineLimit(2)
                .truncationMode(.middle)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func progressRow(item: ServerController.ProgressItem) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text(item.fileName)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Color.white.opacity(0.96))
                    .lineLimit(1)
                    .truncationMode(.middle)

                Text(item.folderName)
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(Color.white.opacity(0.56))
                    .lineLimit(1)
                    .truncationMode(.middle)

                // П.4 – thin progress bar
                if let fraction = item.progressFraction {
                    GeometryReader { geo in
                        ZStack(alignment: .leading) {
                            Capsule()
                                .fill(Color.white.opacity(0.14))
                                .frame(height: 3)
                            Capsule()
                                .fill(Color.white.opacity(0.72))
                                .frame(width: geo.size.width * CGFloat(fraction), height: 3)
                        }
                    }
                    .frame(height: 3)
                    .padding(.top, 2)
                }
            }

            Spacer(minLength: 10)

            HStack(spacing: 10) {
                VStack(alignment: .trailing, spacing: 2) {
                    if !item.timecode.isEmpty {
                        Text(item.timecode)
                            .font(.system(size: 12, weight: .bold, design: .monospaced))
                            .foregroundStyle(Color.white.opacity(0.92))
                    }

                    Text(item.updatedText)
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(Color.white.opacity(0.56))
                        .lineLimit(1)
                }

                Button {
                    controller.deleteProgressItem(item)
                } label: {
                    Image(systemName: "minus")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundStyle(selectedTheme.windowButtonGlyph.opacity(0.88))
                        .frame(width: 24, height: 24)
                        .background(
                            Circle()
                                .fill(selectedTheme.windowButtonFill.opacity(0.92))
                        )
                        .overlay(
                            Circle()
                                .stroke(selectedTheme.border.opacity(0.85), lineWidth: 1)
                        )
                }
                .buttonStyle(.plain)
                .disabled(controller.isBusy)
                .opacity(controller.isBusy ? 0.5 : 1.0)
                .accessibilityLabel("Delete progress for \(item.fileName)")
            }
        }
        .padding(.vertical, 2)
    }

    // П.2 + П.3 – action button с hover binding, press animation и опциональным spinner
    private func actionButton(
        title: String,
        subtitle: String,
        icon: String,
        gradient: [Color],
        iconTint: Color,
        showSpinner: Bool,
        isHovered: Binding<Bool>,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            HStack(spacing: 14) {
                // П.3 – spinner вместо иконки при isBusy
                ZStack {
                    if showSpinner {
                        ProgressView()
                            .controlSize(.regular)
                            .tint(iconTint)
                    } else {
                        Image(systemName: icon)
                            .font(.system(size: 18, weight: .bold))
                            .foregroundStyle(iconTint)
                    }
                }
                .frame(width: 42, height: 42)
                .background(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)  // П.11: was 14
                        .fill(Color.white.opacity(0.14))
                )

                VStack(alignment: .leading, spacing: 3) {
                    Text(title)
                        .font(.system(size: 16, weight: .bold, design: .rounded))
                        .foregroundStyle(selectedTheme.primaryText)

                    Text(subtitle)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(selectedTheme.secondaryText)
                        .fixedSize(horizontal: false, vertical: true)
                        .lineLimit(2)
                }

                Spacer()
            }
            .padding(18)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                LinearGradient(colors: gradient, startPoint: .topLeading, endPoint: .bottomTrailing),
                in: RoundedRectangle(cornerRadius: 18, style: .continuous)  // П.11: was 28
            )
            .overlay(
                RoundedRectangle(cornerRadius: 18, style: .continuous)  // П.11: was 28
                    .stroke(selectedTheme.border, lineWidth: 1)
            )
            .shadow(color: selectedTheme.shadowColor, radius: 14, x: 0, y: 10)
        }
        .buttonStyle(ActionCardButtonStyle(isHovered: isHovered.wrappedValue, reduceMotion: reduceMotion))
        .disabled(controller.isBusy)
        .opacity(controller.isBusy ? 0.78 : 1.0)
        .onHover { h in
            withAnimation(reduceMotion ? nil : .easeOut(duration: 0.15)) {
                isHovered.wrappedValue = h
            }
        }
    }

    private func secondaryButton(
        title: String,
        icon: String,
        isHovered: Binding<Bool>,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: icon)
                Text(title)
                    .fontWeight(.semibold)
            }
            .font(.system(size: 14, weight: .semibold, design: .rounded))
            .foregroundStyle(selectedTheme.primaryText)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 15)
            .background(
                RoundedRectangle(cornerRadius: 14, style: .continuous)  // П.11: was 20
                    .fill(selectedTheme.buttonSurface)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 14, style: .continuous)  // П.11: was 20
                    .stroke(selectedTheme.border, lineWidth: 1)
            )
        }
        .buttonStyle(SecondaryButtonHoverStyle(isHovered: isHovered.wrappedValue, reduceMotion: reduceMotion))
        .disabled(controller.isBusy)
        .opacity(controller.isBusy ? 0.72 : 1.0)
        .onHover { h in
            withAnimation(reduceMotion ? nil : .easeOut(duration: 0.15)) {
                isHovered.wrappedValue = h
            }
        }
    }

    @ViewBuilder
    private func themePickerLabel(for option: ThemePreference) -> some View {
        switch option {
        case .auto:
            Text("A")
                .font(.system(size: 14, weight: .bold, design: .rounded))
                .frame(maxWidth: .infinity)
        case .light:
            Image(systemName: "sun.max.fill")
                .frame(maxWidth: .infinity)
        case .dark:
            Image(systemName: "moon.fill")
                .frame(maxWidth: .infinity)
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
}

struct ContentView_Previews: PreviewProvider {
    static var previews: some View {
        ContentView()
            .frame(width: 820, height: 620)
    }
}
