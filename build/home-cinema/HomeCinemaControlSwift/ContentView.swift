import AppKit
import SwiftUI

struct ContentView: View {
    @StateObject private var controller = ServerController()
    @AppStorage("selectedTheme") private var selectedThemeRawValue: String = AppTheme.dark.rawValue
    @State private var autoTick = Timer.publish(every: 4, on: .main, in: .common).autoconnect()

    private var selectedTheme: AppTheme {
        get { AppTheme(rawValue: selectedThemeRawValue) ?? .dark }
        set { selectedThemeRawValue = newValue.rawValue }
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
            }
        }
        .preferredColorScheme(selectedTheme.colorScheme)
        .onAppear {
            controller.refreshStatus()
        }
        .onReceive(autoTick) { _ in
            controller.refreshStatus()
        }
    }

    private var background: some View {
        ZStack {
            LinearGradient(
                colors: [selectedTheme.backgroundTop, selectedTheme.backgroundBottom],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
            .ignoresSafeArea()

            Circle()
                .fill(selectedTheme.accent.opacity(selectedTheme == .light ? 0.26 : 0.24))
                .frame(width: 360, height: 360)
                .blur(radius: 14)
                .offset(x: -260, y: -180)

            Circle()
                .fill(selectedTheme.secondaryAccent.opacity(selectedTheme == .light ? 0.22 : 0.18))
                .frame(width: 320, height: 320)
                .blur(radius: 20)
                .offset(x: 300, y: -120)

            RoundedRectangle(cornerRadius: 180, style: .continuous)
                .fill(selectedTheme.accent.opacity(selectedTheme == .light ? 0.10 : 0.12))
                .frame(width: 480, height: 220)
                .rotationEffect(.degrees(-18))
                .blur(radius: 16)
                .offset(x: 180, y: 260)
        }
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 20) {
            VStack(alignment: .leading, spacing: 10) {
                Text("Home Cinema")
                    .font(.system(size: 34, weight: .bold, design: .rounded))
                    .foregroundStyle(selectedTheme.primaryText)
                Text("A glassy control deck for your DLNA server on macOS.")
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(selectedTheme.secondaryText)
            }

            Spacer(minLength: 12)

            Picker("Theme", selection: $selectedThemeRawValue) {
                ForEach(AppTheme.allCases) { theme in
                    Text(theme.title).tag(theme.rawValue)
                }
            }
            .pickerStyle(.segmented)
            .frame(width: 180)
        }
    }

    private var heroPanel: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 8) {
                    statusChip
                    Text(controller.isRunning ? "Server online and discoverable on your network." : "Server is idle. Pick a folder and bring it online.")
                        .font(.system(size: 20, weight: .semibold, design: .rounded))
                        .foregroundStyle(selectedTheme.primaryText)
                        .fixedSize(horizontal: false, vertical: true)
                }

                Spacer(minLength: 20)

                VStack(alignment: .trailing, spacing: 8) {
                    Label("Port \(controller.serverPort)", systemImage: "dot.radiowaves.left.and.right")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(selectedTheme.secondaryText)
                    Text(controller.endpoint)
                        .font(.system(size: 13, weight: .medium, design: .monospaced))
                        .foregroundStyle(selectedTheme.primaryText)
                        .multilineTextAlignment(.trailing)
                }
            }

            Divider().overlay(selectedTheme.border.opacity(0.55))

            HStack(alignment: .top, spacing: 18) {
                VStack(alignment: .leading, spacing: 10) {
                    metricRow(title: "Media Folder", value: controller.mediaDir)
                    metricRow(title: "Started", value: controller.startedAtText)
                }
                Spacer(minLength: 12)
                VStack(alignment: .leading, spacing: 10) {
                    metricRow(title: "Status", value: controller.statusMessage)
                    metricRow(title: "Log File", value: controller.logPath)
                }
            }
        }
        .padding(22)
        .glassCard(theme: selectedTheme, tint: selectedTheme.panelTint)
    }

    private var statusChip: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(controller.isRunning ? Color.green : Color.red)
                .frame(width: 10, height: 10)
                .shadow(color: (controller.isRunning ? Color.green : Color.red).opacity(0.45), radius: 8, x: 0, y: 0)
            Text(controller.isRunning ? "Running" : "Stopped")
                .font(.system(size: 13, weight: .bold, design: .rounded))
                .foregroundStyle(selectedTheme.primaryText)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(
            Capsule(style: .continuous)
                .fill(selectedTheme.panelTint.opacity(selectedTheme == .light ? 0.92 : 0.84))
        )
        .overlay(
            Capsule(style: .continuous)
                .stroke(selectedTheme.border, lineWidth: 1)
        )
    }

    private var actionPanel: some View {
        VStack(spacing: 12) {
            HStack(spacing: 14) {
                actionButton(
                    title: controller.isRunning ? "Stop Server" : "Start Server",
                    subtitle: controller.isRunning ? "Send graceful stop signal" : "Launch bundled HomeCinemaServer",
                    icon: controller.isRunning ? "stop.fill" : "play.fill",
                    gradient: controller.isRunning
                        ? [Color(red: 0.95, green: 0.28, blue: 0.32), Color(red: 0.82, green: 0.12, blue: 0.18)]
                        : [Color(red: 0.26, green: 0.78, blue: 0.42), Color(red: 0.14, green: 0.60, blue: 0.30)],
                    action: toggleServer
                )

                actionButton(
                    title: "Choose Folder",
                    subtitle: "Point the server to your movie library",
                    icon: "folder.fill",
                    gradient: [selectedTheme.accent, selectedTheme.secondaryAccent],
                    action: pickFolder
                )
            }

            HStack(spacing: 14) {
                secondaryButton(title: "Refresh", icon: "arrow.clockwise", action: controller.refreshStatus)
                secondaryButton(title: "Open Logs", icon: "doc.text.magnifyingglass", action: controller.openLogs)
            }
        }
    }

    private var footer: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("Home Cinema v.1.5")
                    .font(.system(size: 12, weight: .bold))
            }
            .foregroundStyle(selectedTheme.secondaryText)
            Spacer()
            Text(selectedTheme.title + " Theme")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(selectedTheme.secondaryText)
            if controller.isBusy {
                ProgressView()
                    .controlSize(.small)
                    .tint(selectedTheme.accent)
            }
        }
        .padding(.horizontal, 4)
    }

    private func metricRow(title: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title.uppercased())
                .font(.system(size: 11, weight: .bold))
                .tracking(1.2)
                .foregroundStyle(selectedTheme.secondaryText)
            Text(value.isEmpty ? "—" : value)
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(selectedTheme.primaryText)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func actionButton(title: String, subtitle: String, icon: String, gradient: [Color], action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 14) {
                Image(systemName: icon)
                    .font(.system(size: 18, weight: .bold))
                    .frame(width: 42, height: 42)
                    .background(Color.white.opacity(0.18), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                VStack(alignment: .leading, spacing: 4) {
                    Text(title)
                        .font(.system(size: 16, weight: .bold, design: .rounded))
                    Text(subtitle)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(Color.white.opacity(0.82))
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer()
            }
            .foregroundStyle(Color.white)
            .padding(18)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                LinearGradient(colors: gradient, startPoint: .topLeading, endPoint: .bottomTrailing),
                in: RoundedRectangle(cornerRadius: 24, style: .continuous)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 24, style: .continuous)
                    .stroke(Color.white.opacity(0.18), lineWidth: 1)
            )
            .shadow(color: gradient.first?.opacity(0.32) ?? .clear, radius: 18, x: 0, y: 12)
        }
        .buttonStyle(.plain)
        .disabled(controller.isBusy)
        .opacity(controller.isBusy ? 0.72 : 1.0)
    }

    private func secondaryButton(title: String, icon: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: icon)
                Text(title)
                    .fontWeight(.semibold)
            }
            .font(.system(size: 14, weight: .semibold, design: .rounded))
            .foregroundStyle(selectedTheme.primaryText)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
            .background(
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .fill(selectedTheme.panelTint.opacity(selectedTheme == .light ? 0.88 : 0.82))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .stroke(selectedTheme.border, lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
        .disabled(controller.isBusy)
    }

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

private struct GlassCardModifier: ViewModifier {
    let theme: AppTheme
    let tint: Color

    func body(content: Content) -> some View {
        content
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 30, style: .continuous))
            .background(
                RoundedRectangle(cornerRadius: 30, style: .continuous)
                    .fill(tint)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 30, style: .continuous)
                    .stroke(theme.border, lineWidth: 1)
            )
            .shadow(color: Color.black.opacity(theme == .light ? 0.08 : 0.28), radius: 22, x: 0, y: 18)
    }
}

private extension View {
    func glassCard(theme: AppTheme, tint: Color) -> some View {
        modifier(GlassCardModifier(theme: theme, tint: tint))
    }
}

struct ContentView_Previews: PreviewProvider {
    static var previews: some View {
        ContentView()
            .frame(width: 780, height: 520)
    }
}
