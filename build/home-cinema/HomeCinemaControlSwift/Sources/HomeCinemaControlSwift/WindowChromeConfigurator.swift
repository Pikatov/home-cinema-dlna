import AppKit
import SwiftUI

struct WindowChromeConfigurator: NSViewRepresentable {
    let theme: AppTheme

    func makeNSView(context: Context) -> NSView {
        let view = NSView(frame: .zero)
        view.wantsLayer = true
        view.layer?.opacity = 0
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        DispatchQueue.main.async {
            applyStyle(to: nsView.window)
        }
    }

    private func applyStyle(to window: NSWindow?) {
        guard let window else { return }

        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.styleMask.insert(.fullSizeContentView)
        if #available(macOS 11.0, *) {
            window.toolbarStyle = .unifiedCompact
        }
        window.isMovableByWindowBackground = true
        window.backgroundColor = .clear
        window.standardWindowButton(.miniaturizeButton)?.isHidden = false
        window.standardWindowButton(.zoomButton)?.isHidden = false
        window.standardWindowButton(.closeButton)?.isHidden = false

        let buttons: [NSWindow.ButtonType] = [.closeButton, .miniaturizeButton, .zoomButton]
        for type in buttons {
            guard let button = window.standardWindowButton(type) else { continue }
            style(button: button)
        }
    }

    private func style(button: NSButton) {
        button.wantsLayer = true
        button.isBordered = false
        button.imagePosition = .imageOnly
        button.contentTintColor = NSColor(theme.windowButtonGlyph)
        button.layer?.backgroundColor = NSColor(theme.windowButtonFill).cgColor
        button.layer?.cornerRadius = 8
        button.layer?.masksToBounds = true
        button.alphaValue = 0.92
    }
}
