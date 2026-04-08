import SwiftUI

enum AppTheme: String, CaseIterable, Identifiable {
    case light
    case dark

    var id: String { rawValue }

    var title: String {
        switch self {
        case .light:
            return "Light"
        case .dark:
            return "Dark"
        }
    }

    var colorScheme: ColorScheme {
        switch self {
        case .light:
            return .light
        case .dark:
            return .dark
        }
    }

    var accent: Color {
        switch self {
        case .light:
            return Color(red: 0.10, green: 0.47, blue: 0.78)
        case .dark:
            return Color(red: 0.54, green: 0.82, blue: 0.98)
        }
    }

    var secondaryAccent: Color {
        switch self {
        case .light:
            return Color(red: 0.98, green: 0.58, blue: 0.35)
        case .dark:
            return Color(red: 0.98, green: 0.73, blue: 0.40)
        }
    }

    var backgroundTop: Color {
        switch self {
        case .light:
            return Color(red: 0.93, green: 0.97, blue: 1.00)
        case .dark:
            return Color(red: 0.08, green: 0.11, blue: 0.18)
        }
    }

    var backgroundBottom: Color {
        switch self {
        case .light:
            return Color(red: 0.83, green: 0.90, blue: 0.98)
        case .dark:
            return Color(red: 0.03, green: 0.05, blue: 0.10)
        }
    }

    var panelTint: Color {
        switch self {
        case .light:
            return Color.white.opacity(0.58)
        case .dark:
            return Color.white.opacity(0.10)
        }
    }

    var border: Color {
        switch self {
        case .light:
            return Color.white.opacity(0.70)
        case .dark:
            return Color.white.opacity(0.18)
        }
    }

    var primaryText: Color {
        switch self {
        case .light:
            return Color(red: 0.10, green: 0.16, blue: 0.24)
        case .dark:
            return Color.white.opacity(0.96)
        }
    }

    var secondaryText: Color {
        switch self {
        case .light:
            return Color(red: 0.28, green: 0.35, blue: 0.45)
        case .dark:
            return Color.white.opacity(0.70)
        }
    }
}
